package search

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/teamthy/edumart/apps/api/internal/auth"
	"github.com/teamthy/edumart/apps/api/internal/httpx"
)

// Handlers exposes search over HTTP.
type Handlers struct {
	svc    *Service
	logger *slog.Logger
}

// NewHandlers builds the HTTP layer.
func NewHandlers(svc *Service, logger *slog.Logger) *Handlers {
	return &Handlers{svc: svc, logger: logger}
}

// Search handles GET /v1/search?q=&type=&limit=.
//
// Public on purpose: the catalogue and the syllabus are what a prospective learner is looking for, and
// requiring an account to find out whether the product exists would be a strange way to sell it. The
// endpoint therefore re-checks visibility against the source tables — see Service.visible.
func (h *Handlers) Search(w http.ResponseWriter, r *http.Request) error {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if len([]rune(query)) > 200 {
		return httpx.Validation([]httpx.FieldError{{Field: "q", Message: "Search terms are limited to 200 characters."}})
	}

	limit := 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 {
			return httpx.Validation([]httpx.FieldError{{Field: "limit", Message: "Use a whole number between 1 and 50."}})
		}
		limit = parsed
	}

	entityType := r.URL.Query().Get("type")
	switch entityType {
	case "", "all", "product", "subject", "topic":
	default:
		return httpx.Validation([]httpx.FieldError{{Field: "type", Message: "Use all, product, subject or topic."}})
	}

	started := time.Now()
	results, terms, err := h.svc.Search(r.Context(), query, entityType, limit)
	if errors.Is(err, ErrEmptyQuery) {
		// Not an error the client should treat as a failure: an empty box returning zero results is the
		// honest answer, and it keeps the client's error path for actual failures.
		httpx.JSON(w, http.StatusOK, map[string]any{
			"query": query, "terms": []string{}, "results": []Result{}, "count": 0, "took_ms": 0,
		})
		return nil
	}
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"query":   query,
		"terms":   terms,
		"results": results,
		"count":   len(results),
		// How long the server spent, not how long the round trip took: it is the number that answers
		// "is this slow because of us".
		"took_ms": time.Since(started).Milliseconds(),
	})
	return nil
}

// Reindex handles POST /v1/admin/search/reindex.
//
// A full rebuild rather than an incremental patch, so there is exactly one code path that decides what is
// searchable. It takes a lock-shaped lock on nothing else, which is why it is safe to call repeatedly: the
// rebuild replaces the projection inside one transaction.
func (h *Handlers) Reindex(w http.ResponseWriter, r *http.Request) error {
	user, err := auth.RequireRole(r, "platform_admin")
	if err != nil {
		return err
	}
	stats, err := h.svc.Reindex(r.Context())
	if err != nil {
		return err
	}
	h.logger.Info("search reindexed",
		"documents", stats.Documents, "tokens", stats.Tokens, "by", user.ID,
		"request_id", httpx.RequestID(r.Context()))
	httpx.JSON(w, http.StatusOK, map[string]any{"search": stats})
	return nil
}
