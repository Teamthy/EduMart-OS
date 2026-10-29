// Command api serves the Edumart REST API.
//
//	go run ./cmd/api                       # SQLite at ./data/edumart.db, seeded demo data
//	EDUMART_DRIVER=postgres EDUMART_DSN=postgres://… go run ./cmd/api
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
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
	"github.com/teamthy/edumart/apps/api/internal/notify"
	"github.com/teamthy/edumart/apps/api/internal/orgs"
	"github.com/teamthy/edumart/apps/api/internal/rewards"
	"github.com/teamthy/edumart/apps/api/internal/seed"
	"github.com/teamthy/edumart/apps/api/internal/signup"
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

	// Codes and reset links go out through a durable courier: the message is recorded first, attempted
	// once, then retried by a worker. With no provider configured the log sender is used, and the
	// bodies are redacted whenever the deployment is not a development one.
	notifier, notifyProduction := buildCourier(db, logger)
	codeSecret := env("EDUMART_VERIFICATION_SECRET", "")
	if codeSecret == "" {
		codeSecret = auth.DefaultCodeSecret
		if notifyProduction {
			logger.Warn("EDUMART_VERIFICATION_SECRET is unset: stored verification codes share a " +
				"well-known key. Set it before handling real accounts.")
		}
	}
	authService := auth.New(db,
		auth.WithNotifier(notifier),
		auth.WithCodeSecret(codeSecret),
		auth.WithLogger(logger),
	)
	shop := commerce.New(db, env("EDUMART_PAYMENT_PROVIDER", "sandbox"), env("EDUMART_WEBHOOK_SECRET", "dev_sandbox_webhook_secret")).
		WithNotifier(notifier).
		WithLogger(logger)

	// Links in invitations point at the web app, which is a different origin from the API.
	webURL := env("EDUMART_WEB_URL", "http://localhost:3000")
	institutions := orgs.New(db).WithNotifier(notifier, webURL)

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
		Signup:   signup.New(db),
		Orgs:     institutions,
		Notifier: notifier,
		Logger:   logger,
		Config: api.Config{
			AllowedOrigins:    splitList(env("EDUMART_ALLOWED_ORIGINS", "http://localhost:3000,http://localhost:8081")),
			AllowSandboxPay:   env("EDUMART_ALLOW_SANDBOX_PAYMENTS", "true") == "true",
			SandboxWebhookKey: env("EDUMART_WEBHOOK_SECRET", "dev_sandbox_webhook_secret"),
			// Opt-in: believing X-Forwarded-For without a proxy in front means any caller can pick
			// their own rate-limit bucket, and guessing a password becomes a matter of a loop.
			TrustProxy: env("EDUMART_TRUST_PROXY", "false") == "true",
		},
	})

	exams := assessment.New(db)
	stopExpiry := startExpiryWorker(logger, exams)
	defer stopExpiry()

	// The retry sweep: whatever failed to send is attempted again on its backoff schedule, across
	// restarts, because the queue lives in the database rather than in memory.
	workerCtx, stopWorker := context.WithCancel(context.Background())
	defer stopWorker()
	notifier.Start(workerCtx, 30*time.Second)

	// Reminders are planned and sent on their own timer. A reminder is a promise made in advance — the
	// row is written when the decision is made, so a restart between "decided" and "due" loses nothing.
	notifier.StartReminders(workerCtx, reminderInterval())

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

// buildCourier chooses how messages leave the process and wraps it in the durable delivery queue.
//
// A misconfigured mail provider stops the process at startup. The alternative — booting happily and
// silently failing every verification email — is the failure mode that costs a launch.
func buildCourier(db *store.Store, logger *slog.Logger) (*notify.Service, bool) {
	production := env("EDUMART_ENV", "development") == "production"
	mode := env("EDUMART_NOTIFY", "log")

	var sender notify.Sender
	switch mode {
	case "smtp":
		config := notify.SMTPConfig{
			Host:          env("EDUMART_SMTP_HOST", ""),
			Port:          atoi(env("EDUMART_SMTP_PORT", "587")),
			Username:      env("EDUMART_SMTP_USERNAME", ""),
			Password:      env("EDUMART_SMTP_PASSWORD", ""),
			From:          env("EDUMART_SMTP_FROM", ""),
			TLSMode:       env("EDUMART_SMTP_TLS", "starttls"),
			AllowInsecure: env("EDUMART_SMTP_ALLOW_INSECURE", "false") == "true",
		}
		if err := config.Validate(production); err != nil {
			logger.Error("smtp configuration is invalid", "error", err.Error())
			os.Exit(1)
		}
		sender = notify.NewSMTPSender(config)
		logger.Info("sending mail over smtp", "host", config.Host, "port", config.Port, "tls", config.TLSMode)
	default:
		if production {
			// Redacting keeps production logs from becoming a code archive; the warning says plainly
			// that nothing is actually being delivered.
			logger.Warn("no notification provider is configured; codes and reset links are only logged",
				"hint", "set EDUMART_NOTIFY=smtp with EDUMART_SMTP_* to deliver mail")
		}
		sender = notify.NewLogSender(logger, production)
	}
	return notify.NewService(db, sender, logger), production
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

// atoi parses an integer setting, falling back to zero so a typo surfaces in config validation rather
// than as a panic at startup.
func atoi(value string) int {
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0
	}
	return n
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

// reminderInterval is how often the reminder planner and sweep run. It is configurable because the
// right interval differs between a laptop (where a test wants it immediately) and production (where
// every pass scans the same tables for no new work).
func reminderInterval() time.Duration {
	raw := env("EDUMART_REMINDER_INTERVAL", "5m")
	parsed, err := time.ParseDuration(raw)
	if err != nil || parsed <= 0 {
		return 5 * time.Minute
	}
	return parsed
}
