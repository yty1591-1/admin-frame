package catalog_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/admin-frame/admin-frame/internal/iam"
	"github.com/admin-frame/admin-frame/internal/iam/catalog"
)

// menuSeedRow 解析 000003 基线种子中的每条菜单 VALUES 行。
//
// 只捕获一致性校验需要的列：channel / code / parent_code / node_type /
// route_name / permission_code。列顺序与迁移文件保持一致。
var menuSeedRow = regexp.MustCompile(
	`\(\d{19},\s*'(admin|biz)',\s*'([^']+)',\s*(NULL|'[^']*'),\s*'(catalog|menu|iframe|link)',` +
		`\s*'([^']+)',\s*'[^']*',\s*'[^']*',\s*'[^']*',\s*'[^']*',\s*'[^']*',\s*'[^']*',` +
		`\s*(NULL|'[^']*'),\s*\d+`,
)

// menuSeedFile 返回菜单种子迁移的绝对路径。
func menuSeedFile(t *testing.T) string {
	t.Helper()
	return filepath.Join(repoRoot(t), "migrations", "000003_seed_iam_baseline.up.sql")
}

// TestMenuSeedConsistency 校验菜单种子与权限目录、自身唯一性及父子关系一致。
//
// 菜单只由迁移种子维护，接口不开放写入，因此这里承担「菜单即代码」的门禁。
func TestMenuSeedConsistency(t *testing.T) {
	raw, err := os.ReadFile(menuSeedFile(t))
	if err != nil {
		t.Fatalf("读取菜单种子失败: %v", err)
	}
	matches := menuSeedRow.FindAllStringSubmatch(string(raw), -1)
	if len(matches) == 0 {
		t.Fatal("菜单种子为空，至少需要一条菜单")
	}

	type kindKey struct{ channel, code string }
	byCode := make(map[kindKey]string, len(matches))
	byRoute := make(map[kindKey]string, len(matches))
	for _, match := range matches {
		channel := match[1]
		code := match[2]
		nodeType := match[4]
		routeName := match[5]
		permission := trimSQLString(match[6])

		key := kindKey{channel: channel, code: code}
		if first, ok := byCode[key]; ok {
			t.Fatalf("菜单 %s/%s 重复（已存在于 %s）", channel, code, first)
		}
		byCode[key] = nodeType
		routeKey := kindKey{channel: channel, code: routeName}
		if first, ok := byRoute[routeKey]; ok {
			t.Fatalf("路由 name %s/%s 重复（已存在于 %s）", channel, routeName, first)
		}
		byRoute[routeKey] = code
		if permission != "" {
			if _, ok := catalog.ByCode(iam.PermissionCode(permission)); !ok {
				t.Fatalf("菜单 %s/%s 的 permission_code=%q 不在权限目录中", channel, code, permission)
			}
		}
	}

	for _, match := range matches {
		parent := trimSQLString(match[3])
		if parent == "" {
			continue
		}
		channel := match[1]
		code := match[2]
		if parent == code {
			t.Fatalf("菜单 %s/%s 不能以自身为父节点", channel, code)
		}
		if _, ok := byCode[kindKey{channel: channel, code: parent}]; !ok {
			t.Fatalf("菜单 %s/%s 的父节点 %s 不存在", channel, code, parent)
		}
	}
}

// trimSQLString 去掉 SQL 字面量的引号；NULL 归一为空串。
func trimSQLString(value string) string {
	if value == "NULL" {
		return ""
	}
	return strings.Trim(value, "'")
}
