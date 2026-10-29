package notify

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/teamthy/edumart/apps/api/internal/store"
)

// Reminder kinds. Each has its own planner, its own dedupe window and its own revalidation rule, so a
// new kind cannot accidentally inherit another kind's "still worth sending" logic.
const (
	// KindStudyNudge is a once-a-day prompt to study, for learners who set a target date.
	KindStudyNudge = "study_nudge"
	// KindDeadline is a warning that a class assessment closes soon and the learner has not submitted.
	KindDeadline = "deadline"
)

// Reminder statuses.
const (
	ReminderPending   = "pending"
	ReminderClaimed   = "claimed"
	ReminderSent      = "sent"
	ReminderSkipped   = "skipped"
	ReminderCancelled = "cancelled"
)

const (
	// deadlineWarningLead is how long before a class assessment closes its learner is warned.
	deadlineWarningLead = 2 * time.Hour
	// deadlinePlannerHorizon bounds how far ahead the planner looks, so a "warning" is a warning and not
	// a queue of messages scheduled for a month's time that nobody can cancel.
	deadlinePlannerHorizon = 24 * time.Hour
	// nudgeHour is the local hour a study nudge is due. Evening, after school, before the night is gone.
	nudgeHour = 17
)

// Reminder is one message the system has promised to send later.
type Reminder struct {
	ID          string `json:"id"`
	UserID      string `json:"userId"`
	Kind        string `json:"kind"`
	TemplateKey string `json:"templateKey"`
	Subject     string `json:"subject"`
	Body        string `json:"body"`
	ResourceID  string `json:"resourceId"`
	DueAt       string `json:"dueAt"`
	DedupeKey   string `json:"dedupeKey"`
	Status      string `json:"status"`
	CreatedAt   string `json:"createdAt"`
	ClaimedAt   string `json:"claimedAt"`
	SettledAt   string `json:"settledAt"`
	DeliveryID  string `json:"deliveryId"`
	Note        string `json:"note"`
}

// scheduleReminder records a reminder unless one with the same dedupe key already exists.
//
// It reports whether this call created the row. "Already scheduled" is success, not an error: the planner
// runs on a timer and re-deciding the same thing is its normal state.
func (s *Service) scheduleReminder(ctx context.Context, userID, kind, templateKey, subject, body, resourceID, dedupeKey string, dueAt time.Time) (bool, error) {
	result, err := s.db.DB.ExecContext(ctx, `
		INSERT INTO scheduled_notifications
			(id, user_id, kind, template_key, subject, body, resource_id, due_at, dedupe_key, status, created_at)
		VALUES (?,?,?,?,?,?,?,?,?,'pending',?)
		ON CONFLICT (dedupe_key) DO NOTHING`,
		store.NewID(), userID, kind, templateKey, subject, body, resourceID,
		store.TS(dueAt), dedupeKey, store.TS(s.now()))
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

// Plan materialises the reminders that are due to be decided now, and returns how many were newly
// scheduled. It is idempotent: running it twice in a minute schedules nothing the second time.
//
// Planning and sending are deliberately separate. Planning can be wrong (a stale target date) and the
// send path revalidates; sending never invents content.
func (s *Service) Plan(ctx context.Context) (int, error) {
	nudges, err := s.planStudyNudges(ctx)
	if err != nil {
		return 0, err
	}
	deadlines, err := s.planDeadlines(ctx)
	if err != nil {
		return nudges, err
	}
	return nudges + deadlines, nil
}

// planStudyNudges schedules today's nudge for every active, verified learner with a study plan and a
// target date still in the future.
func (s *Service) planStudyNudges(ctx context.Context) (int, error) {
	rows, err := s.db.DB.QueryContext(ctx, `
		SELECT u.id, u.email_normalized, u.display_name, u.timezone, u.daily_minutes, sp.target_date
		  FROM users u JOIN study_plans sp ON sp.user_id = u.id
		 WHERE u.status = 'active' AND u.email_verified_at IS NOT NULL AND u.email_normalized <> ''
		   AND sp.target_date IS NOT NULL AND sp.target_date <> ''`)
	if err != nil {
		return 0, err
	}

	type candidate struct {
		id, email, name, timezone string
		dailyMinutes              int
		targetDate                string
	}
	candidates := []candidate{}
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.id, &c.email, &c.name, &c.timezone, &c.dailyMinutes, &c.targetDate); err != nil {
			_ = rows.Close()
			return 0, err
		}
		candidates = append(candidates, c)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, err
	}
	_ = rows.Close()

	scheduled := 0
	for _, c := range candidates {
		target, err := time.Parse("2006-01-02", c.targetDate)
		if err != nil {
			continue // a malformed target date is a data problem, not a reason to spam the learner
		}
		location := loadLocation(c.timezone)
		now := s.now().In(location)
		if !target.After(now.AddDate(0, 0, -1)) {
			continue // the exam is today or past: "keep preparing" is no longer honest advice
		}

		due := nextLocalHour(now, nudgeHour)
		days := int(target.Sub(due.In(location)).Hours() / 24)
		if days < 0 {
			days = 0
		}
		message := nudgeMessage(c.name, days, c.dailyMinutes)
		key := fmt.Sprintf("%s:%s:%s", KindStudyNudge, c.id, due.In(location).Format("2006-01-02"))
		created, err := s.scheduleReminder(ctx, c.id, KindStudyNudge, KindStudyNudge,
			"A few minutes today", message, "", key, due.UTC())
		if err != nil {
			return scheduled, err
		}
		if created {
			scheduled++
		}
	}
	return scheduled, nil
}

// planDeadlines schedules a warning for each learner who has not submitted a class assessment that
// closes within the planner horizon.
func (s *Service) planDeadlines(ctx context.Context) (int, error) {
	now := s.now()
	rows, err := s.db.DB.QueryContext(ctx, `
		SELECT d.id, d.title, d.availability_end, u.id, u.email_normalized
		  FROM assessment_definitions d
		  JOIN class_memberships m ON m.class_id = d.class_id AND m.status = 'active'
		  JOIN users u ON u.id = m.user_id
		 WHERE d.status = 'published'
		   AND d.availability_end IS NOT NULL AND d.availability_end <> ''
		   AND d.availability_end > ? AND d.availability_end <= ?
		   AND m.role = 'student'
		   AND u.status = 'active' AND u.email_normalized <> ''
		   AND NOT EXISTS (
		         SELECT 1 FROM assessment_sessions s
		          WHERE s.definition_id = d.id AND s.user_id = u.id AND s.status = 'submitted')`,
		store.TS(now), store.TS(now.Add(deadlinePlannerHorizon)))
	if err != nil {
		return 0, err
	}

	type candidate struct {
		definitionID, title, closesAt, userID, email string
	}
	candidates := []candidate{}
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.definitionID, &c.title, &c.closesAt, &c.userID, &c.email); err != nil {
			_ = rows.Close()
			return 0, err
		}
		candidates = append(candidates, c)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, err
	}
	_ = rows.Close()

	scheduled := 0
	for _, c := range candidates {
		closes, err := store.ParseTS(c.closesAt)
		if err != nil {
			continue
		}
		due := closes.Add(-deadlineWarningLead)
		if due.Before(now) {
			// Already inside the warning window: send at the next sweep rather than scheduling into the past.
			due = now
		}
		message := fmt.Sprintf("%s closes %s. You have not submitted yet — your teacher will see it as missing.",
			c.title, closes.UTC().Format("Mon 2 Jan at 15:04 UTC"))
		key := fmt.Sprintf("%s:%s:%s", KindDeadline, c.userID, c.definitionID)
		created, err := s.scheduleReminder(ctx, c.userID, KindDeadline, KindDeadline,
			"An assessment closes soon", message, c.definitionID, key, due)
		if err != nil {
			return scheduled, err
		}
		if created {
			scheduled++
		}
	}
	return scheduled, nil
}

// RunReminders claims and sends every reminder that is due.
//
// The claim is the concurrency control: `UPDATE ... WHERE status = 'pending'` and a row-count check means
// exactly one worker wins a given reminder, even with two instances sweeping at the same second. A
// reminder claimed by a worker that then crashes stays claimed — visible in the admin view as stuck, which
// is the failure mode an operator can act on, rather than a duplicate email nobody can explain.
func (s *Service) RunReminders(ctx context.Context, limit int) (sent int, skipped int, err error) {
	if limit <= 0 {
		limit = defaultBatch
	}
	rows, err := s.db.DB.QueryContext(ctx, `
		SELECT id FROM scheduled_notifications
		 WHERE status = 'pending' AND due_at <= ?
		 ORDER BY due_at LIMIT ?`, store.TS(s.now()), limit)
	if err != nil {
		return 0, 0, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return 0, 0, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, 0, err
	}
	_ = rows.Close()

	for _, id := range ids {
		outcome, err := s.sendOne(ctx, id)
		if err != nil {
			s.logger.Error("reminder failed", "reminderId", id, "error", err.Error())
			continue
		}
		switch outcome {
		case ReminderSent:
			sent++
		case ReminderSkipped:
			skipped++
		}
	}
	return sent, skipped, nil
}

// sendOne claims a reminder, revalidates it, and hands it to the delivery queue.
func (s *Service) sendOne(ctx context.Context, reminderID string) (string, error) {
	now := s.now()
	claim, err := s.db.DB.ExecContext(ctx, `
		UPDATE scheduled_notifications SET status = 'claimed', claimed_at = ?
		 WHERE id = ? AND status = 'pending'`, store.TS(now), reminderID)
	if err != nil {
		return "", err
	}
	if affected, err := claim.RowsAffected(); err != nil {
		return "", err
	} else if affected == 0 {
		// Someone else has it, or it was cancelled between the query and the claim.
		return "", nil
	}

	var r Reminder
	var recipient string
	err = s.db.DB.QueryRowContext(ctx, `
		SELECT n.id, n.user_id, n.kind, n.template_key, n.subject, n.body, n.resource_id, n.due_at,
		       u.email_normalized
		  FROM scheduled_notifications n JOIN users u ON u.id = n.user_id
		 WHERE n.id = ?`, reminderID).
		Scan(&r.ID, &r.UserID, &r.Kind, &r.TemplateKey, &r.Subject, &r.Body, &r.ResourceID, &r.DueAt, &recipient)
	if errors.Is(err, sql.ErrNoRows) {
		// The learner was deleted between claim and read. Nothing to send, nothing to apologise for.
		return s.settle(ctx, reminderID, ReminderSkipped, "", "recipient no longer exists")
	}
	if err != nil {
		return "", err
	}

	ok, reason, err := s.stillWorthSending(ctx, r)
	if err != nil {
		return "", err
	}
	if !ok {
		return s.settle(ctx, reminderID, ReminderSkipped, "", reason)
	}

	if err := s.Send(ctx, Message{
		Channel:     ChannelEmail,
		To:          recipient,
		Subject:     r.Subject,
		Text:        r.Body,
		TemplateKey: r.TemplateKey,
	}, Ref{Type: RefReminder, ID: reminderID}); err != nil {
		// The reminder stays claimed and the delivery row does not exist; the admin view shows an
		// unsent, claimed reminder, which is exactly the state an operator needs to see.
		return "", err
	}
	return s.settle(ctx, reminderID, ReminderSent, "", "")
}

// stillWorthSending revalidates a reminder at the moment of sending rather than at the moment of
// planning. A deadline warning written on Monday is a lie by Thursday if the learner submitted on
// Tuesday, and a nudge to study is a lie if the learner's plan was cleared.
func (s *Service) stillWorthSending(ctx context.Context, r Reminder) (bool, string, error) {
	switch r.Kind {
	case KindDeadline:
		var submitted int
		if err := s.db.DB.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM assessment_sessions
			 WHERE definition_id = ? AND user_id = ? AND status = 'submitted'`,
			r.ResourceID, r.UserID).Scan(&submitted); err != nil {
			return false, "", err
		}
		if submitted > 0 {
			return false, "already submitted", nil
		}
		var closesAt sql.NullString
		err := s.db.DB.QueryRowContext(ctx,
			`SELECT availability_end FROM assessment_definitions WHERE id = ? AND status = 'published'`,
			r.ResourceID).Scan(&closesAt)
		if errors.Is(err, sql.ErrNoRows) {
			return false, "assessment withdrawn", nil
		}
		if err != nil {
			return false, "", err
		}
		if closes, err := store.ParseTS(closesAt.String); err == nil && s.now().After(closes) {
			return false, "assessment already closed", nil
		}
		return true, "", nil

	case KindStudyNudge:
		var targetDate sql.NullString
		var status string
		err := s.db.DB.QueryRowContext(ctx, `
			SELECT sp.target_date, u.status FROM users u JOIN study_plans sp ON sp.user_id = u.id
			 WHERE u.id = ?`, r.UserID).Scan(&targetDate, &status)
		if errors.Is(err, sql.ErrNoRows) {
			return false, "no study plan", nil
		}
		if err != nil {
			return false, "", err
		}
		if status != "active" {
			return false, "account is not active", nil
		}
		if !targetDate.Valid || targetDate.String == "" {
			return false, "target date cleared", nil
		}
		return true, "", nil
	}
	// An unknown kind is a programming error, and the safe response to "I do not know what this is" is
	// not to email a learner.
	return false, "unknown reminder kind", nil
}

// settle records the final state of a reminder. The row is kept: "we decided to send this and then did
// not, because they had already submitted" is an answer support needs.
func (s *Service) settle(ctx context.Context, reminderID, status, deliveryID, note string) (string, error) {
	if _, err := s.db.DB.ExecContext(ctx, `
		UPDATE scheduled_notifications SET status = ?, settled_at = ?, delivery_id = ?, note = ?
		 WHERE id = ?`, status, store.TS(s.now()), deliveryID, truncateError(note), reminderID); err != nil {
		return "", err
	}
	return status, nil
}

// Reminders lists scheduled reminders, newest first, for the staff view.
func (s *Service) Reminders(ctx context.Context, limit int) ([]Reminder, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.DB.QueryContext(ctx, `
		SELECT id, user_id, kind, template_key, subject, resource_id, due_at, dedupe_key, status,
		       created_at, COALESCE(claimed_at, ''), COALESCE(settled_at, ''), COALESCE(delivery_id, ''),
		       note
		  FROM scheduled_notifications ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Reminder{}
	for rows.Next() {
		var r Reminder
		if err := rows.Scan(&r.ID, &r.UserID, &r.Kind, &r.TemplateKey, &r.Subject, &r.ResourceID, &r.DueAt,
			&r.DedupeKey, &r.Status, &r.CreatedAt, &r.ClaimedAt, &r.SettledAt, &r.DeliveryID,
			&r.Note); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ReminderStats counts reminders by state.
type ReminderStats struct {
	Pending   int `json:"pending"`
	Claimed   int `json:"claimed"`
	Sent      int `json:"sent"`
	Skipped   int `json:"skipped"`
	Cancelled int `json:"cancelled"`
}

// ReminderStats summarises the reminder queue.
func (s *Service) ReminderStats(ctx context.Context) (*ReminderStats, error) {
	out := &ReminderStats{}
	rows, err := s.db.DB.QueryContext(ctx,
		`SELECT status, COUNT(*) FROM scheduled_notifications GROUP BY status`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return nil, err
		}
		switch status {
		case ReminderPending:
			out.Pending = count
		case ReminderClaimed:
			out.Claimed = count
		case ReminderSent:
			out.Sent = count
		case ReminderSkipped:
			out.Skipped = count
		case ReminderCancelled:
			out.Cancelled = count
		}
	}
	return out, rows.Err()
}

// StartReminders plans and sends reminders until the context is cancelled.
func (s *Service) StartReminders(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	ticker := time.NewTicker(interval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				planned, err := s.Plan(ctx)
				if err != nil {
					s.logger.Error("reminder planning failed", "error", err.Error())
					continue
				}
				sent, skipped, err := s.RunReminders(ctx, defaultBatch)
				if err != nil {
					s.logger.Error("reminder sweep failed", "error", err.Error())
					continue
				}
				if planned > 0 || sent > 0 || skipped > 0 {
					s.logger.Info("reminder sweep", "planned", planned, "sent", sent, "skipped", skipped)
				}
			}
		}
	}()
}

// nextLocalHour returns the next occurrence of hour:00 in the given zone: today if it is still ahead,
// otherwise tomorrow.
func nextLocalHour(now time.Time, hour int) time.Time {
	due := time.Date(now.Year(), now.Month(), now.Day(), hour, 0, 0, 0, now.Location())
	if !due.After(now) {
		due = due.AddDate(0, 0, 1)
	}
	return due
}

// loadLocation falls back to UTC rather than failing: a bad timezone should delay a nudge's hour, never
// stop the sweep for everyone else.
func loadLocation(name string) *time.Location {
	if name == "" {
		return time.UTC
	}
	location, err := time.LoadLocation(name)
	if err != nil {
		return time.UTC
	}
	return location
}

func nudgeMessage(name string, days, minutes int) string {
	firstName := name
	for i, r := range name {
		if r == ' ' {
			firstName = name[:i]
			break
		}
	}
	switch {
	case days == 0:
		return fmt.Sprintf("%s, your exam is today. %d focused minutes now beat an hour of reading.", firstName, minutes)
	case days == 1:
		return fmt.Sprintf("%s, your exam is tomorrow. %d minutes today is enough to keep sharp — do not start anything new.",
			firstName, minutes)
	default:
		return fmt.Sprintf("%s, your exam is in %d days. %d minutes today keeps the plan on track.",
			firstName, days, minutes)
	}
}
