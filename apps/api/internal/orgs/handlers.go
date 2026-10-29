package orgs

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/teamthy/edumart/apps/api/internal/auth"
	"github.com/teamthy/edumart/apps/api/internal/httpx"
)

// Handlers wires institution use cases to HTTP.
type Handlers struct {
	svc *Service
}

// NewHandlers builds the organisation HTTP layer.
func NewHandlers(svc *Service) *Handlers { return &Handlers{svc: svc} }

// List handles GET /v1/organizations — the institutions the caller belongs to.
func (h *Handlers) List(w http.ResponseWriter, r *http.Request) error {
	user, err := auth.RequireUser(r)
	if err != nil {
		return err
	}
	organizations, err := h.svc.List(r.Context(), user.ID)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"organizations": organizations})
	return nil
}

// Create handles POST /v1/organizations.
func (h *Handlers) Create(w http.ResponseWriter, r *http.Request) error {
	user, err := auth.RequireUser(r)
	if err != nil {
		return err
	}
	var in struct {
		Name string `json:"name"`
		Kind string `json:"kind"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	org, err := h.svc.Create(r.Context(), user, in.Name, in.Kind)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"organization": org})
	return nil
}

// Get handles GET /v1/organizations/{id}.
func (h *Handlers) Get(w http.ResponseWriter, r *http.Request) error {
	user, err := auth.RequireUser(r)
	if err != nil {
		return err
	}
	org, err := h.svc.Get(r.Context(), user, chi.URLParam(r, "id"))
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"organization": org})
	return nil
}

// Members handles GET /v1/organizations/{id}/members.
func (h *Handlers) Members(w http.ResponseWriter, r *http.Request) error {
	user, err := auth.RequireUser(r)
	if err != nil {
		return err
	}
	members, err := h.svc.Members(r.Context(), user, chi.URLParam(r, "id"))
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"members": members})
	return nil
}

// Invite handles POST /v1/organizations/{id}/invitations.
//
// The token is returned here and never again: it is a credential, and there is no endpoint that can
// recover it. Delivery is the caller's problem (email in production), which keeps this endpoint
// usable from an admin console and testable without a mail server.
func (h *Handlers) Invite(w http.ResponseWriter, r *http.Request) error {
	user, err := auth.RequireUser(r)
	if err != nil {
		return err
	}
	var in struct {
		Email string `json:"email"`
		Role  string `json:"role"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	invitation, token, err := h.svc.Invite(r.Context(), user, chi.URLParam(r, "id"), in.Email, in.Role)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"invitation": invitation, "token": token})
	return nil
}

// Invitations handles GET /v1/organizations/{id}/invitations.
func (h *Handlers) Invitations(w http.ResponseWriter, r *http.Request) error {
	user, err := auth.RequireUser(r)
	if err != nil {
		return err
	}
	invitations, err := h.svc.Invitations(r.Context(), user, chi.URLParam(r, "id"))
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"invitations": invitations})
	return nil
}

// RevokeInvitation handles DELETE /v1/organizations/{id}/invitations/{invitationId}.
func (h *Handlers) RevokeInvitation(w http.ResponseWriter, r *http.Request) error {
	user, err := auth.RequireUser(r)
	if err != nil {
		return err
	}
	if err := h.svc.RevokeInvitation(r.Context(), user, chi.URLParam(r, "id"), chi.URLParam(r, "invitationId")); err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"revoked": true})
	return nil
}

// Accept handles POST /v1/organizations/invitations/accept.
func (h *Handlers) Accept(w http.ResponseWriter, r *http.Request) error {
	user, err := auth.RequireUser(r)
	if err != nil {
		return err
	}
	var in struct {
		Token string `json:"token"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	// Admins paste links, and links grow whitespace when copied. Trimming here is friendliness, not a
	// security decision — the token is still compared as a hash.
	org, err := h.svc.Accept(r.Context(), user, strings.TrimSpace(in.Token))
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"joined": true, "organization": org})
	return nil
}

// RemoveMember handles DELETE /v1/organizations/{id}/members/{userId}.
func (h *Handlers) RemoveMember(w http.ResponseWriter, r *http.Request) error {
	user, err := auth.RequireUser(r)
	if err != nil {
		return err
	}
	if err := h.svc.RemoveMember(r.Context(), user, chi.URLParam(r, "id"), chi.URLParam(r, "userId")); err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"removed": true})
	return nil
}

// Report handles GET /v1/organizations/{id}/report — the institution dashboard.
func (h *Handlers) Report(w http.ResponseWriter, r *http.Request) error {
	user, err := auth.RequireUser(r)
	if err != nil {
		return err
	}
	report, err := h.svc.Report(r.Context(), user, chi.URLParam(r, "id"))
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"report": report})
	return nil
}

// ApprovalQueue handles GET /v1/admin/organizations?status=&limit= — the staff review queue.
func (h *Handlers) ApprovalQueue(w http.ResponseWriter, r *http.Request) error {
	user, err := auth.RequireRole(r, auth.RolePlatformAdmin)
	if err != nil {
		return err
	}
	limit := 25
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if parsed, convErr := strconv.Atoi(raw); convErr == nil {
			limit = parsed
		}
	}
	organizations, err := h.svc.ApprovalQueue(r.Context(), user, r.URL.Query().Get("status"), limit)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"organizations": organizations})
	return nil
}

// Reject handles POST /v1/admin/organizations/{id}/reject.
func (h *Handlers) Reject(w http.ResponseWriter, r *http.Request) error {
	user, err := auth.RequireRole(r, auth.RolePlatformAdmin)
	if err != nil {
		return err
	}
	var in struct {
		Reason string `json:"reason"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	organization, err := h.svc.Reject(r.Context(), user, chi.URLParam(r, "id"), in.Reason)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"organization": organization})
	return nil
}

// Verify handles POST /v1/admin/organizations/{id}/verify.
func (h *Handlers) Verify(w http.ResponseWriter, r *http.Request) error {
	user, err := auth.RequireRole(r, auth.RolePlatformAdmin)
	if err != nil {
		return err
	}
	org, err := h.svc.Verify(r.Context(), user, chi.URLParam(r, "id"))
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"organization": org})
	return nil
}
