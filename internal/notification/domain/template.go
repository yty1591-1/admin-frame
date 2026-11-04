package domain

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// 模板长度上限（与迁移 000016 列宽对齐）。
const (
	// MaxTemplateSubjectLength 邮件主题上限。
	MaxTemplateSubjectLength = 255
	// MaxTemplateBodyLength 模板正文上限（邮件正文允许较长）。
	MaxTemplateBodyLength = 8192
	// MaxTemplateVersion 起始版本号。
	InitialTemplateVersion = 1
	// MaxTemplateParamValueLength 模板变量取值上限（防御正文膨胀）。
	MaxTemplateParamValueLength = 512
)

// placeholderPattern 匹配模板中的变量占位符 `{{name}}`。
//
// 变量名只允许小写字母 / 数字 / 下划线：占位符集合即变量白名单
// （声明式，不需要单独的 whitelist 字段），渲染期与白名单精确比对。
var placeholderPattern = regexp.MustCompile(`\{\{([a-z_][a-z0-9_]*)\}\}`)

// Template 是一条渲染模板，按 (key, channel, locale) 定位、版本递增。
//
// 内容承载渠道载体：email 有主题 + 正文；sms 正文是模板参数描述
// （供应商侧模板 ID + 参数在 params 里传）；push 是标题 + 正文。
// v1 用统一的两字段（Subject 可空 + Body）表达，不为每渠道建结构。
type Template struct {
	// Key 模板键（同 scene 命名空间，如 tenant.invitation）。
	Key string
	// Channel 目标渠道。
	Channel Channel
	// Locale 语言标签。
	Locale string
	// Version 版本号：同键重传即新版本，渲染锁定单版本，
	// 保证一次编排内多次重试渲染结果一致。
	Version int64
	// Subject 主题（email 用；其他渠道留空）。
	Subject string
	// Body 正文 / 模板体。
	Body string
	// Enabled 停用的模板不可渲染。
	Enabled bool
	// CreatedBy 创建 / 最近更新的操作主体（审计留痕）。
	CreatedBy string
	// CreatedAt 创建时间。
	CreatedAt time.Time
	// UpdatedAt 最近更新时间。
	UpdatedAt time.Time
}

// NewTemplate 构造 v1 版本模板并做结构校验。
func NewTemplate(key string, channel Channel, locale, subject, body, createdBy string, now time.Time) (Template, error) {
	trimmedKey := strings.TrimSpace(key)
	if trimmedKey == "" {
		return Template{}, ErrTemplateKeyRequired
	}
	if len([]rune(trimmedKey)) > MaxTemplateKeyLength {
		return Template{}, ErrTemplateKeyTooLong
	}
	if !validSceneKey(trimmedKey) {
		return Template{}, &InvalidTemplateKeyError{Key: trimmedKey}
	}
	if !channel.Implemented() {
		return Template{}, &ChannelNotImplementedError{Channel: channel}
	}
	trimmedLocale := strings.TrimSpace(locale)
	if err := validateLocale(trimmedLocale); err != nil {
		return Template{}, err
	}
	trimmedSubject := strings.TrimSpace(subject)
	if channel == ChannelEmail && trimmedSubject == "" {
		return Template{}, ErrTemplateSubjectRequired
	}
	if len([]rune(trimmedSubject)) > MaxTemplateSubjectLength {
		return Template{}, ErrTemplateSubjectTooLong
	}
	trimmedBody := strings.TrimSpace(body)
	if trimmedBody == "" {
		return Template{}, ErrTemplateBodyRequired
	}
	if len([]rune(trimmedBody)) > MaxTemplateBodyLength {
		return Template{}, ErrTemplateBodyTooLong
	}
	if _, err := placeholders(trimmedSubject + trimmedBody); err != nil {
		return Template{}, err
	}
	return Template{
		Key:       trimmedKey,
		Channel:   channel,
		Locale:    trimmedLocale,
		Version:   InitialTemplateVersion,
		Subject:   trimmedSubject,
		Body:      trimmedBody,
		Enabled:   true,
		CreatedBy: strings.TrimSpace(createdBy),
		CreatedAt: now.UTC(),
		UpdatedAt: now.UTC(),
	}, nil
}

// placeholders 提取模板内容中的变量白名单（占位符名集合）。
func placeholders(content string) ([]string, error) {
	matches := placeholderPattern.FindAllStringSubmatch(content, -1)
	seen := make(map[string]struct{}, len(matches))
	names := make([]string, 0, len(matches))
	for _, m := range matches {
		name := m[1]
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	return names, nil
}

// Validate 校验仓储回读的模板自洽性。
func (t Template) Validate() error {
	if strings.TrimSpace(t.Key) == "" {
		return ErrTemplateKeyRequired
	}
	if !t.Channel.Implemented() {
		return &ChannelNotImplementedError{Channel: t.Channel}
	}
	if t.Version <= 0 {
		return fmt.Errorf("notification: template version must be positive, got %d", t.Version)
	}
	if strings.TrimSpace(t.Body) == "" {
		return ErrTemplateBodyRequired
	}
	return nil
}

// Rendered 是渲染产物；只存在于投递瞬时内存，不落库。
type Rendered struct {
	// Subject 渲染后主题（email 用）。
	Subject string
	// Body 渲染后正文。
	Body string
}

// Render 用给定参数渲染模板。
//
// 校验规则（缺一拒绝，宁可失败也不发出残缺通知）：
//   - 模板占位符集合与参数键集合必须精确相等：缺失参数 → ErrTemplateParamMissing，
//     多余参数 → ErrTemplateParamUnknown（防止调用方拼错键还以为发对了）；
//   - 参数值非空且不超长（空值渲染出空洞正文，多半是上游 bug）。
//
// 值原样替换不转义：模板内容是运营维护的受信文本，调用方模板层
// 负责 HTML 转义用户输入（notification-channels.md §6 的分工约定）。
func (t Template) Render(params map[string]string) (Rendered, error) {
	if !t.Enabled {
		return Rendered{}, ErrTemplateDisabled
	}
	names, err := placeholders(t.Subject + t.Body)
	if err != nil {
		return Rendered{}, err
	}
	want := make(map[string]struct{}, len(names))
	for _, name := range names {
		want[name] = struct{}{}
	}
	checked := make(map[string]string, len(params))
	for key, value := range params {
		if _, ok := want[key]; !ok {
			return Rendered{}, &TemplateParamUnknownError{Key: key}
		}
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			return Rendered{}, &TemplateParamMissingError{Key: key}
		}
		if len([]rune(trimmed)) > MaxTemplateParamValueLength {
			return Rendered{}, &TemplateParamTooLongError{Key: key}
		}
		checked[key] = value
	}
	for _, name := range names {
		if _, ok := checked[name]; !ok {
			return Rendered{}, &TemplateParamMissingError{Key: name}
		}
	}
	render := func(content string) string {
		return placeholderPattern.ReplaceAllStringFunc(content, func(match string) string {
			name := match[2 : len(match)-2]
			if v, ok := checked[name]; ok {
				return v
			}
			return match
		})
	}
	return Rendered{Subject: render(t.Subject), Body: render(t.Body)}, nil
}
