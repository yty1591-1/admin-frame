package catalog_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/admin-frame/admin-frame/internal/iam"
	"github.com/admin-frame/admin-frame/internal/iam/catalog"
)

// fixHint 是所有一致性失败共用的修复指引：四处必须同 MR 修改。
//
// 权限点属于「只追加」数据：新增权限点写进新的 *_seed_*.up.sql（不要改写已应用的
// 种子文件，否则迁移历史表会报 checksum 漂移），并在对应 down 中追加删除语句。
const fixHint = "请同步 internal/iam/catalog/catalog.go、internal/iam/perm.go、" +
	"migrations/*_seed_*.up.sql、docs/iam/PERMISSIONS.md"

// seedFiles 返回排序后的种子迁移文件名（绝对路径）。
func seedFiles(t *testing.T, suffix string) []string {
	t.Helper()
	pattern := filepath.Join(repoRoot(t), "migrations", suffix)
	files, err := filepath.Glob(pattern)
	if err != nil {
		t.Fatalf("枚举种子迁移 %s 失败: %v", suffix, err)
	}
	if len(files) == 0 {
		t.Fatalf("未找到任何种子迁移（%s）", pattern)
	}
	slices.Sort(files)
	return files
}

// seedRow 解析种子迁移中每条权限点行。
//
// 依赖约定：每条权限点独占一行、行首为 "('"，列顺序与 000003 的 INSERT 列表一致。
// 该约定写在迁移文件头，改动列顺序时必须同步本表达式与目录测试。
//
// resource 是生成列（由 code 首段派生），不参与 INSERT，因此本表达式不匹配该列。
var seedRow = regexp.MustCompile(
	`^\s*\('([a-z][a-z0-9_:]*)',\s*'([^']*)',\s*'(admin|biz|both)',\s*([01]),\s*(\d+),`,
)

// docRow 解析 PERMISSIONS.md 目录表。
var docRow = regexp.MustCompile(
	"^\\|\\s*`([a-z][a-z0-9_:]*)`\\s*\\|\\s*(admin|biz|both)\\s*\\|\\s*(是|否)\\s*\\|",
)

// TestPermissionCatalog 校验目录自身：格式、唯一性与排序。
func TestPermissionCatalog(t *testing.T) {
	if err := catalog.Validate(); err != nil {
		t.Fatalf("权限目录非法: %v；%s", err, fixHint)
	}
	if got := len(catalog.All()); got == 0 {
		t.Fatal("权限目录为空")
	}
}

// TestEntryResourceAndCodes 验证权限码首段派生、全量码导出与按码查找。
func TestEntryResourceAndCodes(t *testing.T) {
	entry := catalog.Entry{Code: iam.PermissionCode("iam:role:read")}
	if got := entry.Resource(); got != "iam" {
		t.Fatalf("Resource() = %q, want iam", got)
	}

	entries := catalog.All()
	codes := catalog.Codes()
	if len(codes) != len(entries) {
		t.Fatalf("Codes() 长度 %d != All() 长度 %d", len(codes), len(entries))
	}
	for i := range entries {
		if codes[i] != entries[i].Code {
			t.Fatalf("Codes()[%d] = %q, want %q", i, codes[i], entries[i].Code)
		}
	}

	found, ok := catalog.ByCode(entries[0].Code)
	if !ok || found.Code != entries[0].Code {
		t.Fatalf("ByCode(%q) = %v, %v", entries[0].Code, found.Code, ok)
	}
	if _, ok := catalog.ByCode("missing:code"); ok {
		t.Fatal("ByCode 不应命中不存在的权限码")
	}
}

// TestPermissionCatalogMatchesConstants 保证 perm.go 的常量与目录双向一致。
//
// 用 go/ast 解析源码而不是 shell grep：可精确定位常量声明、忽略注释中的示例，
// 且避免 Windows 下引号与中文编码问题。
func TestPermissionCatalogMatchesConstants(t *testing.T) {
	path := filepath.Join(repoRoot(t), "internal", "iam", "perm.go")
	consts := parsePermissionConstants(t, path)

	inCatalog := make(map[iam.PermissionCode]struct{}, len(consts))
	for _, e := range catalog.All() {
		inCatalog[e.Code] = struct{}{}
	}

	var missingConst, missingEntry []string
	for _, e := range catalog.All() {
		if _, ok := consts[e.Code]; !ok {
			missingConst = append(missingConst, string(e.Code))
		}
	}
	for code, name := range consts {
		if _, ok := inCatalog[code]; !ok {
			missingEntry = append(missingEntry, name+"="+string(code))
		}
	}
	slices.Sort(missingConst)
	slices.Sort(missingEntry)

	if len(missingConst) > 0 {
		t.Fatalf("目录中存在但 perm.go 缺少常量: %v；%s", missingConst, fixHint)
	}
	if len(missingEntry) > 0 {
		t.Fatalf("perm.go 中存在常量但目录缺少条目: %v；%s", missingEntry, fixHint)
	}
	if len(consts) != len(catalog.All()) {
		t.Fatalf("常量数 %d 与目录条目数 %d 不一致；%s", len(consts), len(catalog.All()), fixHint)
	}
}

// TestPermissionCatalogMatchesSeedMigration 保证全部种子迁移与目录元数据一致。
//
// 遍历 migrations/*_seed_*.up.sql：权限点可以分散在多个追加式种子文件中，
// 但同一权限码只能出现一次，且元数据必须与目录逐字段一致。
func TestPermissionCatalogMatchesSeedMigration(t *testing.T) {
	byCode := make(map[iam.PermissionCode]catalog.Entry, len(catalog.All()))
	for _, e := range catalog.All() {
		byCode[e.Code] = e
	}

	seen := make(map[iam.PermissionCode]string)
	for _, path := range seedFiles(t, "*_seed_*.up.sql") {
		name := filepath.Base(path)
		data, err := os.ReadFile(path) //nolint:gosec // G304：path 由 runtime.Caller 定位仓库内固定文件，非用户输入。
		if err != nil {
			t.Fatalf("读取种子迁移 %s 失败: %v", name, err)
		}
		for i, line := range strings.Split(string(data), "\n") {
			m := seedRow.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			code := iam.PermissionCode(m[1])
			if first, dup := seen[code]; dup {
				t.Fatalf("%s:%d 种子权限码 %q 已在 %s 定义；%s", name, i+1, code, first, fixHint)
			}
			seen[code] = name

			e, ok := byCode[code]
			if !ok {
				t.Fatalf("%s:%d 种子包含目录中不存在的权限码 %q；%s", name, i+1, code, fixHint)
			}
			if m[2] != e.Name {
				t.Fatalf("%s:%d %q 的 name=%q，目录为 %q；%s", name, i+1, code, m[2], e.Name, fixHint)
			}
			if catalog.Channel(m[3]) != e.Channel {
				t.Fatalf("%s:%d %q 的 channel=%q，目录为 %q；%s", name, i+1, code, m[3], e.Channel, fixHint)
			}
			wantBearing := "0"
			if e.DataBearing {
				wantBearing = "1"
			}
			if m[4] != wantBearing {
				t.Fatalf("%s:%d %q 的 data_bearing=%s，目录为 %s；%s", name, i+1, code, m[4], wantBearing, fixHint)
			}
			if m[5] != strconv.Itoa(e.Sort) {
				t.Fatalf("%s:%d %q 的 sort=%s，目录为 %d；%s", name, i+1, code, m[5], e.Sort, fixHint)
			}
		}
	}

	var missing []string
	for _, e := range catalog.All() {
		if _, ok := seen[e.Code]; !ok {
			missing = append(missing, string(e.Code))
		}
	}
	slices.Sort(missing)
	if len(missing) > 0 {
		t.Fatalf("种子迁移缺少权限码: %v；%s", missing, fixHint)
	}
}

// TestPermissionCatalogMatchesDoc 保证 PERMISSIONS.md 目录表与目录一致。
func TestPermissionCatalogMatchesDoc(t *testing.T) {
	path := filepath.Join(repoRoot(t), "docs", "iam", "PERMISSIONS.md")
	data, err := os.ReadFile(path) //nolint:gosec // G304：path 由 runtime.Caller 定位仓库内固定文件，非用户输入。
	if err != nil {
		t.Fatalf("读取权限目录文档失败: %v", err)
	}

	byCode := make(map[iam.PermissionCode]catalog.Entry, len(catalog.All()))
	for _, e := range catalog.All() {
		byCode[e.Code] = e
	}

	seen := make(map[iam.PermissionCode]struct{})
	for i, line := range strings.Split(string(data), "\n") {
		m := docRow.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		code := iam.PermissionCode(m[1])
		if _, dup := seen[code]; dup {
			t.Fatalf("%s:%d 文档权限码 %q 重复；%s", filepath.Base(path), i+1, code, fixHint)
		}
		seen[code] = struct{}{}

		e, ok := byCode[code]
		if !ok {
			t.Fatalf("%s:%d 文档包含目录中不存在的权限码 %q；%s", filepath.Base(path), i+1, code, fixHint)
		}
		if catalog.Channel(m[2]) != e.Channel {
			t.Fatalf("%s:%d %q 的 channel=%q，目录为 %q；%s", filepath.Base(path), i+1, code, m[2], e.Channel, fixHint)
		}
		want := "否"
		if e.DataBearing {
			want = "是"
		}
		if m[3] != want {
			t.Fatalf("%s:%d %q 的 data_bearing=%q，目录为 %q；%s", filepath.Base(path), i+1, code, m[3], want, fixHint)
		}
	}

	var missing []string
	for _, e := range catalog.All() {
		if _, ok := seen[e.Code]; !ok {
			missing = append(missing, string(e.Code))
		}
	}
	slices.Sort(missing)
	if len(missing) > 0 {
		t.Fatalf("PERMISSIONS.md 缺少权限码: %v；%s", missing, fixHint)
	}
}

// TestPermissionCatalogDownMigration 保证全部种子回滚覆盖对应权限码。
func TestPermissionCatalogDownMigration(t *testing.T) {
	quoted := regexp.MustCompile(`'([a-z][a-z0-9_:]*)'`)
	present := make(map[string]struct{})
	for _, path := range seedFiles(t, "*_seed_*.down.sql") {
		data, err := os.ReadFile(path) //nolint:gosec // G304：path 由 runtime.Caller 定位仓库内固定文件，非用户输入。
		if err != nil {
			t.Fatalf("读取种子回滚迁移 %s 失败: %v", filepath.Base(path), err)
		}
		for _, m := range quoted.FindAllStringSubmatch(string(data), -1) {
			present[m[1]] = struct{}{}
		}
	}

	var missing []string
	for _, e := range catalog.All() {
		if _, ok := present[string(e.Code)]; !ok {
			missing = append(missing, string(e.Code))
		}
	}
	slices.Sort(missing)
	if len(missing) > 0 {
		t.Fatalf("种子回滚迁移缺少权限码: %v；新增权限点时必须同步 down；%s", missing, fixHint)
	}
}

// TestPermissionCatalogSeedIsIdempotent 保证种子迁移可重放而不报错。
func TestPermissionCatalogSeedIsIdempotent(t *testing.T) {
	for _, path := range seedFiles(t, "*_seed_*.up.sql") {
		data, err := os.ReadFile(path) //nolint:gosec // G304：path 由 runtime.Caller 定位仓库内固定文件，非用户输入。
		if err != nil {
			t.Fatalf("读取种子迁移 %s 失败: %v", filepath.Base(path), err)
		}
		if !strings.Contains(string(data), "ON DUPLICATE KEY UPDATE") {
			t.Fatalf("%s 必须是幂等写入（INSERT ... ON DUPLICATE KEY UPDATE），否则重放会因主键冲突失败；%s",
				filepath.Base(path), fixHint)
		}
	}
}

// parsePermissionConstants 从 perm.go 提取 "Perm*" 常量，返回 code → 常量名。
func parsePermissionConstants(t *testing.T, path string) map[iam.PermissionCode]string {
	t.Helper()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("解析 %s 失败: %v", path, err)
	}

	out := make(map[iam.PermissionCode]string)
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, name := range value.Names {
				if !strings.HasPrefix(name.Name, "Perm") || i >= len(value.Values) {
					continue
				}
				lit, ok := value.Values[i].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					t.Fatalf("%s: 常量 %s 必须是字符串字面量（权限码契约不得运行时拼接）", path, name.Name)
				}
				code, err := strconv.Unquote(lit.Value)
				if err != nil {
					t.Fatalf("%s: 常量 %s 的值无法解析: %v", path, name.Name, err)
				}
				if exists, dup := out[iam.PermissionCode(code)]; dup {
					t.Fatalf("%s: 常量 %s 与 %s 使用同一权限码 %q", path, name.Name, exists, code)
				}
				out[iam.PermissionCode(code)] = name.Name
			}
		}
	}
	return out
}

// repoRoot 返回仓库根目录（测试文件位于 internal/iam/catalog）。
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("无法定位测试文件路径，不能执行跨文件一致性校验")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
}
