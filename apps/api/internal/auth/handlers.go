package auth

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/teamthy/edumart/apps/api/internal/httpx"
)

// Handlers wires the identity use cases to HTTP.
type Handlers struct {
	svc    *Service
	logger *slog.Logger
}

// NewHandlers builds the identity HTTP layer.
func NewHandlers(svc *Service, logger *slog.Logger) *Handlers {
	return &Handlers{svc: svc, logger: logger}
}

type ctxUserKey struct{}

// WithUser stores the authenticated principal on the request context.
func WithUser(ctx context.Context, user *User) context.Context {
	return context.WithValue(ctx, ctxUserKey{}, user)
}

// UserFrom returns the principal attached by the auth middleware, if any.
func UserFrom(ctx context.Context) *User {
	user, _ := ctx.Value(ctxUserKey{}).(*User)
	return user
}

// RequireUser returns the principal or a 401 error.
func RequireUser(r *http.Request) (*User, error) {
	user := UserFrom(r.Context())
	if user == nil {
		return nil, httpx.NewError(httpx.CodeUnauthenticated, "Please sign in to continue.")
	}
	return user, nil
}

// RequireRole enforces at least one of the given roles.
func RequireRole(r *http.Request, roles ...string) (*User, error) {
	user, err := RequireUser(r)
	if err != nil {
		return nil, err
	}
	if !user.HasAnyRole(roles...) {
		return nil, httpx.NewError(httpx.CodeForbidden, "You do not have permission to perform this action.")
	}
	return user, nil
}

// Middleware resolves the session on every request and attaches the principal when present.
// Authentication is optional here: individual routes decide whether it is required.
func (h *Handlers) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, err := h.svc.Authenticate(r.Context(), r)
		if err != nil {
			h.logger.Error("authentication failed", "error", err.Error(), "request_id", httpx.RequestID(r.Context()))
			next.ServeHTTP(w, r)
			return
		}
		if user != nil {
			r = r.WithContext(WithUser(r.Context(), user))
		}
		next.ServeHTTP(w, r)
	})
}

func sessionCookie(token string, expires time.Time, secure bool) *http.Cookie {
	return &http.Cookie{
		Name:     SessionCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		Expires:  expires,
		MaxAge:   int(time.Until(expires).Seconds()),
	}
}

// Register handles POST /v1/auth/register.
func (h *Handlers) Register(w http.ResponseWriter, r *http.Request) error {
	var in RegisterInput
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	session, err := h.svc.Register(r.Context(), in, r.UserAgent())
	if err != nil {
		return err
	}
	secure := r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
	http.SetCookie(w, sessionCookie(session.Token, session.ExpiresAt, secure))
	httpx.JSON(w, http.StatusCreated, map[string]any{
		"token":      session.Token,
		"expires_at": session.ExpiresAt,
		"user":       session.User,
		"next":       "/dashboard",
	})
	return nil
}

// Login handles POST /v1/auth/login.
func (h *Handlers) Login(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		Client   string `json:"client"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}

	limiter, ok := r.Context().Value(limiterKey{}).(*httpx.Limiter)
	if ok {
		key := "login:" + httpx.ClientIP(r) + ":" + NormalizeEmail(in.Email)
		if allowed, retry := limiter.Allow(key, 8, time.Minute); !allowed {
			w.Header().Set("Retry-After", itoa(retry))
			return httpx.NewError(httpx.CodeRateLimited, "Too many attempts. Try again shortly.")
		}
	}

	session, err := h.svc.Login(r.Context(), in.Email, in.Password, r.UserAgent(), in.Client)
	if err != nil {
		return err
	}
	secure := r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
	http.SetCookie(w, sessionCookie(session.Token, session.ExpiresAt, secure))
	httpx.JSON(w, http.StatusOK, map[string]any{
		"token":      session.Token,
		"expires_at": session.ExpiresAt,
		"user":       session.User,
		"next":       nextRoute(session.User),
	})
	return nil
}

func nextRoute(user *User) string {
	switch {
	case user.HasRole(RolePlatformAdmin), user.HasRole(RoleContentEditor):
		return "/admin"
	case user.HasAnyRole(RoleTeacher, RoleOrgAdmin):
		return "/teacher"
	default:
		return "/dashboard"
	}
}

type limiterKey struct{}

// WithLimiter exposes the shared limiter to handlers that need finer-grained keys.
func WithLimiter(ctx context.Context, l *httpx.Limiter) context.Context {
	return context.WithValue(ctx, limiterKey{}, l)
}

// Logout handles POST /v1/auth/logout.
func (h *Handlers) Logout(w http.ResponseWriter, r *http.Request) error {
	token := ""
	if cookie, err := r.Cookie(SessionCookie); err == nil {
		token = cookie.Value
	}
	if token == "" {
		if header := r.Header.Get("Authorization"); len(header) > 7 {
			token = header[7:]
		}
	}
	if token != "" {
		if err := h.svc.Revoke(r.Context(), token); err != nil {
			return err
		}
	}
	http.SetCookie(w, sessionCookie("", time.Now().Add(-time.Hour), true))
	httpx.JSON(w, http.StatusOK, map[string]any{"signedOut": true})
	return nil
}

// Forgot handles POST /v1/auth/password/forgot.
func (h *Handlers) Forgot(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Email string `json:"email"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	token, exists := h.svc.RequestPasswordReset(r.Context(), in.Email)
	if exists {
		// A notification worker would deliver this. In development the token is logged so the
		// flow remains testable without an email provider.
		h.logger.Info("password reset requested", "email", NormalizeEmail(in.Email), "token", token)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"sent":    true,
		"message": "If that email is registered, a reset link is on its way.",
	})
	return nil
}

// Reset handles POST /v1/auth/password/reset.
func (h *Handlers) Reset(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	if in.Token == "" {
		return httpx.NewError(httpx.CodeValidation, "This reset link is incomplete.")
	}
	if err := h.svc.ResetPassword(r.Context(), in.Token, in.Password); err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"reset": true})
	return nil
}

// Me handles GET /v1/me.
func (h *Handlers) Me(w http.ResponseWriter, r *http.Request) error {
	user, err := RequireUser(r)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"user": user})
	return nil
}

// UpdateMe handles PATCH /v1/me, used by settings and by the signup wizard for progressive save.
func (h *Handlers) UpdateMe(w http.ResponseWriter, r *http.Request) error {
	user, err := RequireUser(r)
	if err != nil {
		return err
	}
	var in UpdateProfileInput
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	updated, err := h.svc.UpdateProfile(r.Context(), user.ID, in)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"user": updated})
	return nil
}

func itoa(v int) string {
	if v <= 0 {
		return "1"
	}
	digits := ""
	for v > 0 {
		digits = string(rune('0'+v%10)) + digits
		v /= 10
	}
	return digits
}
