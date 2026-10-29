package auth

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

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

type ctxSessionKey struct{}

// WithSessionID stores the id of the session that authenticated this request.
func WithSessionID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxSessionKey{}, id)
}

// SessionIDFrom returns the current session id, or "" for anonymous requests.
func SessionIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(ctxSessionKey{}).(string)
	return id
}

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
		user, sessionID, err := h.svc.AuthenticateSession(r.Context(), r)
		if err != nil {
			h.logger.Error("authentication failed", "error", err.Error(), "request_id", httpx.RequestID(r.Context()))
			next.ServeHTTP(w, r)
			return
		}
		if user != nil {
			r = r.WithContext(WithUser(r.Context(), user))
			r = r.WithContext(WithSessionID(r.Context(), sessionID))
		}
		next.ServeHTTP(w, r)
	})
}

// Sessions handles GET /v1/me/sessions — the caller's active devices.
func (h *Handlers) Sessions(w http.ResponseWriter, r *http.Request) error {
	user, err := RequireUser(r)
	if err != nil {
		return err
	}
	sessions, err := h.svc.ListSessions(r.Context(), user.ID, SessionIDFrom(r.Context()))
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"sessions": sessions})
	return nil
}

// RevokeSession handles DELETE /v1/me/sessions/{id}.
func (h *Handlers) RevokeSession(w http.ResponseWriter, r *http.Request) error {
	user, err := RequireUser(r)
	if err != nil {
		return err
	}
	sessionID := chi.URLParam(r, "id")
	if sessionID == "" {
		return httpx.NewError(httpx.CodeValidation, "A session id is required.")
	}
	if err := h.svc.RevokeSessionForUser(r.Context(), user.ID, sessionID); err != nil {
		return err
	}
	// Revoking your own current session is allowed and ends this request's session too.
	current := sessionID == SessionIDFrom(r.Context())
	httpx.JSON(w, http.StatusOK, map[string]any{"revoked": true, "wasCurrent": current})
	return nil
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
	// The response is identical whether or not the address exists: telling an anonymous caller which
	// emails are registered is how account enumeration starts.
	exists := h.svc.RequestPasswordReset(r.Context(), in.Email)
	if exists {
		// The token itself is never logged — it is a credential. In development the courier's log
		// sender prints the message body, which is the same capability with a narrower blast radius.
		h.logger.Info("password reset queued", "email", NormalizeEmail(in.Email))
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

// RequestVerification handles POST /v1/auth/verification/request — send a code.
//
// The response never contains the code and never echoes the full destination: whoever can call this
// endpoint knows the account, and a full address in the response would turn a session into an
// address book.
func (h *Handlers) RequestVerification(w http.ResponseWriter, r *http.Request) error {
	user, err := RequireUser(r)
	if err != nil {
		return err
	}
	var in struct {
		Channel string `json:"channel"`
		Phone   string `json:"phone"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	issue, err := h.svc.RequestVerification(r.Context(), user, in.Channel, in.Phone)
	if err != nil {
		return err
	}
	h.logger.Info("verification code issued", "userId", user.ID, "channel", issue.Channel)
	httpx.JSON(w, http.StatusAccepted, map[string]any{
		"sent":             true,
		"channel":          issue.Channel,
		"destination":      issue.Destination,
		"expires_at":       issue.ExpiresAt,
		"resend_after":     issue.ResendAfterSeconds,
		"attempts_allowed": issue.AttemptsAllowed,
	})
	return nil
}

// ConfirmVerification handles POST /v1/auth/verification/confirm — check a code and record proof.
func (h *Handlers) ConfirmVerification(w http.ResponseWriter, r *http.Request) error {
	user, err := RequireUser(r)
	if err != nil {
		return err
	}
	var in struct {
		Channel string `json:"channel"`
		Code    string `json:"code"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	updated, err := h.svc.ConfirmVerification(r.Context(), user.ID, in.Channel, in.Code)
	if err != nil {
		return err
	}
	h.logger.Info("channel verified", "userId", user.ID, "channel", in.Channel)
	httpx.JSON(w, http.StatusOK, map[string]any{"verified": true, "channel": in.Channel, "user": updated})
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
