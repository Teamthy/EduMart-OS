// Command api serves the Edumart REST API.
//
//	go run ./cmd/api              # SQLite at ./data/edumart.db, seeded demo data
//	EDUMART_DRIVER=postgres go run ./cmd/api
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

	"github.com/teamthy/edumart/apps/api/internal/admin"
	"github.com/teamthy/edumart/apps/api/internal/api"
	"github.com/teamthy/edumart/apps/api/internal/assessment"
	"github.com/teamthy/edumart/apps/api/internal/auth"
	"github.com/teamthy/edumart/apps/api/internal/classroom"
	"github.com/teamthy/edumart/apps/api/internal/commerce"
	"github.com/teamthy/edumart/apps/api/internal/content"
	"github.com/teamthy/edumart/apps/api/internal/insights"
	"github.com/teamthy/edumart/apps/api/internal/rewards"
	"github.com/teamthy/edumart/apps/api/internal/seed"
	"github.com/teamthy/edumart/apps/api/internal/store"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	driver := env("EDUMART_DRIVER", "sqlite3")
	dsn := env("EDUMART_DSN", "data/edumart.db")

	db, err := store.Open(driver, dsn)
	if err != nil {
		logger.Error("database unavailable", "error", err.Error())
		os.Exit(1)
	}
	defer db.Close()

	if env("EDUMART_SEED", "true") == "true" {
		if err := seed.Run(context.Background(), db, seed.Options{
			IncludeDemoUsers: env("EDUMART_SEED_DEMO_USERS", "true") == "true",
			IncludeSamples:   true,
		}); err != nil {
			logger.Error("seed failed", "error", err.Error())
			os.Exit(1)
		}
	}

	authService := auth.New(db)
	shop := commerce.New(db, env("EDUMART_PAYMENT_PROVIDER", "sandbox"), env("EDUMART_WEBHOOK_SECRET", "dev_sandbox_webhook_secret"))

	router := api.NewRouter(api.Deps{
		Store:    db,
		Auth:     authService,
		Items:    content.New(db),
		Exams:    assessment.New(db),
		Classes:  classroom.New(db),
		Shop:     shop,
		Rewards:  rewards.New(db),
		Insights: insights.New(db),
		Staff:    admin.New(db),
		Logger:   logger,
		Config: api.Config{
			AllowedOrigins:    splitList(env("EDUMART_ALLOWED_ORIGINS", "http://localhost:3000,http://localhost:8081")),
			AllowSandboxPay:   env("EDUMART_ALLOW_SANDBOX_PAYMENTS", "true") == "true",
			SandboxWebhookKey: env("EDUMART_WEBHOOK_SECRET", "dev_sandbox_webhook_secret"),
		},
	})

	exams := assessment.New(db)
	stopExpiry := startExpiryWorker(logger, exams)
	defer stopExpiry()

	port := env("PORT", "8080")
	server := &http.Server{
		Addr:              "0.0.0.0:" + port,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		logger.Info("api listening", "addr", server.Addr, "driver", driver)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server stopped", "error", err.Error())
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		logger.Error("shutdown error", "error", err.Error())
	}
	logger.Info("api stopped")
}

// startExpiryWorker finalizes attempts whose deadline passed. This is a safety net in front of
// the request-time check, never the only defence.
func startExpiryWorker(logger *slog.Logger, exams *assessment.Service) func() {
	ticker := time.NewTicker(60 * time.Second)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-ticker.C:
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				count, err := exams.ExpireDue(ctx, 50)
				cancel()
				if err != nil {
					logger.Error("expiry sweep failed", "error", err.Error())
				} else if count > 0 {
					logger.Info("expired attempts finalized", "count", count)
				}
			case <-done:
				ticker.Stop()
				return
			}
		}
	}()
	return func() { close(done) }
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func splitList(value string) []string {
	out := []string{}
	for _, part := range strings.Split(value, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
