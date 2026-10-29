// Package notify delivers short, one-time messages — verification codes and reset links — and
// nothing else.
//
// It exists so the identity code never knows how a message actually leaves the building. Today the
// only implementation writes to the structured log, which is enough for development and for the
// test suite; a real provider (SMTP, SES, Termii, Twilio) is a new Sender and one line of wiring.
//
// The rules a provider must keep:
//   - Send is called at most once per message; retries belong to the caller, not the sender.
//   - The code in a Message is a credential. It must not be logged, persisted or included in an
//     error returned to the client.
package notify

import (
	"context"
	"log/slog"
	"sync"
)

// Channel is a delivery route.
type Channel string

const (
	// ChannelEmail delivers by email.
	ChannelEmail Channel = "email"
	// ChannelPhone delivers by SMS.
	ChannelPhone Channel = "phone"
)

// Message is one outbound message. It is deliberately plain text: rich templates are a Phase 24
// concern and should not be invented here.
type Message struct {
	Channel Channel
	To      string
	Subject string
	Text    string
	// TemplateKey names the message kind ("verify_email", "password_reset"), so delivery records can
	// be grouped without storing the code they carried.
	TemplateKey string
	// Code is the one-time secret the message carries, if any. Senders must not persist it.
	Code string
}

// Sender delivers a Message. An error means the message did not leave; the caller decides whether
// to retry, and must assume failure unless told otherwise.
type Sender interface {
	Send(ctx context.Context, msg Message) error
}

// LogSender writes messages to the structured log instead of sending them. It is the development
// default: without it, no one could complete signup on a laptop with no mail server.
//
// Redact replaces the code with a placeholder. Set it in production: a log line is not a private
// channel, and production has a real provider and no need to read codes from logs.
type LogSender struct {
	Logger  *slog.Logger
	Redact  bool
	subject string // overridable in tests; empty means the channel default
}

// NewLogSender builds a development sender.
func NewLogSender(logger *slog.Logger, redact bool) *LogSender {
	return &LogSender{Logger: logger, Redact: redact}
}

// Send logs the message and reports success.
func (s *LogSender) Send(_ context.Context, msg Message) error {
	code := msg.Code
	if s.Redact && code != "" {
		code = "[redacted]"
	}
	s.Logger.Info("outbound message (log sender)",
		"channel", string(msg.Channel),
		"to", msg.To,
		"subject", msg.Subject,
		"code", code,
		"text", msg.Text,
	)
	return nil
}

// Recorder keeps messages in memory. Tests use it to read the code a user would have received,
// which is the only honest way to assert on a flow that depends on an out-of-band secret.
//
// It must never be wired into a running server.
type Recorder struct {
	mu       sync.Mutex
	messages []Message
	err      error
}

// NewRecorder builds an empty recorder.
func NewRecorder() *Recorder { return &Recorder{} }

// Send stores the message and returns any error injected with Fail.
func (r *Recorder) Send(_ context.Context, msg Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return r.err
	}
	r.messages = append(r.messages, msg)
	return nil
}

// Fail makes every subsequent Send fail, so tests can prove the caller does not treat an
// undelivered code as delivered.
func (r *Recorder) Fail(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.err = err
}

// Last returns the most recent message sent to a destination, if any.
func (r *Recorder) Last(to string) (Message, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := len(r.messages) - 1; i >= 0; i-- {
		if r.messages[i].To == to {
			return r.messages[i], true
		}
	}
	return Message{}, false
}

// All returns every message sent so far, oldest first.
func (r *Recorder) All() []Message {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Message, len(r.messages))
	copy(out, r.messages)
	return out
}
