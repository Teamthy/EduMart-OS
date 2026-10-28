package content

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/teamthy/edumart/apps/api/internal/httpx"
)

// Handlers exposes taxonomy endpoints.
type Handlers struct {
	svc    *Service
	logger *slog.Logger
}

// NewHandlers builds the content HTTP layer.
func NewHandlers(svc *Service, logger *slog.Logger) *Handlers {
	return &Handlers{svc: svc, logger: logger}
}

// Pathways handles GET /v1/exam-pathways.
func (h *Handlers) Pathways(w http.ResponseWriter, r *http.Request) error {
	pathways, err := h.svc.Pathways(r.Context())
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"pathways": pathways})
	return nil
}

// Subjects handles GET /v1/subjects?pathwayKey=.
func (h *Handlers) Subjects(w http.ResponseWriter, r *http.Request) error {
	subjects, err := h.svc.Subjects(r.Context(), r.URL.Query().Get("pathwayKey"))
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"subjects": subjects})
	return nil
}

// Topics handles GET /v1/subjects/{subjectId}/topics.
func (h *Handlers) Topics(w http.ResponseWriter, r *http.Request) error {
	subjectID := chi.URLParam(r, "subjectId")
	subject, err := h.svc.SubjectByID(r.Context(), subjectID)
	if err != nil {
		return err
	}
	topics, err := h.svc.Topics(r.Context(), subjectID)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"subject": subject, "topics": topics})
	return nil
}
