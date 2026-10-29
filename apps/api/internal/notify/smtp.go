package notify

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

// SMTPConfig describes an SMTP relay. It is deliberately close to the plain protocol rather than to
// any one vendor: SES, Postmark, Mailgun and a self-hosted Postfix all speak it, so one adapter covers
// the realistic options without a dependency per provider.
type SMTPConfig struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
	// TLSMode is "starttls" (default), "tls" (implicit, normally port 465) or "none" (local relays
	// such as Mailpit in development).
	TLSMode string
	// Timeout bounds one delivery attempt, so a hung relay cannot hold a request open.
	Timeout time.Duration
	// AllowInsecure permits TLSMode "none". It exists so that production configuration cannot
	// silently send credentials over cleartext by accident.
	AllowInsecure bool
}

// Validate reports whether the configuration is usable. It is called at startup: a mail configuration
// that is wrong should stop the process, not surface as undelivered codes hours later.
func (c SMTPConfig) Validate(production bool) error {
	if strings.TrimSpace(c.Host) == "" {
		return errors.New("smtp: host is required")
	}
	if c.Port <= 0 || c.Port > 65535 {
		return errors.New("smtp: port is required")
	}
	if strings.TrimSpace(c.From) == "" || !strings.Contains(c.From, "@") {
		return errors.New("smtp: from address is required")
	}
	mode := c.TLSMode
	if mode == "" {
		mode = "starttls"
	}
	switch mode {
	case "starttls", "tls":
	case "none":
		if production || !c.AllowInsecure {
			return errors.New(`smtp: tls mode "none" sends credentials in the clear; set EDUMART_SMTP_ALLOW_INSECURE=true to allow it outside production`)
		}
	default:
		return fmt.Errorf("smtp: unknown tls mode %q", mode)
	}
	if (c.Username == "") != (c.Password == "") {
		return errors.New("smtp: username and password must be set together")
	}
	return nil
}

// SMTPSender delivers messages through an SMTP relay.
//
// It is a Sender, not a retrying client: one call is one attempt, and the caller owns the retry
// schedule. That separation is what lets delivery attempts be recorded and observed.
type SMTPSender struct {
	Config SMTPConfig
}

// NewSMTPSender builds a sender from validated configuration.
func NewSMTPSender(config SMTPConfig) *SMTPSender {
	if config.Timeout <= 0 {
		config.Timeout = 10 * time.Second
	}
	if config.TLSMode == "" {
		config.TLSMode = "starttls"
	}
	return &SMTPSender{Config: config}
}

// Send delivers one message. Header injection is blocked by rejecting newlines in the fields that end
// up in headers: a recipient address containing "\r\nBcc: …" is the classic way to turn a transactional
// mailer into a spam relay.
func (s *SMTPSender) Send(ctx context.Context, msg Message) error {
	cfg := s.Config
	if err := validateHeaderValue(msg.To); err != nil {
		return err
	}
	if err := validateHeaderValue(msg.Subject); err != nil {
		return err
	}

	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	dialer := &net.Dialer{Timeout: cfg.Timeout}

	var conn net.Conn
	var err error
	if cfg.TLSMode == "tls" {
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, &tls.Config{ServerName: cfg.Host})
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("smtp dial %s: %w", addr, err)
	}
	// The deadline covers the whole conversation, not just the connection: a relay that accepts the
	// connection and then stalls is the common failure, and it must not hold the caller open.
	deadline := time.Now().Add(cfg.Timeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	if err := conn.SetDeadline(deadline); err != nil {
		_ = conn.Close()
		return err
	}

	client, err := smtp.NewClient(conn, cfg.Host)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("smtp handshake: %w", err)
	}
	defer func() { _ = client.Close() }()

	if cfg.TLSMode == "starttls" {
		if ok, _ := client.Extension("STARTTLS"); ok {
			if err := client.StartTLS(&tls.Config{ServerName: cfg.Host}); err != nil {
				return fmt.Errorf("smtp starttls: %w", err)
			}
		} else if cfg.Username != "" {
			return errors.New("smtp: relay does not offer STARTTLS but credentials are configured")
		}
	}
	if cfg.Username != "" {
		if err := client.Auth(smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host)); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}
	if err := client.Mail(cfg.From); err != nil {
		return fmt.Errorf("smtp mail from: %w", err)
	}
	if err := client.Rcpt(msg.To); err != nil {
		return fmt.Errorf("smtp rcpt to: %w", err)
	}
	writer, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}
	if _, err := writer.Write(composeMessage(cfg.From, msg)); err != nil {
		return fmt.Errorf("smtp write: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("smtp commit: %w", err)
	}
	return client.Quit()
}

// composeMessage builds a minimal RFC 5322 message: plain text only, UTF-8 declared, no attachments.
// Transactional codes do not need HTML, and plain text is what survives every client.
func composeMessage(from string, msg Message) []byte {
	var b strings.Builder
	b.WriteString("From: " + from + "\r\n")
	b.WriteString("To: " + msg.To + "\r\n")
	b.WriteString("Subject: " + msg.Subject + "\r\n")
	b.WriteString("Date: " + time.Now().UTC().Format(time.RFC1123Z) + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	b.WriteString("Content-Transfer-Encoding: 8bit\r\n")
	b.WriteString("\r\n")
	// SMTP requires CRLF line endings and dot-stuffing for lines that begin with a period.
	body := strings.ReplaceAll(msg.Text, "\r\n", "\n")
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, ".") {
			line = "." + line
		}
		b.WriteString(line + "\r\n")
	}
	return []byte(b.String())
}

func validateHeaderValue(value string) error {
	if strings.ContainsAny(value, "\r\n") {
		return errors.New("smtp: header value contains a newline")
	}
	return nil
}
