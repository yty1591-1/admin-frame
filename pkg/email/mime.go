package email

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/textproto"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	subjectChunkBytes = 18
	maxHeaderLine     = 76
)

// MIMEOptions 控制 MIME 构造。
type MIMEOptions struct {
	// From 是消息未提供发件人时的默认值。
	From Address
	// MessageID 为空时自动生成；非空必须是可打印 ASCII。
	MessageID string
	// Date 是 Date 头；零值使用 time.Now()。驱动应传入注入时钟的时间。
	Date time.Time
	// Limits 是校验上限；零值使用 DefaultLimits。
	Limits Limits
}

type bodyPart struct {
	contentType string
	encoding    string
	body        []byte
}

// BuildMIME 将消息编码为 RFC 5322/MIME 字节流，并返回 Message-ID（不含尖括号）。
//
// 校验失败返回 ErrInvalidMessage / ErrInvalidRecipient / ErrRejected；
// 生成的字节流使用 CRLF 行尾，可直接交给 SMTP DATA。
func BuildMIME(msg Message, opts MIMEOptions) (data []byte, messageID string, err error) {
	limits := opts.Limits.normalized()
	if msg.From.Email == "" {
		msg.From = opts.From
	}
	if err := msg.ValidateWith(limits); err != nil {
		return nil, "", err
	}
	date := opts.Date
	if date.IsZero() {
		date = time.Now()
	}
	messageID = opts.MessageID
	if messageID == "" {
		generated, err := newMessageID(msg.From.Email)
		if err != nil {
			return nil, "", fmt.Errorf("%w: generate message id", ErrInvalidMessage)
		}
		messageID = generated
	}
	if containsControl(messageID) || strings.ContainsAny(messageID, "<> ") {
		return nil, "", invalidMessage("message id is invalid")
	}
	body, err := buildBody(msg)
	if err != nil {
		return nil, "", err
	}

	var buf bytes.Buffer
	writeAddressHeader(&buf, "From", []Address{msg.From})
	writeAddressHeader(&buf, "To", msg.To)
	if len(msg.Cc) > 0 {
		writeAddressHeader(&buf, "Cc", msg.Cc)
	}
	if msg.ReplyTo != nil {
		writeAddressHeader(&buf, "Reply-To", []Address{*msg.ReplyTo})
	}
	writeSubjectHeader(&buf, msg.Subject)
	writeHeader(&buf, "Date", date.Format(time.RFC1123Z))
	writeHeader(&buf, "Message-ID", "<"+messageID+">")
	writeHeader(&buf, "MIME-Version", "1.0")
	keys := make([]string, 0, len(msg.Headers))
	for key := range msg.Headers {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		writeHeader(&buf, textproto.CanonicalMIMEHeaderKey(key), msg.Headers[key])
	}
	writeHeader(&buf, "Content-Type", body.contentType)
	if body.encoding != "" {
		writeHeader(&buf, "Content-Transfer-Encoding", body.encoding)
	}
	buf.WriteString("\r\n")
	buf.Write(body.body)
	return buf.Bytes(), messageID, nil
}

func buildBody(msg Message) (bodyPart, error) {
	if len(msg.Attachments) == 0 {
		return buildInlineBody(msg)
	}
	inline, err := buildInlineBody(msg)
	if err != nil {
		return bodyPart{}, err
	}
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	header := make(textproto.MIMEHeader)
	header.Set("Content-Type", inline.contentType)
	if inline.encoding != "" {
		header.Set("Content-Transfer-Encoding", inline.encoding)
	}
	part, err := writer.CreatePart(header)
	if err != nil {
		return bodyPart{}, err
	}
	if _, err := part.Write(inline.body); err != nil {
		return bodyPart{}, err
	}
	for _, attachment := range msg.Attachments {
		if err := writeAttachment(writer, attachment); err != nil {
			return bodyPart{}, err
		}
	}
	if err := writer.Close(); err != nil {
		return bodyPart{}, err
	}
	return bodyPart{
		contentType: "multipart/mixed; boundary=\"" + writer.Boundary() + "\"",
		body:        buf.Bytes(),
	}, nil
}

func buildInlineBody(msg Message) (bodyPart, error) {
	switch {
	case msg.Text != "" && msg.HTML != "":
		var buf bytes.Buffer
		writer := multipart.NewWriter(&buf)
		if err := writeTextPart(writer, "text/plain; charset=UTF-8", msg.Text); err != nil {
			return bodyPart{}, err
		}
		if err := writeTextPart(writer, "text/html; charset=UTF-8", msg.HTML); err != nil {
			return bodyPart{}, err
		}
		if err := writer.Close(); err != nil {
			return bodyPart{}, err
		}
		return bodyPart{
			contentType: "multipart/alternative; boundary=\"" + writer.Boundary() + "\"",
			body:        buf.Bytes(),
		}, nil
	case msg.Text != "":
		body, err := encodeQuotedPrintable(msg.Text)
		if err != nil {
			return bodyPart{}, err
		}
		return bodyPart{contentType: "text/plain; charset=UTF-8", encoding: "quoted-printable", body: body}, nil
	default:
		body, err := encodeQuotedPrintable(msg.HTML)
		if err != nil {
			return bodyPart{}, err
		}
		return bodyPart{contentType: "text/html; charset=UTF-8", encoding: "quoted-printable", body: body}, nil
	}
}

func writeTextPart(writer *multipart.Writer, contentType, text string) error {
	header := make(textproto.MIMEHeader)
	header.Set("Content-Type", contentType)
	header.Set("Content-Transfer-Encoding", "quoted-printable")
	part, err := writer.CreatePart(header)
	if err != nil {
		return err
	}
	encoder := quotedprintable.NewWriter(part)
	if _, err := encoder.Write([]byte(text)); err != nil {
		return err
	}
	return encoder.Close()
}

func writeAttachment(writer *multipart.Writer, attachment Attachment) error {
	contentType := attachment.ContentType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	header := make(textproto.MIMEHeader)
	header.Set("Content-Type", contentType)
	header.Set("Content-Transfer-Encoding", "base64")
	header.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{
		"filename": attachment.Filename,
	}))
	part, err := writer.CreatePart(header)
	if err != nil {
		return err
	}
	return writeBase64(part, attachment.Data)
}

func encodeQuotedPrintable(text string) ([]byte, error) {
	var buf bytes.Buffer
	encoder := quotedprintable.NewWriter(&buf)
	if _, err := encoder.Write([]byte(text)); err != nil {
		return nil, err
	}
	if err := encoder.Close(); err != nil {
		return nil, err
	}
	data := buf.Bytes()
	if !bytes.HasSuffix(data, []byte("\r\n")) {
		data = append(data, '\r', '\n')
	}
	return data, nil
}

func writeBase64(w io.Writer, data []byte) error {
	const chunk = 57 // 57 字节编码后正好 76 个字符。
	for len(data) > 0 {
		size := chunk
		if len(data) < size {
			size = len(data)
		}
		line := base64.StdEncoding.EncodeToString(data[:size])
		if _, err := io.WriteString(w, line+"\r\n"); err != nil {
			return err
		}
		data = data[size:]
	}
	return nil
}

func writeHeader(buf *bytes.Buffer, key, value string) {
	buf.WriteString(key)
	buf.WriteString(": ")
	buf.WriteString(value)
	buf.WriteString("\r\n")
}

func writeSubjectHeader(buf *bytes.Buffer, subject string) {
	if subject == "" {
		writeHeader(buf, "Subject", "")
		return
	}
	writeFoldedWords(buf, "Subject", encodeHeaderWords(subject))
}

func encodeHeaderWords(value string) []string {
	if isASCII(value) {
		return strings.Fields(value)
	}
	words := make([]string, 0, utf8.RuneCountInString(value)/subjectChunkBytes+1)
	var chunk strings.Builder
	for _, r := range value {
		if chunk.Len()+utf8.RuneLen(r) > subjectChunkBytes && chunk.Len() > 0 {
			words = append(words, mime.QEncoding.Encode("UTF-8", chunk.String()))
			chunk.Reset()
		}
		chunk.WriteRune(r)
	}
	if chunk.Len() > 0 {
		words = append(words, mime.QEncoding.Encode("UTF-8", chunk.String()))
	}
	return words
}

// encodeDisplayName 将非 ASCII 显示名编码为若干 RFC 2047 encoded-word，避免单行超长。
func encodeDisplayName(name string) string {
	return strings.Join(encodeHeaderWords(name), " ")
}

func writeFoldedWords(buf *bytes.Buffer, key string, words []string) {
	buf.WriteString(key)
	buf.WriteString(": ")
	line := len(key) + 2
	for i, word := range words {
		if i > 0 && line+1+len(word) > maxHeaderLine {
			buf.WriteString("\r\n ")
			line = 1
		} else if i > 0 {
			buf.WriteString(" ")
			line++
		}
		buf.WriteString(word)
		line += len(word)
	}
	buf.WriteString("\r\n")
}

func writeAddressHeader(buf *bytes.Buffer, key string, addresses []Address) {
	items := make([]string, 0, len(addresses))
	for _, address := range addresses {
		items = append(items, formatAddress(address))
	}
	buf.WriteString(key)
	buf.WriteString(": ")
	line := len(key) + 2
	for i, item := range items {
		switch {
		case i == 0:
		case line+2+len(item) > maxHeaderLine:
			buf.WriteString(",\r\n ")
			line = 1
		default:
			buf.WriteString(", ")
			line += 2
		}
		buf.WriteString(item)
		line += len(item)
	}
	buf.WriteString("\r\n")
}

func formatAddress(address Address) string {
	if address.Name == "" {
		return address.Email
	}
	name := address.Name
	switch {
	case isASCII(name) && !strings.ContainsAny(name, `()<>[]:;@\,."`):
		name = `"` + name + `"`
	case isASCII(name):
		escaped := strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(name)
		name = `"` + escaped + `"`
	default:
		name = encodeDisplayName(name)
	}
	return name + " <" + address.Email + ">"
}

func newMessageID(from string) (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	domain := "localhost"
	if i := strings.LastIndex(from, "@"); i >= 0 && i+1 < len(from) {
		domain = from[i+1:]
	}
	return hex.EncodeToString(buf[:]) + "@" + domain, nil
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}
