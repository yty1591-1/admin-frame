package email_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"strings"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/pkg/clock"
	"github.com/admin-frame/admin-frame/pkg/email"
	"github.com/admin-frame/admin-frame/pkg/email/emailtest"
)

func TestMessageValidate(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*email.Message)
		want   error
	}{
		{name: "valid", mutate: func(*email.Message) {}},
		{name: "missing to", mutate: func(m *email.Message) { m.To = nil }, want: email.ErrInvalidRecipient},
		{name: "bad to", mutate: func(m *email.Message) { m.To = []email.Address{{Email: "bad"}} }, want: email.ErrInvalidRecipient},
		{name: "bad cc", mutate: func(m *email.Message) { m.Cc = []email.Address{{Email: "a@b,c@d"}} }, want: email.ErrInvalidRecipient},
		{name: "too many to", mutate: func(m *email.Message) {
			m.To = make([]email.Address, 51)
			for i := range m.To {
				m.To[i] = email.Address{Email: "user" + string(rune('a'+i%26)) + "@example.com"}
			}
		}, want: email.ErrInvalidRecipient},
		{name: "missing body", mutate: func(m *email.Message) { m.Text = ""; m.HTML = "" }, want: email.ErrInvalidMessage},
		{name: "subject injection", mutate: func(m *email.Message) { m.Subject = "hi\r\nBcc: x@y.z" }, want: email.ErrInvalidMessage},
		{name: "subject too long", mutate: func(m *email.Message) { m.Subject = strings.Repeat("主", 201) }, want: email.ErrInvalidMessage},
		{name: "header not allowed", mutate: func(m *email.Message) {
			m.Headers = map[string]string{"Bcc": "x@y.z"}
		}, want: email.ErrInvalidMessage},
		{name: "header value injection", mutate: func(m *email.Message) {
			m.Headers = map[string]string{"X-Priority": "1\r\nBcc: x@y.z"}
		}, want: email.ErrInvalidMessage},
		{name: "header allowed", mutate: func(m *email.Message) {
			m.Headers = map[string]string{"List-Unsubscribe": "<https://example.com/unsubscribe>"}
		}},
		{name: "attachment path", mutate: func(m *email.Message) {
			m.Attachments[0].Filename = "../payload.txt"
		}, want: email.ErrInvalidMessage},
		{name: "attachment executable", mutate: func(m *email.Message) {
			m.Attachments[0].Filename = "payload.exe"
		}, want: email.ErrRejected},
		{name: "attachment content type", mutate: func(m *email.Message) {
			m.Attachments[0].ContentType = "not a type"
		}, want: email.ErrInvalidMessage},
		{name: "too many attachments", mutate: func(m *email.Message) {
			m.Attachments = make([]email.Attachment, 11)
			for i := range m.Attachments {
				m.Attachments[i] = email.Attachment{Filename: "a.txt"}
			}
		}, want: email.ErrInvalidMessage},
		{name: "display name control", mutate: func(m *email.Message) {
			m.To[0].Name = "a\nb"
		}, want: email.ErrInvalidMessage},
		{name: "bad from", mutate: func(m *email.Message) { m.From.Email = "bad" }, want: email.ErrInvalidRecipient},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := emailtest.ValidMessage()
			tt.mutate(&msg)
			err := msg.Validate()
			if tt.want == nil {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if !errors.Is(err, tt.want) {
				t.Fatalf("err=%v want %v", err, tt.want)
			}
		})
	}
}

func TestExecutableAttachmentCanBeAllowed(t *testing.T) {
	msg := emailtest.ValidMessage()
	msg.Attachments[0].Filename = "setup.exe"
	if err := msg.ValidateWith(email.Limits{AllowExecutableAttachments: true}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRecipientsDeduplicatesAndIncludesBcc(t *testing.T) {
	msg := emailtest.ValidMessage()
	msg.Cc = append(msg.Cc, email.Address{Email: "USER@example.com"})
	got := msg.Recipients()
	want := []string{"user@example.com", "cc@example.com", "bcc@example.com"}
	if len(got) != len(want) {
		t.Fatalf("recipients=%v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("recipients=%v want %v", got, want)
		}
	}
}

func TestMemoryContract(t *testing.T) {
	emailtest.Run(t, func(_ *testing.T) email.Sender {
		return email.NewMemory(email.MemoryConfig{Clock: clock.NewManual(time.Unix(0, 0))})
	})
}

func TestMemoryStoresDeepCopy(t *testing.T) {
	mem := email.NewMemory(email.MemoryConfig{})
	t.Cleanup(func() { _ = mem.Close() })
	msg := emailtest.ValidMessage()
	if _, err := mem.Send(context.Background(), msg); err != nil {
		t.Fatalf("send: %v", err)
	}
	stored := mem.Messages()
	if len(stored) != 1 {
		t.Fatalf("len=%d want 1", len(stored))
	}
	stored[0].Attachments[0].Data[0] = 'X'
	stored[0].To[0].Email = "mutated@example.com"
	again := mem.Messages()
	if again[0].Attachments[0].Data[0] == 'X' || again[0].To[0].Email == "mutated@example.com" {
		t.Fatal("Messages must return a deep copy")
	}
}

func TestMemoryUsesConfigFrom(t *testing.T) {
	sender, err := email.Open(context.Background(), email.Config{
		Driver: "memory",
		From:   email.Address{Email: "noreply@example.com"},
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = sender.Close() })
	msg := emailtest.ValidMessage()
	msg.From = email.Address{}
	if _, err := sender.Send(context.Background(), msg); err != nil {
		t.Fatalf("send: %v", err)
	}
}

func TestBuildMIMEPlainText(t *testing.T) {
	msg := emailtest.ValidMessage()
	msg.Attachments = nil
	msg.HTML = ""
	msg.Bcc = nil
	msg.Cc = nil
	date := time.Date(2026, 10, 20, 10, 0, 0, 0, time.FixedZone("CST", 8*3600))
	data, id, err := email.BuildMIME(msg, email.MIMEOptions{From: msg.From, Date: date})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if id == "" {
		t.Fatal("message id is empty")
	}
	parsed, err := mail.ReadMessage(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := parsed.Header.Get("Message-ID"); got != "<"+id+">" {
		t.Fatalf("message id header=%q", got)
	}
	if got := parsed.Header.Get("Date"); got != date.Format(time.RFC1123Z) {
		t.Fatalf("date=%q", got)
	}
	if got := parsed.Header.Get("Content-Type"); got != "text/plain; charset=UTF-8" {
		t.Fatalf("content type=%q", got)
	}
	subject, err := new(mime.WordDecoder).DecodeHeader(parsed.Header.Get("Subject"))
	if err != nil {
		t.Fatalf("decode subject: %v", err)
	}
	if subject != msg.Subject {
		t.Fatalf("subject=%q", subject)
	}
	body, err := io.ReadAll(quotedprintable.NewReader(parsed.Body))
	if err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if !strings.Contains(string(body), "验证码") {
		t.Fatalf("body=%q", body)
	}
	if !bytes.Contains(data, []byte("\r\n")) {
		t.Fatal("message must use CRLF")
	}
	if bytes.Contains(bytes.ToLower(data), []byte("bcc:")) {
		t.Fatal("Bcc header must never be written")
	}
}

func TestBuildMIMEAlternativeAndAttachment(t *testing.T) {
	msg := emailtest.ValidMessage()
	data, _, err := email.BuildMIME(msg, email.MIMEOptions{From: msg.From})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	parsed, err := mail.ReadMessage(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	mediaType, params, err := mime.ParseMediaType(parsed.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/mixed" {
		t.Fatalf("content type=%q err=%v", mediaType, err)
	}
	reader := multipart.NewReader(parsed.Body, params["boundary"])
	first, err := reader.NextPart()
	if err != nil {
		t.Fatalf("first part: %v", err)
	}
	innerType, innerParams, err := mime.ParseMediaType(first.Header.Get("Content-Type"))
	if err != nil || innerType != "multipart/alternative" {
		t.Fatalf("inner type=%q err=%v", innerType, err)
	}
	inner := multipart.NewReader(first, innerParams["boundary"])
	if _, err := inner.NextPart(); err != nil {
		t.Fatalf("alternative text part: %v", err)
	}
	if _, err := inner.NextPart(); err != nil {
		t.Fatalf("alternative html part: %v", err)
	}
	attachment, err := reader.NextPart()
	if err != nil {
		t.Fatalf("attachment part: %v", err)
	}
	if got := attachment.FileName(); got != "report.txt" {
		t.Fatalf("filename=%q", got)
	}
	if got := attachment.Header.Get("Content-Transfer-Encoding"); got != "base64" {
		t.Fatalf("encoding=%q", got)
	}
}

func TestBuildMIMEDefaultsFrom(t *testing.T) {
	msg := emailtest.ValidMessage()
	msg.From = email.Address{}
	from := email.Address{Email: "noreply@example.com"}
	data, _, err := email.BuildMIME(msg, email.MIMEOptions{From: from})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if !bytes.Contains(data, []byte("From: noreply@example.com")) {
		t.Fatalf("missing default from: %s", data)
	}
}

func TestBuildMIMERejectsInjection(t *testing.T) {
	msg := emailtest.ValidMessage()
	msg.Subject = "hi\r\nBcc: x@y.z"
	if _, _, err := email.BuildMIME(msg, email.MIMEOptions{From: msg.From}); !errors.Is(err, email.ErrInvalidMessage) {
		t.Fatalf("err=%v want ErrInvalidMessage", err)
	}
}

func TestBuildMIMEFoldsLongHeaders(t *testing.T) {
	msg := emailtest.ValidMessage()
	msg.Attachments = nil
	msg.Subject = strings.Repeat("这是一封很长的中文主题", 10)
	data, _, err := email.BuildMIME(msg, email.MIMEOptions{From: msg.From})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	for _, line := range bytes.Split(data, []byte("\r\n")) {
		if len(line) > 998 {
			t.Fatalf("header line too long: %d", len(line))
		}
	}
	if !bytes.Contains(data, []byte("\r\n ")) {
		t.Fatal("expected folded headers")
	}
}

func TestOpenUnknownDriver(t *testing.T) {
	_, err := email.Open(context.Background(), email.Config{
		Driver: "nope",
		From:   email.Address{Email: "noreply@example.com"},
	})
	if !errors.Is(err, email.ErrUnknownDriver) {
		t.Fatalf("err=%v want ErrUnknownDriver", err)
	}
}

func TestOpenRequiresFrom(t *testing.T) {
	_, err := email.Open(context.Background(), email.Config{Driver: "memory"})
	if !errors.Is(err, email.ErrInvalidConfig) {
		t.Fatalf("err=%v want ErrInvalidConfig", err)
	}
}

func TestObserverRecordsFailure(t *testing.T) {
	obs := &recordingObserver{}
	mem := email.NewMemory(email.MemoryConfig{Observer: obs})
	t.Cleanup(func() { _ = mem.Close() })
	msg := emailtest.ValidMessage()
	msg.To = nil
	if _, err := mem.Send(context.Background(), msg); err == nil {
		t.Fatal("expected error")
	}
	if len(obs.events) != 1 || obs.events[0].Channel != "email" || obs.events[0].Result != "failure" {
		t.Fatalf("unexpected events: %+v", obs.events)
	}
	if obs.events[0].Code != "invalid_argument" {
		t.Fatalf("code=%q", obs.events[0].Code)
	}
}

type recordingObserver struct {
	events []email.Event
}

func (o *recordingObserver) Observe(_ context.Context, ev email.Event) {
	o.events = append(o.events, ev)
}
