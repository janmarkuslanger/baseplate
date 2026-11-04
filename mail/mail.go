// Package mail sends e-mails. SMTP is used in production; LogMailer prints
// messages instead, for development.
package mail

import (
	"context"
	"fmt"
	"log/slog"
	"mime"
	"net"
	"net/smtp"
	"strings"
	"time"
)

type Message struct {
	To      []string
	ReplyTo string
	Subject string
	Text    string
}

type Mailer interface {
	Send(ctx context.Context, m Message) error
}

type SMTPConfig struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
}

// SMTP sends mail via an SMTP server using STARTTLS when offered.
type SMTP struct{ Config SMTPConfig }

func (s SMTP) Send(_ context.Context, m Message) error {
	if len(m.To) == 0 {
		return fmt.Errorf("mail: no recipients")
	}
	c := s.Config
	addr := net.JoinHostPort(c.Host, fmt.Sprint(c.Port))
	var auth smtp.Auth
	if c.Username != "" {
		auth = smtp.PlainAuth("", c.Username, c.Password, c.Host)
	}
	return smtp.SendMail(addr, auth, c.From, m.To, build(c.From, m))
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
	b.WriteString(strings.ReplaceAll(m.Text, "\n", "\r\n"))
	return []byte(b.String())
}

var headerSafe = strings.NewReplacer("\r", "", "\n", "")

// LogMailer logs messages instead of sending them.
type LogMailer struct{ Logger *slog.Logger }

func (l LogMailer) Send(_ context.Context, m Message) error {
	l.Logger.Info("mail (not sent, no SMTP configured)", "to", m.To, "subject", m.Subject, "text", m.Text)
	return nil
}
