package notify_test

import (
	"bufio"
	"context"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/teamthy/edumart/apps/api/internal/notify"
)

// fakeRelay is a minimal SMTP server: enough of RFC 5321 to prove the client speaks it correctly.
//
// It is a real listener on a real socket rather than a mocked interface, because the interesting bugs
// in an SMTP client are in the conversation (greeting order, dot-stuffing, CRLF endings), not in the
// function signature. It also keeps the test offline, which CI needs.
type fakeRelay struct {
	listener net.Listener
	// rejectRcpt makes the relay refuse the recipient, simulating a policy rejection.
	rejectRcpt bool

	mu        sync.Mutex
	messages  []string
	envelopes []string
}

func newFakeRelay(t *testing.T, rejectRcpt bool) *fakeRelay {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	relay := &fakeRelay{listener: listener, rejectRcpt: rejectRcpt}
	go relay.serve()
	t.Cleanup(func() { _ = listener.Close() })
	return relay
}

func (r *fakeRelay) port() int {
	return r.listener.Addr().(*net.TCPAddr).Port
}

func (r *fakeRelay) connect() net.Conn {
	conn, err := r.listener.Accept()
	if err != nil {
		return nil
	}
	return conn
}

func (r *fakeRelay) serve() {
	for {
		conn := r.connect()
		if conn == nil {
			return
		}
		go r.handle(conn)
	}
}

func (r *fakeRelay) handle(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)
	reply := func(line string) {
		_, _ = writer.WriteString(line + "\r\n")
		_ = writer.Flush()
	}

	reply("220 fake-relay ESMTP")
	var envelope string
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		command := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(command, "EHLO"), strings.HasPrefix(command, "HELO"):
			reply("250-fake-relay")
			reply("250 8BITMIME")
		case strings.HasPrefix(command, "MAIL FROM:"):
			envelope = line
			reply("250 OK")
		case strings.HasPrefix(command, "RCPT TO:"):
			if r.rejectRcpt {
				reply("550 mailbox unavailable")
				continue
			}
			envelope += strings.TrimSpace(line)
			reply("250 OK")
		case command == "DATA":
			reply("354 end with <CRLF>.<CRLF>")
			var body strings.Builder
			for {
				dataLine, err := reader.ReadString('\n')
				if err != nil {
					return
				}
				if dataLine == ".\r\n" || dataLine == ".\n" {
					break
				}
				body.WriteString(dataLine)
			}
			r.mu.Lock()
			r.messages = append(r.messages, body.String())
			r.envelopes = append(r.envelopes, envelope)
			r.mu.Unlock()
			reply("250 queued")
		case command == "QUIT":
			reply("221 bye")
			return
		case command == "RSET", command == "NOOP":
			reply("250 OK")
		default:
			reply("250 OK")
		}
	}
}

func (r *fakeRelay) lastMessage() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.messages) == 0 {
		return ""
	}
	return r.messages[len(r.messages)-1]
}

func (r *fakeRelay) lastEnvelope() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.envelopes) == 0 {
		return ""
	}
	return r.envelopes[len(r.envelopes)-1]
}

func TestSMTPSenderDeliversAMessage(t *testing.T) {
	relay := newFakeRelay(t, false)
	sender := notify.NewSMTPSender(notify.SMTPConfig{
		Host: "127.0.0.1", Port: relay.port(), From: "no-reply@edumart.ng",
		TLSMode: "none", AllowInsecure: true, Timeout: 5 * time.Second,
	})

	msg := notify.Render(notify.TemplateVerifyEmail, notify.ChannelEmail, "ada@example.com",
		map[string]string{"name": "Ada", "code": "123456", "ttl": "15 minutes"})
	if err := sender.Send(context.Background(), msg); err != nil {
		t.Fatalf("send: %v", err)
	}

	envelope := relay.lastEnvelope()
	if !strings.Contains(envelope, "<ada@example.com>") || !strings.Contains(envelope, "no-reply@edumart.ng") {
		t.Fatalf("unexpected envelope: %q", envelope)
	}

	raw := relay.lastMessage()
	if !strings.Contains(raw, "Subject: Your Edumart verification code") {
		t.Fatalf("subject missing from the message: %q", raw)
	}
	if !strings.Contains(raw, "123456") {
		t.Fatalf("the code must appear in the body: %q", raw)
	}
	if !strings.Contains(raw, "Content-Type: text/plain; charset=utf-8") {
		t.Fatalf("a UTF-8 text message must say so: %q", raw)
	}
	// CRLF line endings are not optional: some relays reject bare LF.
	if strings.Contains(strings.ReplaceAll(raw, "\r\n", ""), "\n") {
		t.Fatalf("message contains a bare LF: %q", raw)
	}
}

func TestSMTPSenderRefusesHeaderInjection(t *testing.T) {
	relay := newFakeRelay(t, false)
	sender := notify.NewSMTPSender(notify.SMTPConfig{
		Host: "127.0.0.1", Port: relay.port(), From: "no-reply@edumart.ng",
		TLSMode: "none", AllowInsecure: true, Timeout: 5 * time.Second,
	})

	msg := notify.Message{
		Channel: notify.ChannelEmail,
		To:      "ada@example.com\r\nBcc: everyone@example.com",
		Subject: "Hello",
		Text:    "body",
	}
	if err := sender.Send(context.Background(), msg); err == nil {
		t.Fatal("an address containing CRLF must be refused, not sent")
	}
	if relay.lastMessage() != "" {
		t.Fatal("nothing may be delivered when the header is unsafe")
	}
}

func TestSMTPSenderReportsRejection(t *testing.T) {
	relay := newFakeRelay(t, true)
	sender := notify.NewSMTPSender(notify.SMTPConfig{
		Host: "127.0.0.1", Port: relay.port(), From: "no-reply@edumart.ng",
		TLSMode: "none", AllowInsecure: true, Timeout: 5 * time.Second,
	})

	err := sender.Send(context.Background(), notify.Message{
		Channel: notify.ChannelEmail, To: "nobody@example.com", Subject: "Hello", Text: "body",
	})
	// The relay refusing a recipient is the single most common "why didn't my email arrive", so it has
	// to surface as an error the delivery record can show.
	if err == nil || !strings.Contains(err.Error(), "rcpt") {
		t.Fatalf("expected a recipient rejection, got %v", err)
	}
}

func TestSMTPSenderTimesOutOnASilentRelay(t *testing.T) {
	// A listener that accepts and then says nothing: the failure mode that hangs a request handler.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = listener.Close() }()
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr == nil {
			<-make(chan struct{}) // hold the connection open, saying nothing
			_ = conn.Close()
		}
	}()

	sender := notify.NewSMTPSender(notify.SMTPConfig{
		Host: "127.0.0.1", Port: listener.Addr().(*net.TCPAddr).Port, From: "no-reply@edumart.ng",
		TLSMode: "none", AllowInsecure: true, Timeout: 250 * time.Millisecond,
	})

	start := time.Now()
	err = sender.Send(context.Background(), notify.Message{
		Channel: notify.ChannelEmail, To: "ada@example.com", Subject: "Hello", Text: "body",
	})
	if err == nil {
		t.Fatal("a silent relay must fail rather than hang")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("the attempt should be bounded by the timeout, took %s", elapsed)
	}
}

func TestSMTPConfigValidation(t *testing.T) {
	cases := []struct {
		name       string
		config     notify.SMTPConfig
		production bool
		wantError  bool
	}{
		{
			name:   "complete",
			config: notify.SMTPConfig{Host: "smtp.example.com", Port: 587, From: "a@b.com", Username: "u", Password: "p"},
		},
		{
			name:      "missing host",
			config:    notify.SMTPConfig{Port: 587, From: "a@b.com"},
			wantError: true,
		},
		{
			name:      "missing from",
			config:    notify.SMTPConfig{Host: "smtp.example.com", Port: 587},
			wantError: true,
		},
		{
			name:      "username without password",
			config:    notify.SMTPConfig{Host: "smtp.example.com", Port: 587, From: "a@b.com", Username: "u"},
			wantError: true,
		},
		{
			name:       "cleartext in production",
			config:     notify.SMTPConfig{Host: "smtp.example.com", Port: 25, From: "a@b.com", TLSMode: "none"},
			production: true,
			wantError:  true,
		},
		{
			name:   "cleartext by explicit opt-in outside production",
			config: notify.SMTPConfig{Host: "127.0.0.1", Port: 1025, From: "a@b.com", TLSMode: "none", AllowInsecure: true},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.config.Validate(tc.production)
			if tc.wantError && err == nil {
				t.Fatal("expected a configuration error")
			}
			if !tc.wantError && err != nil {
				t.Fatalf("unexpected configuration error: %v", err)
			}
		})
	}
}

func TestSMTPPortParsing(t *testing.T) {
	// Ports arrive as strings from the environment; a bad one must fail validation rather than dial 0.
	config := notify.SMTPConfig{Host: "smtp.example.com", Port: 0, From: "a@b.com"}
	if err := config.Validate(false); err == nil {
		t.Fatal("port 0 must be rejected")
	}
	_ = strconv.Itoa(config.Port)
}
