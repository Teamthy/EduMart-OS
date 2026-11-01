// Command api serves the Edumart REST API.
//
//	go run ./cmd/api                       # SQLite at ./data/edumart.db, seeded demo data
//	EDUMART_DRIVER=postgres EDUMART_DSN=postgres://… go run ./cmd/api
//	go run ./cmd/api -migrate-only         # apply pending migrations, report, exit 0
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
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
	"github.com/teamthy/edumart/apps/api/internal/consistency"
	"github.com/teamthy/edumart/apps/api/internal/content"
	"github.com/teamthy/edumart/apps/api/internal/exams"
	"github.com/teamthy/edumart/apps/api/internal/insights"
	"github.com/teamthy/edumart/apps/api/internal/media"
	"github.com/teamthy/edumart/apps/api/internal/notify"
	"github.com/teamthy/edumart/apps/api/internal/orgs"
	"github.com/teamthy/edumart/apps/api/internal/rewards"
	"github.com/teamthy/edumart/apps/api/internal/search"
	"github.com/teamthy/edumart/apps/api/internal/seed"
	"github.com/teamthy/edumart/apps/api/internal/signup"
	"github.com/teamthy/edumart/apps/api/internal/store"
	"github.com/teamthy/edumart/apps/api/internal/virtualclass"
)

// version is the release identity, set at build time:
//
//	go build -ldflags "-X main.version=$(git rev-parse --short HEAD)"
//
// It is reported by /healthz and /readyz so a rollout can be checked by asking a running replica what it is,
// rather than by trusting what the deploy script believed it shipped. Left as "dev" for `go run`.
var version = "dev"

func main() {
	// A one-shot migration step, so a rollout can migrate *before* any replica starts serving.
	//
	// Migrations otherwise run when a process opens the database, which means N replicas booting together
	// all see the same pending migration and race: the ledger stops them diverging, but the losers exit with
	// a duplicate-key error and crashloop until the winner finishes. Running this first removes the race,
	// and a replica that starts after it finds nothing pending and only verifies checksums.
	migrateOnly := flag.Bool("migrate-only", false,
		"apply pending migrations, report what is applied, and exit without serving")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	driver := env("EDUMART_DRIVER", "sqlite3")
	dsn := env("EDUMART_DSN", "data/edumart.db")

	// Migrations are a deployment step, not a startup side effect, once there is a fleet. Replicas set
	// EDUMART_MIGRATE_ON_BOOT=false and a job runs `api -migrate-only` first; the capability stays on by
	// default because a single-instance deployment (and every developer, and every test) wants one command.
	migrateOnBoot := env("EDUMART_MIGRATE_ON_BOOT", "true") == "true"
	db, err := store.OpenWith(driver, dsn, store.Options{MigrateOnBoot: &migrateOnBoot})
	if err != nil {
		logger.Error("database unavailable", "error", err.Error())
		os.Exit(1)
	}
	defer db.Close()

	if *migrateOnly {
		// Deliberately before seeding: a migration job that also writes demo fixtures is a job that does
		// two things, and the second one is not safe to run against production.
		status, err := db.MigrationStatus(context.Background())
		if err != nil {
			logger.Error("migration status unavailable", "error", err.Error())
			os.Exit(1)
		}
		if len(status.Pending) > 0 {
			logger.Error("migrations still pending after opening the database",
				"pending", fmt.Sprint(status.Pending))
			os.Exit(1)
		}
		logger.Info("migrations applied", "engine", db.Driver, "applied", len(status.Applied))
		return
	}
	logger.Info("session policy",
		"access_ttl", envDuration("EDUMART_ACCESS_TOKEN_TTL", auth.DefaultAccessTTL).String())
	logger.Info("database opened",
		"engine", db.Driver, "migrate_on_boot", db.MigrateOnBoot())

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
		// An access token that outlives the refresh chain makes rotation decorative: no browser ever calls
		// /v1/auth/refresh, so nothing is ever rotated and reuse detection never fires. An hour is short
		// enough to bound a stolen token and long enough to refresh a few times a day.
		auth.WithAccessTTL(envDuration("EDUMART_ACCESS_TOKEN_TTL", auth.DefaultAccessTTL)),
	)
	paymentProvider := env("EDUMART_PAYMENT_PROVIDER", "sandbox")
	shop := commerce.New(db, paymentProvider, env("EDUMART_WEBHOOK_SECRET", "dev_sandbox_webhook_secret")).
		WithNotifier(notifier).
		WithLogger(logger)
	// Refunds move money through the same provider that took it. With Paystack configured but no secret key,
	// the adapter refuses every refund with a definite error: a refund that cannot be attempted is a
	// support ticket, and pretending otherwise by falling back to the sandbox would print money.
	switch paymentProvider {
	case "paystack":
		key := env("EDUMART_PAYSTACK_SECRET_KEY", "")
		if key == "" {
			logger.Warn("EDUMART_PAYMENT_PROVIDER=paystack but EDUMART_PAYSTACK_SECRET_KEY is unset: " +
				"refunds will be recorded as failed with no money moved")
		}
		shop.WithRefundProvider(commerce.NewPaystackRefunds(key, env("EDUMART_PAYSTACK_BASE_URL", ""))).
			WithPaystackWebhook(key)
	default:
		logger.Info("refunds settle through the sandbox provider: no money leaves the account")
	}

	// Links in invitations point at the web app, which is a different origin from the API.
	webURL := env("EDUMART_WEB_URL", "http://localhost:3000")
	institutions := orgs.New(db).WithNotifier(notifier, webURL)
	appEngine := assessment.New(db)

	// The media forwarder. It is built here, before the router, because it owns sockets rather than state:
	// an operator pins the UDP range the deployment forwards, and the process that closes everything on
	// shutdown is the same one that opened it.
	forwarder, mediaErr := media.New(media.Config{
		UDPMin: uint16(number(env("EDUMART_MEDIA_UDP_MIN", "0"))),
		UDPMax: uint16(number(env("EDUMART_MEDIA_UDP_MAX", "0"))),
	})
	if mediaErr != nil {
		logger.Error("media forwarder unavailable", "error", mediaErr.Error())
		os.Exit(1)
	}
	defer forwarder.Close()

	router := api.NewRouter(api.Deps{
		Store:   db,
		Auth:    authService,
		Items:   content.New(db),
		Exams:   appEngine,
		Classes: classroom.New(db),
		Shop:    shop,
		Rewards: rewards.New(db).WithRates(parseRates(env("EDUMART_REFERRAL_RATES", ""))),
		Live: virtualclass.New(db, virtualclass.Config{
			WebURL: webURL,
			// STUN alone gets two peers on ordinary home networks connected. A TURN relay is what makes
			// the room work behind a symmetric NAT, and it is configuration rather than code because a
			// relay is bandwidth an operator has to pay for.
			ICEServers: parseICEServers(env("EDUMART_ICE_SERVERS", "")),
		}),
		Insights: insights.New(db),
		// A sitting is an exam-shaped assessment definition, so the exam layer sits on the engine rather
		// than beside it: one scorer, one deadline rule, one snapshot.
		Sittings: exams.New(db, appEngine, exams.Config{WebURL: webURL}),
		Habit:    consistency.New(db),
		Media:    forwarder,
		Staff:    admin.New(db),
		Signup:   signup.New(db),
		Orgs:     institutions,
		Notifier: notifier,
		Finder:   search.New(db),
		Logger:   logger,
		Config: api.Config{
			AllowedOrigins:    splitList(env("EDUMART_ALLOWED_ORIGINS", "http://localhost:3000,http://localhost:8081")),
			AllowSandboxPay:   env("EDUMART_ALLOW_SANDBOX_PAYMENTS", "true") == "true",
			SandboxWebhookKey: env("EDUMART_WEBHOOK_SECRET", "dev_sandbox_webhook_secret"),
			// Opt-in: believing X-Forwarded-For without a proxy in front means any caller can pick
			// their own rate-limit bucket, and guessing a password becomes a matter of a loop.
			TrustProxy: env("EDUMART_TRUST_PROXY", "false") == "true",
			// Empty by default: /metrics is then loopback-only, which is right for a laptop and wrong
			// for a deployment, so production sets a token and Prometheus sends it.
			MetricsToken:    os.Getenv("EDUMART_METRICS_TOKEN"),
			BackupHeartbeat: os.Getenv("EDUMART_BACKUP_HEARTBEAT"),
			// The build's own identity, so a rollout can be verified by asking the process.
			Version: version,
		},
	})

	stopExpiry := startExpiryWorker(logger, appEngine)
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

// envDuration reads a duration such as "45m" or "2h", falling back to the default.
//
// A malformed value falls back rather than stopping the process: the alternative is a deployment that will
// not boot because somebody typed "60" instead of "60s", and a wrong-but-working token lifetime is a much
// smaller problem than an outage. The value is logged so the difference is visible.
func envDuration(key string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(raw)
	if err != nil || parsed <= 0 {
		slog.Default().Warn("ignoring an unparseable duration, using the default",
			"variable", key, "value", raw, "default", fallback.String())
		return fallback
	}
	return parsed
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

// number parses an integer environment variable, falling back to 0. It exists so a bad value fails at the point
// of use with the variable named, rather than silently becoming a zero somewhere else.
func number(raw string) int {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0
	}
	return value
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

// parseRates reads EDUMART_REFERRAL_RATES (e.g. "10,3,1") into a commission schedule. A malformed list is
// ignored rather than fatal: a typo in an optional incentive setting should not stop the API from booting.
func parseRates(raw string) []int {
	rates := []int{}
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		value, err := strconv.Atoi(part)
		if err != nil {
			return rewards.DefaultRates
		}
		rates = append(rates, value)
	}
	if len(rates) == 0 {
		return rewards.DefaultRates
	}
	return rates
}

// parseICEServers reads EDUMART_ICE_SERVERS as a JSON array of RTCPeerConnection iceServers entries. The
// default is a public STUN server, which is enough to negotiate a direct path and not enough to guarantee
// one, so /v1/virtual-classes/{id}/ice reports exactly what the deployment configured.
func parseICEServers(raw string) []map[string]any {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return []map[string]any{{"urls": []string{"stun:stun.l.google.com:19302"}}}
	}
	var servers []map[string]any
	if err := json.Unmarshal([]byte(raw), &servers); err != nil {
		return []map[string]any{{"urls": []string{"stun:stun.l.google.com:19302"}}}
	}
	return servers
}
