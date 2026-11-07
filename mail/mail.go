// Package mail sends e-mails. SMTP is used in production; LogMailer prints
// messages instead, for development.
package mail

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log/slog"
	"mime"
	"net"
	"net/smtp"
	"strings"
	"time"
)

// Message is one outgoing e-mail.
type Message struct {
	To      []string
	ReplyTo string
	Subject string
	Text    string
}

// Mailer sends e-mails.
type Mailer interface {
	Send(ctx context.Context, m Message) error
}

// SMTPConfig is the connection of the SMTP mailer.
type SMTPConfig struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
	// ImplicitTLS connects with TLS from the start (SMTPS) instead of
	// upgrading with STARTTLS. Port 465 always uses it.
	ImplicitTLS bool
	// Timeout bounds the whole SMTP exchange (default 30s).
	Timeout time.Duration
}

// SMTP sends mail via an SMTP server: with implicit TLS on port 465 (or
// Config.ImplicitTLS), otherwise using STARTTLS when offered. Credentials
// are only sent over TLS (or to localhost). The exchange is bounded by
// Config.Timeout and aborted when ctx ends.
type SMTP struct {
	Config SMTPConfig

	// roots overrides the system CAs (tests).
	roots *x509.CertPool
}

func (s SMTP) Send(ctx context.Context, m Message) error {
	if len(m.To) == 0 {
		return fmt.Errorf("mail: no recipients")
	}
	c := s.Config
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	tlsConf := &tls.Config{ServerName: c.Host, RootCAs: s.roots}
	addr := net.JoinHostPort(c.Host, fmt.Sprint(c.Port))
	var conn net.Conn
	var err error
	if c.ImplicitTLS || c.Port == 465 {
		d := tls.Dialer{Config: tlsConf}
		conn, err = d.DialContext(ctx, "tcp", addr)
	} else {
		var d net.Dialer
		conn, err = d.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("mail: dial: %w", err)
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		conn.SetDeadline(deadline)
	}
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()

	cl, err := smtp.NewClient(conn, c.Host)
	if err != nil {
		return fmt.Errorf("mail: %w", err)
	}
	defer cl.Close()
	if ok, _ := cl.Extension("STARTTLS"); ok {
		if err := cl.StartTLS(tlsConf); err != nil {
			return fmt.Errorf("mail: starttls: %w", err)
		}
	}
	if c.Username != "" {
		if ok, _ := cl.Extension("AUTH"); !ok {
			return fmt.Errorf("mail: server does not support AUTH")
		}
		if err := cl.Auth(smtp.PlainAuth("", c.Username, c.Password, c.Host)); err != nil {
			return fmt.Errorf("mail: auth: %w", err)
		}
	}
	if err := cl.Mail(c.From); err != nil {
		return fmt.Errorf("mail: from: %w", err)
	}
	for _, to := range m.To {
		if err := cl.Rcpt(to); err != nil {
			return fmt.Errorf("mail: rcpt %s: %w", to, err)
		}
	}
	w, err := cl.Data()
	if err != nil {
		return fmt.Errorf("mail: data: %w", err)
	}
	if _, err := w.Write(build(c.From, m)); err != nil {
		return fmt.Errorf("mail: write: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("mail: data: %w", err)
	}
	return cl.Quit()
}

func build(from string, m Message) []byte {
	var b strings.Builder
	// Header values never contain line breaks, so user input (Reply-To)
	// cannot inject headers.
	h := func(k, v string) { b.WriteString(k + ": " + headerSafe.Replace(v) + "\r\n") }
	h("From", from)
	h("To", strings.Join(m.To, ", "))
	if m.ReplyTo != "" {
		h("Reply-To", m.ReplyTo)
	}
	h("Subject", mime.QEncoding.Encode("utf-8", m.Subject))
	h("Date", time.Now().Format(time.RFC1123Z))
	h("MIME-Version", "1.0")
	h("Content-Type", "text/plain; charset=utf-8")
	h("Content-Transfer-Encoding", "8bit")
	b.WriteString("\r\n")
	// Normalize to LF first: textarea input arrives with CRLF.
	text := strings.ReplaceAll(strings.ReplaceAll(m.Text, "\r\n", "\n"), "\r", "\n")
	b.WriteString(strings.ReplaceAll(text, "\n", "\r\n"))
	return []byte(b.String())
}

var headerSafe = strings.NewReplacer("\r", "", "\n", "")

// LogMailer logs messages instead of sending them: recipient and subject
// at info level, the body at debug level.
type LogMailer struct{ Logger *slog.Logger }

func (l LogMailer) Send(_ context.Context, m Message) error {
	// The body may contain visitor data; it is only logged at debug level.
	l.Logger.Info("mail not sent (no SMTP configured)", "to", m.To, "subject", m.Subject)
	l.Logger.Debug("mail body", "to", m.To, "text", m.Text)
	return nil
}
