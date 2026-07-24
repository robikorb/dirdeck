package main

import (
	"context"
	"embed"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/liquid-glass-file-manager/backend/internal/api"
	"github.com/liquid-glass-file-manager/backend/internal/auth"
	"github.com/liquid-glass-file-manager/backend/internal/config"
	"github.com/liquid-glass-file-manager/backend/internal/db"
	appfs "github.com/liquid-glass-file-manager/backend/internal/fs"
	"github.com/liquid-glass-file-manager/backend/internal/prefs"
	"github.com/liquid-glass-file-manager/backend/internal/preview"
	"github.com/liquid-glass-file-manager/backend/internal/transfer"
	"github.com/liquid-glass-file-manager/backend/internal/volumes"
)

//go:embed all:static
var embeddedStatic embed.FS

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

	username, err := config.ReadSecretFile(cfg.AdminUsernameFile)
	if err != nil {
		log.Fatalf("admin username: %v", err)
	}
	password, err := config.ReadSecretFile(cfg.AdminPasswordFile)
	if err != nil {
		log.Fatalf("admin password: %v", err)
	}

	authSvc := auth.New(database, cfg.SecureCookie, cfg.SessionTTLHours, cfg.LoginRateLimitMax, cfg.LoginRateLimitSec)
	if err := authSvc.BootstrapAdmin(username, password); err != nil {
		log.Fatalf("bootstrap admin: %v", err)
	}
	if err := authSvc.PruneSessions(); err != nil {
		log.Fatalf("prune sessions: %v", err)
	}

	reg, err := volumes.Load(cfg.VolumesFile)
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
		ReadTimeout:       60 * time.Second,
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

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		log.Printf("http shutdown: %v", err)
	}
	if err := xfer.Shutdown(shutdownCtx); err != nil {
		log.Printf("transfer shutdown: %v", err)
	}
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
