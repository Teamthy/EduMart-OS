package orgs

import (
	"context"
	"database/sql"
	"time"

	"github.com/teamthy/edumart/apps/api/internal/auth"
	"github.com/teamthy/edumart/apps/api/internal/store"
)

// reportWindow is how far back the activity numbers look.
//
// Thirty days is a deliberate choice over "this term": a month is long enough to show a trend and short
// enough that a broken weekly routine is visible before the term is over, and it does not need a
// calendar concept the product does not have yet.
const reportWindow = 30 * 24 * time.Hour

// MemberCounts is the roster broken down by role.
type MemberCounts struct {
	Total    int `json:"total"`
	Owners   int `json:"owners"`
	Admins   int `json:"admins"`
	Teachers int `json:"teachers"`
	Students int `json:"students"`
}

// TeachingCounts is what the institution is running.
type TeachingCounts struct {
	Classes int `json:"classes"`
	// Assignments counts published assessments attached to the institution's classes. Drafts are work
	// in progress, not teaching, and counting them would make every institution look busy.
	Assignments int `json:"assignments"`
}

// ResultSummary is what the institution's learners have done recently.
type ResultSummary struct {
	// ActiveLearners is how many members have submitted at least one assessment in the window. It is the
	// number an institution actually cares about: total members says nothing about use.
	ActiveLearners int `json:"activeLearners"`
	Submissions    int `json:"submissions"`
	// AverageScorePercent is null rather than 0 when nothing has been submitted. A zero would be a claim
	// about performance; null is the truth, and the client renders it as "no data yet".
	AverageScorePercent *int `json:"averageScorePercent"`
}

// ClassReport is one class's slice of the same numbers.
type ClassReport struct {
	ID                  string `json:"id"`
	Name                string `json:"name"`
	Members             int    `json:"members"`
	Assignments         int    `json:"assignments"`
	Submissions         int    `json:"submissions"`
	AverageScorePercent *int   `json:"averageScorePercent"`
}

// Report is the institution dashboard: who is here, what is being taught, and what is actually happening.
type Report struct {
	OrganizationID string         `json:"organizationId"`
	GeneratedAt    string         `json:"generatedAt"`
	WindowDays     int            `json:"windowDays"`
	Members        MemberCounts   `json:"members"`
	Teaching       TeachingCounts `json:"teaching"`
	Results        ResultSummary  `json:"results"`
	Classes        []ClassReport  `json:"classes"`
}

// Report builds the institution dashboard for its owners and administrators.
//
// Every query is scoped by the organisation id at the SQL level rather than filtered in Go afterwards:
// a report is exactly the place where one institution's numbers would leak into another's, and a filter
// that lives in application code is one refactor away from being forgotten.
func (s *Service) Report(ctx context.Context, actor *auth.User, orgID string) (*Report, error) {
	if _, err := s.memberOrAdmin(ctx, actor, orgID, true); err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	since := store.TS(now.Add(-reportWindow))

	report := &Report{
		OrganizationID: orgID,
		GeneratedAt:    store.TS(now),
		WindowDays:     int(reportWindow.Hours() / 24),
		Classes:        []ClassReport{},
	}

	if err := s.readMemberCounts(ctx, orgID, &report.Members); err != nil {
		return nil, err
	}
	if err := s.readTeachingCounts(ctx, orgID, &report.Teaching); err != nil {
		return nil, err
	}
	if err := s.readResults(ctx, orgID, since, &report.Results); err != nil {
		return nil, err
	}
	if err := s.readClassRows(ctx, orgID, since, &report.Classes); err != nil {
		return nil, err
	}
	return report, nil
}

func (s *Service) readMemberCounts(ctx context.Context, orgID string, out *MemberCounts) error {
	rows, err := s.db.DB.QueryContext(ctx, `
		SELECT role, COUNT(*) FROM organization_memberships
		 WHERE organization_id = ? AND status = 'active'
		 GROUP BY role`, orgID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var role string
		var count int
		if err := rows.Scan(&role, &count); err != nil {
			return err
		}
		switch role {
		case RoleOwner:
			out.Owners += count
		case RoleAdmin:
			out.Admins += count
		case RoleTeacher:
			out.Teachers += count
		default:
			out.Students += count
		}
		out.Total += count
	}
	return rows.Err()
}

func (s *Service) readTeachingCounts(ctx context.Context, orgID string, out *TeachingCounts) error {
	err := s.db.DB.QueryRowContext(ctx, `
		SELECT
			(SELECT COUNT(*) FROM classes c
			  WHERE c.organization_id = ? AND c.status = 'active'),
			(SELECT COUNT(*) FROM assessment_definitions d
			  JOIN classes c ON c.id = d.class_id
			 WHERE c.organization_id = ? AND d.status = 'published')`, orgID, orgID).
		Scan(&out.Classes, &out.Assignments)
	return err
}

func (s *Service) readResults(ctx context.Context, orgID, since string, out *ResultSummary) error {
	var average sql.NullFloat64
	err := s.db.DB.QueryRowContext(ctx, `
		SELECT COUNT(*), COUNT(DISTINCT s.user_id), AVG(s.score_percent)
		  FROM assessment_sessions s
		  JOIN organization_memberships m
		    ON m.user_id = s.user_id AND m.organization_id = ? AND m.status = 'active'
		 WHERE s.status = 'submitted' AND s.submitted_at >= ?`, orgID, since).
		Scan(&out.Submissions, &out.ActiveLearners, &average)
	if err != nil {
		return err
	}
	out.AverageScorePercent = percentOrNil(average)
	return nil
}

// readClassRows reads per-class numbers, then closes the cursor before touching the database again: an
// open result set plus a second statement on the same connection is exactly the pattern PostgreSQL
// rejects and SQLite tolerates, and this code has to run on both.
func (s *Service) readClassRows(ctx context.Context, orgID, since string, out *[]ClassReport) error {
	rows, err := s.db.DB.QueryContext(ctx, `
		SELECT c.id, c.name,
		       (SELECT COUNT(*) FROM class_memberships cm
		         WHERE cm.class_id = c.id AND cm.status = 'active'),
		       (SELECT COUNT(*) FROM assessment_definitions d
		         WHERE d.class_id = c.id AND d.status = 'published'),
		       (SELECT COUNT(*) FROM assessment_sessions s
		         WHERE s.class_id = c.id AND s.status = 'submitted' AND s.submitted_at >= ?),
		       (SELECT AVG(s.score_percent) FROM assessment_sessions s
		         WHERE s.class_id = c.id AND s.status = 'submitted' AND s.submitted_at >= ?)
		  FROM classes c
		 WHERE c.organization_id = ? AND c.status = 'active'
		 ORDER BY c.name`, since, since, orgID)
	if err != nil {
		return err
	}

	classRows := []ClassReport{}
	averages := []sql.NullFloat64{}
	for rows.Next() {
		var row ClassReport
		var average sql.NullFloat64
		if err := rows.Scan(&row.ID, &row.Name, &row.Members, &row.Assignments, &row.Submissions, &average); err != nil {
			_ = rows.Close()
			return err
		}
		classRows = append(classRows, row)
		averages = append(averages, average)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	_ = rows.Close()

	for i := range classRows {
		classRows[i].AverageScorePercent = percentOrNil(averages[i])
	}
	*out = classRows
	return nil
}

// percentOrNil rounds an average the way a score is shown, and keeps "nothing submitted" distinct from
// "everybody scored zero".
func percentOrNil(value sql.NullFloat64) *int {
	if !value.Valid {
		return nil
	}
	rounded := int(value.Float64 + 0.5)
	return &rounded
}
