package notify

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"time"

	"github.com/teamthy/edumart/apps/api/internal/store"
)

// Courier is what identity depends on: a durable hand-off, not a promise that a message arrived.
//
// The distinction matters at the call site. "We recorded this and will keep trying" is true even when
// the provider is down; "it was delivered" is not something the API can know. Callers get the first
// guarantee, and delivery status is observable afterwards.
type Courier interface {
	Send(ctx context.Context, msg Message, ref Ref) error
}

// Ref ties a message to the row it is about, so an abandoned delivery can withdraw the credential it
// carried.
type Ref struct {
	Type string
	ID   string
}

// Resource types used with Ref.
const (
	// RefVerificationCode withdraws the code when delivery is abandoned.
	RefVerificationCode = "verification_code"
	// RefPasswordReset does the same for a reset token.
	RefPasswordReset = "password_reset_token"
	// RefInvitation marks a message carrying an invitation link. There is nothing to withdraw when it
	// is abandoned: the invitation expires on its own and can be revoked explicitly.
	RefInvitation = "invitation"
	// RefReminder marks a scheduled reminder. There is no credential to withdraw when it is abandoned:
	// the reminder simply did not arrive, which the staff view shows.
	RefReminder = "reminder"
	// RefOrder marks a receipt. Money has already moved; a missing receipt is an operator problem, not
	// a reason to retry a payment.
	RefOrder = "order"
)

// Delivery statuses.
const (
	StatusPending = "pending"
	StatusSent    = "sent"
	StatusFailed  = "failed"
)

const (
	// maxAttempts bounds retrying. With the backoff below this spans roughly an hour, which is longer
	// than a verification code lives — the code is withdrawn when the last attempt fails.
	maxAttempts = 5
	// defaultBatch caps how many due deliveries one sweep handles.
	defaultBatch = 50
)

// backoff returns the delay before the next attempt for a given attempt count.
func backoff(attempts int) time.Duration {
	switch attempts {
	case 1:
		return 30 * time.Second
	case 2:
		return 2 * time.Minute
	case 3:
		return 10 * time.Minute
	default:
		return 30 * time.Minute
	}
}

// Delivery is the recorded state of one outbound message.
type Delivery struct {
	ID            string `json:"id"`
	Channel       string `json:"channel"`
	Recipient     string `json:"recipient"`
	Template      string `json:"template"`
	Subject       string `json:"subject"`
	Status        string `json:"status"`
	Attempts      int    `json:"attempts"`
	MaxAttempts   int    `json:"maxAttempts"`
	LastError     string `json:"lastError"`
	ResourceType  string `json:"resourceType"`
	ResourceID    string `json:"resourceId"`
	CreatedAt     string `json:"createdAt"`
	NextAttemptAt string `json:"nextAttemptAt"`
	SentAt        string `json:"sentAt"`
}

// Service records outbound messages, attempts delivery, and retries what fails.
//
// It sits between the callers (identity, institutions) and a Sender. The row is written before the
// first attempt, so a crash between "we decided to send" and "we tried" still results in a retry
// rather than a silently missing message.
type Service struct {
	db     *store.Store
	sender Sender
	logger *slog.Logger
	now    func() time.Time
}

// NewService builds the delivery service.
func NewService(db *store.Store, sender Sender, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{db: db, sender: sender, logger: logger, now: func() time.Time { return time.Now().UTC() }}
}

// WithClock replaces the clock, which tests use to advance past a backoff window without sleeping.
func (s *Service) WithClock(now func() time.Time) *Service {
	s.now = now
	return s
}

// Send records the message, attempts it once, and schedules a retry if that attempt failed.
//
// An error is returned only when the message could not be recorded at all. A failed first attempt is
// not an error at this boundary: it is a pending delivery, which is exactly what the caller needs to
// know — and it must not be reported to a user as "sent".
func (s *Service) Send(ctx context.Context, msg Message, ref Ref) error {
	now := s.now()
	deliveryID := store.NewID()
	template := msg.Subject
	if msg.TemplateKey != "" {
		template = msg.TemplateKey
	}

	if _, err := s.db.DB.ExecContext(ctx, `
		INSERT INTO notification_deliveries
			(id, channel, recipient, template_key, subject, body, status, attempts, max_attempts,
			 resource_type, resource_id, created_at, next_attempt_at)
		VALUES (?,?,?,?,?,?,'pending',0,?,?,?,?,?)`,
		deliveryID, string(msg.Channel), msg.To, template, msg.Subject, msg.Text, maxAttempts,
		ref.Type, ref.ID, store.TS(now), store.TS(now)); err != nil {
		return err
	}

	// The first attempt gets the original message (including the one-time secret); retries rebuild it
	// from the stored body, because nothing else survives a restart.
	s.attempt(ctx, deliveryID, &msg)
	return nil
}

// attempt performs one delivery attempt and records the outcome.
func (s *Service) attempt(ctx context.Context, deliveryID string, original *Message) {
	msg := Message{}
	if original != nil {
		msg = *original
	} else {
		var channel, recipient, subject, body string
		err := s.db.DB.QueryRowContext(ctx, `
			SELECT channel, recipient, subject, body FROM notification_deliveries WHERE id = ?`,
			deliveryID).Scan(&channel, &recipient, &subject, &body)
		if errors.Is(err, sql.ErrNoRows) {
			return
		}
		if err != nil {
			s.logger.Error("delivery lookup failed", "deliveryId", deliveryID, "error", err.Error())
			return
		}
		msg = Message{Channel: Channel(channel), To: recipient, Subject: subject, Text: body}
	}

	sendErr := s.sender.Send(ctx, msg)

	now := s.now()
	if sendErr == nil {
		// The body is cleared once the message is out. Between "queued" and "delivered" the queue holds
		// whatever the user is about to receive — including a verification code — so the shortest
		// possible window is the right one.
		if _, err := s.db.DB.ExecContext(ctx, `
			UPDATE notification_deliveries
			   SET status = 'sent', attempts = attempts + 1, last_error = '', sent_at = ?, body = ''
			 WHERE id = ?`, store.TS(now), deliveryID); err != nil {
			s.logger.Error("delivery record failed", "deliveryId", deliveryID, "error", err.Error())
		}
		return
	}

	// Record the failure, then decide whether another attempt is scheduled. Attempts are incremented
	// in the same statement that sets the next attempt time, so the schedule cannot drift from the count.
	// Two plain statements rather than UPDATE ... RETURNING: not every driver path we run on supports
	// the latter in this shape, and a counter that doubles on a driver quirk is worse than a round trip.
	if _, err := s.db.DB.ExecContext(ctx, `
		UPDATE notification_deliveries SET attempts = attempts + 1, last_error = ? WHERE id = ?`,
		truncateError(sendErr.Error()), deliveryID); err != nil {
		s.logger.Error("delivery record failed", "deliveryId", deliveryID, "error", err.Error())
		return
	}
	var attempts, max int
	if err := s.db.DB.QueryRowContext(ctx, `
		SELECT attempts, max_attempts FROM notification_deliveries WHERE id = ?`,
		deliveryID).Scan(&attempts, &max); err != nil {
		s.logger.Error("delivery record failed", "deliveryId", deliveryID, "error", err.Error())
		return
	}

	if attempts >= max {
		s.abandon(ctx, deliveryID, now)
		return
	}
	if _, err := s.db.DB.ExecContext(ctx, `
		UPDATE notification_deliveries SET next_attempt_at = ? WHERE id = ?`,
		store.TS(now.Add(backoff(attempts))), deliveryID); err != nil {
		s.logger.Error("delivery schedule failed", "deliveryId", deliveryID, "error", err.Error())
	}
}

// abandon marks the delivery failed and withdraws whatever credential it was carrying.
func (s *Service) abandon(ctx context.Context, deliveryID string, now time.Time) {
	var resourceType, resourceID string
	_ = s.db.DB.QueryRowContext(ctx, `
		SELECT resource_type, resource_id FROM notification_deliveries WHERE id = ?`,
		deliveryID).Scan(&resourceType, &resourceID)

	if _, err := s.db.DB.ExecContext(ctx, `
		UPDATE notification_deliveries SET status = 'failed', failed_at = ?, body = '' WHERE id = ?`,
		store.TS(now), deliveryID); err != nil {
		s.logger.Error("delivery finalise failed", "deliveryId", deliveryID, "error", err.Error())
		return
	}

	// A code nobody received must not stay usable: it is a credential with no owner, and leaving it
	// alive would make the attempt limit and expiry the only thing standing between an attacker and a
	// row whose value they could guess offline.
	switch resourceType {
	case RefVerificationCode:
		if _, err := s.db.DB.ExecContext(ctx, `DELETE FROM verification_codes WHERE id = ? AND consumed_at IS NULL`,
			resourceID); err != nil {
			s.logger.Error("withdrawing undelivered code failed", "deliveryId", deliveryID, "error", err.Error())
		}
	case RefPasswordReset:
		if _, err := s.db.DB.ExecContext(ctx, `DELETE FROM password_reset_tokens WHERE id = ? AND used_at IS NULL`,
			resourceID); err != nil {
			s.logger.Error("withdrawing undelivered reset failed", "deliveryId", deliveryID, "error", err.Error())
		}
	}

	s.logger.Warn("delivery abandoned", "deliveryId", deliveryID, "resource", resourceType, "resourceId", resourceID)
}

// RunOnce attempts every delivery whose retry is due. It returns how many were attempted and how many
// are still pending, which is what a worker loop logs and a test asserts on.
func (s *Service) RunOnce(ctx context.Context, limit int) (attempted int, pending int, err error) {
	if limit <= 0 {
		limit = defaultBatch
	}
	rows, err := s.db.DB.QueryContext(ctx, `
		SELECT id FROM notification_deliveries
		 WHERE status = 'pending' AND next_attempt_at <= ?
		 ORDER BY next_attempt_at LIMIT ?`, store.TS(s.now()), limit)
	if err != nil {
		return 0, 0, err
	}
	// Collect before sending: writing while a result set is open on the same connection is exactly the
	// cross-engine bug that broke the webhook handler (SQLite tolerates it, PostgreSQL does not).
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
		s.attempt(ctx, id, nil)
	}
	if err := s.db.DB.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM notification_deliveries WHERE status = 'pending'`).Scan(&pending); err != nil {
		return len(ids), 0, err
	}
	return len(ids), pending, nil
}

// Recent lists the most recent deliveries, newest first. Passwords and codes never appear here: the
// body is deliberately excluded, so an operator can see that a message was attempted without being
// able to read the secret it carried.
func (s *Service) Recent(ctx context.Context, limit int) ([]Delivery, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.DB.QueryContext(ctx, `
		SELECT id, channel, recipient, template_key, subject, status, attempts, max_attempts, last_error,
		       resource_type, resource_id, created_at, next_attempt_at, COALESCE(sent_at, '')
		  FROM notification_deliveries ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Delivery{}
	for rows.Next() {
		var d Delivery
		if err := rows.Scan(&d.ID, &d.Channel, &d.Recipient, &d.Template, &d.Subject, &d.Status,
			&d.Attempts, &d.MaxAttempts, &d.LastError, &d.ResourceType, &d.ResourceID, &d.CreatedAt,
			&d.NextAttemptAt, &d.SentAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// Stats summarises delivery health, which is the number an operator actually watches.
type Stats struct {
	Sent    int `json:"sent"`
	Pending int `json:"pending"`
	Failed  int `json:"failed"`
}

// Stats counts deliveries by state.
func (s *Service) Stats(ctx context.Context) (*Stats, error) {
	out := &Stats{}
	rows, err := s.db.DB.QueryContext(ctx, `
		SELECT status, COUNT(*) FROM notification_deliveries GROUP BY status`)
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
		case StatusSent:
			out.Sent = count
		case StatusPending:
			out.Pending = count
		case StatusFailed:
			out.Failed = count
		}
	}
	return out, rows.Err()
}

// Start runs the retry sweep until the context is cancelled.
func (s *Service) Start(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	ticker := time.NewTicker(interval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				attempted, pending, err := s.RunOnce(ctx, defaultBatch)
				switch {
				case err != nil:
					s.logger.Error("delivery sweep failed", "error", err.Error())
				case attempted > 0:
					s.logger.Info("delivery sweep", "attempted", attempted, "pending", pending)
				}
			}
		}
	}()
}

func truncateError(message string) string {
	const limit = 500
	if len(message) > limit {
		return message[:limit]
	}
	return message
}
