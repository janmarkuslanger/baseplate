package mail

import (
	"bufio"
	"context"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"
)

// fakeSMTP is a minimal SMTP server that records the DATA payload.
func fakeSMTP(t *testing.T, stall bool) (port int, got chan string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	got = make(chan string, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		if stall {
			time.Sleep(2 * time.Second) // never greets
			return
		}
		r := bufio.NewReader(conn)
		say := func(s string) { conn.Write([]byte(s + "\r\n")) }
		say("220 fake")
		var data strings.Builder
		inData := false
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			if inData {
				if line == ".\r\n" {
					inData = false
					got <- data.String()
					say("250 ok")
					continue
				}
				data.WriteString(line)
				continue
			}
			switch cmd := strings.ToUpper(strings.TrimSpace(line)); {
			case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
				say("250 fake")
			case cmd == "DATA":
				inData = true
				say("354 go")
			case cmd == "QUIT":
				say("221 bye")
				return
			default:
				say("250 ok")
			}
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port, got
}

func TestSMTPSend(t *testing.T) {
	port, got := fakeSMTP(t, false)
	m := SMTP{Config: SMTPConfig{Host: "127.0.0.1", Port: port, From: "web@acme.test"}}
	err := m.Send(context.Background(), Message{To: []string{"team@acme.test"}, Subject: "Hallo", Text: "Zeile 1\nZeile 2"})
	if err != nil {
		t.Fatal(err)
	}
	body := <-got
	if !strings.Contains(body, "Subject: Hallo\r\n") || !strings.Contains(body, "Zeile 1\r\nZeile 2") {
		t.Errorf("message: %q", body)
	}
}

func TestSMTPTimeout(t *testing.T) {
	port, _ := fakeSMTP(t, true)
	m := SMTP{Config: SMTPConfig{Host: "127.0.0.1", Port: port, From: "a@b.de", Timeout: 200 * time.Millisecond}}
	start := time.Now()
	if err := m.Send(context.Background(), Message{To: []string{"x@y.de"}}); err == nil {
		t.Fatal("want timeout error")
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("Send took %s, want ~200ms", d)
	}
}

func TestBuildLineEndings(t *testing.T) {
	msg := string(build("a@b.de", Message{To: []string{"x@y.de"}, Text: "eins\r\nzwei\nzwei\rdrei"}))
	body := msg[strings.Index(msg, "\r\n\r\n")+4:]
	if body != "eins\r\nzwei\r\nzwei\r\ndrei" {
		t.Errorf("body = %q", body)
	}
}

func TestLogMailerHidesBody(t *testing.T) {
	var buf strings.Builder
	l := LogMailer{Logger: slog.New(slog.NewTextHandler(&buf, nil))} // default level: info
	l.Send(context.Background(), Message{To: []string{"x@y.de"}, Subject: "Anfrage", Text: "Ada Lovelace, 0171 123"})
	if !strings.Contains(buf.String(), "Anfrage") || strings.Contains(buf.String(), "Lovelace") {
		t.Errorf("log: %s", buf.String())
	}
}
