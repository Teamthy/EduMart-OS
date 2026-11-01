package api_test

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/teamthy/edumart/apps/api/internal/store"
)

// seedApprovedQuestions inserts `count` approved, published questions for a subject and returns their question
// version ids. The sample dataset deliberately ships questions that are still pending review, so an exam test
// that wants a paper has to create one the way a reviewer would.
func (h *harness) seedApprovedQuestions(t *testing.T, subjectID string, count int) []string {
	t.Helper()
	ids := make([]string, 0, count)
	options, err := json.Marshal([]map[string]string{
		{"id": "opt_1", "label": "A. First option"},
		{"id": "opt_2", "label": "B. Second option"},
		{"id": "opt_3", "label": "C. Third option"},
		{"id": "opt_4", "label": "D. Fourth option"},
	})
	if err != nil {
		t.Fatalf("options: %v", err)
	}
	now := store.Now()
	for index := 0; index < count; index++ {
		questionID := store.NewID()
		qvID := store.NewID()
		if _, err := h.store.DB.Exec(`INSERT INTO questions
			(id, subject_id, type, difficulty, status, created_at) VALUES (?,?,'mcq','medium','published',?)`,
			questionID, subjectID, now); err != nil {
			t.Fatalf("insert question: %v", err)
		}
		if _, err := h.store.DB.Exec(`INSERT INTO question_versions
			(id, question_id, version, stem, options_json, answer_option_id, explanation, source_name,
			 license_basis, attribution, review_state, published_at, created_at)
			VALUES (?,?,1,?,?,'opt_2','Because.',  'Exam bank','licensed','Test','approved',?,?)`,
			qvID, questionID, "Question "+store.NewID()[:8]+": which option is correct?",
			string(options), now, now); err != nil {
			t.Fatalf("insert question version: %v", err)
		}
		ids = append(ids, qvID)
	}
	return ids
}

// createExam schedules a sitting and returns the exam payload.
func (h *harness) createExam(t *testing.T, host string, body map[string]any) map[string]any {
	t.Helper()
	status, payload := h.do("POST", "/v1/exams", host, body)
	if status != http.StatusCreated {
		t.Fatalf("create exam: %d %v", status, payload)
	}
	return payload["exam"].(map[string]any)
}

// TestExamIsOnePaperForEverybodyInADifferentOrder is the acceptance test for "same questions, shuffled".
//
// Two candidates start the same sitting. They must receive the same *set* of questions — otherwise the paper is
// not the same exam — in a different order, and neither response may carry an answer key.
func TestExamIsOnePaperForEverybodyInADifferentOrder(t *testing.T) {
	h := newHarness(t)
	host := h.registerAndVerify("exam-host@example.com")
	first := h.registerAndVerify("exam-first@example.com")
	second := h.registerAndVerify("exam-second@example.com")

	subjectID := h.subjectID("mathematics")
	questions := h.seedApprovedQuestions(t, subjectID, 8)
	startsAt := time.Now().Add(-time.Minute)
	exam := h.createExam(t, host, map[string]any{
		"title":              "Synchronised Algebra Test",
		"durationMinutes":    20,
		"startsAt":           store.TS(startsAt),
		"endsAt":             store.TS(startsAt.Add(30 * time.Minute)),
		"shuffle":            true,
		"questionVersionIds": questions,
	})

	if !strings.Contains(exam["url"].(string), "/exams/") {
		t.Fatalf("an exam needs a unique shareable URL, got %v", exam["url"])
	}
	slug := exam["slug"].(string)
	if len(slug) < 12 {
		t.Fatalf("exam code is too guessable: %q", slug)
	}
	if !exam["open"].(bool) {
		t.Fatalf("the exam should be open inside its window, got %v", exam)
	}

	// The invite link answers before sign-in and says nothing about the paper's contents.
	status, payload := h.do("GET", "/v1/exams/link/"+slug, "", nil)
	if status != http.StatusOK {
		t.Fatalf("exam invite: %d %v", status, payload)
	}
	invite := payload["exam"].(map[string]any)
	if invite["title"] != "Synchronised Algebra Test" {
		t.Fatalf("invite must name the exam, got %v", invite)
	}
	for _, forbidden := range []string{"questions", "answerOptionId", "answerKey"} {
		if _, leaked := invite[forbidden]; leaked {
			t.Fatalf("the public invite must not carry %q", forbidden)
		}
	}
	if status, _ := h.do("GET", "/v1/exams/link/AAAAAAAABBBBBBBB", "", nil); status != http.StatusNotFound {
		t.Fatalf("an unknown exam code must be a 404, got %d", status)
	}

	orderFor := func(token string) []string {
		status, body := h.do("POST", "/v1/exams/"+exam["id"].(string)+"/start", token, nil)
		if status != http.StatusCreated {
			t.Fatalf("start sitting: %d %v", status, body)
		}
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if strings.Contains(string(raw), "answerOptionId") || strings.Contains(string(raw), "explanation") {
			t.Fatalf("an in-progress sitting must not carry the answer key: %s", raw)
		}
		if deadline, ok := body["deadline_at"].(string); !ok || deadline == "" {
			t.Fatalf("the server must set the deadline, got %v", body)
		}
		list, _ := body["questions"].([]any)
		ids := make([]string, 0, len(list))
		for _, item := range list {
			ids = append(ids, item.(map[string]any)["qvId"].(string))
		}
		return ids
	}

	firstOrder := orderFor(first)
	secondOrder := orderFor(second)

	if len(firstOrder) != len(questions) || len(secondOrder) != len(questions) {
		t.Fatalf("both candidates must get the whole paper: %d and %d of %d", len(firstOrder), len(secondOrder), len(questions))
	}
	sortedFirst := append([]string(nil), firstOrder...)
	sortedSecond := append([]string(nil), secondOrder...)
	sort.Strings(sortedFirst)
	sort.Strings(sortedSecond)
	if strings.Join(sortedFirst, ",") != strings.Join(sortedSecond, ",") {
		t.Fatalf("candidates must sit the same questions:\n%v\n%v", sortedFirst, sortedSecond)
	}
	if strings.Join(firstOrder, ",") == strings.Join(secondOrder, ",") {
		t.Fatalf("the paper must be shuffled per candidate; both saw the same order")
	}

	// One attempt each: the second start is refused, and the reason is the attempt limit rather than a crash.
	if status, body := h.do("POST", "/v1/exams/"+exam["id"].(string)+"/start", first, nil); status != http.StatusConflict {
		t.Fatalf("a second attempt must be refused, got %d %v", status, body)
	}

	// The host sets papers; sitting one would make the report meaningless.
	if status, _ := h.do("POST", "/v1/exams/"+exam["id"].(string)+"/start", host, nil); status != http.StatusConflict {
		t.Fatalf("the host must not sit their own exam, got %d", status)
	}
}

// TestExamDeadlineNeverOutlivesItsWindow proves the "same duration" rule against the clock: an attempt started
// late on a short window gets the window, not the nominal duration.
func TestExamDeadlineNeverOutlivesItsWindow(t *testing.T) {
	h := newHarness(t)
	host := h.registerAndVerify("window-host@example.com")
	candidate := h.registerAndVerify("window-candidate@example.com")
	questions := h.seedApprovedQuestions(t, h.subjectID("physics"), 3)

	start := time.Now().Add(-time.Minute)
	end := start.Add(5 * time.Minute)
	exam := h.createExam(t, host, map[string]any{
		"title":              "Five minute window, twenty minute paper",
		"durationMinutes":    20,
		"startsAt":           store.TS(start),
		"endsAt":             store.TS(end),
		"questionVersionIds": questions,
	})

	status, body := h.do("POST", "/v1/exams/"+exam["id"].(string)+"/start", candidate, nil)
	if status != http.StatusCreated {
		t.Fatalf("start: %d %v", status, body)
	}
	deadline, err := store.ParseTS(body["deadline_at"].(string))
	if err != nil {
		t.Fatalf("deadline: %v", err)
	}
	if deadline.After(end.Add(time.Second)) {
		t.Fatalf("an attempt must not outlive the sitting: deadline %v, window ends %v", deadline, end)
	}

	// Submitting inside the window produces a score attached to the attempt, and the host can see it end to end.
	sessionID := body["session_id"].(string)
	questionsInAttempt, _ := body["questions"].([]any)
	answers := map[string]any{}
	for _, item := range questionsInAttempt {
		question := item.(map[string]any)
		status, saved := h.do("PUT", "/v1/assessment-sessions/"+sessionID+"/responses/"+question["qvId"].(string),
			candidate, map[string]any{"selectedOptionId": "opt_2"})
		if status != http.StatusOK {
			t.Fatalf("save answer: %d %v", status, saved)
		}
		answers[question["qvId"].(string)] = true
	}
	if status, submitted := h.do("POST", "/v1/assessment-sessions/"+sessionID+"/submit", candidate, nil); status != http.StatusOK {
		t.Fatalf("submit: %d %v", status, submitted)
	}

	status, results := h.do("GET", "/v1/exams/"+exam["id"].(string)+"/results", host, nil)
	if status != http.StatusOK {
		t.Fatalf("host results: %d %v", status, results)
	}
	candidates := results["candidates"].([]any)
	if len(candidates) != 1 {
		t.Fatalf("expected one candidate in the report, got %v", candidates)
	}
	row := candidates[0].(map[string]any)
	if row["submittedAt"] == nil {
		t.Fatalf("the report must record when the server saw the submission, got %v", row)
	}
	if row["scoreRaw"] == nil {
		t.Fatalf("the report must carry the score, got %v", row)
	}

	// A candidate cannot read the host's report, and cannot read another candidate's attempt.
	if status, _ := h.do("GET", "/v1/exams/"+exam["id"].(string)+"/results", candidate, nil); status != http.StatusForbidden {
		t.Fatalf("only the host may read every candidate's result, got %d", status)
	}
	other := h.registerAndVerify("nosy-candidate@example.com")
	if status, _ := h.do("GET", "/v1/assessment-sessions/"+sessionID, other, nil); status == http.StatusOK {
		t.Fatalf("another account must not read somebody else's attempt")
	}
}

// TestExamRefusesCandidatesOutsideTheWindow makes "at the same time" an enforced fact rather than a convention.
func TestExamRefusesCandidatesOutsideTheWindow(t *testing.T) {
	h := newHarness(t)
	host := h.registerAndVerify("closed-host@example.com")
	candidate := h.registerAndVerify("closed-candidate@example.com")
	questions := h.seedApprovedQuestions(t, h.subjectID("chemistry"), 2)

	future := time.Now().Add(2 * time.Hour)
	exam := h.createExam(t, host, map[string]any{
		"title":              "Next week's mock",
		"durationMinutes":    45,
		"startsAt":           store.TS(future),
		"questionVersionIds": questions,
	})

	status, body := h.do("POST", "/v1/exams/"+exam["id"].(string)+"/start", candidate, nil)
	if status != http.StatusConflict {
		t.Fatalf("an exam before its window must refuse a start, got %d %v", status, body)
	}
	if code := body["error"].(map[string]any)["code"]; code != "ASSESSMENT_NOT_OPEN" {
		t.Fatalf("expected a not-open assessment code, got %v", code)
	}

	// An exam with no answer key in its own definition cannot be published, because it would be unanswerable.
	status, payload := h.do("POST", "/v1/exams", host, map[string]any{
		"title": "Empty paper", "durationMinutes": 10, "startsAt": store.TS(future),
	})
	if status != http.StatusBadRequest {
		t.Fatalf("an exam with no questions must be refused, got %d %v", status, payload)
	}
}
