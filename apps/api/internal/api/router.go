// Package api assembles the HTTP surface. Every route is versioned under /v1.
package api

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/cors"

	"github.com/teamthy/edumart/apps/api/internal/admin"
	"github.com/teamthy/edumart/apps/api/internal/assessment"
	"github.com/teamthy/edumart/apps/api/internal/auth"
	"github.com/teamthy/edumart/apps/api/internal/classroom"
	"github.com/teamthy/edumart/apps/api/internal/commerce"
	"github.com/teamthy/edumart/apps/api/internal/content"
	"github.com/teamthy/edumart/apps/api/internal/httpx"
	"github.com/teamthy/edumart/apps/api/internal/insights"
	"github.com/teamthy/edumart/apps/api/internal/notify"
	"github.com/teamthy/edumart/apps/api/internal/orgs"
	"github.com/teamthy/edumart/apps/api/internal/rewards"
	"github.com/teamthy/edumart/apps/api/internal/signup"
	"github.com/teamthy/edumart/apps/api/internal/store"
)

// Config carries runtime settings that affect routing.
type Config struct {
	AllowedOrigins    []string
	AllowSandboxPay   bool
	SandboxWebhookKey string
}

// Deps are the constructed services the router needs.
type Deps struct {
	Store    *store.Store
	Auth     *auth.Service
	Items    *content.Service
	Exams    *assessment.Service
	Classes  *classroom.Service
	Shop     *commerce.Service
	Rewards  *rewards.Service
	Insights *insights.Service
	Staff    *admin.Service
	Signup   *signup.Service
	Orgs     *orgs.Service
	Notifier *notify.Service
	Logger   *slog.Logger
	Config   Config
}

// NewRouter builds the complete handler tree.
func NewRouter(d Deps) http.Handler {
	r := chi.NewRouter()
	limiter := httpx.NewLimiter()

	authHandlers := auth.NewHandlers(d.Auth, d.Logger)
	contentHandlers := content.NewHandlers(d.Items, d.Logger)
	assessmentHandlers := assessment.NewHandlers(d.Exams, d.Logger)
	classroomHandlers := classroom.NewHandlers(d.Classes)
	commerceHandlers := commerce.NewHandlers(d.Shop)
	rewardsHandlers := rewards.NewHandlers(d.Rewards)
	insightsHandlers := insights.NewHandlers(d.Insights)
	staffHandlers := admin.NewHandlers(d.Staff, d.Notifier).WithReminders(d.Notifier)
	signupHandlers := signup.NewHandlers(d.Signup)
	orgHandlers := orgs.NewHandlers(d.Orgs)

	r.Use(httpx.RequestIDMiddleware)
	r.Use(httpx.Recover(d.Logger))
	r.Use(httpx.AccessLog(d.Logger))
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   d.Config.AllowedOrigins,
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-Request-Id", "X-Edumart-Signature"},
		ExposedHeaders:   []string{"X-Request-Id", "Retry-After"},
		AllowCredentials: true,
		MaxAge:           300,
	}))
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(auth.WithLimiter(req.Context(), limiter)))
		})
	})
	r.Use(authHandlers.Middleware)

	// Liveness: cheap, no dependencies. Readiness: real checks, used by the load balancer.
	r.Get("/healthz", func(w http.ResponseWriter, req *http.Request) {
		httpx.JSON(w, http.StatusOK, map[string]any{"status": "ok", "time": store.Now(), "driver": d.Store.Driver})
	})
	r.Get("/readyz", readyzHandler(d.Store))

	r.Route("/v1", func(v1 chi.Router) {
		wrap := func(fn httpx.HandlerFunc) http.HandlerFunc { return httpx.Wrap(d.Logger, fn) }

		// ── identity ──────────────────────────────────────────────────────
		v1.Group(func(g chi.Router) {
			g.Use(limiter.Limit("auth", 30, time.Minute))
			g.Post("/auth/register", wrap(authHandlers.Register))
			g.Post("/auth/login", wrap(authHandlers.Login))
			g.Post("/auth/logout", wrap(authHandlers.Logout))
			g.Post("/auth/password/forgot", wrap(authHandlers.Forgot))
			g.Post("/auth/password/reset", wrap(authHandlers.Reset))
			g.Post("/auth/verification/request", wrap(authHandlers.RequestVerification))
			g.Post("/auth/verification/confirm", wrap(authHandlers.ConfirmVerification))
		})
		// Anonymous signup wizard: progress is persisted server-side before an account exists.
		v1.Get("/signup-drafts/current", wrap(signupHandlers.Current))
		v1.Post("/signup-drafts", wrap(signupHandlers.Create))
		v1.Patch("/signup-drafts/current", wrap(signupHandlers.Update))

		v1.Get("/me", wrap(authHandlers.Me))
		v1.Get("/me/sessions", wrap(authHandlers.Sessions))
		v1.Delete("/me/sessions/{id}", wrap(authHandlers.RevokeSession))
		v1.Patch("/me", wrap(authHandlers.UpdateMe))

		// ── taxonomy (public: needed before an account exists) ───────────
		v1.Get("/exam-pathways", wrap(contentHandlers.Pathways))
		v1.Get("/subjects", wrap(contentHandlers.Subjects))
		v1.Get("/subjects/{subjectId}/topics", wrap(contentHandlers.Topics))
		v1.Get("/referrals/validate/{code}", wrap(rewardsHandlers.ValidateCode))

		// ── learner home and analytics ───────────────────────────────────
		v1.Get("/dashboard", wrap(insightsHandlers.Dashboard))
		v1.Get("/learning-summary", wrap(insightsHandlers.Summary))
		v1.Get("/study-plan", wrap(insightsHandlers.GetPlan))
		v1.Put("/study-plan", wrap(insightsHandlers.SavePlan))

		// ── assessment engine ────────────────────────────────────────────
		v1.Group(func(g chi.Router) {
			g.Use(limiter.Limit("assessment", 120, time.Minute))
			g.Post("/practice-sessions", wrap(assessmentHandlers.StartPractice))
			g.Post("/assessment-definitions/{id}/sessions", wrap(assessmentHandlers.StartDefinition))
			g.Get("/assessment-sessions", wrap(assessmentHandlers.History))
			g.Get("/assessment-sessions/{id}", wrap(assessmentHandlers.Get))
			g.Put("/assessment-sessions/{id}/responses/{qvId}", wrap(assessmentHandlers.SaveResponse))
			g.Put("/assessment-sessions/{id}/corrections/{qvId}", wrap(assessmentHandlers.EditResponse))
			g.Post("/assessment-sessions/{id}/submit", wrap(assessmentHandlers.Submit))
			g.Post("/assessment-sessions/{id}/cancel", wrap(assessmentHandlers.Cancel))
		})

		// ── institutions ─────────────────────────────────────────────────
		// Membership, not a self-selected role, is what grants authority here. A non-member gets 404
		// rather than 403, so the API cannot be used to enumerate other organisations.
		v1.Group(func(g chi.Router) {
			g.Use(limiter.Limit("organizations", 60, time.Minute))
			g.Get("/organizations", wrap(orgHandlers.List))
			g.Post("/organizations", wrap(orgHandlers.Create))
			g.Get("/organizations/{id}", wrap(orgHandlers.Get))
			g.Get("/organizations/{id}/members", wrap(orgHandlers.Members))
			g.Post("/organizations/{id}/invitations", wrap(orgHandlers.Invite))
			g.Get("/organizations/{id}/invitations", wrap(orgHandlers.Invitations))
			g.Delete("/organizations/{id}/invitations/{invitationId}", wrap(orgHandlers.RevokeInvitation))
			g.Delete("/organizations/{id}/members/{userId}", wrap(orgHandlers.RemoveMember))
			g.Post("/organizations/invitations/accept", wrap(orgHandlers.Accept))
		})

		// ── classes ──────────────────────────────────────────────────────
		v1.Get("/classes", wrap(classroomHandlers.List))
		v1.Post("/classes", wrap(classroomHandlers.Create))
		v1.Post("/classes/join", wrap(classroomHandlers.Join))
		v1.Post("/classes/{id}/join-code", wrap(classroomHandlers.RotateJoinCode))
		v1.Get("/classes/{id}/roster", wrap(classroomHandlers.Roster))
		v1.Get("/classes/{id}/assignments", wrap(classroomHandlers.Assignments))
		v1.Get("/organizations/{id}/report", wrap(orgHandlers.Report))
		// Institution classes belong to the organisation's own administrators, not to whoever holds a
		// platform-wide teacher role.
		v1.Get("/organizations/{id}/classes", wrap(classroomHandlers.OrganizationClasses))

		// ── commerce ─────────────────────────────────────────────────────
		v1.Get("/products", wrap(commerceHandlers.Products))
		v1.Get("/orders", wrap(commerceHandlers.ListOrders))
		v1.Post("/orders", wrap(commerceHandlers.CreateOrder))
		v1.Get("/orders/{id}", wrap(commerceHandlers.GetOrder))
		v1.Get("/library", wrap(commerceHandlers.Library))
		if d.Config.AllowSandboxPay {
			v1.Post("/orders/{id}/simulate-payment", wrap(commerceHandlers.Simulate))
		}
		// Webhooks are authenticated by signature, never by session.
		v1.Post("/webhooks/payments/{provider}", wrap(commerceHandlers.Webhook))

		// ── rewards ──────────────────────────────────────────────────────
		v1.Get("/rewards", wrap(rewardsHandlers.Summary))
		v1.Post("/rewards/redeem", wrap(rewardsHandlers.Redeem))
		v1.Get("/referrals/me", wrap(rewardsHandlers.ReferralCode))

		// ── support and staff ────────────────────────────────────────────
		v1.Post("/support-cases", wrap(staffHandlers.OpenCase))
		v1.Get("/admin/review-queue", wrap(staffHandlers.ReviewQueue))
		v1.Post("/admin/question-versions/{id}/review", wrap(staffHandlers.Review))
		v1.Get("/admin/metrics", wrap(staffHandlers.Metrics))
		v1.Get("/admin/cases", wrap(staffHandlers.Cases))
		v1.Post("/admin/cases/{id}/resolve", wrap(staffHandlers.Resolve))
		v1.Get("/admin/organizations", wrap(orgHandlers.ApprovalQueue))
		v1.Post("/admin/organizations/{id}/verify", wrap(orgHandlers.Verify))
		v1.Post("/admin/organizations/{id}/reject", wrap(orgHandlers.Reject))
		// Account status is a moderation decision, so it lives with identity rather than with the
		// generic staff console: suspending also ends every session the account holds.
		v1.Get("/admin/users", wrap(authHandlers.Users))
		v1.Post("/admin/users/{id}/suspend", wrap(authHandlers.Suspend))
		v1.Post("/admin/users/{id}/reinstate", wrap(authHandlers.Reinstate))
		// Delivery health, so "the emails are not arriving" is answerable from the API instead of
		// from a mail provider's dashboard.
		v1.Get("/admin/deliveries", wrap(staffHandlers.Deliveries))
		v1.Get("/admin/reminders", wrap(staffHandlers.Reminders))
		v1.Post("/admin/reminders/sweep", wrap(staffHandlers.SweepReminders))
	})

	r.NotFound(func(w http.ResponseWriter, req *http.Request) {
		httpx.JSON(w, http.StatusNotFound, map[string]any{
			"error": map[string]any{
				"code":       httpx.CodeNotFound,
				"message":    "That endpoint does not exist.",
				"request_id": httpx.RequestID(req.Context()),
				"details":    []any{},
			},
		})
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, req *http.Request) {
		httpx.JSON(w, http.StatusMethodNotAllowed, map[string]any{
			"error": map[string]any{
				"code":       httpx.CodeValidation,
				"message":    "That method is not supported for this endpoint.",
				"request_id": httpx.RequestID(req.Context()),
				"details":    []any{},
			},
		})
	})
	return r
}
