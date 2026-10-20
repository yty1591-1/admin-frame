package email

import (
	"fmt"
	"mime"
	"net/mail"
	"net/textproto"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	defaultMaxRecipients      = 50
	defaultMaxAttachments     = 10
	defaultMaxAttachmentBytes = 5 << 20
	defaultMaxMessageBytes    = 10 << 20
	maxSubjectRunes           = 200
	maxDisplayNameRunes       = 64
	maxAttachmentNameBytes    = 255
	maxHeaderValueBytes       = 998
	maxIdempotencyBytes       = 128
	maxReferenceBytes         = 128
)

// 允许的自定义 Header；其余一律拒绝，避免伪造回信地址与注入。
var allowedHeaders = map[string]struct{}{
	"X-Entity-Ref-Id":          {},
	"List-Unsubscribe":         {},
	"List-Unsubscribe-Post":    {},
	"X-Priority":               {},
	"X-Mailer":                 {},
	"X-Auto-Response-Suppress": {},
	"Auto-Submitted":           {},
	"Importance":               {},
}

var executableExtensions = map[string]struct{}{
	".exe": {}, ".bat": {}, ".cmd": {}, ".js": {}, ".vbs": {},
	".scr": {}, ".ps1": {}, ".com": {}, ".msi": {}, ".jar": {},
}

// Limits 是消息校验的资源上限；零值字段使用包默认值。
type Limits struct {
	// MaxRecipients 是 To 与 Cc+Bcc 各自的上限；0 使用 50。
	MaxRecipients int
	// MaxAttachments 是附件个数上限；0 使用 10。
	MaxAttachments int
	// MaxAttachmentBytes 是单个附件上限；0 使用 5 MiB。
	MaxAttachmentBytes int64
	// MaxMessageBytes 是整封邮件上限（正文 + 附件）；0 使用 10 MiB。
	MaxMessageBytes int64
	// AllowExecutableAttachments 为 true 时允许可执行扩展名；默认拒绝。
	AllowExecutableAttachments bool
}

// DefaultLimits 返回默认上限。
func DefaultLimits() Limits {
	return Limits{
		MaxRecipients:      defaultMaxRecipients,
		MaxAttachments:     defaultMaxAttachments,
		MaxAttachmentBytes: defaultMaxAttachmentBytes,
		MaxMessageBytes:    defaultMaxMessageBytes,
	}
}

func (l Limits) normalized() Limits {
	if l.MaxRecipients <= 0 {
		l.MaxRecipients = defaultMaxRecipients
	}
	if l.MaxAttachments <= 0 {
		l.MaxAttachments = defaultMaxAttachments
	}
	if l.MaxAttachmentBytes <= 0 {
		l.MaxAttachmentBytes = defaultMaxAttachmentBytes
	}
	if l.MaxMessageBytes <= 0 {
		l.MaxMessageBytes = defaultMaxMessageBytes
	}
	return l
}

// Address 是 RFC 5322 地址；Name 可为 UTF-8，Email 必须为 ASCII。
type Address struct {
	// Name 是显示名；空表示只显示地址。
	Name string
	// Email 是纯邮件地址（如 user@example.com），不含尖括号与显示名。
	Email string
}

// Attachment 是内存中的附件；大文件应由业务改用对象存储链接。
type Attachment struct {
	// Filename 是展示文件名；禁止路径分隔符与控制字符。
	Filename string
	// ContentType 是 MIME 类型；空时使用 application/octet-stream。
	ContentType string
	// Data 是附件内容。
	Data []byte
}

// Message 是一封待发送邮件；零值非法。
//
// Message 可安全复制，但 Attachments.Data 与 Headers 为引用类型，
// 调用方不得在 Send 期间并发修改。
type Message struct {
	From    Address
	To      []Address
	Cc      []Address
	Bcc     []Address
	ReplyTo *Address
	Subject string
	// Text 是纯文本正文；与 HTML 至少一个非空。
	Text string
	// HTML 是 HTML 正文；调用方必须自行转义用户输入。
	HTML string
	// Headers 仅允许白名单键；Bcc 不会写入消息头。
	Headers     map[string]string
	Attachments []Attachment
	// IdempotencyKey 由调用方提供，≤128 字节，用于编排层去重。
	IdempotencyKey string
	// Reference 为业务引用（如 export_id），只允许低基数、非 PII。
	Reference string
}

// Validate 使用默认上限校验消息；不访问网络。
func (m Message) Validate() error { return m.ValidateWith(DefaultLimits()) }

// ValidateWith 按给定上限校验消息。
//
// From 为空时允许，由驱动的 Config.From 补齐；其余结构问题返回 ErrInvalidMessage，
// 收件人问题返回 ErrInvalidRecipient，策略拒绝（可执行附件）返回 ErrRejected。
func (m Message) ValidateWith(limits Limits) error {
	limits = limits.normalized()
	if m.From.Email != "" || m.From.Name != "" {
		if err := validateAddress(m.From); err != nil {
			return err
		}
	}
	if len(m.To) == 0 {
		return invalidRecipient("at least one recipient is required")
	}
	if len(m.To) > limits.MaxRecipients {
		return invalidRecipient("too many recipients: %d > %d", len(m.To), limits.MaxRecipients)
	}
	for _, addr := range m.To {
		if err := validateAddress(addr); err != nil {
			return err
		}
	}
	if len(m.Cc)+len(m.Bcc) > limits.MaxRecipients {
		return invalidRecipient("too many cc/bcc recipients")
	}
	for _, addr := range append(append([]Address{}, m.Cc...), m.Bcc...) {
		if err := validateAddress(addr); err != nil {
			return err
		}
	}
	if m.ReplyTo != nil {
		if err := validateAddress(*m.ReplyTo); err != nil {
			return err
		}
	}
	if utf8.RuneCountInString(m.Subject) > maxSubjectRunes {
		return invalidMessage("subject exceeds %d runes", maxSubjectRunes)
	}
	if containsControl(m.Subject) {
		return invalidMessage("subject contains control characters")
	}
	if m.Text == "" && m.HTML == "" {
		return invalidMessage("text or html body is required")
	}
	if err := validateHeaders(m.Headers); err != nil {
		return err
	}
	if err := validateAttachments(m.Attachments, limits); err != nil {
		return err
	}
	total := int64(len(m.Text) + len(m.HTML))
	for _, att := range m.Attachments {
		total += int64(len(att.Data))
	}
	if total > limits.MaxMessageBytes {
		return invalidMessage("message exceeds %d bytes", limits.MaxMessageBytes)
	}
	if len(m.IdempotencyKey) > maxIdempotencyBytes || containsControl(m.IdempotencyKey) {
		return invalidMessage("idempotency key is invalid")
	}
	if len(m.Reference) > maxReferenceBytes || containsControl(m.Reference) {
		return invalidMessage("reference is invalid")
	}
	return nil
}

// Recipients 返回 SMTP 信封收件人（To、Cc、Bcc），按小写地址去重并保持顺序。
func (m Message) Recipients() []string {
	all := make([]Address, 0, len(m.To)+len(m.Cc)+len(m.Bcc))
	all = append(all, m.To...)
	all = append(all, m.Cc...)
	all = append(all, m.Bcc...)
	seen := make(map[string]struct{}, len(all))
	out := make([]string, 0, len(all))
	for _, addr := range all {
		key := strings.ToLower(strings.TrimSpace(addr.Email))
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, addr.Email)
	}
	return out
}

// String 返回脱敏摘要，不包含地址、主题与正文。
func (m Message) String() string {
	return fmt.Sprintf("email.Message{recipients=%d, attachments=%d}", len(m.To)+len(m.Cc)+len(m.Bcc), len(m.Attachments))
}

func validateAddress(addr Address) error {
	if addr.Email == "" {
		return invalidRecipient("email address is required")
	}
	if containsControl(addr.Email) || strings.ContainsAny(addr.Email, " <>,;\"") {
		return invalidRecipient("email address contains invalid characters")
	}
	parsed, err := mail.ParseAddress(addr.Email)
	if err != nil || parsed.Address != addr.Email {
		return invalidRecipient("email address is malformed")
	}
	if utf8.RuneCountInString(addr.Name) > maxDisplayNameRunes {
		return invalidMessage("display name exceeds %d runes", maxDisplayNameRunes)
	}
	if containsControl(addr.Name) {
		return invalidMessage("display name contains control characters")
	}
	return nil
}

func validateHeaders(headers map[string]string) error {
	for key, value := range headers {
		canonical := textproto.CanonicalMIMEHeaderKey(key)
		if _, ok := allowedHeaders[canonical]; !ok {
			return invalidMessage("header %q is not allowed", key)
		}
		if len(value) > maxHeaderValueBytes || containsControl(value) {
			return invalidMessage("header %q has an invalid value", key)
		}
	}
	return nil
}

func validateAttachments(attachments []Attachment, limits Limits) error {
	if len(attachments) > limits.MaxAttachments {
		return invalidMessage("too many attachments: %d > %d", len(attachments), limits.MaxAttachments)
	}
	for _, att := range attachments {
		if att.Filename == "" {
			return invalidMessage("attachment filename is required")
		}
		if len(att.Filename) > maxAttachmentNameBytes || containsControl(att.Filename) {
			return invalidMessage("attachment filename is invalid")
		}
		if strings.ContainsAny(att.Filename, `/\`) || strings.Contains(att.Filename, "..") {
			return invalidMessage("attachment filename must not contain a path")
		}
		if int64(len(att.Data)) > limits.MaxAttachmentBytes {
			return invalidMessage("attachment %q exceeds %d bytes", att.Filename, limits.MaxAttachmentBytes)
		}
		if att.ContentType != "" {
			if _, _, err := mime.ParseMediaType(att.ContentType); err != nil {
				return invalidMessage("attachment %q has an invalid content type", att.Filename)
			}
		}
		if !limits.AllowExecutableAttachments {
			ext := strings.ToLower(filepath.Ext(att.Filename))
			if _, ok := executableExtensions[ext]; ok {
				return rejected("executable attachment %q is not allowed", att.Filename)
			}
		}
	}
	return nil
}

func containsControl(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}
