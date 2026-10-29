package api_test

import (
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/teamthy/edumart/apps/api/internal/store"
)

// reminderCount counts rows for a kind, optionally for one learner. The API deliberately exposes no way
// to ask this, because it is a question about the queue rather than about a learner.
func reminderCount(t *testing.T, h *harness, kind, userID string) int {
	t.Helper()
	query := `SELECT COUNT(*) FROM scheduled_notifications WHERE kind = ?`
	args := []any{kind}
	if userID != "" {
		query += ` AND user_id = ?`
		args = append(args, userID)
	}
	var count int
	if err := h.store.DB.QueryRow(query, args...).Scan(&count); err != nil {
		t.Fatalf("count reminders: %v", err)
	}
	return count
}

func deliveriesFor(t *testing.T, h *harness, template string) int {
	t.Helper()
	var count int
	if err := h.store.DB.QueryRow(
		`SELECT COUNT(*) FROM notification_deliveries WHERE template_key = ?`, template).Scan(&count); err != nil {
		t.Fatalf("count deliveries: %v", err)
	}
	return count
}

// makeDue pulls a scheduled reminder into the past so the next sweep sends it. Waiting until 17:00 local
// is not a test, it is a coffee break.
func makeDue(t *testing.T, h *harness, kind, userID string) {
	t.Helper()
	if _, err := h.store.DB.Exec(
		`UPDATE scheduled_notifications SET due_at = ? WHERE kind = ? AND user_id = ? AND status = 'pending'`,
		store.TS(time.Now().UTC().Add(-time.Minute)), kind, userID); err != nil {
		t.Fatalf("make reminder due: %v", err)
	}
}

// TestAReminderIsPlannedOnceAndSentOnce covers the two properties that make a scheduled queue usable:
// re-deciding does not duplicate the promise, and running the sweep again does not duplicate the email.
func TestAReminderIsPlannedOnceAndSentOnce(t *testing.T) {
	h := newHarness(t)
	learner := h.registerAndVerify("nudged@example.com")
	learnerID := h.userID("nudged@example.com")

	status, body := h.do("PUT", "/v1/study-plan", learner, map[string]any{
		"targetDate":   time.Now().UTC().AddDate(0, 0, 40).Format("2006-01-02"),
		"dailyMinutes": 25,
	})
	if status != http.StatusOK {
		t.Fatalf("save study plan: %d %v", status, body)
	}

	admin := h.adminToken()
	status, body = h.do("POST", "/v1/admin/reminders/sweep", admin, nil)
	if status != http.StatusOK {
		t.Fatalf("sweep: %d %v", status, body)
	}
	if planned := body["planned"].(float64); planned < 1 {
		t.Fatalf("expected the planner to schedule at least one reminder, got %v", planned)
	}
	if got := reminderCount(t, h, "study_nudge", learnerID); got != 1 {
		t.Fatalf("expected exactly one study nudge, got %d", got)
	}

	// The planner runs on a timer. Running it again must be a no-op, not a second nudge.
	if status, body = h.do("POST", "/v1/admin/reminders/sweep", admin, nil); status != http.StatusOK {
		t.Fatalf("second sweep: %d %v", status, body)
	}
	if got := reminderCount(t, h, "study_nudge", learnerID); got != 1 {
		t.Fatalf("re-planning duplicated the reminder: %d rows", got)
	}
	if got := deliveriesFor(t, h, "study_nudge"); got != 0 {
		t.Fatalf("a reminder due later today must not be sent early, %d deliveries", got)
	}

	// Once it is due, it goes out — exactly once.
	makeDue(t, h, "study_nudge", learnerID)
	for i := 0; i < 2; i++ {
		if status, body = h.do("POST", "/v1/admin/reminders/sweep", admin, nil); status != http.StatusOK {
			t.Fatalf("sweep %d: %d %v", i, status, body)
		}
	}
	if got := deliveriesFor(t, h, "study_nudge"); got != 1 {
		t.Fatalf("expected exactly one nudge email, got %d", got)
	}

	var nudgeStatus, note string
	if err := h.store.DB.QueryRow(
		`SELECT status, note FROM scheduled_notifications WHERE kind = 'study_nudge' AND user_id = ?`,
		learnerID).Scan(&nudgeStatus, &note); err != nil {
		t.Fatalf("read reminder: %v", err)
	}
	if nudgeStatus != "sent" {
		t.Fatalf("expected the reminder to settle as sent, got %q (%s)", nudgeStatus, note)
	}

	// The message reached the learner's inbox and is visible to staff without exposing anything secret.
	status, body = h.do("GET", "/v1/admin/reminders", admin, nil)
	if status != http.StatusOK {
		t.Fatalf("list reminders: %d %v", status, body)
	}
	items := body["reminders"].([]any)
	if len(items) == 0 {
		t.Fatal("expected the staff view to show the reminder")
	}
	first := items[0].(map[string]any)
	if first["kind"] != "study_nudge" || first["status"] != "sent" {
		t.Fatalf("unexpected reminder row: %v", first)
	}
}

// TestADeadlineReminderIsWithdrawnWhenTheLearnerSubmits is the revalidation rule, and the reason a
// reminder carries a resource id rather than only a message: a warning written on Monday must not tell a
// learner to submit on Thursday if they submitted on Tuesday.
func TestADeadlineReminderIsWithdrawnWhenTheLearnerSubmits(t *testing.T) {
	h := newHarness(t)
	teacher := h.login("teacher@edumart.ng", "Teacher123!")

	status, body := h.do("POST", "/v1/classes", teacher, map[string]any{"name": "Deadline 301"})
	if status != http.StatusCreated {
		t.Fatalf("create class: %d %v", status, body)
	}
	class := body["class"].(map[string]any)
	classID := class["id"].(string)
	joinCode := class["joinCode"].(string)

	learner := h.registerAndVerify("deadline-learner@example.com")
	learnerID := h.userID("deadline-learner@example.com")
	if status, body := h.do("POST", "/v1/classes/join", learner, map[string]any{"joinCode": joinCode}); status != http.StatusOK {
		t.Fatalf("join class: %d %v", status, body)
	}

	// A published assessment closing in 20 hours: inside the planner's horizon, outside the 2-hour
	// warning window, so the reminder is scheduled for later rather than fired immediately.
	definitionID := store.NewID()
	closesAt := time.Now().UTC().Add(20 * time.Hour)
	if _, err := h.store.DB.Exec(`INSERT INTO assessment_definitions
		(id, owner_user_id, class_id, title, type, duration_seconds, attempt_limit, availability_end,
		 status, published_at, created_at)
		VALUES (?,?,?,?,'class',1800,1,?,'published',?,?)`,
		definitionID, h.userID("teacher@edumart.ng"), classID, "Cell Biology Test",
		store.TS(closesAt), store.Now(), store.Now()); err != nil {
		t.Fatalf("seed definition: %v", err)
	}

	admin := h.adminToken()
	if status, body := h.do("POST", "/v1/admin/reminders/sweep", admin, nil); status != http.StatusOK {
		t.Fatalf("sweep: %d %v", status, body)
	}
	if got := reminderCount(t, h, "deadline", learnerID); got != 1 {
		t.Fatalf("expected one deadline reminder, got %d", got)
	}
	if got := deliveriesFor(t, h, "deadline"); got != 0 {
		t.Fatalf("a warning for a test closing in 20 hours must not be sent yet, got %d", got)
	}

	// The learner submits before the warning is due. The reminder must recognise that and stay unsent.
	if _, err := h.store.DB.Exec(`INSERT INTO assessment_sessions
		(id, user_id, definition_id, class_id, mode, attempt_no, status, started_at, deadline_at,
		 submitted_at, score_raw, score_max, score_percent, snapshot_json, created_at)
		VALUES (?,?,?,?,'class',1,'submitted',?,?,?,18,20,90,'{}',?)`,
		store.NewID(), learnerID, definitionID, classID,
		store.Now(), store.TS(closesAt), store.Now(), store.Now()); err != nil {
		t.Fatalf("seed submission: %v", err)
	}

	makeDue(t, h, "deadline", learnerID)
	if status, body := h.do("POST", "/v1/admin/reminders/sweep", admin, nil); status != http.StatusOK {
		t.Fatalf("sweep after submission: %d %v", status, body)
	}

	if got := deliveriesFor(t, h, "deadline"); got != 0 {
		t.Fatalf("a learner who already submitted was still warned: %d deliveries", got)
	}
	var reminderStatus, note string
	if err := h.store.DB.QueryRow(
		`SELECT status, note FROM scheduled_notifications WHERE kind = 'deadline' AND user_id = ?`,
		learnerID).Scan(&reminderStatus, &note); err != nil {
		t.Fatalf("read reminder: %v", err)
	}
	if reminderStatus != "skipped" || note != "already submitted" {
		t.Fatalf("expected the reminder to settle as skipped/already submitted, got %q/%q", reminderStatus, note)
	}

	// The row is kept: "we decided to warn them and then did not, because they had submitted" is an
	// answer support needs, so the record is corrected rather than deleted.
	if reminderCount(t, h, "deadline", learnerID) != 1 {
		t.Fatal("the skipped reminder must be retained for audit")
	}
}

// TestTwoSweepsDoNotSendTheSameReminderTwice is the concurrency property: the claim (pending -> claimed)
// is what stops two workers from both emailing the same learner.
func TestTwoSweepsDoNotSendTheSameReminderTwice(t *testing.T) {
	h := newHarness(t)
	learner := h.registerAndVerify("raced@example.com")
	learnerID := h.userID("raced@example.com")

	if status, body := h.do("PUT", "/v1/study-plan", learner, map[string]any{
		"targetDate":   time.Now().UTC().AddDate(0, 0, 12).Format("2006-01-02"),
		"dailyMinutes": 30,
	}); status != http.StatusOK {
		t.Fatalf("save study plan: %d %v", status, body)
	}

	admin := h.adminToken()
	if status, body := h.do("POST", "/v1/admin/reminders/sweep", admin, nil); status != http.StatusOK {
		t.Fatalf("plan: %d %v", status, body)
	}
	makeDue(t, h, "study_nudge", learnerID)

	var wait sync.WaitGroup
	for i := 0; i < 2; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			status, _ := h.do("POST", "/v1/admin/reminders/sweep", admin, nil)
			if status != http.StatusOK {
				t.Errorf("concurrent sweep returned %d", status)
			}
		}()
	}
	wait.Wait()

	if got := deliveriesFor(t, h, "study_nudge"); got != 1 {
		t.Fatalf("two sweeps produced %d nudges for one learner; the claim did not hold", got)
	}
}

// TestRemindersAreStaffOnly: the queue lists who is being emailed and why, so it is staff data.
func TestRemindersAreStaffOnly(t *testing.T) {
	h := newHarness(t)
	learner := h.login("student@edumart.ng", "Student123!")

	if status, _ := h.do("GET", "/v1/admin/reminders", learner, nil); status != http.StatusForbidden {
		t.Fatalf("a learner must not read the reminder queue, got %d", status)
	}
	if status, _ := h.do("POST", "/v1/admin/reminders/sweep", learner, nil); status != http.StatusForbidden {
		t.Fatalf("a learner must not trigger the sweep, got %d", status)
	}
	// Support can read the queue — it answers "was this learner told?" — but cannot trigger a sweep,
	// because that sends real mail to real people.
	editor := h.login("editor@edumart.ng", "Editor123!")
	editorID := h.userID("editor@edumart.ng")
	if _, err := h.store.DB.Exec(`INSERT INTO role_assignments (id, user_id, role, scope) VALUES (?,?,'support','global')`,
		store.NewID(), editorID); err != nil {
		t.Fatalf("grant support: %v", err)
	}
	if status, body := h.do("GET", "/v1/admin/reminders", editor, nil); status != http.StatusOK {
		t.Fatalf("support must be able to read the reminder queue: %d %v", status, body)
	}
	if status, _ := h.do("POST", "/v1/admin/reminders/sweep", editor, nil); status != http.StatusForbidden {
		t.Fatalf("support must not trigger the sweep, got %d", status)
	}
}

// TestALearnerWithoutAPlanIsNotNudged: reminders follow decisions the learner made. Someone with no study
// plan has not asked to be reminded, and emailing them is not "engagement".
func TestALearnerWithoutAPlanIsNotNudged(t *testing.T) {
	h := newHarness(t)
	h.registerAndVerify("quiet@example.com")
	quietID := h.userID("quiet@example.com")

	admin := h.adminToken()
	if status, body := h.do("POST", "/v1/admin/reminders/sweep", admin, nil); status != http.StatusOK {
		t.Fatalf("sweep: %d %v", status, body)
	}
	if got := reminderCount(t, h, "study_nudge", quietID); got != 0 {
		t.Fatalf("a learner with no study plan received %d nudges", got)
	}
}
