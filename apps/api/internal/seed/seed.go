// Package seed loads deterministic demo content so every environment starts from the same state.
// It is idempotent: running it twice never duplicates rows and never rewrites learner data.
package seed

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/teamthy/edumart/apps/api/internal/auth"
	"github.com/teamthy/edumart/apps/api/internal/store"
)

//go:embed content.json
var contentFS embed.FS

type bankQuestion struct {
	Topic       string   `json:"topic"`
	Difficulty  string   `json:"difficulty"`
	Stem        string   `json:"stem"`
	Options     []string `json:"options"`
	Answer      int      `json:"answer"`
	Explanation string   `json:"explanation"`
}

type bankSubject struct {
	Key       string         `json:"key"`
	Name      string         `json:"name"`
	Icon      string         `json:"icon"`
	Pathway   string         `json:"pathway"`
	Topics    []bankTopic    `json:"topics"`
	Questions []bankQuestion `json:"questions"`
}

type bankTopic struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}

type bankProduct struct {
	Slug        string `json:"slug"`
	Title       string `json:"title"`
	Subtitle    string `json:"subtitle"`
	Description string `json:"description"`
	Type        string `json:"type"`
	SubjectKey  string `json:"subjectKey"`
	ExamKey     string `json:"examKey"`
	PriceMinor  int    `json:"priceMinor"`
	Pages       int    `json:"pages"`
	CoverTone   string `json:"coverTone"`
	Publisher   string `json:"publisher"`
}

type contentFile struct {
	Subjects []bankSubject `json:"subjects"`
	Products []bankProduct `json:"products"`
}

// DemoUsers are the documented logins. They exist only when seeding is enabled.
var DemoUsers = []struct {
	Email       string
	Password    string
	DisplayName string
	Roles       []string
}{
	{"student@edumart.ng", "Student123!", "Amara Okafor", []string{auth.RoleStudent}},
	{"teacher@edumart.ng", "Teacher123!", "Mr. Adeyemi Balogun", []string{auth.RoleTeacher, auth.RoleStudent}},
	{"admin@edumart.ng", "Admin123!", "Platform Admin", []string{auth.RolePlatformAdmin}},
	{"editor@edumart.ng", "Editor123!", "Ngozi Content", []string{auth.RoleContentEditor}},
}

// Options configures seeding.
type Options struct {
	IncludeDemoUsers bool
	IncludeSamples   bool
}

// Run applies the seed. It returns an error only for genuine failures.
func Run(ctx context.Context, db *store.Store, opts Options) error {
	if existing, ok := db.MetaGet(ctx, "seed_version"); ok && existing == "2" {
		return nil
	}

	raw, err := contentFS.ReadFile("content.json")
	if err != nil {
		return err
	}
	var content contentFile
	if err := json.Unmarshal(raw, &content); err != nil {
		return fmt.Errorf("decode seed content: %w", err)
	}

	pathways := []struct{ Key, Name, Body, Blurb string }{
		{"jamb", "JAMB UTME", "Joint Admissions and Matriculation Board", "The most common route into Nigerian universities."},
		{"waec", "WAEC SSCE", "West African Examinations Council", "School certificate for university and work."},
		{"neco", "NECO SSCE", "National Examinations Council", "June/July school certificate."},
		{"post_utme", "Post-UTME", "Institution screening", "University-specific screening after JAMB."},
	}

	subjectIDs := map[string]string{}
	topicIDs := map[string]string{}

	err = db.Tx(ctx, func(tx *sql.Tx) error {
		pathwayIDs := map[string]string{}
		for _, p := range pathways {
			id := deterministicID("pw", p.Key)
			if _, err := tx.ExecContext(ctx, `INSERT INTO exam_pathways (id, key, name, body, blurb, active)
				VALUES (?,?,?,?,?,1) ON CONFLICT (key) DO NOTHING`, id, p.Key, p.Name, p.Body, p.Blurb); err != nil {
				return err
			}
			var actual string
			if err := tx.QueryRowContext(ctx, `SELECT id FROM exam_pathways WHERE key = ?`, p.Key).Scan(&actual); err != nil {
				return err
			}
			pathwayIDs[p.Key] = actual
		}

		now := store.Now()
		for _, subject := range content.Subjects {
			subjectID := deterministicID("subj", subject.Key)
			pathwayKey := subject.Pathway
			if pathwayKey == "" {
				pathwayKey = "jamb"
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO subjects (id, key, name, exam_pathway_id, icon, active)
				VALUES (?,?,?,?,?,1) ON CONFLICT (key) DO NOTHING`,
				subjectID, subject.Key, subject.Name, pathwayIDs[pathwayKey], subject.Icon); err != nil {
				return err
			}
			subjectIDs[subject.Key] = subjectID

			for i, topic := range subject.Topics {
				topicID := deterministicID("top", subject.Key+":"+topic.Key)
				if _, err := tx.ExecContext(ctx, `INSERT INTO topics
					(id, key, name, subject_id, sort_order, syllabus_version) VALUES (?,?,?,?,?,'2025')
					ON CONFLICT (subject_id, key) DO NOTHING`,
					topicID, topic.Key, topic.Name, subjectID, i); err != nil {
					return err
				}
				topicIDs[subject.Key+":"+topic.Key] = topicID
			}

			for i, question := range subject.Questions {
				questionID := deterministicID("q", subject.Key+fmt.Sprintf(":%02d", i))
				topicID := topicIDs[subject.Key+":"+question.Topic]
				difficulty := question.Difficulty
				if difficulty == "" {
					difficulty = "medium"
				}
				if _, err := tx.ExecContext(ctx, `INSERT INTO questions
					(id, subject_id, topic_id, type, difficulty, status, created_at) VALUES (?,?,?,'mcq',?,'published',?)
					ON CONFLICT (id) DO NOTHING`, questionID, subjectID, nullable(topicID), difficulty, now); err != nil {
					return err
				}

				options := make([]map[string]string, 0, len(question.Options))
				labels := []string{"A", "B", "C", "D", "E", "F"}
				for j, text := range question.Options {
					options = append(options, map[string]string{
						"id":    fmt.Sprintf("opt_%d", j+1),
						"label": labels[j] + ". " + text,
					})
				}
				optionsJSON, err := json.Marshal(options)
				if err != nil {
					return err
				}
				answerOptionID := fmt.Sprintf("opt_%d", question.Answer+1)
				qvID := deterministicID("qv", subject.Key+fmt.Sprintf(":%02d:1", i))
				if _, err := tx.ExecContext(ctx, `INSERT INTO question_versions
					(id, question_id, version, stem, options_json, answer_option_id, explanation, source_name,
					 license_basis, attribution, review_state, published_at, created_at)
					VALUES (?,?,1,?,?,?,?,'Edumart practice bank','internal','Edumart content team','approved',?,?)
					ON CONFLICT (question_id, version) DO NOTHING`,
					qvID, questionID, question.Stem, string(optionsJSON), answerOptionID,
					question.Explanation, now, now); err != nil {
					return err
				}
			}
		}

		// Two draft versions for the editorial queue, so the review workflow is demonstrable.
		if err := seedReviewDrafts(ctx, tx); err != nil {
			return err
		}

		publisherIDs := map[string]string{}
		for _, product := range content.Products {
			name := product.Publisher
			if name == "" {
				name = "Edumart Press"
			}
			if _, ok := publisherIDs[name]; !ok {
				publisherID := deterministicID("pub", name)
				if _, err := tx.ExecContext(ctx, `INSERT INTO publishers (id, name, status, email)
					VALUES (?,?, 'approved','partners@edumart.ng') ON CONFLICT (name) DO NOTHING`, publisherID, name); err != nil {
					return err
				}
				var actual string
				if err := tx.QueryRowContext(ctx, `SELECT id FROM publishers WHERE name = ?`, name).Scan(&actual); err != nil {
					return err
				}
				publisherIDs[name] = actual
			}

			productType := product.Type
			if productType == "" {
				productType = "book"
			}
			coverTone := product.CoverTone
			if coverTone == "" {
				coverTone = "indigo"
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO products
				(id, publisher_id, slug, title, subtitle, description, type, subject_key, exam_key,
				 price_minor, currency, status, pages, cover_tone, created_at)
				VALUES (?,?,?,?,?,?,?,?,?,?,'NGN','published',?,?,?)
				ON CONFLICT (slug) DO NOTHING`,
				deterministicID("prod", product.Slug), publisherIDs[name], product.Slug, product.Title,
				product.Subtitle, product.Description, productType, product.SubjectKey, product.ExamKey,
				product.PriceMinor, product.Pages, coverTone, now); err != nil {
				return err
			}
		}

		if opts.IncludeDemoUsers {
			if err := seedDemoUsers(ctx, tx, subjectIDs); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}

	if opts.IncludeSamples {
		if err := seedSamples(ctx, db); err != nil {
			return err
		}
	}

	return db.MetaSet(ctx, "seed_version", "2")
}

func seedDemoUsers(ctx context.Context, tx *sql.Tx, subjectIDs map[string]string) error {
	now := store.Now()
	for _, demo := range DemoUsers {
		email := auth.NormalizeEmail(demo.Email)
		var userID string
		err := tx.QueryRowContext(ctx, `SELECT id FROM users WHERE email_normalized = ?`, email).Scan(&userID)
		if errors.Is(err, sql.ErrNoRows) {
			userID = deterministicID("user", email)
			hash, err := auth.HashPassword(demo.Password)
			if err != nil {
				return err
			}
			// Deterministic ids keep seeded attempts and referrals stable across resets.
			target := time.Now().UTC().AddDate(0, 0, 45).Format("2006-01-02")
			// Demo accounts are fixtures for an established cohort, so their email is already
			// proven. Real signups are not: they must confirm before the address is trusted.
			if _, err := tx.ExecContext(ctx, `INSERT INTO users
				(id, email_normalized, password_hash, display_name, status, exam_pathway_id, target_date, timezone,
				 daily_minutes, onboarding_step, onboarding_done_at, email_verified_at, created_at, updated_at)
				VALUES (?,?,?,?,'active',?,?, 'Africa/Lagos', 30, 5, ?, ?,?,?)`,
				userID, email, hash, demo.DisplayName,
				func() any {
					var id string
					_ = tx.QueryRowContext(ctx, `SELECT id FROM exam_pathways WHERE key = 'jamb'`).Scan(&id)
					return nullable(id)
				}(),
				target, now, now, now, now); err != nil {
				return err
			}
		}

		for _, role := range demo.Roles {
			if _, err := tx.ExecContext(ctx, `INSERT INTO role_assignments (id, user_id, role, scope)
				VALUES (?,?,?,'global') ON CONFLICT (user_id, role, scope) DO NOTHING`,
				deterministicID("role", email+":"+role), userID, role); err != nil {
				return err
			}
		}

		if strings.Contains(email, "student") {
			for _, key := range []string{"mathematics", "use_of_english"} {
				if id, ok := subjectIDs[key]; ok {
					if _, err := tx.ExecContext(ctx, `INSERT INTO user_subjects (user_id, subject_id, created_at)
						VALUES (?,?,?) ON CONFLICT (user_id, subject_id) DO NOTHING`, userID, id, now); err != nil {
						return err
					}
				}
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO referral_codes (id, owner_user_id, code, status, created_at)
				VALUES (?,?,'AMARA-2026','active',?) ON CONFLICT (owner_user_id) DO NOTHING`,
				deterministicID("ref", email), userID, now); err != nil {
				return err
			}
		}
	}
	return nil
}

func seedReviewDrafts(ctx context.Context, tx *sql.Tx) error {
	var subjectID, topicID string
	if err := tx.QueryRowContext(ctx, `SELECT id FROM subjects WHERE key = 'mathematics'`).Scan(&subjectID); err != nil {
		return nil // taxonomy not present yet
	}
	_ = tx.QueryRowContext(ctx, `SELECT id FROM topics WHERE subject_id = ? ORDER BY sort_order LIMIT 1`, subjectID).Scan(&topicID)

	drafts := []struct{ key, stem, explanation string }{
		{"review-1", "Simplify: (2x + 3)(x − 4).", "Expand each term: 2x² − 8x + 3x − 12 = 2x² − 5x − 12."},
		{"review-2", "A trader buys an item for ₦4,500 and sells it for ₦5,400. What is the percentage profit?",
			"Profit is ₦900 on ₦4,500 cost, so 900 ÷ 4,500 = 20%."},
	}
	now := store.Now()
	options, _ := json.Marshal([]map[string]string{
		{"id": "opt_1", "label": "A. First option"},
		{"id": "opt_2", "label": "B. Second option"},
		{"id": "opt_3", "label": "C. Third option"},
		{"id": "opt_4", "label": "D. Fourth option"},
	})

	for i, draft := range drafts {
		questionID := deterministicID("q", draft.key)
		if _, err := tx.ExecContext(ctx, `INSERT INTO questions
			(id, subject_id, topic_id, type, difficulty, status, created_at) VALUES (?,?,?,'mcq','medium','draft',?)
			ON CONFLICT (id) DO NOTHING`, questionID, subjectID, nullable(topicID), now); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO question_versions
			(id, question_id, version, stem, options_json, answer_option_id, explanation, source_name,
			 license_basis, attribution, review_state, created_at)
			VALUES (?,?,1,?,?,'opt_2',?,'Submitted by partner school','licensed','Lagos Tutorial Collective','pending_review',?)
			ON CONFLICT (question_id, version) DO NOTHING`,
			deterministicID("qv", draft.key+":1"), questionID, draft.stem, string(options), draft.explanation, now); err != nil {
			return err
		}
		_ = i
	}
	return nil
}

func seedSamples(ctx context.Context, db *store.Store) error {
	var teacherID, studentID string
	if err := db.DB.QueryRowContext(ctx, `SELECT id FROM users WHERE email_normalized = 'teacher@edumart.ng'`).Scan(&teacherID); err != nil {
		return nil // demo users disabled: nothing to sample
	}
	if err := db.DB.QueryRowContext(ctx, `SELECT id FROM users WHERE email_normalized = 'student@edumart.ng'`).Scan(&studentID); err != nil {
		return nil
	}

	now := store.Now()
	classID := deterministicID("class", "LEKKI24")
	var mathSubjectID string
	_ = db.DB.QueryRowContext(ctx, `SELECT id FROM subjects WHERE key = 'mathematics'`).Scan(&mathSubjectID)

	if _, err := db.DB.ExecContext(ctx, `INSERT INTO classes
		(id, organization_id, owner_user_id, name, subject_id, join_code, status, created_at)
		VALUES (?,NULL,?,'Lekki Study Circle',?,'LEKKI24','active',?) ON CONFLICT (join_code) DO NOTHING`,
		classID, teacherID, nullable(mathSubjectID), now); err != nil {
		return err
	}
	var actualClassID string
	if err := db.DB.QueryRowContext(ctx, `SELECT id FROM classes WHERE join_code = 'LEKKI24'`).Scan(&actualClassID); err != nil {
		return err
	}
	for _, member := range []struct{ id, role string }{{teacherID, "teacher"}, {studentID, "student"}} {
		if _, err := db.DB.ExecContext(ctx, `INSERT INTO class_memberships (id, class_id, user_id, role, status, joined_at)
			VALUES (?,?,?,?,'active',?) ON CONFLICT (class_id, user_id) DO NOTHING`,
			deterministicID("cm", member.id), actualClassID, member.id, member.role, now); err != nil {
			return err
		}
	}

	definitionID := deterministicID("def", "LEKKI24:mock1")
	endsAt := time.Now().UTC().AddDate(0, 0, 7)
	err := db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO assessment_definitions
			(id, owner_user_id, class_id, title, type, duration_seconds, availability_start, availability_end,
			 attempt_limit, scoring_policy_json, config_json, result_release, status, published_at, created_at)
			VALUES (?,?,?,'Lekki Mock 1 — Algebra & Number','mock',1800,?,?,2,
			 '{"version":1,"correct":1,"wrong":0,"unanswered":0,"negativeMarking":false}','{}','immediate','published',?,?)
			ON CONFLICT (id) DO NOTHING`,
			definitionID, teacherID, actualClassID, now, store.TS(endsAt), now, now); err != nil {
			return err
		}

		rows, err := tx.QueryContext(ctx, `SELECT qv.id FROM question_versions qv
			JOIN questions q ON q.id = qv.question_id
			JOIN subjects s ON s.id = q.subject_id
			WHERE s.key = 'mathematics' AND qv.review_state = 'approved'
			ORDER BY qv.id LIMIT 10`)
		if err != nil {
			return err
		}
		var qvIDs []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			qvIDs = append(qvIDs, id)
		}
		rows.Close()

		for i, qvID := range qvIDs {
			if _, err := tx.ExecContext(ctx, `INSERT INTO assessment_items
				(id, definition_id, question_version_id, points, position) VALUES (?,?,?,1,?)
				ON CONFLICT (definition_id, position) DO NOTHING`,
				deterministicID("item", definitionID+fmt.Sprintf(":%02d", i)), definitionID, qvID, i+1); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}

	// One historical completed attempt so the dashboard, analytics and points ledger are non-empty.
	var existing string
	err = db.DB.QueryRowContext(ctx, `SELECT id FROM assessment_sessions WHERE user_id = ? AND mode = 'practice' LIMIT 1`,
		studentID).Scan(&existing)
	if errors.Is(err, sql.ErrNoRows) {
		return seedHistoricalAttempt(ctx, db, studentID)
	}
	return err
}

func seedHistoricalAttempt(ctx context.Context, db *store.Store, studentID string) error {
	completed := time.Now().UTC().AddDate(0, 0, -2)
	sessionID := deterministicID("session", "seed:practice:1")
	snapshot, err := buildSnapshot(ctx, db, "mathematics", 10, sessionID)
	if err != nil || len(snapshot.Questions) == 0 {
		return err
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}

	if _, err := db.DB.ExecContext(ctx, `INSERT INTO assessment_sessions
		(id, user_id, definition_id, class_id, mode, attempt_no, status, started_at, deadline_at, submitted_at,
		 score_raw, score_max, score_percent, snapshot_json, created_at)
		VALUES (?,?,NULL,NULL,'practice',1,'submitted',?,?,?,?,?,?,?,?)
		ON CONFLICT (id) DO NOTHING`,
		sessionID, studentID, store.TS(completed.Add(-12*time.Minute)), store.TS(completed.Add(-2*time.Minute)),
		store.TS(completed), 7, len(snapshot.Questions), 70, string(raw), store.Now()); err != nil {
		return err
	}

	correct := 0
	for i, question := range snapshot.Questions {
		selected := question.Options[0]["id"]
		isCorrect := false
		if i%3 != 0 { // 7 of 10 correct, deterministically
			selected = question.AnswerOption
			isCorrect = true
			correct++
		}
		var flag any
		if isCorrect {
			flag = 1
		} else {
			flag = 0
		}
		awarded := 0
		if isCorrect {
			awarded = 1
		}
		if _, err := db.DB.ExecContext(ctx, `INSERT INTO assessment_responses
			(id, session_id, question_version_id, selected_option_id, is_correct, awarded_points,
			 answer_version, saved_at, updated_at) VALUES (?,?,?,?,?,?,1,?,?)
			ON CONFLICT (session_id, question_version_id) DO NOTHING`,
			deterministicID("resp", sessionID+":"+question.QvID), sessionID, question.QvID, selected,
			flag, awarded, store.TS(completed), store.TS(completed)); err != nil {
			return err
		}
		if question.TopicID != "" {
			correctFlag := 0
			if isCorrect {
				correctFlag = 1
			}
			if _, err := db.DB.ExecContext(ctx, `INSERT INTO topic_performance
				(id, user_id, topic_id, attempts, correct, last_practiced_at) VALUES (?,?,?,1,?,?)
				ON CONFLICT (user_id, topic_id) DO UPDATE SET attempts = topic_performance.attempts + 1,
					correct = topic_performance.correct + ?, last_practiced_at = ?`,
				deterministicID("tp", studentID+":"+question.TopicID), studentID, question.TopicID,
				correctFlag, store.TS(completed), correctFlag, store.TS(completed)); err != nil {
				return err
			}
		}
	}

	_, err = db.DB.ExecContext(ctx, `INSERT INTO points_ledger_entries
		(id, user_id, amount, entry_type, source_type, source_id, rule_version, idempotency_key, note, created_at)
		VALUES (?,?,20,'earn','practice_completed',?,'v1',?,'Practice set completed',?)
		ON CONFLICT (idempotency_key) DO NOTHING`,
		deterministicID("pts", sessionID), studentID, sessionID, "points:attempt:"+sessionID, store.TS(completed))
	return err
}

func buildSnapshot(ctx context.Context, db *store.Store, subjectKey string, limit int, seedKey string) (*snapshotShape, error) {
	out := &snapshotShape{Policy: policyShape{Version: 1, Correct: 1, Wrong: 0, Unanswered: 0}}
	rows, err := db.DB.QueryContext(ctx, `SELECT qv.id, qv.question_id, qv.stem, qv.options_json, qv.answer_option_id,
		qv.explanation, q.difficulty, q.subject_id, s.name, COALESCE(q.topic_id,''), COALESCE(t.name,'')
		FROM question_versions qv
		JOIN questions q ON q.id = qv.question_id
		JOIN subjects s ON s.id = q.subject_id
		LEFT JOIN topics t ON t.id = q.topic_id
		WHERE s.key = ? AND qv.review_state = 'approved' AND qv.published_at IS NOT NULL
		ORDER BY qv.id LIMIT ?`, subjectKey, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	position := 0
	for rows.Next() {
		var q questionShape
		var optionsJSON string
		if err := rows.Scan(&q.QvID, &q.QuestionID, &q.Stem, &optionsJSON, &q.AnswerOption, &q.Explanation,
			&q.Difficulty, &q.SubjectID, &q.SubjectName, &q.TopicID, &q.TopicName); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(optionsJSON), &q.Options); err != nil {
			return nil, err
		}
		position++
		q.Points = 1
		q.Position = position
		out.Questions = append(out.Questions, q)
	}
	return out, rows.Err()
}

type snapshotShape struct {
	Policy    policyShape     `json:"policy"`
	Questions []questionShape `json:"questions"`
}

type policyShape struct {
	Version         int  `json:"version"`
	Correct         int  `json:"correct"`
	Wrong           int  `json:"wrong"`
	Unanswered      int  `json:"unanswered"`
	NegativeMarking bool `json:"negativeMarking"`
}

type questionShape struct {
	QvID         string              `json:"qvId"`
	QuestionID   string              `json:"questionId"`
	SubjectID    string              `json:"subjectId"`
	SubjectName  string              `json:"subjectName"`
	TopicID      string              `json:"topicId"`
	TopicName    string              `json:"topicName"`
	Stem         string              `json:"stem"`
	Options      []map[string]string `json:"options"`
	AnswerOption string              `json:"answerOptionId"`
	Explanation  string              `json:"explanation"`
	Difficulty   string              `json:"difficulty"`
	Points       int                 `json:"points"`
	Position     int                 `json:"position"`
}

// deterministicID derives a stable UUID-shaped id from content, so seeds are reproducible and
// re-running them cannot create duplicates.
func deterministicID(prefix, key string) string {
	var h1, h2 uint64 = 1469598103934665603, 1099511628211
	for i := 0; i < len(key); i++ {
		h1 ^= uint64(key[i])
		h1 *= 1099511628211
		h2 = (h2 ^ uint64(key[i])) * 6364136223846793005
	}
	return fmt.Sprintf("%08x-%04x-4%03x-a%03x-%012x",
		uint32(h1), uint16(h1>>32), uint16(h2>>16)&0x0fff, uint16(h2>>32)&0x0fff, h2>>16)
}

func nullable(v string) any {
	if v == "" {
		return nil
	}
	return v
}
