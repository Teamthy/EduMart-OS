// Package httpx provides the HTTP contract: error envelope, request ids, idempotency,
// rate limiting and the middleware chain shared by every route.
package httpx

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/teamthy/edumart/apps/api/internal/metrics"
)

// ErrorCode mirrors the documented client-facing error vocabulary.
type ErrorCode string

const (
	CodeValidation       ErrorCode = "VALIDATION_ERROR"
	CodeUnauthenticated  ErrorCode = "UNAUTHENTICATED"
	CodeForbidden        ErrorCode = "FORBIDDEN"
	CodeNotFound         ErrorCode = "NOT_FOUND"
	CodeConflict         ErrorCode = "CONFLICT"
	CodeAssessmentClosed ErrorCode = "ASSESSMENT_NOT_OPEN"
	CodeSessionExpired   ErrorCode = "SESSION_EXPIRED"
	CodeSessionClosed    ErrorCode = "SESSION_CLOSED"
	CodeRateLimited      ErrorCode = "RATE_LIMITED"
	CodePaymentPending   ErrorCode = "PAYMENT_PENDING"
	CodeEmailUnverified  ErrorCode = "EMAIL_NOT_VERIFIED"

	// CodeJoinCodeExpired is a class code that was right once and is not any more. It is distinct from
	// NOT_FOUND because the remedy is different: ask the teacher for the new code, do not retype this one.
	CodeJoinCodeExpired ErrorCode = "JOIN_CODE_EXPIRED"
	// CodeAccountSuspended is distinct from CodeForbidden on purpose: the person needs to know that
	// signing in again will not help, and that support is the next step.
	CodeAccountSuspended ErrorCode = "ACCOUNT_SUSPENDED"

	// CodeSessionReused means a refresh token was presented twice, so the whole session family has been
	// revoked. It is distinct from UNAUTHENTICATED because retrying cannot help: the client must send
	// the user back to sign-in, and silently retrying would hide the one signal that matters.
	CodeSessionReused ErrorCode = "SESSION_REUSED"
	CodeInternal      ErrorCode = "INTERNAL_ERROR"
)

var statusFor = map[ErrorCode]int{
	CodeValidation:       http.StatusBadRequest,
	CodeUnauthenticated:  http.StatusUnauthorized,
	CodeForbidden:        http.StatusForbidden,
	CodeNotFound:         http.StatusNotFound,
	CodeConflict:         http.StatusConflict,
	CodeAssessmentClosed: http.StatusConflict,
	CodeSessionExpired:   http.StatusConflict,
	CodeSessionClosed:    http.StatusConflict,
	CodeRateLimited:      http.StatusTooManyRequests,
	CodePaymentPending:   http.StatusConflict,
	CodeEmailUnverified:  http.StatusForbidden,
	CodeJoinCodeExpired:  http.StatusGone,
	CodeAccountSuspended: http.StatusForbidden,
	CodeSessionReused:    http.StatusUnauthorized,
	CodeInternal:         http.StatusInternalServerError,
}

// FieldError is a single validation failure shown next to a form input.
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// Error is the only error type handlers return; it carries the client-facing code and message.
type Error struct {
	Code    ErrorCode
	Message string
	Details any
	Status  int
}

func (e *Error) Error() string { return string(e.Code) + ": " + e.Message }

// NewError builds a domain error with the default status for its code.
func NewError(code ErrorCode, message string, details ...any) *Error {
	status, known := statusFor[code]
	if !known {
		// An unmapped code used to produce Status 0, which `http.ResponseWriter` rejects with a panic
		// deep inside the handler — a confusing 500 for what is really a programming mistake. Defaulting
		// to 500 keeps the API's error envelope intact and the mistake visible in the logs.
		status = http.StatusInternalServerError
	}
	err := &Error{Code: code, Message: message, Status: status}
	if len(details) > 0 {
		err.Details = details[0]
	}
	return err
}

// Validation reports field-level problems.
func Validation(details []FieldError) *Error {
	return NewError(CodeValidation, "Please fix the highlighted fields.", details)
}

// HandlerFunc is a handler that can return an error instead of writing one by hand.
type HandlerFunc func(w http.ResponseWriter, r *http.Request) error

type ctxKey string

const (
	ctxRequestID ctxKey = "request_id"
	ctxUser      ctxKey = "user"
)

// RequestID returns the id attached to the current request.
func RequestID(ctx context.Context) string {
	if v, ok := ctx.Value(ctxRequestID).(string); ok {
		return v
	}
	return ""
}

// Middleware is the standard handler wrapper signature.
type Middleware func(http.Handler) http.Handler

// RequestIDMiddleware attaches a request id, honouring an inbound header when present.
func RequestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-Id")
		if id == "" || len(id) > 120 {
			id = randomHex(8)
		}
		w.Header().Set("X-Request-Id", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxRequestID, id)))
	})
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "unknown"
	}
	return hex.EncodeToString(b)
}

// Recover converts a panic into a logged 500 without leaking internals.
func Recover(logger *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					logger.Error("panic recovered", "request_id", RequestID(r.Context()), "path", r.URL.Path, "panic", rec)
					writeError(w, r, NewError(CodeInternal, "Something went wrong. Please try again with the request ID shown."))
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

type metricsKey struct{}

// WithMetrics puts the registry in the request context so the error path can count codes it refuses.
func WithMetrics(ctx context.Context, recorder ErrorRecorder) context.Context {
	return context.WithValue(ctx, metricsKey{}, recorder)
}

// ErrorRecorder is the slice of the metrics registry the HTTP layer needs. Keeping it an interface means
// httpx does not import a package that knows about Prometheus, and a test can pass a counter.
type ErrorRecorder interface {
	CountError(code string)
}

// Metrics records every request by route pattern, method and status code, plus how long it took.
//
// The route label is chi's pattern, never the request path: `/v1/orders/{id}` is one series. Using the raw
// path would create a series per order id, and a metric whose cardinality is chosen by the caller is how a
// monitoring system falls over during the incident it exists to describe.
func Metrics(registry *metrics.Registry) Middleware {
	requests := registry.Counter("edumart_http_requests_total",
		"Requests handled, by route pattern, method and status code.", "method", "route", "status")
	duration := registry.Histogram("edumart_http_request_duration_seconds",
		"Request duration in seconds, by route pattern and method.", "route", "method")
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)

			route := "unmatched"
			if pattern := chi.RouteContext(r.Context()).RoutePattern(); pattern != "" {
				route = pattern
			}
			requests.With(r.Method, route, strconv.Itoa(rec.status)).Inc()
			duration.With(route, r.Method).Observe(time.Since(start).Seconds())
		})
	}
}

// AccessLog emits one structured line per request with latency and status.
func AccessLog(logger *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)
			logger.Info("request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", rec.status,
				"duration_ms", time.Since(start).Milliseconds(),
				"request_id", RequestID(r.Context()),
			)
		})
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// Wrap adapts a HandlerFunc into an http.HandlerFunc with uniform error handling.
func Wrap(logger *slog.Logger, fn HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := fn(w, r); err != nil {
			var apiErr *Error
			if !errors.As(err, &apiErr) {
				logger.Error("unhandled error", "request_id", RequestID(r.Context()), "path", r.URL.Path, "error", err.Error())
				apiErr = NewError(CodeInternal, "Something went wrong. Please try again with the request ID shown.")
			}
			writeError(w, r, apiErr)
		}
	}
}

func writeError(w http.ResponseWriter, r *http.Request, err *Error) {
	status := err.Status
	if status == 0 {
		status = statusFor[err.Code]
	}
	// Counted here rather than in each handler: a refusal is a refusal whether it came from a handler, the
	// rate limiter or the router's own 404, and the alert has to see all of them.
	if recorder, ok := r.Context().Value(metricsKey{}).(ErrorRecorder); ok {
		recorder.CountError(string(err.Code))
	}
	details := err.Details
	if details == nil {
		details = []FieldError{}
	}
	JSON(w, status, map[string]any{
		"error": map[string]any{
			"code":       err.Code,
			"message":    err.Message,
			"request_id": RequestID(r.Context()),
			"details":    details,
		},
	})
}

// JSON writes a JSON response with no-store caching (assessment and account data must never be cached).
func JSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if payload == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		// The response is already partially written; log upstream via middleware.
		_ = err
	}
}

// Decode reads a JSON object body with a size limit and rejects unknown content types.
func Decode(r *http.Request, dst any) error {
	ct := r.Header.Get("Content-Type")
	if ct != "" && !strings.Contains(ct, "application/json") {
		return NewError(CodeValidation, "Expected a JSON request body.")
	}
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var typeErr *json.UnmarshalTypeError
		if errors.As(err, &typeErr) {
			return Validation([]FieldError{{Field: typeErr.Field, Message: "Unexpected value type."}})
		}
		if strings.Contains(err.Error(), "unknown field") {
			return NewError(CodeValidation, "The request contains an unsupported field.")
		}
		return NewError(CodeValidation, "The request body is not valid JSON.")
	}
	return nil
}

// SecurityHeaders sets the response headers that stop a browser from reinterpreting an API response
// as something it is not.
//
// These are not decoration. A JSON error body that a browser sniffs and renders as HTML is a
// reflected-XSS primitive; an API response a page can frame is a clickjacking surface; and any
// response cached by an intermediary may be another learner's order. The API is also the thing behind
// the web app's own proxy, so headers set here cover the native and proxy paths both.
func SecurityHeaders() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("X-Frame-Options", "DENY")
			h.Set("Referrer-Policy", "no-referrer")
			// Every response here can carry someone's data: orders, answers, scores. None of it may be
			// stored by a browser, a proxy or a shared cache.
			h.Set("Cache-Control", "no-store")
			next.ServeHTTP(w, r)
		})
	}
}

// ───────────────────────────── rate limiting ─────────────────────────────
//
// In-process sliding window. Production swaps the backing store for Redis behind the same method.

type window struct {
	hits []time.Time
}

// Limiter is a keyed sliding-window rate limiter.
type Limiter struct {
	mu      sync.Mutex
	windows map[string]*window
}

// NewLimiter returns an empty limiter.
func NewLimiter() *Limiter { return &Limiter{windows: map[string]*window{}} }

// Allow reports whether the key may proceed, with the seconds to wait when refused.
func (l *Limiter) Allow(key string, limit int, per time.Duration) (bool, int) {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()

	w, ok := l.windows[key]
	if !ok {
		w = &window{}
		l.windows[key] = w
	}
	kept := w.hits[:0]
	for _, hit := range w.hits {
		if now.Sub(hit) < per {
			kept = append(kept, hit)
		}
	}
	w.hits = kept
	if len(w.hits) >= limit {
		wait := int((per - now.Sub(w.hits[0])).Seconds()) + 1
		return false, wait
	}
	w.hits = append(w.hits, now)
	if len(l.windows) > 20000 { // crude bound so a hostile client cannot grow memory forever
		l.windows = map[string]*window{key: w}
	}
	return true, 0
}

// Limit returns a middleware enforcing a per-IP budget on a named route group.
func (l *Limiter) Limit(name string, limit int, per time.Duration) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := name + ":" + ClientIP(r)
			if ok, retry := l.Allow(key, limit, per); !ok {
				w.Header().Set("Retry-After", fmt.Sprintf("%d", retry))
				writeError(w, r, NewError(CodeRateLimited, "Too many requests. Please slow down and try again."))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

type trustProxyKey struct{}

// TrustProxy records whether forwarded headers may be believed, for the lifetime of one request.
//
// Forwarding headers are set by a proxy and are trivially spoofable by anyone who can reach the
// process directly. Believing them unconditionally means every rate limit in the system is keyed on a
// value the caller chooses: an attacker rotating `X-Forwarded-For` gets unlimited password attempts
// against a known address. So the default is to ignore them, and an operator behind a load balancer
// opts in once, at the edge.
func TrustProxy(enabled bool) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), trustProxyKey{}, enabled)))
		})
	}
}

// ClientIP returns the caller's address, believing forwarded headers only when the request was told
// a trusted proxy is in front of the process.
func ClientIP(r *http.Request) string {
	if trusted, _ := r.Context().Value(trustProxyKey{}).(bool); trusted {
		if v := r.Header.Get("X-Forwarded-For"); v != "" {
			if i := strings.Index(v, ","); i >= 0 {
				return strings.TrimSpace(v[:i])
			}
			return strings.TrimSpace(v)
		}
		if v := r.Header.Get("X-Real-Ip"); v != "" {
			return v
		}
	}
	host := r.RemoteAddr
	if i := strings.LastIndex(host, ":"); i > 0 {
		host = host[:i]
	}
	return host
}
