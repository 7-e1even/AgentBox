package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"agentbox/internal/catalog"
	"agentbox/internal/httpapi"
	"agentbox/internal/store"
	"github.com/joho/godotenv"
)

var version = "dev"

func main() {
	_ = godotenv.Load(".env", "server/.env")
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		logger.Error("DATABASE_URL is required")
		os.Exit(1)
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "8091"
	}
	bindHost := strings.TrimSpace(os.Getenv("AGENTBOX_BIND_HOST"))
	if bindHost == "" {
		bindHost = "127.0.0.1"
	}
	origins := strings.Split(os.Getenv("ALLOWED_ORIGINS"), ",")
	disableAuth := strings.EqualFold(strings.TrimSpace(os.Getenv("AGENTBOX_DISABLE_AUTH")), "true")
	workerBinaryDir := strings.TrimSpace(os.Getenv("AGENTBOX_WORKER_BINARY_DIR"))
	workerVersion := strings.TrimSpace(os.Getenv("AGENTBOX_WORKER_VERSION"))
	if workerVersion == "" {
		workerVersion = version
	}
	workerReleaseURL := strings.TrimSpace(os.Getenv("AGENTBOX_WORKER_RELEASE_BASE_URL"))
	if workerReleaseURL == "" {
		workerReleaseURL = "https://github.com/7-e1even/AgentBox/releases/download"
	}
	workerCacheDir := strings.TrimSpace(os.Getenv("AGENTBOX_WORKER_CACHE_DIR"))
	if workerCacheDir == "" {
		workerCacheDir = "/var/lib/agentbox/worker-cache"
	}
	if disableAuth && !strings.EqualFold(strings.TrimSpace(os.Getenv("AGENTBOX_ENV")), "development") {
		logger.Error("AGENTBOX_DISABLE_AUTH is only allowed when AGENTBOX_ENV=development")
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	repository, err := store.New(ctx, databaseURL, catalog.BuiltinCatalog)
	if err != nil {
		logger.Error("initialize store", "error", err)
		os.Exit(1)
	}
	defer repository.Close()

	server := &http.Server{
		Addr: net.JoinHostPort(bindHost, port),
		Handler: httpapi.New(repository, catalog.BuiltinCatalog, logger, origins, httpapi.Config{
			DisableAuth:      disableAuth,
			WorkerBinaryDir:  workerBinaryDir,
			WorkerVersion:    workerVersion,
			WorkerReleaseURL: workerReleaseURL,
			WorkerCacheDir:   workerCacheDir,
		}),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      3 * time.Minute,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		if disableAuth {
			logger.Warn("authentication disabled for development")
		}
		logger.Info("agentbox api listening", "address", server.Addr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("serve api", "error", err)
			cancel()
		}
	}()

	<-ctx.Done()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("shutdown api", "error", err)
	}
}
