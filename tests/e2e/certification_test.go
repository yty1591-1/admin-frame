package e2e_test

// P1-7 签收条件 2：责任人经 A 端真实入口完成一轮裁决。
//
// 与 tests/integration/iam 的差别：那里直接调用例层，这里跑**真实二进制**，
// 请求经 HTTP → 认证中间件 → 权限点校验（iam:certification:read/write/decide）
// → handler → 用例 → MySQL。验证的是「运维照手册点，能不能真的点完一轮」：
// 建活动 → 激活 → 取清单 → 裁决 → 导出证据 → 关闭活动。
//
// 造数也走 A 端 API（角色、绑定），不绕过权限点——否则测不出权限点是否
// 漏授给管理员。本用例是 §13.3 第 1 条「责任人确认」缺口的补齐证据。

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/pkg/db"
)

func TestCertificationReviewOnAdminEntry(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv(envDSN))
	redisAddr := strings.TrimSpace(os.Getenv(envRedis))
	if dsn == "" || redisAddr == "" {
		t.Skipf("%s / %s not set: e2e skipped (nightly job sets them; see make check-e2e)", envDSN, envRedis)
	}
	normalized, err := db.ForceMySQLCollation("mysql", dsn, db.MySQLDefaultCollation)
	if err != nil {
		t.Fatalf("normalize dsn: %v", err)
	}
	migrateDatabase(t, normalized)
	e2eResetCertificationTables(t, normalized)
	t.Setenv(envCallbackSecret, strings.Repeat("e2e-callback-secret-", 3))

	binDir := buildBinaries(t)
	admin := startService(t, filepath.Join(binDir, exeName("admin-api")), adminConfig(t, normalized, redisAddr))
	probeOK(t, admin)
	token := loginAdmin(t, admin.baseURL)

	suffix := fmt.Sprintf("%d", time.Now().UnixNano()%1_000_000)

	// ① 造一条生效绑定：角色与绑定都走 A 端 API。
	roleID := e2eCertCreateRole(t, admin.baseURL, token, suffix)
	bindingID := e2eCertCreateBinding(t, admin.baseURL, token, roleID, suffix)

	// ② 建活动并激活：范围限定到本用例的角色，避免覆盖端内全部角色。
	status, body := doJSON(t, http.MethodPost, admin.baseURL+"/admin/v1/iam/certifications", token, map[string]any{
		"channel": "admin",
		"name":    "E2E 季度复核 " + suffix,
		"role_ids": []string{
			roleID,
		},
		"due_at": time.Now().UTC().Add(48 * time.Hour).Format(time.RFC3339),
		"reason": "P1-7 签收条件 2：责任人经 A 端真实入口裁决",
	})
	if status != http.StatusOK || codeOf(body) != 0 {
		t.Fatalf("create campaign: status=%d body=%v", status, body)
	}
	campaignID := dataID(t, body)
	if campaignID == "" {
		t.Fatalf("create campaign returned empty id: %v", body)
	}

	status, body = doJSON(t, http.MethodPost,
		admin.baseURL+"/admin/v1/iam/certifications/"+campaignID+"/activate", token, nil)
	if status != http.StatusOK || codeOf(body) != 0 {
		t.Fatalf("activate campaign: status=%d body=%v", status, body)
	}
	if got := campaignTaskCount(t, body); got != 1 {
		t.Fatalf("activated task_count=%d, want 1（活动范围应只覆盖本用例的那条绑定）", got)
	}

	// ③ 责任人视角取清单：必须能看到自己待处理的任务。
	taskID, taskStatus, reviewerID := e2eCertListSingleTask(t, admin.baseURL, token, campaignID)
	if taskStatus != "pending" {
		t.Fatalf("task status=%q, want pending", taskStatus)
	}
	if reviewerID == "" {
		t.Fatalf("task reviewer_id 为空：责任人映射未落到任务上（清单无法分派到人）")
	}

	// ④ 裁决：确认保留，原因由领域层强制。
	status, body = doJSON(t, http.MethodPost,
		admin.baseURL+"/admin/v1/iam/certifications/"+campaignID+"/tasks/"+taskID+"/decision", token,
		map[string]any{
			"decision": "confirmed",
			"reason":   "E2E：该绑定在复核期内仍为岗位所需",
		})
	if status != http.StatusOK || codeOf(body) != 0 {
		t.Fatalf("decide task: status=%d body=%v", status, body)
	}
	data, _ := body["data"].(map[string]any)
	if got, _ := data["status"].(string); got != "confirmed" {
		t.Fatalf("decided task status=%q, want confirmed (body=%v)", got, body)
	}
	if decidedBy, _ := data["decided_by"].(string); decidedBy == "" {
		t.Fatalf("decided_by 为空：证据无法追到裁决人（body=%v）", body)
	}

	// ⑤ 重复裁决必须被状态机拒绝：已裁决任务不可二次裁决。
	status, body = doJSON(t, http.MethodPost,
		admin.baseURL+"/admin/v1/iam/certifications/"+campaignID+"/tasks/"+taskID+"/decision", token,
		map[string]any{"decision": "revoked", "reason": "E2E：重复裁决应被拒绝"})
	if status == http.StatusOK && codeOf(body) == 0 {
		t.Fatalf("已裁决任务被二次裁决成功：状态机未拦截（body=%v）", body)
	}

	// ⑥ 证据导出：CSV 必须含表头 + 1 行，且该行是 confirmed 并带裁决依据。
	rows, header := e2eCertExportCSV(t, admin.baseURL, token, campaignID, "csv")
	if len(rows) != 2 {
		t.Fatalf("export rows=%d（含表头）, want 2", len(rows))
	}
	index := e2eCertHeaderIndex(t, header)
	record := rows[1]
	if got := record[index["binding_id"]]; got != bindingID {
		t.Errorf("export binding_id=%q, want %q", got, bindingID)
	}
	if got := record[index["status"]]; got != "confirmed" {
		t.Errorf("export status=%q, want confirmed", got)
	}
	for _, column := range []string{"decision", "reason", "decided_by", "decided_at"} {
		if strings.TrimSpace(record[index[column]]) == "" {
			t.Errorf("已裁决任务的导出列 %q 为空（证据完整率要求 100%%）", column)
		}
	}

	// ⑦ 关闭活动：状态机收口，清单不再可裁决。
	status, body = doJSON(t, http.MethodPost,
		admin.baseURL+"/admin/v1/iam/certifications/"+campaignID+"/close", token,
		map[string]any{"reason": "E2E：本轮复核完成"})
	if status != http.StatusOK || codeOf(body) != 0 {
		t.Fatalf("close campaign: status=%d body=%v", status, body)
	}
	data, _ = body["data"].(map[string]any)
	if got, _ := data["status"].(string); got != "closed" {
		t.Fatalf("campaign status=%q, want closed (body=%v)", got, body)
	}
}

// e2eCertCreateRole 经 A 端 API 建一个平台作用域的复核用角色。
func e2eCertCreateRole(t *testing.T, baseURL, token, suffix string) string {
	t.Helper()
	status, body := doJSON(t, http.MethodPost, baseURL+"/admin/v1/iam/roles", token, map[string]any{
		"channel":     "admin",
		"scope_type":  "platform",
		"code":        "e2e_cert_" + suffix,
		"name":        "E2E 复核角色 " + suffix,
		"permissions": []string{"iam:role:read"},
		"reason":      "P1-7 签收条件 2 造数",
	})
	if status != http.StatusOK || codeOf(body) != 0 {
		t.Fatalf("create role: status=%d body=%v", status, body)
	}
	roleID := dataID(t, body)
	if roleID == "" {
		t.Fatalf("create role returned empty id: %v", body)
	}
	return roleID
}

// e2eCertCreateBinding 经 A 端 API 把角色授予一个员工主体。
func e2eCertCreateBinding(t *testing.T, baseURL, token, roleID, suffix string) string {
	t.Helper()
	status, body := doJSON(t, http.MethodPost, baseURL+"/admin/v1/iam/bindings", token, map[string]any{
		"subject_type": "staff",
		"subject_id":   "e2e-cert-staff-" + suffix,
		"role_id":      roleID,
		"reason":       "P1-7 签收条件 2 造数",
	})
	if status != http.StatusOK || codeOf(body) != 0 {
		t.Fatalf("create binding: status=%d body=%v", status, body)
	}
	bindingID := dataID(t, body)
	if bindingID == "" {
		t.Fatalf("create binding returned empty id: %v", body)
	}
	return bindingID
}

// campaignTaskCount 从活动 DTO 里取 task_count。
func campaignTaskCount(t *testing.T, body map[string]any) int {
	t.Helper()
	data, _ := body["data"].(map[string]any)
	if data == nil {
		t.Fatalf("campaign body missing data: %v", body)
	}
	raw, ok := data["task_count"].(float64)
	if !ok {
		t.Fatalf("task_count missing or not a number: %v", data["task_count"])
	}
	return int(raw)
}

// e2eCertListSingleTask 取清单里唯一的任务，返回 id / status / reviewer_id。
func e2eCertListSingleTask(t *testing.T, baseURL, token, campaignID string) (id, status, reviewerID string) {
	t.Helper()
	query := url.Values{}
	query.Set("page_size", "10")
	code, body := doJSON(t, http.MethodGet,
		baseURL+"/admin/v1/iam/certifications/"+campaignID+"/tasks?"+query.Encode(), token, nil)
	if code != http.StatusOK || codeOf(body) != 0 {
		t.Fatalf("list tasks: status=%d body=%v", code, body)
	}
	data, _ := body["data"].(map[string]any)
	items, _ := data["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("task items=%d, want 1（body=%v）", len(items), body)
	}
	item, _ := items[0].(map[string]any)
	id, _ = item["id"].(string)
	status, _ = item["status"].(string)
	reviewerID, _ = item["reviewer_id"].(string)
	if id == "" {
		t.Fatalf("task id missing: %v", item)
	}
	return id, status, reviewerID
}

// e2eCertExportCSV 下载证据 CSV，返回数据行与表头。
func e2eCertExportCSV(t *testing.T, baseURL, token, campaignID, format string) (records [][]string, header []string) {
	t.Helper()
	endpoint := baseURL + "/admin/v1/iam/certifications/" + campaignID + "/export?format=" + url.QueryEscape(format)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, endpoint, http.NoBody)
	if err != nil {
		t.Fatalf("build export request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("export request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		payload, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		t.Fatalf("export status=%d body=%s", resp.StatusCode, payload)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
		t.Fatalf("export content-type=%q, want text/csv", ct)
	}
	parsed, err := csv.NewReader(resp.Body).ReadAll()
	if err != nil {
		t.Fatalf("parse export csv: %v", err)
	}
	if len(parsed) < 2 {
		t.Fatalf("export records=%d, want header + rows", len(parsed))
	}
	return parsed, parsed[0]
}

// e2eResetCertificationTables 清空复核相关的两张表，保证用例可重复运行。
//
// 为什么要清：扫描是**全局**的（按 active 活动分页扫），上一次运行失败留下的
// 超期活动会混进本轮日志计数，让「本活动处理了 1 条」这类断言失真。
// 只清复核表，不动角色与绑定——它们的唯一性由用例内的 suffix 保证。
func e2eResetCertificationTables(t *testing.T, dsn string) {
	t.Helper()
	conn, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("open db for reset: %v", err)
	}
	defer func() { _ = conn.Close() }()
	// 外键 RESTRICT：任务先于活动删除。
	for _, stmt := range []string{
		"DELETE FROM iam_certification_task",
		"DELETE FROM iam_certification_campaign",
	} {
		if _, err := conn.ExecContext(context.Background(), stmt); err != nil {
			t.Fatalf("reset %q: %v", stmt, err)
		}
	}
}

// e2eCertHeaderIndex 建立列名到下标的映射，并断言证据列齐备。
func e2eCertHeaderIndex(t *testing.T, header []string) map[string]int {
	t.Helper()
	index := make(map[string]int, len(header))
	for i, name := range header {
		index[name] = i
	}
	for _, required := range []string{
		"campaign_id", "task_id", "binding_id", "status", "decision", "reason", "decided_by", "decided_at",
	} {
		if _, ok := index[required]; !ok {
			t.Fatalf("证据 CSV 缺少必需列 %q（header=%v）", required, header)
		}
	}
	return index
}

// TestCertificationQuarterlyDrillEndToEnd 一次完整季度复核演练：
//
//	清单生成 → 责任人确认 → 超期回收 → 证据导出 → 活动关闭
//
// 与前一个用例的差别：这里把「人工裁决」与「超期扫描」放进同一个活动，
// 且超期扫描由**真实 worker 进程**（`-certification-sweep`，与 CronJob 同一入口）
// 执行，不是调用例层。对应 §13.3 第 1 条要的四段链路。
//
// 只在同一活动里同时存在 confirmed 与 expired 任务，才能验证证据导出把两种
// 终态都带上——分开测会漏掉「同一活动内混合状态」的导出语义。
func TestCertificationQuarterlyDrillEndToEnd(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv(envDSN))
	redisAddr := strings.TrimSpace(os.Getenv(envRedis))
	if dsn == "" || redisAddr == "" {
		t.Skipf("%s / %s not set: e2e skipped (nightly job sets them; see make check-e2e)", envDSN, envRedis)
	}
	normalized, err := db.ForceMySQLCollation("mysql", dsn, db.MySQLDefaultCollation)
	if err != nil {
		t.Fatalf("normalize dsn: %v", err)
	}
	migrateDatabase(t, normalized)
	e2eResetCertificationTables(t, normalized)
	t.Setenv(envCallbackSecret, strings.Repeat("e2e-callback-secret-", 3))

	binDir := buildBinaries(t)
	admin := startService(t, filepath.Join(binDir, exeName("admin-api")), adminConfig(t, normalized, redisAddr))
	probeOK(t, admin)
	token := loginAdmin(t, admin.baseURL)

	suffix := fmt.Sprintf("%d", time.Now().UnixNano()%1_000_000)

	// ① 清单来源：一个角色 + 两条生效绑定，活动激活后应生成 2 条任务。
	roleID := e2eCertCreateRole(t, admin.baseURL, token, suffix)
	e2eCertCreateBinding(t, admin.baseURL, token, roleID, suffix+"-a")
	e2eCertCreateBinding(t, admin.baseURL, token, roleID, suffix+"-b")

	// 截止时间留出「激活 + 裁决」的窗口，之后等它自然过期。
	dueAt := time.Now().UTC().Add(8 * time.Second)
	status, body := doJSON(t, http.MethodPost, admin.baseURL+"/admin/v1/iam/certifications", token, map[string]any{
		"channel":  "admin",
		"name":     "E2E 季度复核演练 " + suffix,
		"role_ids": []string{roleID},
		"due_at":   dueAt.Format(time.RFC3339),
		"reason":   "P1-7 签收条件：一次完整季度复核演练",
	})
	if status != http.StatusOK || codeOf(body) != 0 {
		t.Fatalf("create campaign: status=%d body=%v", status, body)
	}
	campaignID := dataID(t, body)

	status, body = doJSON(t, http.MethodPost,
		admin.baseURL+"/admin/v1/iam/certifications/"+campaignID+"/activate", token, nil)
	if status != http.StatusOK || codeOf(body) != 0 {
		t.Fatalf("activate campaign: status=%d body=%v", status, body)
	}
	tasks := e2eCertListTasks(t, admin.baseURL, token, campaignID)
	if len(tasks) != 2 {
		t.Fatalf("tasks=%d, want 2", len(tasks))
	}

	// ② 责任人确认：裁决其中一条，另一条保持待处理等超期扫描。
	confirmedID := tasks[0]
	status, body = doJSON(t, http.MethodPost,
		admin.baseURL+"/admin/v1/iam/certifications/"+campaignID+"/tasks/"+confirmedID+"/decision", token,
		map[string]any{"decision": "confirmed", "reason": "E2E：该绑定在复核期内仍为岗位所需"})
	if status != http.StatusOK || codeOf(body) != 0 {
		t.Fatalf("decide task: status=%d body=%v", status, body)
	}

	// ③ 超期回收：等截止时间过去，再用与 CronJob 相同的入口跑一次扫描。
	if wait := time.Until(dueAt.Add(time.Second)); wait > 0 {
		time.Sleep(wait)
	}
	logs, exitCode := e2eRunCertificationSweep(t, filepath.Join(binDir, exeName("worker")), normalized, redisAddr)
	if exitCode != 0 {
		t.Fatalf("worker -certification-sweep exit=%d\nlogs:\n%s", exitCode, logs)
	}
	if !strings.Contains(logs, "certification sweep finished") {
		t.Fatalf("扫描未执行到完成日志（可能 iam.certification 被关闭）:\n%s", logs)
	}
	// 活动里只有 1 条待处理任务，扫描应恰好处理 1 条。
	if !strings.Contains(logs, `"tasks":1`) {
		t.Errorf("扫描处理的超期任务数不是 1（应只处理保持 pending 的那条）:\n%s", logs)
	}

	// ④ 复核后的清单：一条确认、一条过期，两条终态并存。
	statuses := e2eCertTaskStatuses(t, admin.baseURL, token, campaignID)
	if got := statuses[confirmedID]; got != "confirmed" {
		t.Errorf("已裁决任务 status=%q, want confirmed", got)
	}
	expired := 0
	for id, st := range statuses {
		if id == confirmedID {
			continue
		}
		if st == "expired" {
			expired++
		}
	}
	if expired != 1 {
		t.Fatalf("超期任务数=%d, want 1（statuses=%v）", expired, statuses)
	}

	// ⑤ 证据导出：同一活动内两种终态都要带上，且各自字段完整。
	records, header := e2eCertExportCSV(t, admin.baseURL, token, campaignID, "csv")
	index := e2eCertHeaderIndex(t, header)
	if len(records) != 3 {
		t.Fatalf("export rows=%d（含表头）, want 3", len(records))
	}
	byStatus := map[string]map[string]string{}
	for _, record := range records[1:] {
		byStatus[record[index["status"]]] = map[string]string{
			"decision":   record[index["decision"]],
			"reason":     record[index["reason"]],
			"decided_by": record[index["decided_by"]],
			"decided_at": record[index["decided_at"]],
		}
	}
	for _, want := range []string{"confirmed", "expired"} {
		row, ok := byStatus[want]
		if !ok {
			t.Fatalf("导出缺少 status=%s 的行（got=%v）", want, byStatus)
		}
		if row["decided_at"] == "" {
			t.Errorf("status=%s 的 decided_at 为空：终态任务必须有时间戳", want)
		}
	}
	// confirmed 行必须能追到裁决人；expired 行的 decided_by 本就为空（自动动作只写审计）。
	if byStatus["confirmed"]["decided_by"] == "" {
		t.Error("confirmed 行的 decided_by 为空")
	}
	if byStatus["confirmed"]["decision"] != "confirmed" {
		t.Errorf("confirmed 行 decision=%q", byStatus["confirmed"]["decision"])
	}

	// ⑥ 关闭活动，本轮复核收口。
	status, body = doJSON(t, http.MethodPost,
		admin.baseURL+"/admin/v1/iam/certifications/"+campaignID+"/close", token,
		map[string]any{"reason": "E2E：本轮复核完成"})
	if status != http.StatusOK || codeOf(body) != 0 {
		t.Fatalf("close campaign: status=%d body=%v", status, body)
	}
	if statuses := e2eCertTaskStatuses(t, admin.baseURL, token, campaignID); len(statuses) != 2 {
		t.Errorf("关闭后任务数=%d, want 2（关闭不应删除任务）", len(statuses))
	}
}

// e2eCertListTasks 返回活动下的任务 ID 列表（按接口返回顺序）。
func e2eCertListTasks(t *testing.T, baseURL, token, campaignID string) []string {
	t.Helper()
	ids := e2eCertTaskStatuses(t, baseURL, token, campaignID)
	ordered := make([]string, 0, len(ids))
	// taskStatuses 是 map，顺序不保证；这里按 ID 升序，与列表接口的默认排序一致。
	for id := range ids {
		ordered = append(ordered, id)
	}
	sortStrings(ordered)
	return ordered
}

// e2eCertTaskStatuses 返回 taskID → status。
func e2eCertTaskStatuses(t *testing.T, baseURL, token, campaignID string) map[string]string {
	t.Helper()
	query := url.Values{}
	query.Set("page_size", "50")
	code, body := doJSON(t, http.MethodGet,
		baseURL+"/admin/v1/iam/certifications/"+campaignID+"/tasks?"+query.Encode(), token, nil)
	if code != http.StatusOK || codeOf(body) != 0 {
		t.Fatalf("list tasks: status=%d body=%v", code, body)
	}
	data, _ := body["data"].(map[string]any)
	items, _ := data["items"].([]any)
	out := make(map[string]string, len(items))
	for _, raw := range items {
		item, _ := raw.(map[string]any)
		id, _ := item["id"].(string)
		status, _ := item["status"].(string)
		if id != "" {
			out[id] = status
		}
	}
	return out
}

// e2eRunCertificationSweep 以一次性模式启动 worker 并等它退出，返回日志与退出码。
//
// 与 CronJob 完全同一入口（`worker -certification-sweep`）：CronJob 只是把这条命令
// 放进 Pod 里按 schedule 触发，所以这里跑通即代表入口本身可用。
func e2eRunCertificationSweep(t *testing.T, bin, dsn, redisAddr string) (output string, exitCode int) {
	t.Helper()
	cfgPath := filepath.Join(t.TempDir(), "worker.yaml")
	if err := os.WriteFile(cfgPath, []byte(workerConfig(t, dsn, redisAddr)), 0o600); err != nil {
		t.Fatalf("write worker config: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	var logs bytes.Buffer
	//nolint:gosec // G204: bin 是构建产物路径，cfgPath 是测试临时配置，均非用户输入。
	cmd := exec.CommandContext(ctx, bin, "-config", cfgPath, "-certification-sweep")
	cmd.Dir = repoRoot(t)
	cmd.Stdout = &logs
	cmd.Stderr = &logs
	err := cmd.Run()
	if ctx.Err() != nil {
		t.Fatalf("worker sweep timed out:\n%s", logs.String())
	}
	if err == nil {
		return logs.String(), 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return logs.String(), exitErr.ExitCode()
	}
	t.Fatalf("run worker sweep: %v\nlogs:\n%s", err, logs.String())
	return "", -1
}

// workerConfig 生成一次性扫描进程的配置：与常驻 worker 同形，但不启动 Relay。
func workerConfig(t *testing.T, dsn, redisAddr string) string {
	t.Helper()
	return fmt.Sprintf(`env: test
service: worker
channel: worker
addr: "127.0.0.1:%d"
shutdown_timeout: 5s
log:
  level: info
  format: json
async:
  enabled: true
db:
  driver: mysql
  dsn: "%s"
%s
iam:
  mode: off
  cache:
    driver: memory
identity:
  enabled: false
id:
  node_id: 12
`, freePort(t), dsn, redisBlock(redisAddr))
}

// sortStrings 是插入排序；任务数很少，避免为一处排序引入额外依赖。
func sortStrings(items []string) {
	for i := 1; i < len(items); i++ {
		for j := i; j > 0 && items[j] < items[j-1]; j-- {
			items[j], items[j-1] = items[j-1], items[j]
		}
	}
}
