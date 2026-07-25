package main

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/robikorb/dirdeck/backend/internal/api"
	"github.com/robikorb/dirdeck/backend/internal/auth"
	"github.com/robikorb/dirdeck/backend/internal/config"
	"github.com/robikorb/dirdeck/backend/internal/db"
	appfs "github.com/robikorb/dirdeck/backend/internal/fs"
	"github.com/robikorb/dirdeck/backend/internal/prefs"
	"github.com/robikorb/dirdeck/backend/internal/preview"
	"github.com/robikorb/dirdeck/backend/internal/transfer"
	"github.com/robikorb/dirdeck/backend/internal/volumes"
)

//go:embed all:static
var embeddedStatic embed.FS

// Shutdown budgets are consumed sequentially, so their sum must stay below the
// compose stop_grace_period (30s) or the container is killed mid-cleanup.
const (
	httpShutdownTimeout     = 10 * time.Second
	transferShutdownTimeout = 15 * time.Second
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	database, err := db.Open(cfg.DataDir)
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	defer database.Close()

	authSvc := auth.New(database, cfg.SecureCookie, cfg.SessionTTLHours, cfg.LoginRateLimitMax, cfg.LoginRateLimitSec)
	if err := bootstrapAdmin(authSvc, cfg); err != nil {
		log.Fatalf("bootstrap admin: %v", err)
	}
	if err := authSvc.PruneSessions(); err != nil {
		log.Fatalf("prune sessions: %v", err)
	}

	reg, err := loadVolumes(cfg)
	if err != nil {
		log.Fatalf("volumes: %v", err)
	}

	fsSvc := appfs.New(reg)
	xfer, err := transfer.New(database, fsSvc)
	if err != nil {
		log.Fatalf("transfers: %v", err)
	}
	previewSvc := preview.New(fsSvc, preview.Defaults())
	prefsStore := prefs.New(database)

	staticFS, err := loadStatic(cfg.StaticDir)
	if err != nil {
		log.Fatalf("static: %v", err)
	}

	srv := &api.Server{
		Auth:      authSvc,
		FS:        fsSvc,
		Volumes:   reg,
		Transfers: xfer,
		Preview:   previewSvc,
		Prefs:     prefsStore,
		Static:    staticFS,
		Ready:     true,
	}

	httpSrv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		// No overall read deadline: uploads stream arbitrarily large bodies and a
		// fixed limit would kill them mid-transfer. ReadHeaderTimeout still bounds
		// slow-header attacks.
		ReadTimeout:       0,
		WriteTimeout:      0, // streaming downloads + SSE
		IdleTimeout:       120 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		log.Printf("listening on %s", cfg.ListenAddr)
		errCh <- httpSrv.ListenAndServe()
	}()

	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	select {
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			log.Fatalf("server: %v", err)
		}
	case <-signalCtx.Done():
		log.Printf("shutdown requested")
	}

	// Release SSE streams first. They never return to idle on their own, so
	// http.Server.Shutdown would otherwise block for its full deadline.
	srv.BeginShutdown()

	httpCtx, cancelHTTP := context.WithTimeout(context.Background(), httpShutdownTimeout)
	defer cancelHTTP()
	if err := httpSrv.Shutdown(httpCtx); err != nil {
		log.Printf("http shutdown: %v", err)
	}

	// Independent budget: transfer workers must still get their full grace
	// period to clean staging files and record durable status even when the
	// HTTP shutdown above used all of its own time.
	transferCtx, cancelTransfers := context.WithTimeout(context.Background(), transferShutdownTimeout)
	defer cancelTransfers()
	if err := xfer.Shutdown(transferCtx); err != nil {
		log.Printf("transfer shutdown: %v", err)
	}
}

// bootstrapAdmin prefers operator-managed secret files. When none are present —
// the zero-configuration `docker run` path — it generates a strong password on
// the very first start and prints it once. Only the Argon2id hash is stored, and
// a password is generated only when no administrator exists yet, so restarting
// never resets a password the operator has already been given.
func bootstrapAdmin(authSvc *auth.Service, cfg config.Config) error {
	username, userErr := config.ReadSecretFile(cfg.AdminUsernameFile)
	password, passErr := config.ReadSecretFile(cfg.AdminPasswordFile)

	if userErr == nil && passErr == nil {
		return authSvc.BootstrapAdmin(username, password)
	}
	if passErr != nil && !errors.Is(passErr, os.ErrNotExist) {
		return fmt.Errorf("admin password: %w", passErr)
	}
	if userErr != nil && !errors.Is(userErr, os.ErrNotExist) {
		return fmt.Errorf("admin username: %w", userErr)
	}

	if username == "" {
		username = "admin"
	}
	exists, err := authSvc.HasAdmin()
	if err != nil {
		return err
	}
	if exists {
		return nil
	}

	generated, err := auth.GeneratePassword()
	if err != nil {
		return err
	}
	if err := authSvc.BootstrapAdmin(username, generated); err != nil {
		return err
	}
	log.Printf("┌──────────────────────────────────────────────────────────┐")
	log.Printf("│ DirDeck created its first administrator account.         │")
	log.Printf("│ This password is shown once and is not stored anywhere.  │")
	log.Printf("│                                                          │")
	log.Printf("│   username: %-44s │", username)
	log.Printf("│   password: %-44s │", generated)
	log.Printf("└──────────────────────────────────────────────────────────┘")
	return nil
}

// loadVolumes uses the configured registry when one exists and otherwise
// discovers whatever is mounted under /mnt/volumes, so a plain `docker run`
// with one bind mount is a complete installation.
func loadVolumes(cfg config.Config) (*volumes.Registry, error) {
	if _, err := os.Stat(cfg.VolumesFile); err == nil {
		return volumes.Load(cfg.VolumesFile)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	reg, err := volumes.Discover(cfg.WritableVolumes)
	if err != nil {
		return nil, err
	}
	for _, v := range reg.Public() {
		access := "read-only"
		if !v.ReadOnly {
			access = "read-write"
		}
		log.Printf("volume discovered: %s (%s)", v.ID, access)
	}
	return reg, nil
}

func loadStatic(dir string) (fs.FS, error) {
	if dir != "" {
		return os.DirFS(dir), nil
	}
	sub, err := fs.Sub(embeddedStatic, "static")
	if err != nil {
		return emptyFS{}, nil
	}
	return sub, nil
}

type emptyFS struct{}

func (emptyFS) Open(name string) (fs.File, error) {
	return nil, fs.ErrNotExist
}
