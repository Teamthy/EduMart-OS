package httpserver

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"
)

type envelope struct { Status string `json:"status"`; RequestID string `json:"request_id,omitempty"` }

func New(addr string, logger *slog.Logger) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, http.StatusOK, envelope{Status:"ok"}) })
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		// TODO: verify PostgreSQL and required dependencies before reporting ready.
		writeJSON(w, http.StatusOK, envelope{Status:"ready"})
	})
	mux.HandleFunc("GET /api/v1/health", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, http.StatusOK, envelope{Status:"ok"}) })
	return &http.Server{Addr: addr, Handler: requestLog(logger, mux), ReadHeaderTimeout: 5*time.Second, ReadTimeout: 15*time.Second, WriteTimeout: 30*time.Second, IdleTimeout: 60*time.Second}
}
func requestLog(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now(); next.ServeHTTP(w, r)
		logger.Info("http request", "method", r.Method, "path", r.URL.Path, "duration_ms", time.Since(start).Milliseconds())
	})
}
func writeJSON(w http.ResponseWriter, status int, value any) { w.Header().Set("Content-Type", "application/json"); w.WriteHeader(status); _ = json.NewEncoder(w).Encode(value) }
