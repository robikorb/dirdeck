package main

import (
	"embed"
	"io/fs"
	"log"
	"net/http"
	"os"
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
	log.Printf("listening on %s", cfg.ListenAddr)
	if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("server: %v", err)
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
