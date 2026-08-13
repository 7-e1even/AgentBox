package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/httpapi"
	"agentbox/internal/store"
	"github.com/joho/godotenv"
)

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
	origins := strings.Split(os.Getenv("ALLOWED_ORIGINS"), ",")

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	repository, err := store.New(ctx, databaseURL, agent.BuiltinCatalog)
	if err != nil {
		logger.Error("initialize store", "error", err)
		os.Exit(1)
	}
	defer repository.Close()

	server := &http.Server{
		Addr:              ":" + port,
		Handler:           httpapi.New(repository, agent.BuiltinCatalog, logger, origins),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
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
