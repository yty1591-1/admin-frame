package iam_test

// S7-2 性能实测：10k 主体清单生成与证据导出（MySQL 真实链路）。
//
// 与单元测试的差别在于它量的是「数据库往返次数」而非「逻辑正确性」：
//  - generateTasks 一次拉全量绑定，随后逐条落库——往返次数直接决定耗时；
//  - PrivilegedOnly=true 时会按绑定逐条查角色，属 N+1；
//  - ExportEvidence 无分页，全部任务一次性读入内存编码。
//
// 用例默认跳过：规模化数据会拖慢 CI。显式开启：
//
//	IAM_PERF_DRILL=1 IAM_PERF_BINDINGS=10000 \
//	IAM_TEST_MYSQL_DSN='app:app@tcp(127.0.0.1:3306)/admin_frame_test?parseTime=true&loc=UTC' \
//	go test ./tests/integration/iam/ -run TestCertificationPerf -v
//
// 指标门槛来自 docs/iam-p1-5-7-engineering-plan.md §8：
// 复核清单生成 10k 主体 ≤ 5 分钟。

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/iam"
	"github.com/admin-frame/admin-frame/internal/iam/app"
	"github.com/admin-frame/admin-frame/internal/iam/domain"
)

// perfBudget 是计划 §8 的清单生成门槛。
const perfBudget = 5 * time.Minute

// perfBindingIDBase 选在雪花量级，避开与其它用例的数据重叠。
const perfBindingIDBase int64 = 7300000000000000

func TestCertificationPerf10kOnMySQL(t *testing.T) {
	if strings.TrimSpace(os.Getenv("IAM_PERF_DRILL")) == "" {
		t.Skip("IAM_PERF_DRILL is not set: 10k 性能实测默认跳过（规模数据会拖慢 CI）")
	}
	n := perfEnvInt(t, "IAM_PERF_BINDINGS", 10000)
	env := newCertDrillEnv(t)

	// 角色必须存在，绑定才有外键可挂；权限非高危，避免 PrivilegedOnly 场景先行过滤掉。
	newRole(t, env.sqlRoles, "720250001", "perf_bulk_role",
		[]iam.PermissionCode{iam.PermRoleRead}, nil)

	bindStart := time.Now()
	perfSeedBindings(t, env.db, n, "720250001")
	bindDur := time.Since(bindStart)
	t.Logf("[perf] seed %d bindings: %s", n, bindDur.Round(time.Millisecond))

	// 场景 1：默认复核范围（不判特权）——纯写放大。
	insertsBefore, selectsBefore := perfStatus(t, env.db)
	campaign := env.createCampaign(t, "S7-2 10k 全量复核", "720250001")
	activateStart := time.Now()
	activated := env.activate(t, campaign.ID)
	activateDur := time.Since(activateStart)
	insertsAfter, selectsAfter := perfStatus(t, env.db)

	t.Logf("[perf] ActivateCampaign(n=%d, privileged_only=false): %s", n, activateDur.Round(time.Millisecond))
	t.Logf("[perf] task_count=%d generated, Com_insert delta=%d, Com_select delta=%d",
		activated.TaskCount, insertsAfter-insertsBefore, selectsAfter-selectsBefore)

	if activated.TaskCount != n {
		t.Fatalf("task_count=%d, want %d（清单生成应覆盖全部生效绑定）", activated.TaskCount, n)
	}
	// 落库必须是批量：语句数应远小于任务数。阈值设为任务数的 1/10，
	// 既容许批大小调整，又能在实现退回逐条插入时明确失败。
	if got := insertsAfter - insertsBefore; got > int64(n/10) {
		t.Fatalf("Com_insert delta=%d > %d：落库退化为逐条插入（应为按批多值 INSERT）", got, n/10)
	}
	if activateDur > perfBudget {
		t.Errorf("清单生成 %s 超出计划门槛 %s（n=%d）", activateDur.Round(time.Millisecond), perfBudget, n)
	}

	// 场景 2：PrivilegedOnly=true——每条绑定一次角色查询（N+1）。
	privCampaign := env.createCampaignPrivileged(t, "S7-2 10k 特权复核", "720250001")
	_, privSelectsBefore := perfStatus(t, env.db)
	privStart := time.Now()
	privActivated := env.activate(t, privCampaign.ID)
	privDur := time.Since(privStart)
	_, privSelectsAfter := perfStatus(t, env.db)

	t.Logf("[perf] ActivateCampaign(n=%d, privileged_only=true): %s", n, privDur.Round(time.Millisecond))
	t.Logf("[perf] privileged task_count=%d, Com_select delta=%d",
		privActivated.TaskCount, privSelectsAfter-privSelectsBefore)
	// 特权判定必须按角色去重：1 个角色的 1 万绑定只应查 3 条 SQL（角色+权限+数据范围）。
	// 逐条判定时这里是 3×n（实测 30003）；阈值取与 n 无关的常数，防止规模改动后误判。
	if got := privSelectsAfter - privSelectsBefore; got > 10 {
		t.Fatalf("Com_select delta=%d > 10：特权判定仍在按绑定逐条查角色（N+1）", got)
	}

	// 场景 3：证据导出（当前按活动导出，受单活动上限约束）。
	exportStart := time.Now()
	payload, contentType, err := env.plain.ExportEvidence(context.Background(), campaign.ID,
		app.CertificationScope{Channel: domain.ChannelAdmin}, "csv", "perf")
	exportDur := time.Since(exportStart)
	if err != nil {
		t.Fatalf("ExportEvidence: %v", err)
	}
	rows, header := perfCountCSVRows(t, payload)
	t.Logf("[perf] ExportEvidence csv: %s, bytes=%d, rows=%d, content_type=%s",
		exportDur.Round(time.Millisecond), len(payload), rows, contentType)
	t.Logf("[perf] export header: %s", header)

	if rows != n {
		t.Errorf("export rows=%d, want %d", rows, n)
	}
	fill := perfEvidenceFill(t, payload)
	t.Logf("[perf] status=%v", fill.status)
	t.Logf("[perf] 非空列: decision=%d reason=%d decided_by=%d decided_at=%d（共 %d 行）",
		fill.decision, fill.reason, fill.decidedBy, fill.decidedAt, fill.rows)

	// 场景 4：单活动上限（MaxTasksPerCampaign 默认 10000）的确定性边界。
	// 这决定了「10 万行导出」指标能否单活动达成：第 10001 条绑定必须被拒。
	perfSeedBindingAt(t, env.db, n, "720250001")
	overCampaign := env.createCampaign(t, "S7-2 超上限复核", "720250001")
	_, overErr := env.plain.ActivateCampaign(context.Background(), overCampaign.ID,
		app.CertificationScope{Channel: domain.ChannelAdmin}, "perf")
	if overErr == nil {
		t.Fatalf("%d 条绑定（> 默认上限 10000）应被拒绝，实际成功", n+1)
	}
	if !strings.Contains(overErr.Error(), "exceeds limit") {
		t.Fatalf("超上限错误=%v，want 含 'exceeds limit'", overErr)
	}
	t.Logf("[perf] boundary: %d 条绑定被上限拦截 -> %v", n+1, overErr)

	// 场景 5：已裁决任务的证据字段完整率（应填口径）。
	// S7-4 的「导出字段完整率 100%」此前没有量化证据：pending 任务的裁决列本就为空，
	// 按全列统计会得到 0%，属于口径错误。这里用一批真实裁决过的任务来量。
	const decideCount = 100
	newRole(t, env.sqlRoles, "720250002", "perf_decide_role",
		[]iam.PermissionCode{iam.PermRoleRead}, nil)
	perfSeedBindingsFrom(t, env.db, n+1, decideCount, "720250002")
	decideCampaign := env.createCampaign(t, "S7-2 字段完整率", "720250002")
	decideActivated := env.activate(t, decideCampaign.ID)
	decideTasks, _, err := env.plain.ListTasks(context.Background(), decideActivated.ID,
		app.CertificationScope{Channel: domain.ChannelAdmin}, domain.CertificationTaskFilter{})
	if err != nil {
		t.Fatalf("ListTasks(decide campaign): %v", err)
	}
	if len(decideTasks) != decideCount {
		t.Fatalf("decide campaign tasks=%d, want %d", len(decideTasks), decideCount)
	}
	for _, task := range decideTasks {
		if _, err := env.plain.DecideTask(context.Background(), decideActivated.ID, task.ID,
			domain.CertificationDecisionConfirm, "S7-2 性能实测：确认保留", "perf",
			app.CertificationScope{Channel: domain.ChannelAdmin}); err != nil {
			t.Fatalf("DecideTask %s: %v", task.ID, err)
		}
	}
	decidedPayload, _, err := env.plain.ExportEvidence(context.Background(), decideActivated.ID,
		app.CertificationScope{Channel: domain.ChannelAdmin}, "csv", "perf")
	if err != nil {
		t.Fatalf("ExportEvidence(decide campaign): %v", err)
	}
	decidedFill := perfEvidenceFill(t, decidedPayload)
	t.Logf("[perf] 已裁决证据 %d 行: decision=%d reason=%d decided_by=%d decided_at=%d",
		decidedFill.rows, decidedFill.decision, decidedFill.reason,
		decidedFill.decidedBy, decidedFill.decidedAt)
	// 应填口径必须 100%：只要有一行缺裁决依据，等保与 SIEM 就无法追责。
	if decidedFill.decision != decideCount || decidedFill.reason != decideCount ||
		decidedFill.decidedBy != decideCount || decidedFill.decidedAt != decideCount {
		t.Fatalf("已裁决任务证据字段不完整：%d/%d 行具备裁决依据（应填口径要求 100%%）",
			minInt(decidedFill.decision, minInt(decidedFill.reason,
				minInt(decidedFill.decidedBy, decidedFill.decidedAt))), decideCount)
	}

	// 审计事件必须全部投递（本次含 create/activate/export/decide 多类）。
	delivered := perfDrainOutbox(t, env)
	t.Logf("[perf] audit outbox delivered=%d (pending=0, dlq=0)", delivered)

	perfWriteArtifacts(t, perfSummary{
		Bindings:                   n,
		SeedMillis:                 bindDur.Milliseconds(),
		ActivateFullMillis:         activateDur.Milliseconds(),
		ActivatePrivilegedMillis:   privDur.Milliseconds(),
		InsertStatements:           insertsAfter - insertsBefore,
		SelectStatementsFull:       selectsAfter - selectsBefore,
		SelectStatementsPrivileged: privSelectsAfter - privSelectsBefore,
		ExportMillis:               exportDur.Milliseconds(),
		ExportBytes:                len(payload),
		ExportRows:                 rows,
		StatusCounts:               fill.status,
		FillDecision:               fill.decision,
		FillReason:                 fill.reason,
		FillDecidedBy:              fill.decidedBy,
		FillDecidedAt:              fill.decidedAt,
		BoundaryRejected:           true, // 边界断言已在上面通过
		DecidedRows:                decidedFill.rows,
		DecidedFillDecision:        decidedFill.decision,
		DecidedFillReason:          decidedFill.reason,
		DecidedFillDecidedBy:       decidedFill.decidedBy,
		DecidedFillDecidedAt:       decidedFill.decidedAt,
	}, payload)
}

// createCampaignPrivileged 创建只覆盖高危角色的活动，用于验证特权判定是否 N+1。
func (e *certDrillEnv) createCampaignPrivileged(t *testing.T, name string, roleIDs ...string) *domain.CertificationCampaign {
	t.Helper()
	campaign, err := e.plain.CreateCampaign(context.Background(), app.CreateCertificationCampaignCommand{
		Channel:        domain.ChannelAdmin,
		Name:           name,
		RoleIDs:        roleIDs,
		PrivilegedOnly: true,
		DueAt:          time.Now().UTC().Add(48 * time.Hour),
		Actor:          "perf",
		Reason:         "S7-2 性能实测",
	})
	if err != nil {
		t.Fatalf("CreateCampaign(privileged): %v", err)
	}
	return campaign
}

// perfSeedBindings 批量插入 [0, n) 的生效绑定。
//
// 用 raw SQL 而不是 BindingService.Bind：量测目标是「生成清单」，
// 逐条调 Bind 会把快照失效与审计也计入准备阶段，且 n 次往返本身就要几分钟。
func perfSeedBindings(t *testing.T, db *sql.DB, n int, roleID string) {
	t.Helper()
	perfSeedBindingsFrom(t, db, 0, n, roleID)
}

// perfSeedBindingAt 追加第 index 条绑定（下标从 0 起，与批量种子同一 ID/主体编号空间）。
func perfSeedBindingAt(t *testing.T, db *sql.DB, index int, roleID string) {
	t.Helper()
	perfSeedBindingsFrom(t, db, index, 1, roleID)
}

func perfSeedBindingsFrom(t *testing.T, db *sql.DB, from, count int, roleID string) {
	t.Helper()
	const batch = 500
	now := time.Now().UTC()
	limit := from + count
	for start := from; start < limit; start += batch {
		end := start + batch
		if end > limit {
			end = limit
		}
		placeholders := make([]string, 0, end-start)
		args := make([]any, 0, (end-start)*5)
		for i := start; i < end; i++ {
			placeholders = append(placeholders, "(?, ?, ?, ?, ?, ?, ?, ?)")
			args = append(args,
				perfBindingIDBase+int64(i),
				"admin",
				string(domain.SubjectStaff),
				fmt.Sprintf("perf-staff-%05d", i),
				"",
				roleID,
				"perf-seed",
				now,
			)
		}
		//nolint:gosec // G202: 拼接内容仅为 (?,?,…) 占位符，全部值走参数绑定
		query := `INSERT INTO iam_role_binding
			(id, channel, subject_type, subject_id, merchant_id, role_id, granted_by, created_at)
			VALUES ` + strings.Join(placeholders, ",")
		if _, err := db.Exec(query, args...); err != nil {
			t.Fatalf("seed bindings batch %d-%d: %v", start, end, err)
		}
	}
}

// perfStatus 读取本次连接可见的累计插入/查询语句数。
//
// 用全局状态差值而非单连接：测试进程独占 integration 库，
// 且 reset() 里的 DELETE 不增 Com_insert，差值即本用例产生的语句数。
func perfStatus(t *testing.T, db *sql.DB) (inserts, selects int64) {
	t.Helper()
	return perfGlobalStatus(t, db, "Com_insert"), perfGlobalStatus(t, db, "Com_select")
}

func perfGlobalStatus(t *testing.T, db *sql.DB, name string) int64 {
	t.Helper()
	var (
		key string
		raw string
	)
	// SHOW GLOBAL STATUS 不接受占位符；name 只来自本文件的字面量常量。
	if err := db.QueryRow("SHOW GLOBAL STATUS LIKE '"+name+"'").Scan(&key, &raw); err != nil {
		t.Fatalf("read global status %s: %v", name, err)
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		t.Fatalf("parse global status %s=%q: %v", name, raw, err)
	}
	return value
}

// perfCountCSVRows 返回数据行数（不含表头）与表头文本。
func perfCountCSVRows(t *testing.T, payload []byte) (rows int, header string) {
	t.Helper()
	text := strings.TrimRight(string(payload), "\r\n")
	if text == "" {
		return 0, ""
	}
	lines := strings.Split(text, "\n")
	header = strings.TrimSpace(lines[0])
	return len(lines) - 1, header
}

// perfFill 汇总证据 CSV 的字段填充情况。
//
// 「字段完整率」的口径必须分两层看：
//   - 全列非空率：未裁决任务本就没有 decision/reason/decided_by/decided_at，
//     按全列算会把正常语义算成缺陷；
//   - 应填字段合规率：status 为 expired/revoked 时必须能定位裁决依据，
//     这才是等保与 SIEM 消费方真正关心的。
type perfFill struct {
	rows      int
	status    map[string]int
	decision  int
	reason    int
	decidedBy int
	decidedAt int
}

func perfEvidenceFill(t *testing.T, payload []byte) perfFill {
	t.Helper()
	reader := csv.NewReader(bytes.NewReader(payload))
	records, err := reader.ReadAll()
	if err != nil {
		t.Fatalf("parse evidence csv: %v", err)
	}
	if len(records) < 2 {
		t.Fatalf("evidence csv has %d records, want header + rows", len(records))
	}
	header := records[0]
	index := make(map[string]int, len(header))
	for i, name := range header {
		index[name] = i
	}
	for _, required := range []string{"status", "decision", "reason", "decided_by", "decided_at"} {
		if _, ok := index[required]; !ok {
			t.Fatalf("evidence csv 缺少必需列 %q", required)
		}
	}
	fill := perfFill{status: map[string]int{}}
	for _, record := range records[1:] {
		fill.rows++
		fill.status[record[index["status"]]]++
		if strings.TrimSpace(record[index["decision"]]) != "" {
			fill.decision++
		}
		if strings.TrimSpace(record[index["reason"]]) != "" {
			fill.reason++
		}
		if strings.TrimSpace(record[index["decided_by"]]) != "" {
			fill.decidedBy++
		}
		if strings.TrimSpace(record[index["decided_at"]]) != "" {
			fill.decidedAt++
		}
	}
	return fill
}

// perfSummary 是落盘的结构化实测结果，供报告与后续回归对照。
type perfSummary struct {
	Bindings                   int            `json:"bindings"`
	SeedMillis                 int64          `json:"seed_ms"`
	ActivateFullMillis         int64          `json:"activate_full_ms"`
	ActivatePrivilegedMillis   int64          `json:"activate_privileged_ms"`
	InsertStatements           int64          `json:"insert_statements"`
	SelectStatementsFull       int64          `json:"select_statements_full"`
	SelectStatementsPrivileged int64          `json:"select_statements_privileged"`
	ExportMillis               int64          `json:"export_ms"`
	ExportBytes                int            `json:"export_bytes"`
	ExportRows                 int            `json:"export_rows"`
	StatusCounts               map[string]int `json:"status_counts"`
	FillDecision               int            `json:"fill_non_empty_decision"`
	FillReason                 int            `json:"fill_non_empty_reason"`
	FillDecidedBy              int            `json:"fill_non_empty_decided_by"`
	FillDecidedAt              int            `json:"fill_non_empty_decided_at"`
	BoundaryRejected           bool           `json:"boundary_rejected"`
	DecidedRows                int            `json:"decided_rows"`
	DecidedFillDecision        int            `json:"decided_fill_decision"`
	DecidedFillReason          int            `json:"decided_fill_reason"`
	DecidedFillDecidedBy       int            `json:"decided_fill_decided_by"`
	DecidedFillDecidedAt       int            `json:"decided_fill_decided_at"`
}

// perfWriteArtifacts 把实测结果与证据副本写到 CERT_PERF_ARTIFACT_DIR。
//
// 未设置时不写文件：CI 与本地随机跑不应对工作区产生副作用。
//
//nolint:gosec // G703: 输出目录来自本地测试环境变量 CERT_PERF_ARTIFACT_DIR，非外部输入
func perfWriteArtifacts(t *testing.T, summary perfSummary, evidence []byte) {
	t.Helper()
	dir := strings.TrimSpace(os.Getenv("CERT_PERF_ARTIFACT_DIR"))
	if dir == "" {
		t.Logf("[perf] CERT_PERF_ARTIFACT_DIR 未设置，跳过证据落盘")
		return
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("create artifact dir %s: %v", dir, err)
	}
	encoded, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		t.Fatalf("encode perf summary: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "certification-perf-summary.json"), encoded, 0o600); err != nil {
		t.Fatalf("write perf summary: %v", err)
	}
	// 只落前 20 行样本：完整 CSV 在 1 万行时约 1.4MB，进版本库没有额外核对价值，
	// 行数与字节数已记在 summary 里；样本用于核对列头与列序没有漂移。
	if err := os.WriteFile(filepath.Join(dir, "certification-perf-evidence-sample.csv"),
		perfEvidenceSample(evidence, 20), 0o600); err != nil {
		t.Fatalf("write perf evidence sample: %v", err)
	}
	t.Logf("[perf] artifacts written to %s", dir)
}

// perfDrainOutbox 循环认领并投递全部审计事件，直到无积压。
//
// 场景 5 会产生上百条审计事件，单轮 ClaimBatch(limit=100) 不够；
// 这里按轮消费并断言最终 pending=0、dlq=0，与常驻 worker 的行为一致。
func perfDrainOutbox(t *testing.T, env *certDrillEnv) int {
	t.Helper()
	ctx := context.Background()
	handler := app.AuditEventHandler(env.emitter)
	delivered := 0
	for round := 0; round < 100; round++ {
		events, err := env.store.ClaimBatch(ctx, time.Now().UTC(), time.Minute, 100)
		if err != nil {
			t.Fatalf("ClaimBatch: %v", err)
		}
		if len(events) == 0 {
			break
		}
		for _, event := range events {
			if err := handler(ctx, event); err != nil {
				t.Fatalf("deliver event %s: %v", event.EventID, err)
			}
			if err := env.store.MarkSucceeded(ctx, event.EventID, time.Now().UTC()); err != nil {
				t.Fatalf("MarkSucceeded %s: %v", event.EventID, err)
			}
		}
		delivered += len(events)
	}
	stats, err := env.store.Stats(ctx)
	if err != nil {
		t.Fatalf("outbox Stats: %v", err)
	}
	if stats.Pending != 0 || stats.DLQ != 0 {
		t.Fatalf("outbox not drained: pending=%d dlq=%d", stats.Pending, stats.DLQ)
	}
	return delivered
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// perfEvidenceSample 返回表头 + 前 limit 行，用于版本库内的列结构留证。
func perfEvidenceSample(payload []byte, limit int) []byte {
	lines := bytes.Split(bytes.TrimRight(payload, "\r\n"), []byte("\n"))
	if len(lines) <= limit+1 {
		return payload
	}
	return append(bytes.Join(lines[:limit+1], []byte("\n")), '\n')
}

func perfEnvInt(t *testing.T, key string, fallback int) int {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		t.Fatalf("%s=%q 必须是正整数", key, raw)
	}
	return value
}
