package main

import (
	"cmp"
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
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	envFiles := []string{".env", "server/.env"}
	existing := make([]string, 0, len(envFiles))
	for _, file := range envFiles {
		if info, err := os.Stat(file); err == nil && info.Mode().IsRegular() {
			existing = append(existing, file)
		}
	}
	// godotenv.Load loads files in order and never overrides a variable that is
	// already set, so the first existing file wins on conflicts.
	_ = godotenv.Load(envFiles...)
	if len(existing) > 1 {
		logger.Warn("multiple .env files found; values from the first file win and are NOT overridden",
			"effective", existing[0], "ignored", existing[1:])
	}
	if os.Getenv("POSTGRES_PASSWORD") == "change-me-before-deploy" {
		logger.Warn("POSTGRES_PASSWORD is still the placeholder \"change-me-before-deploy\"; set a strong password before deploying")
	}
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		logger.Error("DATABASE_URL is required")
		os.Exit(1)
	}
	port := cmp.Or(os.Getenv("PORT"), "8091")
	bindHost := cmp.Or(strings.TrimSpace(os.Getenv("AGENTBOX_BIND_HOST")), "127.0.0.1")
	origins := strings.Split(os.Getenv("ALLOWED_ORIGINS"), ",")
	disableAuth := strings.EqualFold(strings.TrimSpace(os.Getenv("AGENTBOX_DISABLE_AUTH")), "true")
	workerBinaryDir := strings.TrimSpace(os.Getenv("AGENTBOX_WORKER_BINARY_DIR"))
	workerVersion := cmp.Or(strings.TrimSpace(os.Getenv("AGENTBOX_WORKER_VERSION")), version)
	workerReleaseURL := cmp.Or(
		strings.TrimSpace(os.Getenv("AGENTBOX_WORKER_RELEASE_BASE_URL")),
		"https://github.com/7-e1even/AgentBox/releases/download",
	)
	workerCacheDir := cmp.Or(
		strings.TrimSpace(os.Getenv("AGENTBOX_WORKER_CACHE_DIR")),
		"/var/lib/agentbox/worker-cache",
	)
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

	handler := httpapi.New(repository, catalog.BuiltinCatalog, logger, origins, httpapi.Config{
		DisableAuth:      disableAuth,
		WorkerBinaryDir:  workerBinaryDir,
		WorkerVersion:    workerVersion,
		WorkerReleaseURL: workerReleaseURL,
		WorkerCacheDir:   workerCacheDir,
	})
	server := &http.Server{
		Addr:              net.JoinHostPort(bindHost, port),
		Handler:           handler,
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
	handler.RecordSystem("info", "startup", "AgentBox API 已启动", map[string]any{
		"address": server.Addr, "version": version,
	})

	<-ctx.Done()
	handler.RecordSystem("info", "shutdown", "AgentBox API 正在关闭", nil)
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("shutdown api", "error", err)
	}
	// 停 HTTP 后再 flush 剩余系统日志，避免 Close 时仍有新日志写入。
	if err := handler.Close(shutdownCtx); err != nil {
		logger.Warn("flush system logs", "error", err)
	}
}
