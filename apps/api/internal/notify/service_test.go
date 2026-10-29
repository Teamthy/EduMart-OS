package notify_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/teamthy/edumart/apps/api/internal/notify"
	"github.com/teamthy/edumart/apps/api/internal/store"
)

func testService(t *testing.T) (*notify.Service, *notify.Recorder, *store.Store, *fakeClock) {
	t.Helper()
	db, err := store.Open("sqlite3", filepath.Join(t.TempDir(), "notify.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	recorder := notify.NewRecorder()
	clock := &fakeClock{at: time.Now().UTC()}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := notify.NewService(db, recorder, logger).WithClock(clock.Now)
	return svc, recorder, db, clock
}

type fakeClock struct{ at time.Time }

func (c *fakeClock) Now() time.Time { return c.at }

func (c *fakeClock) Advance(d time.Duration) { c.at = c.at.Add(d) }

func verifyMessage(to, code string) notify.Message {
	return notify.Render(notify.TemplateVerifyEmail, notify.ChannelEmail, to, map[string]string{
		"name": "Ada", "code": code, "ttl": "15 minutes",
	})
}

func TestSendRecordsAndDeliversInline(t *testing.T) {
	svc, recorder, db, _ := testService(t)

	if err := svc.Send(context.Background(), verifyMessage("ada@example.com", "123456"),
		notify.Ref{Type: notify.RefVerificationCode, ID: "code-1"}); err != nil {
		t.Fatalf("send: %v", err)
	}

	if len(recorder.All()) != 1 {
		t.Fatalf("expected one delivered message, got %d", len(recorder.All()))
	}
	var status, body string
	var attempts int
	if err := db.DB.QueryRow(`SELECT status, attempts, body FROM notification_deliveries`).
		Scan(&status, &attempts, &body); err != nil {
		t.Fatalf("read delivery: %v", err)
	}
	if status != notify.StatusSent || attempts != 1 {
		t.Fatalf("expected sent after one attempt, got %s/%d", status, attempts)
	}
	// The queue is not a second copy of the inbox: once delivered, the body goes.
	if body != "" {
		t.Fatalf("a delivered message must not keep its body, got %q", body)
	}
}

func TestFailedAttemptSchedulesARetry(t *testing.T) {
	svc, recorder, db, _ := testService(t)
	recorder.Fail(errors.New("relay refused connection"))

	if err := svc.Send(context.Background(), verifyMessage("ada@example.com", "123456"),
		notify.Ref{Type: notify.RefVerificationCode, ID: "code-1"}); err != nil {
		t.Fatalf("queueing must not fail because delivery did: %v", err)
	}

	var status, lastError, nextAttempt string
	var attempts int
	if err := db.DB.QueryRow(`SELECT status, attempts, last_error, next_attempt_at FROM notification_deliveries`).
		Scan(&status, &attempts, &lastError, &nextAttempt); err != nil {
		t.Fatalf("read delivery: %v", err)
	}
	if status != notify.StatusPending {
		t.Fatalf("a failed attempt must leave the delivery pending, got %s", status)
	}
	if attempts != 1 || lastError == "" {
		t.Fatalf("expected one recorded failure with a reason, got %d/%q", attempts, lastError)
	}
	if nextAttempt == "" {
		t.Fatal("a retry must be scheduled")
	}
}

func TestRetrySweepDeliversOnRecovery(t *testing.T) {
	svc, recorder, _, clock := testService(t)
	recorder.Fail(errors.New("relay down"))
	if err := svc.Send(context.Background(), verifyMessage("ada@example.com", "123456"), notify.Ref{}); err != nil {
		t.Fatalf("send: %v", err)
	}

	// A sweep before the backoff elapses must do nothing: retrying immediately is how a mail provider
	// rate-limits you.
	attempted, _, err := svc.RunOnce(context.Background(), 10)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if attempted != 0 {
		t.Fatalf("expected no attempt before the backoff, got %d", attempted)
	}

	recorder.Fail(nil)
	clock.Advance(time.Minute)
	attempted, pending, err := svc.RunOnce(context.Background(), 10)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if attempted != 1 || pending != 0 {
		t.Fatalf("expected the retry to succeed, got attempted=%d pending=%d", attempted, pending)
	}
	if len(recorder.All()) != 1 {
		t.Fatalf("expected the message to be delivered on retry, got %d", len(recorder.All()))
	}
}

func TestAbandonedDeliveryWithdrawsTheCode(t *testing.T) {
	svc, recorder, db, clock := testService(t)

	// A user and a code row, as identity would have written them. Foreign keys are enforced, so the
	// order matters here exactly as it does in production.
	if _, err := db.DB.Exec(`INSERT INTO users
		(id, email_normalized, password_hash, display_name, status, created_at, updated_at)
		VALUES ('user-1', 'ada@example.com', 'x', 'Ada', 'active', ?, ?)`, store.Now(), store.Now()); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if _, err := db.DB.Exec(`INSERT INTO verification_codes
		(id, user_id, channel, destination, code_hash, attempts, max_attempts, created_at, expires_at)
		VALUES ('code-1', 'user-1', 'email', 'ada@example.com', 'hash', 0, 5, ?, ?)`,
		store.Now(), store.TS(time.Now().Add(15*time.Minute))); err != nil {
		t.Fatalf("insert code: %v", err)
	}

	recorder.Fail(errors.New("mailbox full"))
	if err := svc.Send(context.Background(), verifyMessage("ada@example.com", "123456"),
		notify.Ref{Type: notify.RefVerificationCode, ID: "code-1"}); err != nil {
		t.Fatalf("send: %v", err)
	}

	for i := 0; i < 6; i++ {
		clock.Advance(time.Hour)
		if _, _, err := svc.RunOnce(context.Background(), 10); err != nil {
			t.Fatalf("sweep: %v", err)
		}
	}

	var status, body string
	var attempts int
	if err := db.DB.QueryRow(`SELECT status, attempts, body FROM notification_deliveries`).
		Scan(&status, &attempts, &body); err != nil {
		t.Fatalf("read delivery: %v", err)
	}
	if status != notify.StatusFailed {
		t.Fatalf("expected the delivery to be abandoned, got %s after %d attempts", status, attempts)
	}
	if body != "" {
		t.Fatalf("an abandoned delivery must not keep the secret, got %q", body)
	}

	var live int
	if err := db.DB.QueryRow(`SELECT COUNT(*) FROM verification_codes`).Scan(&live); err != nil {
		t.Fatalf("count codes: %v", err)
	}
	if live != 0 {
		t.Fatalf("the undelivered code must be withdrawn, %d rows remain", live)
	}
}

func TestRecentDoesNotExposeBodies(t *testing.T) {
	svc, _, _, _ := testService(t)
	if err := svc.Send(context.Background(), verifyMessage("ada@example.com", "123456"), notify.Ref{}); err != nil {
		t.Fatalf("send: %v", err)
	}
	items, err := svc.Recent(context.Background(), 10)
	if err != nil {
		t.Fatalf("recent: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected one delivery, got %d", len(items))
	}
	if items[0].Status != notify.StatusSent {
		t.Fatalf("expected sent, got %s", items[0].Status)
	}
	// Delivery is a struct with no body field at all: the type is the guarantee, so a future handler
	// cannot accidentally serialise a secret.
	if items[0].Recipient != "ada@example.com" {
		t.Fatalf("recipient should be visible to operators, got %q", items[0].Recipient)
	}
}

func TestRenderUnknownTemplateStillSends(t *testing.T) {
	msg := notify.Render("some_future_template", notify.ChannelEmail, "ada@example.com", map[string]string{"code": "999"})
	if msg.Subject == "" || msg.Text == "" {
		t.Fatalf("an unknown template must still produce a usable message, got %+v", msg)
	}
}
