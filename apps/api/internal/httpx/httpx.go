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
	"strings"
	"sync"
	"time"
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
	CodeInternal         ErrorCode = "INTERNAL_ERROR"
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
	err := &Error{Code: code, Message: message, Status: statusFor[code]}
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

// ClientIP prefers the proxy-provided client address and falls back to the socket address.
func ClientIP(r *http.Request) string {
	if v := r.Header.Get("X-Forwarded-For"); v != "" {
		if i := strings.Index(v, ","); i >= 0 {
			return strings.TrimSpace(v[:i])
		}
		return strings.TrimSpace(v)
	}
	if v := r.Header.Get("X-Real-Ip"); v != "" {
		return v
	}
	host := r.RemoteAddr
	if i := strings.LastIndex(host, ":"); i > 0 {
		host = host[:i]
	}
	return host
}
