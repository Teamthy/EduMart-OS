package api_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/teamthy/edumart/apps/api/internal/store"
)

// seedSubmittedAttempt writes a submitted assessment in a class. The report is about what learners did,
// so these tests need real attempts rather than an assumed query.
func (h *harness) seedSubmittedAttempt(classID, userID string, attemptNo, scorePercent int, submittedAt time.Time) {
	h.t.Helper()
	// One published assessment per class, reused across attempts: the report counts what was set, not how
	// many attempts were made.
	var definitionID string
	err := h.store.DB.QueryRow(`SELECT id FROM assessment_definitions WHERE class_id = ?`, classID).Scan(&definitionID)
	if err != nil {
		definitionID = store.NewID()
		if _, err := h.store.DB.Exec(`INSERT INTO assessment_definitions
			(id, owner_user_id, class_id, title, type, duration_seconds, attempt_limit, status, published_at, created_at)
			VALUES (?,?,?,?,'class',1800,1,'published',?,?)`,
			definitionID, userID, classID, "Class Test", store.Now(), store.Now()); err != nil {
			h.t.Fatalf("seed definition: %v", err)
		}
	}
	if _, err := h.store.DB.Exec(`INSERT INTO assessment_sessions
		(id, user_id, definition_id, class_id, mode, attempt_no, status, started_at, deadline_at,
		 submitted_at, score_raw, score_max, score_percent, snapshot_json, created_at)
		VALUES (?,?,?,?, 'class', ?, 'submitted', ?, ?, ?, ?, 20, ?, '{}', ?)`,
		store.NewID(), userID, definitionID, classID, attemptNo,
		store.TS(submittedAt.Add(-30*time.Minute)), store.TS(submittedAt.Add(30*time.Minute)),
		store.TS(submittedAt), scorePercent/5, scorePercent, store.TS(submittedAt)); err != nil {
		h.t.Fatalf("seed attempt: %v", err)
	}
}

// TestInstitutionReportCountsWhatHappened is the Phase 20 dashboards item: an institution can see its own
// people, its own teaching and its own results — and nothing else.
func TestInstitutionReportCountsWhatHappened(t *testing.T) {
	h := newHarness(t)
	owner := h.registerAndVerify("report-owner@example.com")
	orgID := h.createOrg(owner, "Report Academy")

	// A class owned by the institution, plus a teacher and a learner in it.
	status, body := h.do("POST", "/v1/classes", owner, map[string]any{
		"name": "SS2 Economics", "organizationId": orgID,
	})
	if status != http.StatusCreated {
		t.Fatalf("create class: %d %v", status, body)
	}
	classID := body["class"].(map[string]any)["id"].(string)

	teacherInvite := h.invite(owner, orgID, "report-teacher@example.com", "teacher")
	teacher := h.registerAndVerify("report-teacher@example.com")
	if status, _ := h.accept(teacher, teacherInvite); status != http.StatusOK {
		t.Fatal("setup: teacher could not join")
	}
	studentInvite := h.invite(owner, orgID, "report-student@example.com", "student")
	student := h.registerAndVerify("report-student@example.com")
	if status, _ := h.accept(student, studentInvite); status != http.StatusOK {
		t.Fatal("setup: student could not join")
	}
	studentID := h.userID("report-student@example.com")

	// Two submissions inside the window and one well outside it. The old one must not be counted: a
	// window that quietly includes everything makes every trend look flat.
	h.seedSubmittedAttempt(classID, studentID, 1, 80, time.Now().UTC().Add(-24*time.Hour))
	h.seedSubmittedAttempt(classID, studentID, 2, 60, time.Now().UTC().Add(-48*time.Hour))
	h.seedSubmittedAttempt(classID, studentID, 3, 100, time.Now().UTC().AddDate(0, 0, -90))

	status, body = h.do("GET", "/v1/organizations/"+orgID+"/report", owner, nil)
	if status != http.StatusOK {
		t.Fatalf("report: %d %v", status, body)
	}
	report := body["report"].(map[string]any)

	members := report["members"].(map[string]any)
	if members["total"] != float64(3) || members["students"] != float64(1) || members["teachers"] != float64(1) {
		t.Fatalf("unexpected member counts: %v", members)
	}

	teaching := report["teaching"].(map[string]any)
	if teaching["classes"] != float64(1) || teaching["assignments"] != float64(1) {
		t.Fatalf("unexpected teaching counts: %v", teaching)
	}

	results := report["results"].(map[string]any)
	if results["submissions"] != float64(2) {
		t.Fatalf("expected only the two recent submissions, got %v", results["submissions"])
	}
	if results["activeLearners"] != float64(1) {
		t.Fatalf("expected one active learner, got %v", results["activeLearners"])
	}
	if results["averageScorePercent"] != float64(70) {
		t.Fatalf("expected the average of 80 and 60, got %v", results["averageScorePercent"])
	}

	classes := report["classes"].([]any)
	if len(classes) != 1 {
		t.Fatalf("expected one class row, got %d", len(classes))
	}
	classRow := classes[0].(map[string]any)
	if classRow["name"] != "SS2 Economics" || classRow["submissions"] != float64(2) {
		t.Fatalf("unexpected class row: %v", classRow)
	}

	// A second institution's report must never include these numbers.
	otherOwner := h.registerAndVerify("other-report-owner@example.com")
	otherOrg := h.createOrg(otherOwner, "Other Academy")
	status, body = h.do("GET", "/v1/organizations/"+otherOrg+"/report", otherOwner, nil)
	if status != http.StatusOK {
		t.Fatalf("other report: %d %v", status, body)
	}
	otherResults := body["report"].(map[string]any)["results"].(map[string]any)
	if otherResults["submissions"] != float64(0) || otherResults["averageScorePercent"] != nil {
		t.Fatalf("another institution's results leaked in: %v", otherResults)
	}
}

// TestInstitutionReportIsForAdministrators: a teacher teaches, an administrator runs the place. The
// report carries every learner's results, so it follows the roster's rule.
func TestInstitutionReportIsForAdministrators(t *testing.T) {
	h := newHarness(t)
	owner := h.registerAndVerify("report-access-owner@example.com")
	orgID := h.createOrg(owner, "Access Academy")

	invite := h.invite(owner, orgID, "report-access-teacher@example.com", "teacher")
	teacher := h.registerAndVerify("report-access-teacher@example.com")
	h.accept(teacher, invite)
	studentInvite := h.invite(owner, orgID, "report-access-student@example.com", "student")
	student := h.registerAndVerify("report-access-student@example.com")
	h.accept(student, studentInvite)

	if status, _ := h.do("GET", "/v1/organizations/"+orgID+"/report", teacher, nil); status != http.StatusForbidden {
		t.Fatalf("a teacher must not read the institution report, got %d", status)
	}
	if status, _ := h.do("GET", "/v1/organizations/"+orgID+"/report", student, nil); status != http.StatusForbidden {
		t.Fatalf("a student must not read the institution report, got %d", status)
	}
	outsider := h.registerAndVerify("report-outsider@example.com")
	if status, _ := h.do("GET", "/v1/organizations/"+orgID+"/report", outsider, nil); status != http.StatusNotFound {
		t.Fatalf("a non-member must get 404, got %d", status)
	}
	// An empty institution reports zeros with a null average rather than pretending to have data.
	status, body := h.do("GET", "/v1/organizations/"+orgID+"/report", owner, nil)
	if status != http.StatusOK {
		t.Fatalf("owner report: %d %v", status, body)
	}
	results := body["report"].(map[string]any)["results"].(map[string]any)
	if results["averageScorePercent"] != nil {
		t.Fatalf("an institution with no submissions must report a null average, got %v", results["averageScorePercent"])
	}
	if body["report"].(map[string]any)["classes"].([]any) == nil {
		t.Fatal("classes must be an empty array, not null")
	}
}
