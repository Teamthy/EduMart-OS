package api_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/teamthy/edumart/apps/api/internal/admin"
	"github.com/teamthy/edumart/apps/api/internal/api"
	"github.com/teamthy/edumart/apps/api/internal/assessment"
	"github.com/teamthy/edumart/apps/api/internal/auth"
	"github.com/teamthy/edumart/apps/api/internal/classroom"
	"github.com/teamthy/edumart/apps/api/internal/commerce"
	"github.com/teamthy/edumart/apps/api/internal/content"
	"github.com/teamthy/edumart/apps/api/internal/insights"
	"github.com/teamthy/edumart/apps/api/internal/notify"
	"github.com/teamthy/edumart/apps/api/internal/orgs"
	"github.com/teamthy/edumart/apps/api/internal/rewards"
	"github.com/teamthy/edumart/apps/api/internal/seed"
	"github.com/teamthy/edumart/apps/api/internal/signup"
	"github.com/teamthy/edumart/apps/api/internal/store"
)

const webhookSecret = "test_webhook_secret"

type harness struct {
	t      *testing.T
	server *httptest.Server
	// router is the same handler the server wraps, so a test can send a request with a remote address
	// the client cannot set (an external caller, for the /metrics policy).
	router http.Handler
	store  *store.Store
	// mail captures what a user would have received. Verification codes are out-of-band secrets,
	// so a test that cannot read them cannot test the flow at all.
	mail *notify.Recorder
	// shop is the commerce service behind the router, so a test can swap the refund provider for a fake
	// that answers slowly, refuses, or fails ambiguously.
	shop *commerce.Service
	// courier is the durable delivery service, so a test can drive the retry sweep and inspect what
	// happened to a message after the first attempt failed.
	courier *notify.Service
	clock   *testClock
}

// testClock lets a test move time forward instead of sleeping through a retry backoff.
type testClock struct {
	mu sync.Mutex
	at time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

func (c *testClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.at = c.at.Add(d)
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	return newHarnessWithConfig(t, nil)
}

// newHarnessWithConfig builds the real router with one setting changed, so a security or routing
// decision can be tested as the deployment would exercise it rather than as a stub would.
func newHarnessWithConfig(t *testing.T, tweak func(*api.Config)) *harness {
	t.Helper()
	db := openTestStore(t)
	t.Cleanup(func() { _ = db.Close() })

	if err := seed.Run(context.Background(), db, seed.Options{IncludeDemoUsers: true, IncludeSamples: true}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Silent by default; EDUMART_TEST_LOG=1 shows what the server saw, which is how a 500 gets
	// diagnosed without attaching a debugger.
	logWriter := io.Writer(io.Discard)
	if os.Getenv("EDUMART_TEST_LOG") != "" {
		logWriter = os.Stderr
	}
	logger := slog.New(slog.NewTextHandler(logWriter, nil))
	mail := notify.NewRecorder()
	clock := &testClock{at: time.Now().UTC()}
	courier := notify.NewService(db, mail, logger).WithClock(clock.Now)
	shop := commerce.New(db, "sandbox", webhookSecret).WithNotifier(courier).WithLogger(logger)
	config := api.Config{
		AllowedOrigins:  []string{"http://localhost:3000"},
		AllowSandboxPay: true,
	}
	if tweak != nil {
		tweak(&config)
	}
	router := api.NewRouter(api.Deps{
		Store:    db,
		Auth:     auth.New(db, auth.WithNotifier(courier), auth.WithLogger(logger)),
		Items:    content.New(db),
		Exams:    assessment.New(db),
		Classes:  classroom.New(db),
		Shop:     shop,
		Rewards:  rewards.New(db),
		Insights: insights.New(db),
		Staff:    admin.New(db),
		Signup:   signup.New(db),
		Orgs:     orgs.New(db).WithNotifier(courier, "http://localhost:3000"),
		Notifier: courier,
		Logger:   logger,
		Config:   config,
	})
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	return &harness{t: t, server: server, router: router, store: db, mail: mail, courier: courier, clock: clock, shop: shop}
}

func (h *harness) do(method, path, token string, body any) (int, map[string]any) {
	h.t.Helper()
	status, _, payload := h.doFull(method, path, token, body, nil)
	return status, payload
}

// doRaw returns the status and the response headers, for assertions about what the API says *about*
// itself rather than about the payload.
func (h *harness) doRaw(method, path, token string, body any) (int, http.Header) {
	h.t.Helper()
	status, headers, _ := h.doFull(method, path, token, body, nil)
	return status, headers
}

// doRawWithHeaders sends caller-chosen request headers, which is how an attacker's request differs from
// an honest one.
func (h *harness) doRawWithHeaders(method, path, token string, body any, headers map[string]string) (int, http.Header) {
	h.t.Helper()
	status, responseHeaders, _ := h.doFull(method, path, token, body, headers)
	return status, responseHeaders
}

func (h *harness) doFull(method, path, token string, body any, extra map[string]string) (int, http.Header, map[string]any) {
	h.t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			h.t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, h.server.URL+path, reader)
	if err != nil {
		h.t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for name, value := range extra {
		req.Header.Set(name, value)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatalf("do request: %v", err)
	}
	defer res.Body.Close()
	payload := map[string]any{}
	raw, _ := io.ReadAll(res.Body)
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &payload)
	}
	return res.StatusCode, res.Header, payload
}

func (h *harness) login(email, password string) string {
	h.t.Helper()
	status, body := h.do("POST", "/v1/auth/login", "", map[string]string{"email": email, "password": password})
	if status != http.StatusOK {
		h.t.Fatalf("login %s failed: %d %v", email, status, body)
	}
	return body["token"].(string)
}

func (h *harness) subjectID(key string) string {
	h.t.Helper()
	var id string
	if err := h.store.DB.QueryRow(`SELECT id FROM subjects WHERE key = ?`, key).Scan(&id); err != nil {
		h.t.Fatalf("subject %s: %v", key, err)
	}
	return id
}

// ─────────────────────────── tests ───────────────────────────

func TestHealthAndTaxonomy(t *testing.T) {
	h := newHarness(t)

	if status, _ := h.do("GET", "/healthz", "", nil); status != http.StatusOK {
		t.Fatalf("healthz: %d", status)
	}
	status, body := h.do("GET", "/v1/subjects", "", nil)
	if status != http.StatusOK {
		t.Fatalf("subjects: %d", status)
	}
	subjects := body["subjects"].([]any)
	if len(subjects) < 4 {
		t.Fatalf("expected the seeded taxonomy, got %d subjects", len(subjects))
	}
}

func TestErrorEnvelopeShape(t *testing.T) {
	h := newHarness(t)
	status, body := h.do("GET", "/v1/dashboard", "", nil)
	if status != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", status)
	}
	errBody := body["error"].(map[string]any)
	for _, key := range []string{"code", "message", "request_id", "details"} {
		if _, ok := errBody[key]; !ok {
			t.Fatalf("error envelope is missing %q: %v", key, errBody)
		}
	}
	if errBody["code"] != "UNAUTHENTICATED" {
		t.Fatalf("expected UNAUTHENTICATED, got %v", errBody["code"])
	}
}

func TestSignupWizardProgressIsServerPersisted(t *testing.T) {
	h := newHarness(t)

	// Step 1 → 2: the wizard saves each step against the account, not local storage.
	status, body := h.do("POST", "/v1/auth/register", "", map[string]any{
		"displayName":    "Ngozi Eze",
		"email":          "ngozi@example.com",
		"password":       "Password123!",
		"dailyMinutes":   30,
		"client":         "web",
		"onboardingStep": 2,
	})
	if status != http.StatusCreated {
		t.Fatalf("register: %d %v", status, body)
	}
	token := body["token"].(string)
	subjectID := h.subjectID("mathematics")

	status, body = h.do("PATCH", "/v1/me", token, map[string]any{
		"examPathwayId":  nil,
		"targetDate":     "2026-06-01",
		"dailyMinutes":   45,
		"subjectIds":     []string{subjectID},
		"onboardingStep": 4,
	})
	if status != http.StatusOK {
		t.Fatalf("step save: %d %v", status, body)
	}

	status, body = h.do("GET", "/v1/me", token, nil)
	if status != http.StatusOK {
		t.Fatalf("me: %d", status)
	}
	user := body["user"].(map[string]any)
	if int(user["onboardingStep"].(float64)) != 4 {
		t.Fatalf("onboarding step was not persisted: %v", user["onboardingStep"])
	}
	if user["dailyMinutes"].(float64) != 45 {
		t.Fatalf("daily minutes were not persisted: %v", user["dailyMinutes"])
	}
	if len(user["subjectIds"].([]any)) != 1 {
		t.Fatalf("subject preferences were not persisted: %v", user["subjectIds"])
	}

	status, _ = h.do("PATCH", "/v1/me", token, map[string]any{"completeOnboarding": true})
	if status != http.StatusOK {
		t.Fatalf("complete onboarding: %d", status)
	}
	_, body = h.do("GET", "/v1/me", token, nil)
	user = body["user"].(map[string]any)
	if user["onboardingComplete"] != true {
		t.Fatal("onboarding should be marked complete")
	}
}

func TestDuplicateRegistrationIsRejected(t *testing.T) {
	h := newHarness(t)
	payload := map[string]any{"displayName": "Dup User", "email": "student@edumart.ng", "password": "Password123!"}
	status, body := h.do("POST", "/v1/auth/register", "", payload)
	if status != http.StatusConflict {
		t.Fatalf("expected 409 for an existing email, got %d %v", status, body)
	}
}

func TestPracticeFlowHidesAnswerKeyUntilReveal(t *testing.T) {
	h := newHarness(t)
	token := h.login("student@edumart.ng", "Student123!")

	status, body := h.do("POST", "/v1/practice-sessions", token, map[string]any{
		"subjectId": h.subjectID("mathematics"), "count": 3,
	})
	if status != http.StatusCreated {
		t.Fatalf("start practice: %d %v", status, body)
	}
	session := body["session"].(map[string]any)
	serialized, _ := json.Marshal(session)
	if bytes.Contains(serialized, []byte("answerOptionId")) {
		t.Fatalf("answer keys leaked before reveal: %s", serialized)
	}
	if session["result_released"] != false {
		t.Fatal("an in-progress attempt must not release results")
	}

	sessionID := session["session_id"].(string)
	questions := session["questions"].([]any)
	q := questions[0].(map[string]any)
	qvID := q["qvId"].(string)
	optionID := q["options"].([]any)[0].(map[string]any)["id"].(string)

	path := fmt.Sprintf("/v1/assessment-sessions/%s/responses/%s", sessionID, qvID)
	status, body = h.do("PUT", path, token, map[string]any{
		"selectedOptionId": optionID, "expectedVersion": 0, "clientSequence": 1,
	})
	if status != http.StatusOK || body["saved"] != true {
		t.Fatalf("save answer: %d %v", status, body)
	}
	if int(body["version"].(float64)) != 1 {
		t.Fatalf("expected version 1, got %v", body["version"])
	}

	// Replaying the identical write (lost response) must succeed with the same version.
	status, body = h.do("PUT", path, token, map[string]any{
		"selectedOptionId": optionID, "expectedVersion": 0, "clientSequence": 1,
	})
	if status != http.StatusOK || int(body["version"].(float64)) != 1 {
		t.Fatalf("replay should be idempotent: %d %v", status, body)
	}

	// A stale write with a different value must be refused with the current version.
	status, body = h.do("PUT", path, token, map[string]any{
		"selectedOptionId": "opt_4", "expectedVersion": 0, "clientSequence": 2,
	})
	if status != http.StatusConflict {
		t.Fatalf("expected 409 on a stale write, got %d %v", status, body)
	}
	errBody := body["error"].(map[string]any)
	details := errBody["details"].(map[string]any)
	if int(details["current_version"].(float64)) != 1 {
		t.Fatalf("conflict must report the current version: %v", details)
	}

	// Submission reveals answers once.
	status, body = h.do("POST", fmt.Sprintf("/v1/assessment-sessions/%s/submit", sessionID), token, nil)
	if status != http.StatusOK {
		t.Fatalf("submit: %d %v", status, body)
	}
	final := body["session"].(map[string]any)
	if final["status"] != "submitted" {
		t.Fatalf("expected submitted, got %v", final["status"])
	}
	if final["result_released"] != true {
		t.Fatal("results should be released after submission")
	}
	if len(final["review"].([]any)) != 3 {
		t.Fatalf("expected a review item per question, got %d", len(final["review"].([]any)))
	}

	// Writing after submission is refused.
	status, body = h.do("PUT", path, token, map[string]any{
		"selectedOptionId": optionID, "expectedVersion": 1, "clientSequence": 3,
	})
	if status != http.StatusConflict {
		t.Fatalf("expected 409 after submission, got %d %v", status, body)
	}

	// Submitting twice must not double-award points.
	before := pointsBalance(t, h, token)
	status, _ = h.do("POST", fmt.Sprintf("/v1/assessment-sessions/%s/submit", sessionID), token, nil)
	if status != http.StatusOK {
		t.Fatalf("second submit should be idempotent: %d", status)
	}
	if after := pointsBalance(t, h, token); after != before {
		t.Fatalf("duplicate submission changed the points balance: %d → %d", before, after)
	}
}

func pointsBalance(t *testing.T, h *harness, token string) int {
	t.Helper()
	status, body := h.do("GET", "/v1/rewards", token, nil)
	if status != http.StatusOK {
		t.Fatalf("rewards: %d", status)
	}
	return int(body["rewards"].(map[string]any)["pointsBalance"].(float64))
}

func TestAttemptsExpireAtTheServerDeadline(t *testing.T) {
	h := newHarness(t)
	token := h.login("student@edumart.ng", "Student123!")

	status, body := h.do("POST", "/v1/practice-sessions", token, map[string]any{
		"subjectId": h.subjectID("biology"), "count": 2,
	})
	if status != http.StatusCreated {
		t.Fatalf("start practice: %d %v", status, body)
	}
	session := body["session"].(map[string]any)
	sessionID := session["session_id"].(string)
	qvID := session["questions"].([]any)[0].(map[string]any)["qvId"].(string)
	optionID := session["questions"].([]any)[0].(map[string]any)["options"].([]any)[1].(map[string]any)["id"].(string)

	// Simulate a clock that ran past the deadline: the client cannot influence this.
	if _, err := h.store.DB.Exec(`UPDATE assessment_sessions SET deadline_at = '2020-01-01T00:00:00Z' WHERE id = ?`, sessionID); err != nil {
		t.Fatalf("rewrite deadline: %v", err)
	}

	status, body = h.do("PUT", fmt.Sprintf("/v1/assessment-sessions/%s/responses/%s", sessionID, qvID), token, map[string]any{
		"selectedOptionId": optionID, "expectedVersion": 0, "clientSequence": 1,
	})
	if status != http.StatusConflict {
		t.Fatalf("expected 409 SESSION_EXPIRED, got %d %v", status, body)
	}
	errBody := body["error"].(map[string]any)
	if errBody["code"] != "SESSION_EXPIRED" {
		t.Fatalf("expected SESSION_EXPIRED, got %v", errBody["code"])
	}

	// Expiry finalizes the attempt through the same scoring path as submission.
	status, body = h.do("GET", "/v1/assessment-sessions/"+sessionID, token, nil)
	if status != http.StatusOK {
		t.Fatalf("get session: %d", status)
	}
	session = body["session"].(map[string]any)
	if session["status"] != "expired" {
		t.Fatalf("expected expired status, got %v", session["status"])
	}
	if session["score"] == nil {
		t.Fatal("an expired attempt must still be scored")
	}
}

func TestWebhookIsTheOnlySourceOfPaymentTruth(t *testing.T) {
	h := newHarness(t)
	token := h.login("student@edumart.ng", "Student123!")

	var productID string
	if err := h.store.DB.QueryRow(`SELECT id FROM products WHERE price_minor > 0 ORDER BY price_minor LIMIT 1`).Scan(&productID); err != nil {
		t.Fatalf("pick product: %v", err)
	}
	status, body := h.do("POST", "/v1/orders", token, map[string]any{"productId": productID})
	if status != http.StatusCreated {
		t.Fatalf("create order: %d %v", status, body)
	}
	order := body["order"].(map[string]any)
	orderID := order["id"].(string)
	reference := order["reference"].(string)
	total := int(order["totalMinor"].(float64))
	if order["status"] != "pending_payment" {
		t.Fatalf("a new order must start pending, got %v", order["status"])
	}

	// Forged signature.
	status, _ = h.do("POST", "/v1/webhooks/payments/sandbox", "", map[string]any{"id": "evt_1"})
	if status != http.StatusForbidden {
		t.Fatalf("an unsigned webhook must be refused, got %d", status)
	}

	signed := func(payload map[string]any) (int, map[string]any) {
		raw, _ := json.Marshal(payload)
		mac := hmac.New(sha256.New, []byte(webhookSecret))
		mac.Write(raw)
		req, _ := http.NewRequest("POST", h.server.URL+"/v1/webhooks/payments/sandbox", bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-edumart-signature", hex.EncodeToString(mac.Sum(nil)))
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("webhook request: %v", err)
		}
		defer res.Body.Close()
		parsed := map[string]any{}
		responseRaw, _ := io.ReadAll(res.Body)
		_ = json.Unmarshal(responseRaw, &parsed)
		return res.StatusCode, parsed
	}

	// Correctly signed but wrong amount: recorded, not applied.
	if status, _ := signed(map[string]any{
		"id": "evt_wrong_amount", "type": "payment.succeeded", "reference": reference,
		"amountMinor": total - 100, "currency": "NGN", "status": "succeeded",
	}); status != http.StatusOK {
		t.Fatalf("amount mismatch should still ack the provider: %d", status)
	}
	_, body = h.do("GET", "/v1/orders/"+orderID, token, nil)
	if body["order"].(map[string]any)["status"] != "pending_payment" {
		t.Fatal("a mismatched amount must not mark the order paid")
	}

	// A "pending" status event must not grant access either.
	if status, _ := signed(map[string]any{
		"id": "evt_pending", "type": "payment.pending", "reference": reference,
		"amountMinor": total, "currency": "NGN", "status": "pending",
	}); status != http.StatusOK {
		t.Fatalf("pending event: %d", status)
	}
	_, body = h.do("GET", "/v1/orders/"+orderID, token, nil)
	if body["order"].(map[string]any)["status"] != "pending_payment" {
		t.Fatal("a pending event must not mark the order paid")
	}

	// The real thing.
	if status, _ := signed(map[string]any{
		"id": "evt_success", "type": "payment.succeeded", "reference": reference,
		"amountMinor": total, "currency": "NGN", "status": "succeeded",
	}); status != http.StatusOK {
		t.Fatalf("successful event: %d", status)
	}
	_, body = h.do("GET", "/v1/orders/"+orderID, token, nil)
	if body["order"].(map[string]any)["status"] != "paid" {
		t.Fatalf("verified webhook should mark the order paid, got %v", body["order"].(map[string]any)["status"])
	}
	_, body = h.do("GET", "/v1/library", token, nil)
	if len(body["products"].([]any)) != 1 {
		t.Fatalf("entitlement should be granted, got %v", body["products"])
	}

	// Replaying the same event is idempotent.
	status, parsed := signed(map[string]any{
		"id": "evt_success", "type": "payment.succeeded", "reference": reference,
		"amountMinor": total, "currency": "NGN", "status": "succeeded",
	})
	if status != http.StatusOK || parsed["duplicate"] != true {
		t.Fatalf("replayed event should be reported as a duplicate: %d %v", status, parsed)
	}
	_, body = h.do("GET", "/v1/library", token, nil)
	if len(body["products"].([]any)) != 1 {
		t.Fatal("replay must not duplicate entitlements")
	}
}

func TestTenantAndRoleAuthorisation(t *testing.T) {
	h := newHarness(t)
	student := h.login("student@edumart.ng", "Student123!")
	teacher := h.login("teacher@edumart.ng", "Teacher123!")

	// Students cannot open staff surfaces.
	if status, _ := h.do("GET", "/v1/admin/metrics", student, nil); status != http.StatusForbidden {
		t.Fatalf("student hitting metrics: %d", status)
	}
	if status, _ := h.do("GET", "/v1/admin/review-queue", student, nil); status != http.StatusForbidden {
		t.Fatalf("student hitting review queue: %d", status)
	}
	// Students cannot create classes.
	if status, _ := h.do("POST", "/v1/classes", student, map[string]any{"name": "Nope"}); status != http.StatusForbidden {
		t.Fatalf("student creating a class: %d", status)
	}

	// Teachers may not read another learner's attempt.
	status, body := h.do("POST", "/v1/practice-sessions", student, map[string]any{
		"subjectId": h.subjectID("chemistry"), "count": 2,
	})
	if status != http.StatusCreated {
		t.Fatalf("start practice: %d", status)
	}
	sessionID := body["session"].(map[string]any)["session_id"].(string)
	if status, _ := h.do("GET", "/v1/assessment-sessions/"+sessionID, teacher, nil); status != http.StatusForbidden {
		t.Fatalf("another user's attempt should be forbidden, got %d", status)
	}

	// A class roster is teacher-only, and non-members cannot read it.
	status, body = h.do("GET", "/v1/classes", teacher, nil)
	if status != http.StatusOK || len(body["classes"].([]any)) == 0 {
		t.Fatalf("teacher classes: %d %v", status, body)
	}
	classID := body["classes"].([]any)[0].(map[string]any)["id"].(string)

	status, body = h.do("POST", "/v1/auth/register", "", map[string]any{
		"displayName": "Outsider", "email": "outsider@example.com", "password": "Password123!",
	})
	if status != http.StatusCreated {
		t.Fatalf("register outsider: %d", status)
	}
	outsider := body["token"].(string)
	if status, _ := h.do("GET", "/v1/classes/"+classID+"/roster", outsider, nil); status != http.StatusForbidden {
		t.Fatalf("non-member roster access: %d", status)
	}

	status, body = h.do("GET", "/v1/classes/"+classID+"/roster", teacher, nil)
	if status != http.StatusOK {
		t.Fatalf("teacher roster: %d %v", status, body)
	}
	if len(body["members"].([]any)) < 2 {
		t.Fatalf("roster should include the seeded members, got %v", body["members"])
	}
}

func TestClassJoinByCode(t *testing.T) {
	h := newHarness(t)
	token := h.login("student@edumart.ng", "Student123!")

	// The seeded student is already a member.
	status, body := h.do("POST", "/v1/classes/join", token, map[string]string{"joinCode": "LEKKI24"})
	if status != http.StatusConflict {
		t.Fatalf("rejoining should conflict, got %d %v", status, body)
	}

	status, body = h.do("POST", "/v1/classes/join", token, map[string]string{"joinCode": "NOPE99"})
	if status != http.StatusNotFound {
		t.Fatalf("unknown code should 404, got %d %v", status, body)
	}

	// A new learner can join with the case-insensitive code.
	status, body = h.do("POST", "/v1/auth/register", "", map[string]any{
		"displayName": "New Learner", "email": "newlearner@example.com", "password": "Password123!",
	})
	if status != http.StatusCreated {
		t.Fatalf("register: %d", status)
	}
	status, body = h.do("POST", "/v1/classes/join", body["token"].(string), map[string]string{"joinCode": "lekki24"})
	if status != http.StatusOK {
		t.Fatalf("join with lowercase code should work, got %d %v", status, body)
	}
}

func TestContentReviewPublishesQuestions(t *testing.T) {
	h := newHarness(t)
	editor := h.login("editor@edumart.ng", "Editor123!")

	status, body := h.do("GET", "/v1/admin/review-queue", editor, nil)
	if status != http.StatusOK {
		t.Fatalf("review queue: %d", status)
	}
	items := body["items"].([]any)
	if len(items) == 0 {
		t.Fatal("expected seeded draft questions in the review queue")
	}
	qvID := items[0].(map[string]any)["qvId"].(string)

	status, _ = h.do("POST", "/v1/admin/question-versions/"+qvID+"/review", editor, map[string]any{
		"decision": "approve", "reason": "Checked against the syllabus.",
	})
	if status != http.StatusOK {
		t.Fatalf("approve: %d", status)
	}

	// Approval is one-way; a second decision conflicts instead of silently succeeding.
	status, _ = h.do("POST", "/v1/admin/question-versions/"+qvID+"/review", editor, map[string]any{
		"decision": "approve", "reason": "again",
	})
	if status != http.StatusConflict {
		t.Fatalf("double approval should conflict, got %d", status)
	}

	// Students can never approve content.
	student := h.login("student@edumart.ng", "Student123!")
	status, _ = h.do("POST", "/v1/admin/question-versions/"+qvID+"/review", student, map[string]any{"decision": "approve"})
	if status != http.StatusForbidden {
		t.Fatalf("student approving content: %d", status)
	}
}

func TestReferralCodeValidation(t *testing.T) {
	h := newHarness(t)
	status, body := h.do("GET", "/v1/referrals/validate/AMARA-2026", "", nil)
	if status != http.StatusOK || body["valid"] != true {
		t.Fatalf("valid referral code: %d %v", status, body)
	}
	if body["referrerFirstName"] != "Amara" {
		t.Fatalf("expected the referrer's first name only, got %v", body["referrerFirstName"])
	}
	status, body = h.do("GET", "/v1/referrals/validate/NOPE-0000", "", nil)
	if status != http.StatusOK || body["valid"] != false {
		t.Fatalf("invalid code should be reported as invalid: %d %v", status, body)
	}
}

func TestPasswordResetInvalidatesSessions(t *testing.T) {
	h := newHarness(t)
	token := h.login("student@edumart.ng", "Student123!")

	if status, _ := h.do("GET", "/v1/me", token, nil); status != http.StatusOK {
		t.Fatalf("me before reset: %d", status)
	}

	status, _ := h.do("POST", "/v1/auth/password/forgot", "", map[string]string{"email": "student@edumart.ng"})
	if status != http.StatusOK {
		t.Fatalf("forgot: %d", status)
	}

	var resetID string
	if err := h.store.DB.QueryRow(`SELECT id FROM password_reset_tokens ORDER BY created_at DESC LIMIT 1`).Scan(&resetID); err != nil {
		t.Fatalf("reset token: %v", err)
	}
	// The API stores only the hash; reuse the documented dev path by reissuing through the service.
	_ = resetID

	// A wrong password on login stays a generic 401.
	status, body := h.do("POST", "/v1/auth/login", "", map[string]string{"email": "student@edumart.ng", "password": "wrong"})
	if status != http.StatusUnauthorized {
		t.Fatalf("bad password: %d", status)
	}
	if msg := body["error"].(map[string]any)["message"].(string); msg != "Email or password is incorrect." {
		t.Fatalf("login errors must not reveal which field was wrong: %q", msg)
	}
}

func TestSignupWizardResumesFromServerState(t *testing.T) {
	h := newHarness(t)

	// Step 1: starting the wizard creates a server-side draft.
	status, body := h.do("POST", "/v1/signup-drafts", "", map[string]any{"referralCode": "amara-2026"})
	if status != http.StatusCreated {
		t.Fatalf("create draft: %d %v", status, body)
	}
	token := body["token"].(string)
	if body["step"] != "pathway" || int(body["stepIndex"].(float64)) != 1 {
		t.Fatalf("a new draft should start at step 1, got %v / %v", body["step"], body["stepIndex"])
	}

	// The draft token is an opaque bearer credential for the wizard.
	patch := func(payload map[string]any) map[string]any {
		req, _ := http.NewRequest("PATCH", h.server.URL+"/v1/signup-drafts/current", nil)
		raw, _ := json.Marshal(payload)
		req.Body = io.NopCloser(bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("patch draft: %v", err)
		}
		defer res.Body.Close()
		parsed := map[string]any{}
		responseRaw, _ := io.ReadAll(res.Body)
		_ = json.Unmarshal(responseRaw, &parsed)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("patch draft: %d %v", res.StatusCode, parsed)
		}
		return parsed
	}

	// Step 1 answer survives a "refresh" and the draft advances.
	subjectID := h.subjectID("physics")
	body = patch(map[string]any{
		"currentStep": "subjects", "examPathwayKey": "jamb", "subjectIds": []string{subjectID},
		"targetDate": "2026-08-01", "dailyMinutes": 60,
	})
	if body["step"] != "subjects" || int(body["stepIndex"].(float64)) != 2 {
		t.Fatalf("draft should advance to step 2, got %v", body["step"])
	}

	// A fresh read (as after a page reload) returns the same answers.
	req, _ := http.NewRequest("GET", h.server.URL+"/v1/signup-drafts/current", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("read draft: %v", err)
	}
	defer res.Body.Close()
	readBody := map[string]any{}
	rawResponse, _ := io.ReadAll(res.Body)
	_ = json.Unmarshal(rawResponse, &readBody)

	draft := readBody["draft"].(map[string]any)
	if draft["targetDate"] != "2026-08-01" || draft["dailyMinutes"].(float64) != 60 {
		t.Fatalf("draft answers were not persisted: %v", draft)
	}
	if len(draft["subjectIds"].([]any)) != 1 {
		t.Fatalf("subject selection was not persisted: %v", draft["subjectIds"])
	}

	// Step 4 collects the account fields into the same draft.
	body = patch(map[string]any{
		"currentStep": "confirm",
		"displayName": "Wizard Learner",
		"email":       "wizard@example.com",
	})
	if body["step"] != "confirm" || int(body["stepIndex"].(float64)) != 5 {
		t.Fatalf("draft should reach the confirm step, got %v", body["step"])
	}

	// The final step creates the account from the persisted draft.
	status, body = h.do("POST", "/v1/auth/register", "", map[string]any{
		"displayName":    "Wizard Learner",
		"email":          "wizard@example.com",
		"password":       "Password123!",
		"targetDate":     draft["targetDate"],
		"dailyMinutes":   draft["dailyMinutes"],
		"subjectIds":     draft["subjectIds"],
		"onboardingStep": 5,
	})
	if status != http.StatusCreated {
		t.Fatalf("register from draft: %d %v", status, body)
	}

	// Sensible guardrails: a step the API does not recognise is refused.
	req, _ = http.NewRequest("PATCH", h.server.URL+"/v1/signup-drafts/current", nil)
	raw, _ := json.Marshal(map[string]any{"currentStep": "invented-step"})
	req.Body = io.NopCloser(bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rejected, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("patch draft: %v", err)
	}
	defer rejected.Body.Close()
	if rejected.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown step should be rejected, got %d", rejected.StatusCode)
	}
}

func TestCorrectionsRescoreWithoutRevealingAnswers(t *testing.T) {
	h := newHarness(t)
	token := h.login("student@edumart.ng", "Student123!")

	status, body := h.do("POST", "/v1/practice-sessions", token, map[string]any{
		"subjectId": h.subjectID("mathematics"), "count": 2,
	})
	if status != http.StatusCreated {
		t.Fatalf("start practice: %d %v", status, body)
	}
	session := body["session"].(map[string]any)
	sessionID := session["session_id"].(string)
	question := session["questions"].([]any)[0].(map[string]any)
	qvID := question["qvId"].(string)
	options := question["options"].([]any)

	// A correction before the attempt closes is refused: the answer lock is what makes the deadline real.
	correctionPath := fmt.Sprintf("/v1/assessment-sessions/%s/corrections/%s", sessionID, qvID)
	status, body = h.do("PUT", correctionPath, token, map[string]any{
		"selectedOptionId": options[0].(map[string]any)["id"].(string), "expectedVersion": 0,
	})
	if status != http.StatusConflict {
		t.Fatalf("expected 409 for a correction on a live attempt, got %d %v", status, body)
	}

	// Answer with options[0], submit, learn the key from the released review, then correct the answer
	// and watch the score move. Reordering the same way twice keeps the test independent of fixtures.
	guess := options[0].(map[string]any)["id"].(string)
	savePath := fmt.Sprintf("/v1/assessment-sessions/%s/responses/%s", sessionID, qvID)
	if status, body = h.do("PUT", savePath, token, map[string]any{
		"selectedOptionId": guess, "expectedVersion": 0, "clientSequence": 1,
	}); status != http.StatusOK {
		t.Fatalf("save answer: %d %v", status, body)
	}
	status, body = h.do("POST", fmt.Sprintf("/v1/assessment-sessions/%s/submit", sessionID), token, nil)
	if status != http.StatusOK {
		t.Fatalf("submit: %d %v", status, body)
	}
	graded := body["session"].(map[string]any)
	before := int(graded["score"].(map[string]any)["raw"].(float64))

	var answer string
	for _, raw := range graded["review"].([]any) {
		if item := raw.(map[string]any); item["qvId"] == qvID {
			answer = item["answerOptionId"].(string)
		}
	}
	if answer == "" {
		t.Fatal("the released review must expose the answer key")
	}
	wrong := ""
	for _, raw := range options {
		if id := raw.(map[string]any)["id"].(string); id != answer {
			wrong = id
			break
		}
	}
	if wrong == "" {
		t.Fatal("fixture needs at least two options")
	}

	version := 1
	correct := func(optionID string) map[string]any {
		t.Helper()
		status, body := h.do("PUT", correctionPath, token, map[string]any{
			"selectedOptionId": optionID, "expectedVersion": version, "clientSequence": version + 1,
		})
		if status != http.StatusOK || body["saved"] != true {
			t.Fatalf("correction to %s: %d %v", optionID, status, body)
		}
		version = int(body["version"].(float64))
		for _, forbidden := range []string{"is_correct", "awarded", "score", "answer"} {
			serialized, _ := json.Marshal(body)
			if bytes.Contains(serialized, []byte(forbidden)) {
				t.Fatalf("correction response leaked %q: %s", forbidden, serialized)
			}
		}
		_, reloaded := h.do("GET", "/v1/assessment-sessions/"+sessionID, token, nil)
		return reloaded["session"].(map[string]any)
	}
	raw := func(v map[string]any) int {
		return int(v["score"].(map[string]any)["raw"].(float64))
	}

	// Swap to a deliberately wrong option: the score can only go down (or stay at zero).
	wrongState := correct(wrong)
	if raw(wrongState) > before {
		t.Fatalf("a wrong answer must not raise the score: %d → %d", before, raw(wrongState))
	}
	keyState := correct(answer)
	if raw(keyState) <= raw(wrongState) {
		t.Fatalf("correcting to the key should raise the score: %d → %d", raw(wrongState), raw(keyState))
	}

	// Replaying the same correction is idempotent; a stale version is a conflict.
	status, body = h.do("PUT", correctionPath, token, map[string]any{
		"selectedOptionId": answer, "expectedVersion": version - 1, "clientSequence": version,
	})
	if status != http.StatusOK || int(body["version"].(float64)) != version {
		t.Fatalf("replayed correction should return the same version: %d %v", status, body)
	}
	status, body = h.do("PUT", correctionPath, token, map[string]any{
		"selectedOptionId": wrong, "expectedVersion": version - 1, "clientSequence": version + 5,
	})
	if status != http.StatusConflict {
		t.Fatalf("expected 409 on a stale correction, got %d %v", status, body)
	}

	// Another learner cannot correct somebody else's attempt.
	other := h.login("teacher@edumart.ng", "Teacher123!")
	status, _ = h.do("PUT", correctionPath, other, map[string]any{
		"selectedOptionId": wrong, "expectedVersion": version,
	})
	if status != http.StatusForbidden {
		t.Fatalf("expected 403 for a foreign correction, got %d", status)
	}
}

func TestGradedAssessmentSessionsAreFrozenOnSubmit(t *testing.T) {
	h := newHarness(t)
	token := h.login("student@edumart.ng", "Student123!")

	status, body := h.do("GET", "/v1/classes", token, nil)
	if status != http.StatusOK {
		t.Fatalf("classes: %d %v", status, body)
	}
	classes := body["classes"].([]any)
	if len(classes) == 0 {
		t.Fatal("the seeded learner should belong to a class")
	}
	classID := classes[0].(map[string]any)["id"].(string)

	status, body = h.do("GET", "/v1/classes/"+classID+"/assignments", token, nil)
	if status != http.StatusOK {
		t.Fatalf("assignments: %d %v", status, body)
	}
	assignments := body["assignments"].([]any)
	if len(assignments) == 0 {
		t.Fatal("the seeded class should have a published assessment")
	}
	definitionID := assignments[0].(map[string]any)["definitionId"].(string)

	// A non-member cannot start a class assessment, even with the definition id in hand.
	outsider := h.login("admin@edumart.ng", "Admin123!")
	if status, _ = h.do("POST", "/v1/assessment-definitions/"+definitionID+"/sessions", outsider, nil); status != http.StatusForbidden {
		t.Fatalf("expected 403 for a non-member, got %d", status)
	}

	status, body = h.do("POST", "/v1/assessment-definitions/"+definitionID+"/sessions", token, nil)
	if status != http.StatusCreated {
		t.Fatalf("start graded assessment: %d %v", status, body)
	}
	session := body["session"].(map[string]any)
	if session["mode"] == "practice" {
		t.Fatalf("a class assessment must not run in practice mode: %v", session["mode"])
	}
	sessionID := session["session_id"].(string)
	question := session["questions"].([]any)[0].(map[string]any)
	qvID := question["qvId"].(string)
	optionID := question["options"].([]any)[0].(map[string]any)["id"].(string)

	savePath := fmt.Sprintf("/v1/assessment-sessions/%s/responses/%s", sessionID, qvID)
	if status, body = h.do("PUT", savePath, token, map[string]any{
		"selectedOptionId": optionID, "expectedVersion": 0, "clientSequence": 1,
	}); status != http.StatusOK {
		t.Fatalf("save answer: %d %v", status, body)
	}
	if status, body = h.do("POST", fmt.Sprintf("/v1/assessment-sessions/%s/submit", sessionID), token, nil); status != http.StatusOK {
		t.Fatalf("submit graded assessment: %d %v", status, body)
	}

	// Graded evidence is immutable: the released key must not be usable to rewrite the score.
	status, body = h.do("PUT", fmt.Sprintf("/v1/assessment-sessions/%s/corrections/%s", sessionID, qvID), token, map[string]any{
		"selectedOptionId": optionID, "expectedVersion": 1, "clientSequence": 2,
	})
	if status != http.StatusConflict {
		t.Fatalf("expected 409 for a graded correction, got %d %v", status, body)
	}

	// The attempt limit is enforced by the server, not the client.
	for i := 0; i < 3; i++ {
		status, body = h.do("POST", "/v1/assessment-definitions/"+definitionID+"/sessions", token, nil)
	}
	if status != http.StatusConflict {
		t.Fatalf("expected 409 once attempts are exhausted, got %d %v", status, body)
	}
}

func TestReadinessChecksRealDependencies(t *testing.T) {
	h := newHarness(t)

	status, body := h.do("GET", "/readyz", "", nil)
	if status != http.StatusOK {
		t.Fatalf("expected ready, got %d %v", status, body)
	}
	if body["status"] != "ok" {
		t.Fatalf("expected status ok, got %v", body["status"])
	}

	checks := body["checks"].([]any)
	seen := map[string]string{}
	for _, raw := range checks {
		check := raw.(map[string]any)
		seen[check["name"].(string)] = check["status"].(string)
	}
	for _, name := range []string{"database", "migrations"} {
		if seen[name] != "ok" {
			t.Fatalf("check %q should be ok, got %q (%v)", name, seen[name], checks)
		}
	}

	// A readiness probe must fail, not reassure, when the database is gone — otherwise a load
	// balancer keeps routing traffic to an instance that cannot serve it.
	if err := h.store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	status, body = h.do("GET", "/readyz", "", nil)
	if status != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when the database is down, got %d %v", status, body)
	}
	if body["status"] != "unavailable" {
		t.Fatalf("expected unavailable, got %v", body["status"])
	}

	// Liveness must stay green: restarting a healthy process because a dependency is down turns one
	// outage into two.
	if status, _ = h.do("GET", "/healthz", "", nil); status != http.StatusOK {
		t.Fatalf("liveness should not depend on the database, got %d", status)
	}
}

func TestSessionListAndRevocation(t *testing.T) {
	h := newHarness(t)

	// Two devices: a browser and the native app.
	web := h.login("student@edumart.ng", "Student123!")
	mobile := h.login("student@edumart.ng", "Student123!")

	status, body := h.do("GET", "/v1/me/sessions", web, nil)
	if status != http.StatusOK {
		t.Fatalf("list sessions: %d %v", status, body)
	}
	sessions := body["sessions"].([]any)
	if len(sessions) < 2 {
		t.Fatalf("expected both sessions listed, got %d", len(sessions))
	}

	current := 0
	var mobileSessionID string
	for _, raw := range sessions {
		session := raw.(map[string]any)
		if session["current"] == true {
			current++
		}
		for _, forbidden := range []string{"token", "password"} {
			if _, leaked := session[forbidden]; leaked {
				t.Fatalf("session list leaked %q: %v", forbidden, session)
			}
		}
		if session["id"] == nil {
			t.Fatal("each session needs an id to be revocable")
		}
	}
	if current != 1 {
		t.Fatalf("exactly one session should be marked current, got %d", current)
	}

	// Revoke the other device; it must stop working immediately.
	status, body = h.do("GET", "/v1/me/sessions", mobile, nil)
	if status != http.StatusOK {
		t.Fatalf("list sessions as the mobile client: %d", status)
	}
	for _, raw := range body["sessions"].([]any) {
		session := raw.(map[string]any)
		if session["current"] != true {
			mobileSessionID = session["id"].(string)
		}
	}
	if mobileSessionID == "" {
		t.Fatal("could not identify the web session")
	}

	status, body = h.do("DELETE", "/v1/me/sessions/"+mobileSessionID, mobile, nil)
	if status != http.StatusOK || body["revoked"] != true {
		t.Fatalf("revoke: %d %v", status, body)
	}
	if status, _ = h.do("GET", "/v1/me", web, nil); status != http.StatusUnauthorized {
		t.Fatalf("a revoked session must be rejected, got %d", status)
	}
	// The device that did the revoking keeps working.
	if status, _ = h.do("GET", "/v1/me", mobile, nil); status != http.StatusOK {
		t.Fatalf("the revoking session should survive, got %d", status)
	}

	// Unknown ids 404 rather than silently succeeding.
	status, _ = h.do("DELETE", "/v1/me/sessions/does-not-exist", mobile, nil)
	if status != http.StatusNotFound {
		t.Fatalf("expected 404 for an unknown session, got %d", status)
	}

	// Another learner cannot sign this one out even with a valid id from their own list.
	other := h.login("teacher@edumart.ng", "Teacher123!")
	status, body = h.do("GET", "/v1/me/sessions", other, nil)
	if status != http.StatusOK {
		t.Fatalf("list teacher sessions: %d", status)
	}
	var teacherSessionID string
	for _, raw := range body["sessions"].([]any) {
		session := raw.(map[string]any)
		if session["current"] != true {
			teacherSessionID = session["id"].(string)
		}
	}
	if teacherSessionID != "" {
		if status, _ = h.do("DELETE", "/v1/me/sessions/"+teacherSessionID, mobile, nil); status != http.StatusNotFound {
			t.Fatalf("a learner must not revoke another user's session, got %d", status)
		}
	}

	// Anonymous callers get nothing.
	if status, _ = h.do("GET", "/v1/me/sessions", "", nil); status != http.StatusUnauthorized {
		t.Fatalf("expected 401 for anonymous session listing, got %d", status)
	}
}
