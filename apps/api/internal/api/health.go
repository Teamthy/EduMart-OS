package api

import (
	"context"
	"net/http"
	"time"

	"github.com/teamthy/edumart/apps/api/internal/httpx"
	"github.com/teamthy/edumart/apps/api/internal/store"
)

// checkResult is one dependency probe. A readiness response is a list of these, so an operator
// learns *which* dependency is down from the body, not only from the status code.
type checkResult struct {
	Name    string `json:"name"`
	Status  string `json:"status"` // "ok" | "fail"
	Detail  string `json:"detail,omitempty"`
	Latency string `json:"latency,omitempty"`
}

// readyzHandler reports whether this process can serve traffic: the database answers, and the
// schema is the one this binary was built against.
//
// It is deliberately stricter than /healthz. A liveness probe must stay cheap and never fail
// because a dependency is down (that would restart a healthy process); a readiness probe must
// fail, so a load balancer stops sending traffic to an instance that cannot serve it.
func readyzHandler(db *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()

		checks := []checkResult{checkDatabase(ctx, db), checkMigrations(ctx, db)}

		status := "ok"
		code := http.StatusOK
		for _, check := range checks {
			if check.Status != "ok" {
				status = "unavailable"
				code = http.StatusServiceUnavailable
				break
			}
		}

		httpx.JSON(w, code, map[string]any{
			"status":     status,
			"time":       store.Now(),
			"driver":     db.Driver,
			"request_id": httpx.RequestID(ctx),
			"checks":     checks,
		})
	}
}

func checkDatabase(ctx context.Context, db *store.Store) checkResult {
	start := time.Now()
	if err := db.DB.PingContext(ctx); err != nil {
		return checkResult{Name: "database", Status: "fail", Detail: err.Error()}
	}
	// Ping alone can be satisfied by a pooled connection; a real read proves the pool works.
	var one int
	if err := db.DB.QueryRowContext(ctx, `SELECT 1`).Scan(&one); err != nil {
		return checkResult{Name: "database", Status: "fail", Detail: err.Error()}
	}
	return checkResult{
		Name:    "database",
		Status:  "ok",
		Detail:  db.Driver,
		Latency: time.Since(start).Round(time.Microsecond).String(),
	}
}

func checkMigrations(ctx context.Context, db *store.Store) checkResult {
	status, err := db.MigrationStatus(ctx)
	if err != nil {
		return checkResult{Name: "migrations", Status: "fail", Detail: err.Error()}
	}
	if !status.UpToDate {
		// Serving traffic on a stale schema produces confusing failures far from the cause.
		return checkResult{
			Name:   "migrations",
			Status: "fail",
			Detail: "pending: " + join(status.Pending),
		}
	}
	return checkResult{Name: "migrations", Status: "ok", Detail: itoa(len(status.Applied)) + " applied"}
}

func join(values []string) string {
	out := ""
	for i, value := range values {
		if i > 0 {
			out += ", "
		}
		out += value
	}
	return out
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	var digits []byte
	for v > 0 {
		digits = append([]byte{byte('0' + v%10)}, digits...)
		v /= 10
	}
	return string(digits)
}
