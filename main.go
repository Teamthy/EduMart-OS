package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/edumart/platform/services/api/internal/platform/config"
	"github.com/edumart/platform/services/api/internal/platform/httpserver"
)

func main() {
	cfg := config.Load()
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	server := httpserver.New(cfg.HTTPAddr, logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		logger.Info("api starting", "addr", cfg.HTTPAddr, "env", cfg.AppEnv)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("api server failed", "error", err)
			stop()
		}
	}()
	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown failed", "error", err)
	}
}
