package smtp_test

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"net"
	"net/mail"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/pkg/email"
	"github.com/admin-frame/admin-frame/pkg/email/emailtest"
	"github.com/admin-frame/admin-frame/pkg/email/smtp"
	pkgerrors "github.com/admin-frame/admin-frame/pkg/errors"
)

func TestContractOverFakeServer(t *testing.T) {
	emailtest.Run(t, func(t *testing.T) email.Sender {
		server := newFakeServer(t)
		t.Cleanup(server.close)
		return openSender(t, server, "", "")
	})
}

func TestSendWritesEnvelopeAndStripsBccHeader(t *testing.T) {
	server := newFakeServer(t)
	defer server.close()
	sender := openSender(t, server, "", "")
	defer func() { _ = sender.Close() }()

	msg := emailtest.ValidMessage()
	receipt, err := sender.Send(context.Background(), msg)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if receipt.Provider != "smtp" || receipt.MessageID == "" {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}
	from, rcpts, payload := server.lastMessage(t)
	if from != "<noreply@example.com>" {
		t.Fatalf("envelope from=%q", from)
	}
	if len(rcpts) != 3 {
		t.Fatalf("envelope recipients=%v", rcpts)
	}
	if !strings.Contains(strings.Join(rcpts, ","), "bcc@example.com") {
		t.Fatalf("bcc missing from envelope: %v", rcpts)
	}
	parsed, err := mail.ReadMessage(bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("parse payload: %v", err)
	}
	if got := parsed.Header.Get("Bcc"); got != "" {
		t.Fatalf("Bcc header leaked: %q", got)
	}
	if got := parsed.Header.Get("Message-ID"); got != "<"+receipt.MessageID+">" {
		t.Fatalf("message id=%q receipt=%q", got, receipt.MessageID)
	}
	if got := parsed.Header.Get("To"); !strings.Contains(got, "user@example.com") {
		t.Fatalf("to=%q", got)
	}
}

func TestPermanentRecipientRejection(t *testing.T) {
	server := newFakeServer(t)
	defer server.close()
	sender := openSender(t, server, "", "550 5.1.1 user unknown")
	defer func() { _ = sender.Close() }()

	_, err := sender.Send(context.Background(), emailtest.ValidMessage())
	if !errors.Is(err, email.ErrRejected) {
		t.Fatalf("err=%v want ErrRejected", err)
	}
	if !pkgerrors.IsCode(err, pkgerrors.CodeFailedPrecondition) {
		t.Fatalf("code=%s", pkgerrors.CodeOf(err))
	}
	var providerErr *email.ProviderError
	if !errors.As(err, &providerErr) || providerErr.Code != "550" {
		t.Fatalf("unexpected provider error: %+v", providerErr)
	}
	if strings.Contains(err.Error(), "user@example.com") {
		t.Fatalf("error leaks recipient: %v", err)
	}
}

func TestTemporaryFailureIsRetryable(t *testing.T) {
	server := newFakeServer(t)
	defer server.close()
	sender := openSender(t, server, "451 4.3.0 try later", "")
	defer func() { _ = sender.Close() }()

	_, err := sender.Send(context.Background(), emailtest.ValidMessage())
	if !pkgerrors.IsCode(err, pkgerrors.CodeResourceExhausted) {
		t.Fatalf("err=%v want ResourceExhausted", err)
	}
	if !pkgerrors.RetryableOf(err) {
		t.Fatal("451 must be retryable")
	}
}

func TestSendRespectsContextDeadline(t *testing.T) {
	server := newFakeServer(t)
	defer server.close()
	server.setSilent()
	sender := openSender(t, server, "", "")
	defer func() { _ = sender.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	_, err := sender.Send(ctx, emailtest.ValidMessage())
	if err == nil {
		t.Fatal("expected timeout")
	}
	if !errors.Is(err, context.DeadlineExceeded) && !pkgerrors.IsCode(err, pkgerrors.CodeDeadlineExceeded) {
		t.Fatalf("err=%v want deadline exceeded", err)
	}
}

func TestOpenValidatesSMTPConfig(t *testing.T) {
	base := func() email.Config {
		return email.Config{
			Driver: "smtp",
			From:   email.Address{Email: "noreply@example.com"},
			SMTP:   email.SMTPConfig{Host: "smtp.example.com", Port: 587},
		}
	}
	tests := []struct {
		name   string
		mutate func(*email.Config)
	}{
		{name: "missing host", mutate: func(c *email.Config) { c.SMTP.Host = "" }},
		{name: "bad encryption", mutate: func(c *email.Config) { c.SMTP.Encryption = "ssl" }},
		{name: "bad auth", mutate: func(c *email.Config) { c.SMTP.Auth = "oauth" }},
		{name: "insecure disabled", mutate: func(c *email.Config) { c.SMTP.Encryption = "none" }},
		{name: "no auth insecure disabled", mutate: func(c *email.Config) { c.SMTP.Auth = "none" }},
		{name: "missing credentials", mutate: func(c *email.Config) { c.SMTP.Auth = "plain" }},
		{name: "implicit tls on 587", mutate: func(c *email.Config) {
			c.SMTP.Encryption = "tls"
			c.SMTP.Username = "user"
			c.SMTP.Password = "pass"
		}},
		{name: "bad tls version", mutate: func(c *email.Config) {
			c.SMTP.Username = "user"
			c.SMTP.Password = "pass"
			c.SMTP.TLSMinVersion = "1.0"
		}},
		{name: "bad port", mutate: func(c *email.Config) { c.SMTP.Port = 70000 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := base()
			tt.mutate(&cfg)
			if _, err := smtp.Open(context.Background(), cfg); !errors.Is(err, email.ErrInvalidConfig) {
				t.Fatalf("err=%v want ErrInvalidConfig", err)
			}
		})
	}
}

func TestOpenAcceptsSecureDefaults(t *testing.T) {
	sender, err := smtp.Open(context.Background(), email.Config{
		Driver: "smtp",
		From:   email.Address{Email: "noreply@example.com"},
		SMTP: email.SMTPConfig{
			Host:     "smtp.example.com",
			Username: "user",
			Password: "pass",
		},
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if sender.Provider() != "smtp" {
		t.Fatalf("provider=%q", sender.Provider())
	}
	if err := sender.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}

func openSender(t *testing.T, server *fakeServer, mailReply, rcptReply string) email.Sender {
	t.Helper()
	server.setReplies(mailReply, rcptReply)
	host, port := server.hostPort()
	sender, err := smtp.Open(context.Background(), email.Config{
		Driver:  "smtp",
		Timeout: 2 * time.Second,
		From:    email.Address{Name: "平台", Email: "noreply@example.com"},
		SMTP: email.SMTPConfig{
			Host:          host,
			Port:          port,
			Encryption:    "none",
			Auth:          "none",
			AllowInsecure: true,
		},
	})
	if err != nil {
		t.Fatalf("open smtp: %v", err)
	}
	return sender
}

type fakeServer struct {
	ln        net.Listener
	mu        sync.Mutex
	from      []string
	rcpts     [][]string
	payloads  [][]byte
	mailReply string
	rcptReply string
	silent    bool
}

func (s *fakeServer) setReplies(mailReply, rcptReply string) {
	s.mu.Lock()
	s.mailReply = mailReply
	s.rcptReply = rcptReply
	s.mu.Unlock()
}

func (s *fakeServer) setSilent() {
	s.mu.Lock()
	s.silent = true
	s.mu.Unlock()
}

func (s *fakeServer) replies() (mailReply, rcptReply string, silent bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mailReply, s.rcptReply, s.silent
}

func newFakeServer(t *testing.T) *fakeServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server := &fakeServer{ln: ln}
	go server.accept()
	return server
}

func (s *fakeServer) hostPort() (host string, port int) {
	addr := s.ln.Addr().(*net.TCPAddr)
	return "127.0.0.1", addr.Port
}

func (s *fakeServer) close() {
	_ = s.ln.Close()
}

func (s *fakeServer) accept() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.serve(conn)
	}
}

func (s *fakeServer) serve(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	mailReply, rcptReply, silent := s.replies()
	if silent {
		buf := make([]byte, 1)
		for {
			if _, err := conn.Read(buf); err != nil {
				return
			}
		}
	}
	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)
	writeLine := func(line string) {
		_, _ = writer.WriteString(line + "\r\n")
		_ = writer.Flush()
	}
	writeLine("220 fake.smtp ESMTP")
	var from string
	var rcpts []string
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		upper := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(upper, "EHLO"):
			writeLine("250-fake.smtp")
			writeLine("250 OK")
		case strings.HasPrefix(upper, "HELO"):
			writeLine("250 OK")
		case strings.HasPrefix(upper, "MAIL FROM:"):
			if mailReply != "" {
				writeLine(mailReply)
				continue
			}
			from = strings.TrimSpace(line[len("MAIL FROM:"):])
			writeLine("250 2.1.0 OK")
		case strings.HasPrefix(upper, "RCPT TO:"):
			if rcptReply != "" {
				writeLine(rcptReply)
				continue
			}
			rcpts = append(rcpts, strings.TrimSpace(line[len("RCPT TO:"):]))
			writeLine("250 2.1.5 OK")
		case strings.HasPrefix(upper, "DATA"):
			writeLine("354 End data with <CR><LF>.<CR><LF>")
			var payload bytes.Buffer
			for {
				dataLine, err := reader.ReadString('\n')
				if err != nil {
					return
				}
				if dataLine == ".\r\n" || dataLine == ".\n" {
					break
				}
				payload.WriteString(dataLine)
			}
			s.mu.Lock()
			s.from = append(s.from, from)
			s.rcpts = append(s.rcpts, rcpts)
			s.payloads = append(s.payloads, payload.Bytes())
			s.mu.Unlock()
			writeLine("250 2.0.0 queued")
		case strings.HasPrefix(upper, "QUIT"):
			writeLine("221 Bye")
			return
		case strings.HasPrefix(upper, "RSET"):
			writeLine("250 OK")
		default:
			writeLine("250 OK")
		}
	}
}

func (s *fakeServer) lastMessage(t *testing.T) (from string, rcpts []string, payload []byte) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.payloads) == 0 {
		t.Fatal("no message received")
	}
	last := len(s.payloads) - 1
	return s.from[last], s.rcpts[last], s.payloads[last]
}
