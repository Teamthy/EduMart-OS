package auth

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/teamthy/edumart/apps/api/internal/httpx"
	"github.com/teamthy/edumart/apps/api/internal/metrics"
)

// Handlers wires the identity use cases to HTTP.
type Handlers struct {
	svc      *Service
	logger   *slog.Logger
	platform *metrics.Platform
}

// NewHandlers builds the identity HTTP layer.
func NewHandlers(svc *Service, logger *slog.Logger) *Handlers {
	return &Handlers{svc: svc, logger: logger}
}

// WithMetrics attaches the product metrics registry, so a spike in refused sign-ins is visible to an
// alert rather than only to whoever reads the log.
func (h *Handlers) WithMetrics(platform *metrics.Platform) *Handlers {
	h.platform = platform
	return h
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

// Users handles GET /v1/admin/users?q=&status=&limit= — the lookup that makes moderation usable.
func (h *Handlers) Users(w http.ResponseWriter, r *http.Request) error {
	if _, err := RequireRole(r, RolePlatformAdmin); err != nil {
		return err
	}
	query := r.URL.Query()
	limit := 25
	if raw := query.Get("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			limit = parsed
		}
	}
	users, err := h.svc.SearchUsers(r.Context(), query.Get("q"), query.Get("status"), limit)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"users": users})
	return nil
}

// Suspend handles POST /v1/admin/users/{id}/suspend. Platform administrators only.
//
// A reason is required: "why was this person locked out" is the first question support asks, and an
// unexplained suspension is indistinguishable from a bug.
func (h *Handlers) Suspend(w http.ResponseWriter, r *http.Request) error {
	actor, err := RequireRole(r, RolePlatformAdmin)
	if err != nil {
		return err
	}
	userID := chi.URLParam(r, "id")
	var in struct {
		Reason string `json:"reason"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	if len(strings.TrimSpace(in.Reason)) < 3 {
		return httpx.Validation([]httpx.FieldError{{Field: "reason", Message: "Say why the account is being suspended."}})
	}
	user, err := h.svc.Suspend(r.Context(), actor, userID, in.Reason, httpx.RequestID(r.Context()))
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"user": user})
	return nil
}

// Reinstate handles POST /v1/admin/users/{id}/reinstate.
func (h *Handlers) Reinstate(w http.ResponseWriter, r *http.Request) error {
	actor, err := RequireRole(r, RolePlatformAdmin)
	if err != nil {
		return err
	}
	var in struct {
		Note string `json:"note"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	user, err := h.svc.Reinstate(r.Context(), actor, chi.URLParam(r, "id"), in.Note, httpx.RequestID(r.Context()))
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"user": user})
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

// refreshCookie carries the single-use refresh token to the browser.
//
// Three attributes do the work, and dropping any one of them is a different defect:
//
//   - Path=/v1/auth — the token is sent to the three endpoints that need it and to nothing else, so it does
//     not ride along on every page request, static asset and API call for thirty days.
//   - HttpOnly — script cannot read it, so an XSS cannot exfiltrate a thirty-day credential.
//   - SameSite=Lax — another origin cannot trigger the refresh endpoint with the user's cookie.
//
// It is only set for the `web` client. A native app keeps the pair in the OS keystore and posts it, and
// setting a cookie it will never send would be a credential in a place nobody audits.
func refreshCookie(token string, expires time.Time, secure bool) *http.Cookie {
	return &http.Cookie{
		Name:     RefreshCookie,
		Value:    token,
		Path:     RefreshCookiePath,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		Expires:  expires,
		MaxAge:   int(time.Until(expires).Seconds()),
	}
}

// wantsCookies reports whether a client that declared itself `client` should be given cookies.
//
// The decision is the client's, not the transport's: an undeclared client is a browser (every request before
// this existed was one), and a native app says `client: "mobile"` and gets the pair in the body only.
// Inferring it from a User-Agent would be guesswork, and guessing wrong in the permissive direction puts a
// thirty-day credential in a cookie jar nobody meant to use.
func wantsCookies(declared string) bool {
	switch strings.ToLower(strings.TrimSpace(declared)) {
	case "mobile", "native":
		return false
	default:
		return true
	}
}

// clearCookie expires a cookie on the client. The path must match the one it was set with or the browser
// keeps the original: clearing `edumart_refresh` at Path=/ leaves the /v1/auth cookie alive, which is a
// sign-out that does not sign anyone out.
func clearCookie(name, path string, secure bool) *http.Cookie {
	return &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     path,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
	}
}

// sessionResponse writes the token pair and sets the browser cookies.
//
// Both cookies are set for a `web` client, because both are needed for the rotation to mean anything: an
// access cookie that outlives the session it belongs to makes /v1/auth/refresh unreachable, which is exactly
// the state this endpoint was in — rotating tokens with no client that ever rotated them. The refresh token
// is also still returned in the body, because a native client has no cookie jar and a browser that talks to
// the API directly keeps working.
func (h *Handlers) sessionResponse(w http.ResponseWriter, r *http.Request, status int, session *Session, withCookies bool, extra map[string]any) {
	secure := r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
	http.SetCookie(w, sessionCookie(session.Token, session.ExpiresAt, secure))
	if session.RefreshToken != "" && withCookies {
		http.SetCookie(w, refreshCookie(session.RefreshToken, session.RefreshExpiresAt, secure))
	}
	body := map[string]any{
		"token":              session.Token,
		"expires_at":         session.ExpiresAt,
		"refresh_token":      session.RefreshToken,
		"refresh_expires_at": session.RefreshExpiresAt,
		"user":               session.User,
	}
	for key, value := range extra {
		body[key] = value
	}
	httpx.JSON(w, status, body)
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
	h.sessionResponse(w, r, http.StatusCreated, session, wantsCookies(in.Client), map[string]any{"next": "/dashboard"})
	return nil
}

// isRateLimit reports whether the refusal was the limiter rather than a bad credential. Counting a
// throttled request as a failed sign-in would make a brute-force attempt look exponentially worse than it
// is, and would turn the alert into a report about our own limiter.
func isRateLimit(err error) bool {
	var apiErr *httpx.Error
	return errors.As(err, &apiErr) && apiErr.Code == httpx.CodeRateLimited
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
		// Counted without the address or the password, and without distinguishing "no such account" from
		// "wrong password": the metric must not become the membership oracle the response refuses to be.
		if h.platform != nil && !isRateLimit(err) {
			h.platform.LoginFailure()
		}
		return err
	}
	h.sessionResponse(w, r, http.StatusOK, session, wantsCookies(in.Client), map[string]any{"next": nextRoute(session.User)})
	return nil
}

// Refresh handles POST /v1/auth/refresh: exchange a refresh token for a new pair.
//
// The presented token is single-use. The response carries the successor, and the client must replace
// what it holds — keeping the old one only produces a SESSION_REUSED on the next attempt, and that is
// deliberate: a token used twice means two parties have it.
func (h *Handlers) Refresh(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		RefreshToken string `json:"refreshToken"`
	}
	// A body is optional so a client that keeps the token in storage can still post it explicitly.
	if r.ContentLength > 0 {
		if err := httpx.Decode(r, &in); err != nil {
			return err
		}
	}
	fromCookie := false
	if in.RefreshToken == "" {
		if cookie, err := r.Cookie(RefreshCookie); err == nil {
			in.RefreshToken = cookie.Value
			fromCookie = true
		}
	}

	limiter, ok := r.Context().Value(limiterKey{}).(*httpx.Limiter)
	if ok {
		key := "refresh:" + httpx.ClientIP(r)
		if allowed, retry := limiter.Allow(key, 30, time.Minute); !allowed {
			w.Header().Set("Retry-After", itoa(retry))
			return httpx.NewError(httpx.CodeRateLimited, "Too many attempts. Try again shortly.")
		}
	}

	session, err := h.svc.Refresh(r.Context(), in.RefreshToken, r.UserAgent())
	if err != nil {
		// A dead chain must clear the cookies it can no longer use. Otherwise the browser keeps presenting a
		// token that will fail forever, and every page load becomes a failed refresh followed by a sign-in
		// prompt that looks like a session bug rather than the deliberate revocation it is.
		secure := r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
		http.SetCookie(w, clearCookie(SessionCookie, "/", secure))
		http.SetCookie(w, clearCookie(RefreshCookie, RefreshCookiePath, secure))
		return err
	}
	// A rotation that arrived by cookie is answered by cookie: the browser cannot read an HttpOnly cookie, so
	// a body-only response would leave it holding a token this call has just retired. A rotation that arrived
	// in the body is answered in the body, because only a client that reads the body can have sent it.
	h.sessionResponse(w, r, http.StatusOK, session, fromCookie, nil)
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
	// Signing out must kill the chain, not just the row the access token names: otherwise the browser's
	// refresh cookie — or a thief's copy of it — can mint a new session a second later, and "sign out on
	// this device" is a suggestion.
	if cookie, err := r.Cookie(RefreshCookie); err == nil && cookie.Value != "" {
		if err := h.svc.RevokeByRefresh(r.Context(), cookie.Value); err != nil {
			return err
		}
	}
	secure := r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
	http.SetCookie(w, clearCookie(SessionCookie, "/", secure))
	http.SetCookie(w, clearCookie(RefreshCookie, RefreshCookiePath, secure))
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
