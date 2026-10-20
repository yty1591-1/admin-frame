package smtp

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	netsmtp "net/smtp"
	"net/textproto"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/admin-frame/admin-frame/pkg/email"
	pkgerrors "github.com/admin-frame/admin-frame/pkg/errors"
)

const providerName = "smtp"

func init() {
	email.Register(providerName, Open)
}

type sender struct {
	cfg       email.Config
	addr      string
	tlsConfig *tls.Config
	closed    atomic.Bool
}

// Open 构造 SMTP 驱动；配置非法返回包装 ErrInvalidConfig 的错误。
//
// 安全默认：启用 STARTTLS、TLS 1.2+、校验服务端证书；明文或无认证需显式
// AllowInsecure=true（仅限本地/测试）。
func Open(_ context.Context, cfg email.Config) (email.Sender, error) {
	cfg = cfg.Normalized()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	host := strings.TrimSpace(cfg.SMTP.Host)
	if host == "" {
		return nil, fmt.Errorf("%w: smtp host is required", email.ErrInvalidConfig)
	}
	if cfg.SMTP.Port < 1 || cfg.SMTP.Port > 65535 {
		return nil, fmt.Errorf("%w: smtp port is invalid", email.ErrInvalidConfig)
	}
	encryption := strings.ToLower(strings.TrimSpace(cfg.SMTP.Encryption))
	switch encryption {
	case "starttls", "tls", "none":
	default:
		return nil, fmt.Errorf("%w: smtp encryption must be starttls, tls or none", email.ErrInvalidConfig)
	}
	auth := strings.ToLower(strings.TrimSpace(cfg.SMTP.Auth))
	switch auth {
	case "plain", "login", "none":
	default:
		return nil, fmt.Errorf("%w: smtp auth must be plain, login or none", email.ErrInvalidConfig)
	}
	if encryption == "none" && !cfg.SMTP.AllowInsecure {
		return nil, fmt.Errorf("%w: smtp encryption none requires allow_insecure", email.ErrInvalidConfig)
	}
	if encryption == "tls" && cfg.SMTP.Port != 465 && !cfg.SMTP.AllowInsecure {
		return nil, fmt.Errorf("%w: implicit tls on unusual port requires allow_insecure", email.ErrInvalidConfig)
	}
	if cfg.SMTP.Port == 465 && encryption != "tls" && !cfg.SMTP.AllowInsecure {
		return nil, fmt.Errorf("%w: port 465 requires implicit tls", email.ErrInvalidConfig)
	}
	if auth != "none" && (cfg.SMTP.Username == "" || cfg.SMTP.Password == "") {
		return nil, fmt.Errorf("%w: smtp username and password are required", email.ErrInvalidConfig)
	}
	if auth == "none" && !cfg.SMTP.AllowInsecure {
		return nil, fmt.Errorf("%w: smtp auth none requires allow_insecure", email.ErrInvalidConfig)
	}
	minVersion, err := parseTLSVersion(cfg.SMTP.TLSMinVersion)
	if err != nil {
		return nil, err
	}
	return &sender{
		cfg:  cfg,
		addr: net.JoinHostPort(host, strconv.Itoa(cfg.SMTP.Port)),
		tlsConfig: &tls.Config{
			MinVersion: minVersion,
			ServerName: host,
		},
	}, nil
}

// Provider 返回驱动名。
func (s *sender) Provider() string { return providerName }

// Close 标记驱动关闭；幂等，不持有长连接资源。
func (s *sender) Close() error {
	s.closed.Store(true)
	return nil
}

// Send 通过 SMTP 投递一封邮件；成功表示服务端已接收（250）。
func (s *sender) Send(ctx context.Context, msg email.Message) (email.Receipt, error) {
	start := s.cfg.Clock.Now()
	if ctx == nil {
		ctx = context.Background()
	}
	if s.closed.Load() {
		return email.Receipt{}, fmt.Errorf("%w: %s", email.ErrClosed, providerName)
	}
	if err := ctx.Err(); err != nil {
		return email.Receipt{}, s.fail(ctx, start, email.MapContextError(providerName, err))
	}
	if msg.From.Email == "" {
		msg.From = s.cfg.From
	}
	limits := s.cfg.Limits()
	data, messageID, err := email.BuildMIME(msg, email.MIMEOptions{
		From:   s.cfg.From,
		Date:   s.cfg.Clock.Now(),
		Limits: limits,
	})
	if err != nil {
		return email.Receipt{}, s.fail(ctx, start, err)
	}
	if int64(len(data)) > limits.MaxMessageBytes {
		return email.Receipt{}, s.fail(ctx, start, email.Rejected("smtp: encoded message is too large"))
	}
	recipients := msg.Recipients()
	if len(recipients) == 0 {
		return email.Receipt{}, s.fail(ctx, start,
			fmt.Errorf("%w: %w", email.ErrInvalidRecipient, pkgerrors.InvalidArgument("smtp: no recipients")))
	}
	if err := s.deliver(ctx, msg.From.Email, recipients, data); err != nil {
		return email.Receipt{}, s.fail(ctx, start, err)
	}
	receipt := email.Receipt{Provider: providerName, MessageID: messageID, AcceptedAt: s.cfg.Clock.Now()}
	s.emit(ctx, start, nil)
	return receipt, nil
}

func (s *sender) deliver(ctx context.Context, from string, recipients []string, data []byte) error {
	conn, err := s.dial(ctx)
	if err != nil {
		return s.mapTransportError(ctx, err)
	}
	stop := watchContext(ctx, conn)
	defer stop()
	client, err := netsmtp.NewClient(conn, s.cfg.SMTP.Host)
	if err != nil {
		_ = conn.Close()
		return s.mapTransportError(ctx, err)
	}
	defer func() { _ = client.Close() }()
	if name := strings.TrimSpace(s.cfg.SMTP.HELOName); name != "" {
		if err := client.Hello(name); err != nil {
			return s.mapSMTPError(ctx, err)
		}
	}
	if strings.EqualFold(s.cfg.SMTP.Encryption, "starttls") {
		if err := client.StartTLS(s.tlsConfig); err != nil {
			return s.mapSMTPError(ctx, err)
		}
	}
	if !strings.EqualFold(s.cfg.SMTP.Auth, "none") {
		if err := client.Auth(s.auth()); err != nil {
			return s.mapSMTPError(ctx, err)
		}
	}
	if err := client.Mail(from); err != nil {
		return s.mapSMTPError(ctx, err)
	}
	for _, recipient := range recipients {
		if err := client.Rcpt(recipient); err != nil {
			return s.mapSMTPError(ctx, err)
		}
	}
	writer, err := client.Data()
	if err != nil {
		return s.mapSMTPError(ctx, err)
	}
	if _, err := writer.Write(data); err != nil {
		return s.mapSMTPError(ctx, err)
	}
	if err := writer.Close(); err != nil {
		return s.mapSMTPError(ctx, err)
	}
	// QUIT 失败不影响「已接收」结论，避免编排层重复投递。
	_ = client.Quit()
	return nil
}

func (s *sender) dial(ctx context.Context) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: s.cfg.Timeout}
	if strings.EqualFold(s.cfg.SMTP.Encryption, "tls") {
		tlsDialer := &tls.Dialer{NetDialer: dialer, Config: s.tlsConfig}
		return tlsDialer.DialContext(ctx, "tcp", s.addr)
	}
	return dialer.DialContext(ctx, "tcp", s.addr)
}

func (s *sender) auth() netsmtp.Auth {
	if strings.EqualFold(s.cfg.SMTP.Auth, "login") {
		return &loginAuth{
			username: s.cfg.SMTP.Username,
			password: s.cfg.SMTP.Password,
			host:     s.cfg.SMTP.Host,
		}
	}
	return netsmtp.PlainAuth("", s.cfg.SMTP.Username, s.cfg.SMTP.Password, s.cfg.SMTP.Host)
}

func (s *sender) mapTransportError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return email.MapContextError(providerName, ctxErr)
	}
	if ctxErr := contextErrorOf(err); ctxErr != nil {
		return email.MapContextError(providerName, ctxErr)
	}
	return email.NewProviderError(providerName, "transport", 0, pkgerrors.Unavailable("smtp: transport error"))
}

func (s *sender) mapSMTPError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return email.MapContextError(providerName, ctxErr)
	}
	if ctxErr := contextErrorOf(err); ctxErr != nil {
		return email.MapContextError(providerName, ctxErr)
	}
	var protocolErr *textproto.Error
	if errors.As(err, &protocolErr) {
		code := protocolErr.Code
		switch {
		case code == 421:
			return email.NewProviderError(providerName, strconv.Itoa(code), 0,
				pkgerrors.Unavailable("smtp: service not available"))
		case code >= 450 && code < 460:
			return email.NewProviderError(providerName, strconv.Itoa(code), 0,
				pkgerrors.ResourceExhausted("smtp: temporary failure"))
		case code == 535:
			return email.NewProviderError(providerName, strconv.Itoa(code), 0,
				pkgerrors.PermissionDenied("smtp: authentication failed"))
		case code == 552:
			return email.NewProviderError(providerName, strconv.Itoa(code), 0,
				fmt.Errorf("%w: %w", email.ErrRejected, pkgerrors.InvalidArgument("smtp: message too large")))
		case code >= 550 && code < 560:
			return email.NewProviderError(providerName, strconv.Itoa(code), 0,
				fmt.Errorf("%w: %w", email.ErrRejected, pkgerrors.FailedPrecondition("smtp: message rejected")))
		case code >= 500:
			return email.NewProviderError(providerName, strconv.Itoa(code), 0,
				fmt.Errorf("%w: %w", email.ErrRejected, pkgerrors.FailedPrecondition("smtp: permanent failure")))
		case code >= 400:
			return email.NewProviderError(providerName, strconv.Itoa(code), 0,
				pkgerrors.ResourceExhausted("smtp: temporary failure"))
		}
	}
	return email.NewProviderError(providerName, "protocol", 0, pkgerrors.Unavailable("smtp: protocol error"))
}

func (s *sender) fail(ctx context.Context, start time.Time, err error) error {
	s.emit(ctx, start, err)
	return err
}

func (s *sender) emit(ctx context.Context, start time.Time, err error) {
	if s.cfg.Observer == nil {
		return
	}
	ev := email.Event{
		Provider:  providerName,
		Operation: "send",
		Result:    "success",
		Duration:  s.cfg.Clock.Now().Sub(start),
	}
	if err != nil {
		ev.Result = "failure"
		ev.Code = email.CodeOf(err)
		ev.RetryAfter = email.RetryAfterOf(err)
	}
	s.cfg.Observer.Observe(ctx, ev)
}

func watchContext(ctx context.Context, conn net.Conn) func() {
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-done:
		}
	}()
	return func() { close(done) }
}

func contextErrorOf(err error) error {
	switch {
	case errors.Is(err, context.Canceled):
		return context.Canceled
	case errors.Is(err, context.DeadlineExceeded):
		return context.DeadlineExceeded
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return context.DeadlineExceeded
	}
	return nil
}

func parseTLSVersion(value string) (uint16, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "1.2", "tls1.2":
		return tls.VersionTLS12, nil
	case "1.3", "tls1.3":
		return tls.VersionTLS13, nil
	default:
		return 0, fmt.Errorf("%w: smtp tls_min_version must be 1.2 or 1.3", email.ErrInvalidConfig)
	}
}
