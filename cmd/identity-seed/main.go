// Package main 提供身份域账号开通 CLI（docs/identity-authn.md §7.2.1 D4）。
//
// 首个管理员 bootstrap 的唯一受控入口：
//   - 口令只从 stdin 或环境变量读取，禁止出现在命令行参数与日志；
//   - 账号、密码凭据与 auth.account.created 审计事件在同一事务内提交；
//   - 重复执行保持幂等：账号已存在时不修改密码、不重复写审计；
//   - 不写 TOTP 密钥：A 端首次登录返回 200 + mfa_setup_ticket，由浏览器完成绑定。
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/app"
	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/identity/infrastructure/persistence"
	"github.com/admin-frame/admin-frame/internal/platform"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
	"github.com/admin-frame/admin-frame/pkg/db"
	"github.com/admin-frame/admin-frame/pkg/password"
)

// 由 -ldflags -X 注入（RELS-03）；本地默认 dev。
var (
	version   = "dev"
	commit    = "none"
	buildTime = "unknown"
)

// seedService 写入审计事件的 service 字段。
const seedService = "identity-seed"

// seedTraceID 与账号主键组合，保证同一账号的重复执行可追溯。
const seedTraceID = "identity-seed"

// passwordEnv 是口令的替代来源；比命令行参数更安全（不进 shell history / ps）。
const passwordEnv = "IDENTITY_SEED_PASSWORD"

func main() {
	configPath := flag.String("config", "", "optional config file; reads db.driver / db.dsn")
	driver := flag.String("db-driver", "", "database driver: mysql|postgres|pgx (overrides config/env)")
	dsn := flag.String("db-dsn", "", "database DSN (overrides config/env; prefer ADMIN_FRAME_DB_DSN)")
	channel := flag.String("channel", string(domain.ChannelAdmin), "account channel: admin|biz")
	subject := flag.String("subject", "", "login subject (account / email), required")
	merchantID := flag.String("merchant-id", "", "merchant id, required when -channel=biz")
	passwordStdin := flag.Bool("password-stdin", false, "read password from stdin instead of "+passwordEnv)
	nodeID := flag.Int64("node-id", 0, "snowflake node id [0,1023]; must differ from running API nodes")
	dryRun := flag.Bool("dry-run", false, "validate and plan without writing")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("identity-seed %s (commit %s, built %s)\n", version, commit, buildTime)
		return
	}

	opts := options{
		ConfigPath:    *configPath,
		Driver:        *driver,
		DSN:           *dsn,
		Channel:       *channel,
		Subject:       *subject,
		MerchantID:    *merchantID,
		PasswordStdin: *passwordStdin,
		NodeID:        *nodeID,
		DryRun:        *dryRun,
	}
	if err := runWithSignal(opts); err != nil {
		fmt.Fprintf(os.Stderr, "identity-seed: %v\n", err)
		os.Exit(1)
	}
}

// options 汇总 CLI 参数，便于 run 独立测试与阅读。
type options struct {
	ConfigPath    string
	Driver        string
	DSN           string
	Channel       string
	Subject       string
	MerchantID    string
	PasswordStdin bool
	NodeID        int64
	DryRun        bool
}

// seedResult 是 CLI 的可核对输出，禁止包含口令、哈希或密钥。
type seedResult struct {
	Status      string `json:"status"`
	Channel     string `json:"channel"`
	Subject     string `json:"subject"`
	AccountID   string `json:"account_id"`
	MFABinding  string `json:"mfa_binding"`
	AuditAction string `json:"audit_action,omitempty"`
	TraceID     string `json:"trace_id"`
}

// runWithSignal 装配信号与总超时上下文。
func runWithSignal(opts options) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	return run(ctx, opts)
}

func run(ctx context.Context, opts options) error {
	channel, err := parseChannel(opts.Channel)
	if err != nil {
		return err
	}
	subject := domain.NormalizeSubject(opts.Subject)
	if subject == "" {
		return errors.New("-subject is required")
	}
	merchantID := strings.TrimSpace(opts.MerchantID)
	if channel == domain.ChannelBiz && merchantID == "" {
		return errors.New("-merchant-id is required when -channel=biz")
	}
	if channel != domain.ChannelBiz && merchantID != "" {
		return fmt.Errorf("-merchant-id must be empty when -channel=%s", channel)
	}

	dbCfg, err := platform.LoadDBConfig(opts.ConfigPath)
	if err != nil {
		return err
	}
	if opts.Driver != "" {
		dbCfg.Driver = opts.Driver
	}
	if opts.DSN != "" {
		dbCfg.DSN = opts.DSN
	}
	openDriver, err := sqlDriver(dbCfg.Driver)
	if err != nil {
		return err
	}
	if strings.TrimSpace(dbCfg.DSN) == "" {
		return errors.New("db dsn is required: set ADMIN_FRAME_DB_DSN, -db-dsn or db.dsn")
	}

	database, err := db.Open(ctx, db.Config{
		Driver:       openDriver,
		DSN:          dbCfg.DSN,
		MaxOpenConns: 2,
		MaxIdleConns: 1,
		PingTimeout:  5 * time.Second,
	})
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := database.Close(); closeErr != nil {
			fmt.Fprintf(os.Stderr, "identity-seed: close db: %v\n", closeErr)
		}
	}()

	node := opts.NodeID
	ids, _, err := platform.NewIDGenerator(platform.IDConfig{
		NodeIDSource: platform.IDSourceConfig,
		NodeID:       &node,
	})
	if err != nil {
		return err
	}

	sqlDB := database.SQL()
	accounts := persistence.NewSQLAccountRepository(sqlDB)
	credentials := persistence.NewSQLCredentialRepository(sqlDB)
	emitter := app.OutboxEmitter{Events: platform.NewAsyncStore(database)}
	uow := persistence.NewSQLUnitOfWork(sqlDB)

	// 先做只读探测：账号已存在时不需要再次提供口令，CLI 才能真正可重复执行。
	existing, err := accounts.FindBySubject(ctx, channel, subject)
	switch {
	case err == nil:
		if existing.IsDeleted() {
			return fmt.Errorf("subject %q is occupied by a deleted account %s", subject, existing.ID)
		}
		return printSeedResult(seedResult{
			Status:     "exists",
			Channel:    string(channel),
			Subject:    existing.Subject,
			AccountID:  existing.ID,
			MFABinding: "unchanged",
			TraceID:    seedTraceID,
		})
	case errors.Is(err, apperr.ErrNotFound):
	default:
		return err
	}

	plain, err := readPassword(opts.PasswordStdin, os.Stdin)
	if err != nil {
		return err
	}
	hasher, err := password.New(password.Config{})
	if err != nil {
		return err
	}
	passwordSvc, err := app.NewPasswordService(hasher, app.DefaultPasswordPolicy())
	if err != nil {
		return err
	}
	// 哈希在校验策略后立即完成，避免在事务内占用连接做 Argon2id。
	encoded, algorithm, err := passwordSvc.Hash(plain)
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	accountID, err := ids.NextString()
	if err != nil {
		return fmt.Errorf("generate account id: %w", err)
	}
	account, err := domain.NewAccount(domain.NewAccountParams{
		ID:         accountID,
		Channel:    channel,
		Subject:    subject,
		MerchantID: merchantID,
		Now:        now,
	})
	if err != nil {
		return err
	}
	credentialID, err := ids.NextString()
	if err != nil {
		return fmt.Errorf("generate credential id: %w", err)
	}
	credential, err := domain.NewCredential(domain.NewCredentialParams{
		ID:        credentialID,
		AccountID: account.ID,
		Type:      domain.CredentialTypePassword,
		Hash:      encoded,
		Algorithm: algorithm,
		Now:       now,
	})
	if err != nil {
		return err
	}
	eventID, err := ids.NextString()
	if err != nil {
		return fmt.Errorf("generate audit event id: %w", err)
	}
	event := audit.Event{
		EventID:      eventID,
		Action:       audit.ActionAuthAccountCreated,
		ActorID:      "cli:" + seedService,
		ActorType:    "operator",
		Channel:      string(channel),
		MerchantID:   merchantID,
		ResourceType: "account",
		ResourceID:   account.ID,
		Outcome:      audit.OutcomeSuccess,
		OccurredAt:   now,
		TraceID:      seedTraceID,
		Service:      seedService,
		Attrs:        map[string]string{"subject": account.Subject},
	}

	result := seedResult{
		Status:     "created",
		Channel:    string(channel),
		Subject:    account.Subject,
		AccountID:  account.ID,
		MFABinding: "pending_via_login",
		TraceID:    seedTraceID,
	}
	if opts.DryRun {
		result.Status = "planned"
	}
	if err := uow.WithinTx(ctx, func(txCtx context.Context) error {
		existing, findErr := accounts.FindBySubject(txCtx, channel, subject)
		switch {
		case findErr == nil:
			if existing.IsDeleted() {
				return fmt.Errorf("subject %q is occupied by a deleted account %s", subject, existing.ID)
			}
			result.Status = "exists"
			result.AccountID = existing.ID
			result.MFABinding = "unchanged"
			return nil
		case errors.Is(findErr, apperr.ErrNotFound):
		default:
			return findErr
		}
		if opts.DryRun {
			return nil
		}
		if err := accounts.Save(txCtx, account, 0); err != nil {
			return err
		}
		if err := credentials.Save(txCtx, credential); err != nil {
			return err
		}
		if err := emitter.Emit(txCtx, event); err != nil {
			return err
		}
		result.AuditAction = audit.ActionAuthAccountCreated
		return nil
	}); err != nil {
		return err
	}

	return printSeedResult(result)
}

// printSeedResult 输出可核对摘要；stdout 只放 JSON，提示走 stderr。
func printSeedResult(result seedResult) error {
	summary, err := json.Marshal(result)
	if err != nil {
		return err
	}
	fmt.Println(string(summary))
	switch result.Status {
	case "exists":
		fmt.Fprintf(os.Stderr, "identity-seed: account already exists; nothing changed\n")
	case "planned":
		fmt.Fprintf(os.Stderr, "identity-seed: dry-run; nothing written\n")
	}
	return nil
}

// parseChannel 只接受可开通密码凭据的端别。
//
// C 端账号由短信 / OAuth 自助创建，不存在密码凭据，故拒绝。
func parseChannel(raw string) (domain.Channel, error) {
	channel := domain.Channel(strings.ToLower(strings.TrimSpace(raw)))
	switch channel {
	case domain.ChannelAdmin, domain.ChannelBiz:
		return channel, nil
	default:
		return "", fmt.Errorf("unsupported channel %q: want admin|biz", raw)
	}
}

// sqlDriver 把配置驱动名映射为已注册的 database/sql 驱动名。
func sqlDriver(driver string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(driver)) {
	case "mysql", "mariadb":
		return "mysql", nil
	case "postgres", "postgresql", "pgx":
		return "pgx", nil
	case "":
		return "", errors.New("db driver is required: set ADMIN_FRAME_DB_DRIVER, -db-driver or db.driver")
	case "memory":
		return "", errors.New("db driver \"memory\" is not usable for a one-shot seed CLI")
	default:
		return "", fmt.Errorf("unsupported db driver %q (want mysql|postgres)", driver)
	}
}

// readPassword 读取口令：stdin 优先，其次环境变量；禁止命令行参数。
func readPassword(fromStdin bool, stdin io.Reader) (string, error) {
	if fromStdin {
		raw, err := io.ReadAll(io.LimitReader(stdin, 4*1024))
		if err != nil {
			return "", fmt.Errorf("read password from stdin: %w", err)
		}
		plain := strings.TrimRight(string(raw), "\r\n")
		if plain == "" {
			return "", errors.New("password from stdin is empty")
		}
		return plain, nil
	}
	plain := os.Getenv(passwordEnv)
	if strings.TrimSpace(plain) == "" {
		return "", fmt.Errorf("password is required: pass -password-stdin or set %s", passwordEnv)
	}
	return plain, nil
}
