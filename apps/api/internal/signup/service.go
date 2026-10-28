// Package signup owns the pre-account half of the signup wizard.
//
// The wizard spans five pages. Progress lives in `signup_drafts` keyed by an opaque cookie (web)
// or a token held in secure storage (mobile), which means:
//   - a refresh, a crash or a dropped connection resumes on the same step;
//   - the same draft can be continued from the other shell;
//   - the final step writes one account from a single, already-validated payload.
package signup

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/teamthy/edumart/apps/api/internal/httpx"
	"github.com/teamthy/edumart/apps/api/internal/store"
)

// CookieName carries the draft token for the web wizard.
const CookieName = "edumart_signup"

const draftTTL = 30 * 24 * time.Hour

// Steps is the canonical order. The API rejects a step it does not recognise so a stale client
// cannot corrupt its own progress.
var Steps = []string{"pathway", "subjects", "schedule", "account", "confirm"}

// Draft is the persisted wizard state.
type Draft struct {
	Token        string  `json:"-"`
	CurrentStep  string  `json:"currentStep"`
	Draft        Payload `json:"draft"`
	ReferralCode string  `json:"referralCode"`
	CreatedAt    string  `json:"createdAt"`
	UpdatedAt    string  `json:"updatedAt"`
	Completed    bool    `json:"completed"`
}

// Payload holds every answer the wizard collects, all optional while in progress.
type Payload struct {
	ExamPathwayID  string   `json:"examPathwayId"`
	ExamPathwayKey string   `json:"examPathwayKey"`
	SubjectIDs     []string `json:"subjectIds"`
	TargetDate     string   `json:"targetDate"`
	DailyMinutes   int      `json:"dailyMinutes"`
	DisplayName    string   `json:"displayName"`
	Email          string   `json:"email"`
	Timezone       string   `json:"timezone"`
}

// Patch applies a partial update. Only provided fields change, so two devices editing different
// steps do not erase each other's answers.
type Patch struct {
	CurrentStep    *string   `json:"currentStep"`
	ExamPathwayID  *string   `json:"examPathwayId"`
	ExamPathwayKey *string   `json:"examPathwayKey"`
	SubjectIDs     *[]string `json:"subjectIds"`
	TargetDate     *string   `json:"targetDate"`
	DailyMinutes   *int      `json:"dailyMinutes"`
	DisplayName    *string   `json:"displayName"`
	Email          *string   `json:"email"`
	Timezone       *string   `json:"timezone"`
	ReferralCode   *string   `json:"referralCode"`
}

// Service stores and reads drafts.
type Service struct{ db *store.Store }

// New builds the signup service.
func New(db *store.Store) *Service { return &Service{db: db} }

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func newToken() string {
	raw := make([]byte, 32)
	for i := range raw {
		if _, err := rand.Read(raw[i : i+1]); err != nil {
			raw[i] = byte(time.Now().UnixNano() >> (i % 8))
		}
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

// Create starts (or restarts) a draft and returns it with its token.
func (s *Service) Create(ctx context.Context, referralCode string) (*Draft, error) {
	token := newToken()
	now := store.Now()
	payload := Payload{Timezone: "Africa/Lagos", DailyMinutes: 30, SubjectIDs: []string{}}
	raw, _ := json.Marshal(payload)

	_, err := s.db.DB.ExecContext(ctx, `INSERT INTO signup_drafts
		(id, token_hash, draft_json, current_step, referral_code, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?)`,
		store.NewID(), hashToken(token), string(raw), Steps[0], strings.ToUpper(strings.TrimSpace(referralCode)), now, now)
	if err != nil {
		return nil, err
	}
	return s.GetByToken(ctx, token)
}

// GetByToken loads a draft, reporting nil when the token is unknown or expired.
func (s *Service) GetByToken(ctx context.Context, token string) (*Draft, error) {
	if token == "" {
		return nil, nil
	}
	var (
		draftJSON, step, referral, createdAt, updatedAt string
		completedAt                                     sql.NullString
	)
	err := s.db.DB.QueryRowContext(ctx, `SELECT draft_json, current_step, referral_code, created_at, updated_at,
		completed_at FROM signup_drafts WHERE token_hash = ?`, hashToken(token)).
		Scan(&draftJSON, &step, &referral, &createdAt, &updatedAt, &completedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	created, err := store.ParseTS(createdAt)
	if err != nil || time.Now().UTC().Sub(created) > draftTTL {
		return nil, nil
	}

	draft := &Draft{
		Token:        token,
		CurrentStep:  step,
		ReferralCode: referral,
		CreatedAt:    createdAt,
		UpdatedAt:    updatedAt,
		Completed:    completedAt.Valid,
	}
	if err := json.Unmarshal([]byte(draftJSON), &draft.Draft); err != nil {
		draft.Draft = Payload{Timezone: "Africa/Lagos", DailyMinutes: 30, SubjectIDs: []string{}}
	}
	if draft.Draft.SubjectIDs == nil {
		draft.Draft.SubjectIDs = []string{}
	}
	return draft, nil
}

// Update merges a patch and records the step the learner is now on.
func (s *Service) Update(ctx context.Context, token string, patch Patch) (*Draft, error) {
	draft, err := s.GetByToken(ctx, token)
	if err != nil {
		return nil, err
	}
	if draft == nil {
		return nil, httpx.NewError(httpx.CodeUnauthenticated, "Your signup session expired. Please start again.")
	}

	payload := draft.Draft
	step := draft.CurrentStep
	referral := draft.ReferralCode

	if patch.CurrentStep != nil {
		if !validStep(*patch.CurrentStep) {
			return nil, httpx.Validation([]httpx.FieldError{{Field: "currentStep", Message: "Unknown signup step."}})
		}
		step = *patch.CurrentStep
	}
	if patch.ExamPathwayID != nil {
		payload.ExamPathwayID = *patch.ExamPathwayID
	}
	if patch.ExamPathwayKey != nil {
		payload.ExamPathwayKey = *patch.ExamPathwayKey
	}
	if patch.SubjectIDs != nil {
		payload.SubjectIDs = *patch.SubjectIDs
	}
	if patch.TargetDate != nil {
		payload.TargetDate = *patch.TargetDate
	}
	if patch.DailyMinutes != nil {
		if *patch.DailyMinutes < 10 || *patch.DailyMinutes > 240 {
			return nil, httpx.Validation([]httpx.FieldError{{Field: "dailyMinutes", Message: "Choose between 10 and 240 minutes."}})
		}
		payload.DailyMinutes = *patch.DailyMinutes
	}
	if patch.DisplayName != nil {
		payload.DisplayName = strings.TrimSpace(*patch.DisplayName)
	}
	if patch.Email != nil {
		payload.Email = strings.TrimSpace(*patch.Email)
	}
	if patch.Timezone != nil && *patch.Timezone != "" {
		payload.Timezone = *patch.Timezone
	}
	if patch.ReferralCode != nil {
		referral = strings.ToUpper(strings.TrimSpace(*patch.ReferralCode))
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	if _, err := s.db.DB.ExecContext(ctx, `UPDATE signup_drafts SET draft_json = ?, current_step = ?,
		referral_code = ?, updated_at = ? WHERE token_hash = ?`,
		string(raw), step, referral, store.Now(), hashToken(token)); err != nil {
		return nil, err
	}
	return s.GetByToken(ctx, token)
}

// Complete marks the draft as used by the newly created account.
func (s *Service) Complete(ctx context.Context, token, userID string) error {
	if token == "" {
		return nil
	}
	_, err := s.db.DB.ExecContext(ctx, `UPDATE signup_drafts SET completed_user_id = ?, completed_at = ?, updated_at = ?
		WHERE token_hash = ?`, userID, store.Now(), store.Now(), hashToken(token))
	return err
}

// StepIndex returns the 1-based position of a step, for "Step 2 of 5".
func StepIndex(step string) int {
	for i, candidate := range Steps {
		if candidate == step {
			return i + 1
		}
	}
	return 1
}

func validStep(step string) bool {
	for _, candidate := range Steps {
		if candidate == step {
			return true
		}
	}
	return false
}

// Handlers exposes the draft endpoints.
type Handlers struct{ svc *Service }

// NewHandlers builds signup HTTP handlers.
func NewHandlers(svc *Service) *Handlers { return &Handlers{svc: svc} }

func draftCookie(token string) *http.Cookie {
	return &http.Cookie{
		Name:     CookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(draftTTL.Seconds()),
	}
}

// Current handles GET /v1/signup-drafts/current.
func (h *Handlers) Current(w http.ResponseWriter, r *http.Request) error {
	token := draftToken(r)
	if token == "" {
		httpx.JSON(w, http.StatusOK, map[string]any{"draft": nil, "step": nil, "stepIndex": 0})
		return nil
	}
	draft, err := h.svc.GetByToken(r.Context(), token)
	if err != nil {
		return err
	}
	if draft == nil {
		httpx.JSON(w, http.StatusOK, map[string]any{"draft": nil, "step": nil, "stepIndex": 0})
		return nil
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"draft": draft.Draft, "step": draft.CurrentStep,
		"stepIndex": StepIndex(draft.CurrentStep), "referralCode": draft.ReferralCode,
	})
	return nil
}

// Create handles POST /v1/signup-drafts.
func (h *Handlers) Create(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		ReferralCode string `json:"referralCode"`
	}
	if err := httpx.Decode(r, &in); err != nil && err.Error() != "VALIDATION_ERROR: The request body is not valid JSON." {
		// An empty body is fine: the wizard may start without a referral link.
		if !strings.Contains(err.Error(), "not valid JSON") {
			return err
		}
	}
	draft, err := h.svc.Create(r.Context(), in.ReferralCode)
	if err != nil {
		return err
	}
	http.SetCookie(w, draftCookie(draft.Token))
	httpx.JSON(w, http.StatusCreated, map[string]any{
		"token": draft.Token, "draft": draft.Draft, "step": draft.CurrentStep,
		"stepIndex": StepIndex(draft.CurrentStep), "referralCode": draft.ReferralCode,
	})
	return nil
}

// Update handles PATCH /v1/signup-drafts/current.
func (h *Handlers) Update(w http.ResponseWriter, r *http.Request) error {
	token := draftToken(r)
	var patch Patch
	if err := httpx.Decode(r, &patch); err != nil {
		return err
	}
	draft, err := h.svc.Update(r.Context(), token, patch)
	if err != nil {
		return err
	}
	http.SetCookie(w, draftCookie(draft.Token))
	httpx.JSON(w, http.StatusOK, map[string]any{
		"draft": draft.Draft, "step": draft.CurrentStep,
		"stepIndex": StepIndex(draft.CurrentStep), "referralCode": draft.ReferralCode,
	})
	return nil
}

func draftToken(r *http.Request) string {
	if cookie, err := r.Cookie(CookieName); err == nil && cookie.Value != "" {
		return cookie.Value
	}
	if header := r.Header.Get("Authorization"); strings.HasPrefix(strings.ToLower(header), "bearer ") {
		return strings.TrimSpace(header[7:])
	}
	if header := r.Header.Get("X-Signup-Token"); header != "" {
		return header
	}
	return ""
}
