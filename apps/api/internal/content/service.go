// Package content exposes the exam taxonomy and the public question selection rules.
package content

import (
	"context"
	"database/sql"
	"errors"

	"github.com/teamthy/edumart/apps/api/internal/httpx"
	"github.com/teamthy/edumart/apps/api/internal/store"
)

// Pathway is an examination route (JAMB, WAEC, NECO, Post-UTME).
type Pathway struct {
	ID       string `json:"id"`
	Key      string `json:"key"`
	Name     string `json:"name"`
	Body     string `json:"body"`
	Blurb    string `json:"blurb"`
	Subjects int    `json:"subjects"`
}

// Subject belongs to exactly one pathway and owns topics.
type Subject struct {
	ID            string `json:"id"`
	Key           string `json:"key"`
	Name          string `json:"name"`
	Icon          string `json:"icon"`
	PathwayKey    string `json:"pathwayKey"`
	QuestionCount int    `json:"questionCount"`
}

// Topic is a syllabus unit within a subject.
type Topic struct {
	ID            string `json:"id"`
	Key           string `json:"key"`
	Name          string `json:"name"`
	QuestionCount int    `json:"questionCount"`
	SortOrder     int    `json:"sortOrder"`
}

// Service reads taxonomy data.
type Service struct{ db *store.Store }

// New builds the content service.
func New(db *store.Store) *Service { return &Service{db: db} }

// Risk: content is cached briefly. Taxonomy changes are rare and a short TTL keeps learner
// requests cheap without ever caching assessment state.
func (s *Service) Pathways(ctx context.Context) ([]Pathway, error) {
	rows, err := s.db.DB.QueryContext(ctx, `
		SELECT ep.id, ep.key, ep.name, ep.body, ep.blurb,
		       (SELECT COUNT(*) FROM subjects s WHERE s.exam_pathway_id = ep.id AND s.active = 1)
		  FROM exam_pathways ep WHERE ep.active = 1
		 ORDER BY CASE ep.key WHEN 'jamb' THEN 1 WHEN 'waec' THEN 2 WHEN 'neco' THEN 3 ELSE 4 END`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Pathway{}
	for rows.Next() {
		var p Pathway
		if err := rows.Scan(&p.ID, &p.Key, &p.Name, &p.Body, &p.Blurb, &p.Subjects); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Subjects lists subjects, optionally restricted to one pathway key.
func (s *Service) Subjects(ctx context.Context, pathwayKey string) ([]Subject, error) {
	query := `
		SELECT s.id, s.key, s.name, s.icon, ep.key,
		       (SELECT COUNT(DISTINCT q.id) FROM questions q
		         JOIN question_versions qv ON qv.question_id = q.id
		        WHERE q.subject_id = s.id AND qv.review_state = 'approved'
		          AND qv.published_at IS NOT NULL AND q.status = 'published')
		  FROM subjects s JOIN exam_pathways ep ON ep.id = s.exam_pathway_id
		 WHERE s.active = 1`
	args := []any{}
	if pathwayKey != "" {
		query += " AND ep.key = ?"
		args = append(args, pathwayKey)
	}
	query += " ORDER BY ep.key, s.name"

	rows, err := s.db.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Subject{}
	for rows.Next() {
		var sub Subject
		if err := rows.Scan(&sub.ID, &sub.Key, &sub.Name, &sub.Icon, &sub.PathwayKey, &sub.QuestionCount); err != nil {
			return nil, err
		}
		out = append(out, sub)
	}
	return out, rows.Err()
}

// SubjectByID returns one subject or a 404.
func (s *Service) SubjectByID(ctx context.Context, id string) (*Subject, error) {
	var sub Subject
	err := s.db.DB.QueryRowContext(ctx, `
		SELECT s.id, s.key, s.name, s.icon, ep.key
		  FROM subjects s JOIN exam_pathways ep ON ep.id = s.exam_pathway_id WHERE s.id = ?`, id).
		Scan(&sub.ID, &sub.Key, &sub.Name, &sub.Icon, &sub.PathwayKey)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, httpx.NewError(httpx.CodeNotFound, "That subject could not be found.")
	}
	if err != nil {
		return nil, err
	}
	return &sub, nil
}

// Topics lists the syllabus units of a subject with approved-question counts.
func (s *Service) Topics(ctx context.Context, subjectID string) ([]Topic, error) {
	rows, err := s.db.DB.QueryContext(ctx, `
		SELECT t.id, t.key, t.name, t.sort_order,
		       (SELECT COUNT(DISTINCT q.id) FROM questions q
		         JOIN question_versions qv ON qv.question_id = q.id
		        WHERE q.topic_id = t.id AND qv.review_state = 'approved' AND qv.published_at IS NOT NULL)
		  FROM topics t WHERE t.subject_id = ? ORDER BY t.sort_order`, subjectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Topic{}
	for rows.Next() {
		var t Topic
		if err := rows.Scan(&t.ID, &t.Key, &t.Name, &t.SortOrder, &t.QuestionCount); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// PathwayIDByKey resolves a pathway key to its id.
func (s *Service) PathwayIDByKey(ctx context.Context, key string) (string, error) {
	var id string
	err := s.db.DB.QueryRowContext(ctx, `SELECT id FROM exam_pathways WHERE key = ?`, key).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", httpx.NewError(httpx.CodeNotFound, "That exam pathway could not be found.")
	}
	return id, err
}

// VerifySubjects checks that every supplied subject id exists (used by the signup wizard).
func (s *Service) VerifySubjects(ctx context.Context, ids []string) error {
	for _, id := range ids {
		var exists int
		if err := s.db.DB.QueryRowContext(ctx, `SELECT 1 FROM subjects WHERE id = ? AND active = 1`, id).Scan(&exists); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return httpx.Validation([]httpx.FieldError{{Field: "subjectIds", Message: "One of the selected subjects no longer exists."}})
			}
			return err
		}
	}
	return nil
}
