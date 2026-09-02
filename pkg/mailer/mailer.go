// Package mailer sends transactional email over SMTP.
//
// It is deliberately small: one plain-text message type, one Sender interface,
// and a no-op implementation used when no SMTP host is configured. Notifications
// are a convenience, so a deployment without mail credentials must still start
// and behave normally rather than failing at boot or erroring on every write.
package mailer

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"mime"
	"net"
	"net/smtp"
	"strings"
	"sync"
	"time"
)

// dialTimeout bounds the whole exchange. A notification is not worth holding a
// goroutine open indefinitely against an unreachable relay.
const dialTimeout = 15 * time.Second

// Config is the SMTP account the application sends from.
type Config struct {
	Host     string
	Port     int
	User     string
	Password string
	// From is the envelope and header sender, e.g. "crm@example.com".
	From string
	// FromName is the display name shown to recipients; optional.
	FromName string
}

// Message is a plain-text email. HTML is deliberately not supported: these are
// short internal notifications, and plain text renders everywhere.
type Message struct {
	To      []string
	Subject string
	Body    string
}

// Sender delivers a message. Implementations must be safe for concurrent use.
type Sender interface {
	Send(ctx context.Context, m Message) error
}

// New returns an SMTP sender, or a no-op one when the configuration is
// incomplete. Callers do not need to check which they got.
func New(c Config) Sender {
	if c.Host == "" || c.From == "" {
		return &disabled{}
	}
	if c.Port == 0 {
		c.Port = 587
	}
	return &smtpSender{cfg: c}
}

// disabled swallows messages and says so once, so an unconfigured deployment
// neither fails nor fills its logs with the same line on every card move.
type disabled struct{ once sync.Once }

func (d *disabled) Send(_ context.Context, m Message) error {
	d.once.Do(func() {
		log.Printf("mailer: SMTP is not configured (set SMTP_HOST and SMTP_FROM); " +
			"notification emails will be skipped")
	})
	return nil
}

type smtpSender struct{ cfg Config }

func (s *smtpSender) Send(ctx context.Context, m Message) error {
	if len(m.To) == 0 {
		return nil
	}

	addr := net.JoinHostPort(s.cfg.Host, fmt.Sprint(s.cfg.Port))
	dialer := &net.Dialer{Timeout: dialTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("dial %s: %w", addr, err)
	}
	// A deadline on the raw connection covers the whole SMTP conversation; the
	// net/smtp client has no timeout of its own.
	_ = conn.SetDeadline(time.Now().Add(dialTimeout))

	// Port 465 is implicit TLS ("SMTPS"); everything else negotiates STARTTLS
	// after the greeting.
	if s.cfg.Port == 465 {
		conn = tls.Client(conn, &tls.Config{ServerName: s.cfg.Host})
	}

	client, err := smtp.NewClient(conn, s.cfg.Host)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("smtp handshake: %w", err)
	}
	defer func() { _ = client.Close() }()

	if s.cfg.Port != 465 {
		if ok, _ := client.Extension("STARTTLS"); ok {
			if err := client.StartTLS(&tls.Config{ServerName: s.cfg.Host}); err != nil {
				return fmt.Errorf("starttls: %w", err)
			}
		}
	}

	if s.cfg.User != "" {
		// PlainAuth refuses to send credentials over an unencrypted link, which
		// is the behaviour we want rather than something to work around.
		auth := smtp.PlainAuth("", s.cfg.User, s.cfg.Password, s.cfg.Host)
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}

	if err := client.Mail(s.cfg.From); err != nil {
		return fmt.Errorf("smtp from: %w", err)
	}
	for _, to := range m.To {
		if err := client.Rcpt(to); err != nil {
			return fmt.Errorf("smtp rcpt %s: %w", to, err)
		}
	}

	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}
	if _, err := w.Write([]byte(s.render(m))); err != nil {
		_ = w.Close()
		return fmt.Errorf("smtp write: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp close body: %w", err)
	}
	return client.Quit()
}

// render builds the RFC 5322 message. The subject is Q-encoded so a non-ASCII
// company or person's name survives the trip.
func (s *smtpSender) render(m Message) string {
	from := s.cfg.From
	if s.cfg.FromName != "" {
		from = fmt.Sprintf("%s <%s>", mime.QEncoding.Encode("utf-8", s.cfg.FromName), s.cfg.From)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", from)
	fmt.Fprintf(&b, "To: %s\r\n", strings.Join(m.To, ", "))
	fmt.Fprintf(&b, "Subject: %s\r\n", mime.QEncoding.Encode("utf-8", m.Subject))
	fmt.Fprintf(&b, "Date: %s\r\n", time.Now().Format(time.RFC1123Z))
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	b.WriteString("\r\n")
	// Bare newlines are illegal in SMTP data; normalise before writing.
	b.WriteString(strings.ReplaceAll(strings.ReplaceAll(m.Body, "\r\n", "\n"), "\n", "\r\n"))
	return b.String()
}
