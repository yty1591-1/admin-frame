// Package contract 是 OpenAPI 契约与实现的漂移检测（ENT-16）。
//
// 原理：全部业务路由以 `mux.Handle("METHOD /path", ...)` 字面量注册
// （含 cmd/* 装配层），因此可对 Go 源码做静态扫描得到「实现路由集」；
// 对三份 OpenAPI 规范解析顶层 paths 键得到「契约路由集」。双向比对：
//
//   - 实现有、契约没有 → 泄露面（未登记的公开端点）
//   - 契约有、实现没有 → 死契约（文档承诺了不存在的端点）
//
// 该测试无数据库依赖、纯静态，随 go test ./... 在每次 CI 全量执行——
// oasdiff 负责版本间 breaking 检测（check-openapi.sh），本测试负责
// 实现 ↔ 契约的实时一致性。
package contract_test

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// routeExpr 匹配 mux.Handle / mux.HandleFunc 的字面量注册
// （如 mux.Handle("GET /admin/v1/iam/roles", ...)）。
var routeExpr = regexp.MustCompile(`mux\.(?:Handle|HandleFunc)\(\s*"(GET|POST|PUT|PATCH|DELETE) (/[^"]+)"`)

// specFiles 是三端契约文件（与 check-openapi.sh 的 SPECS 对齐）。
var specFiles = []string{
	"api/openapi/admin.yaml",
	"api/openapi/biz.yaml",
	"api/openapi/c.yaml",
}

// scanRoots 是路由注册的扫描范围（internal 传输层 + cmd 装配层）。
var scanRoots = []string{"internal", "cmd"}

// exemptRoutes 是合理豁免（不进入契约的路由）：
//   - /metrics：Prometheus 抓取端点（运维面，非业务 API）；
//   - POST /v1/notification-callbacks/receipt：供应商服务器到服务器回调
//     （ENT-10 NT-07），三件套约定见 RELIABILITY_STANDARDS §5.1，接入文档
//     为 runbooks/notification-delivery.md——非客户端 API 面。
var exemptRoutes = map[string]string{
	"GET /metrics": "Prometheus 抓取端点（运维面）",
	"POST /v1/notification-callbacks/receipt": "供应商回调（RELIABILITY_STANDARDS §5.1）",
}

// TestImplementedRoutesMatchOpenAPI 双向比对实现路由与契约路径。
func TestImplementedRoutesMatchOpenAPI(t *testing.T) {
	implemented, err := scanRoutes("../../")
	if err != nil {
		t.Fatalf("scan routes: %v", err)
	}
	if len(implemented) < 50 {
		t.Fatalf("suspiciously few routes scanned (%d): scanner is broken", len(implemented))
	}

	specRoutes, err := parseSpecPaths("../../")
	if err != nil {
		t.Fatalf("parse specs: %v", err)
	}

	var missingInSpec, deadInSpec []string
	for route := range implemented {
		if _, exempt := exemptRoutes[route]; exempt {
			continue
		}
		if _, ok := specRoutes[route]; !ok {
			missingInSpec = append(missingInSpec, route)
		}
	}
	for route := range specRoutes {
		if _, ok := implemented[route]; !ok {
			deadInSpec = append(deadInSpec, route)
		}
	}
	sort.Strings(missingInSpec)
	sort.Strings(deadInSpec)

	if len(missingInSpec) > 0 {
		t.Errorf("%d implemented route(s) missing from OpenAPI specs (register them or extend exemption):\n  %s",
			len(missingInSpec), strings.Join(missingInSpec, "\n  "))
	}
	if len(deadInSpec) > 0 {
		t.Errorf("%d spec route(s) have no implementation (remove from spec or implement):\n  %s",
			len(deadInSpec), strings.Join(deadInSpec, "\n  "))
	}
}

// scanRoutes 遍历扫描根下的 .go 文件（排除 _test.go），收集路由字面量。
func scanRoutes(root string) (map[string]bool, error) {
	routes := make(map[string]bool)
	for _, scanRoot := range scanRoots {
		base := filepath.Join(root, scanRoot)
		err := filepath.WalkDir(base, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			//nolint:gosec // G304: path 来自固定扫描根（internal / cmd）下的 WalkDir 结果，非用户输入。
			content, rerr := os.ReadFile(path)
			if rerr != nil {
				return rerr
			}
			for _, m := range routeExpr.FindAllStringSubmatch(string(content), -1) {
				routes[m[1]+" "+m[2]] = true
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return routes, nil
}

// parseSpecPaths 解析三份契约的路由集。路径项是 $ref 外置形态
// （path → {$ref: './paths/admin/auth.yaml#/fragment'}），方法键在被引用
// 文件的 paths.<key> 下——因此先取 $ref，再解析目标文件的片段。
func parseSpecPaths(root string) (map[string]bool, error) {
	routes := make(map[string]bool)
	for _, spec := range specFiles {
		doc, err := loadYAML(filepath.Join(root, spec))
		if err != nil {
			return nil, err
		}
		pathsNode, ok := doc["paths"]
		if !ok {
			return nil, os.ErrNotExist
		}
		var entries map[string]yaml.Node
		if err := pathsNode.Decode(&entries); err != nil {
			return nil, err
		}
		for path, item := range entries {
			var itemBody map[string]yaml.Node
			if err := item.Decode(&itemBody); err != nil {
				return nil, err
			}
			refNode, ok := itemBody["$ref"]
			if !ok {
				// 内联定义的路径项：方法键直接在下面。
				collectMethods(routes, path, itemBody)
				continue
			}
			var ref string
			if err := refNode.Decode(&ref); err != nil {
				return nil, err
			}
			methods, err := resolveRefMethods(root, filepath.Dir(spec), ref)
			if err != nil {
				return nil, err
			}
			collectMethods(routes, path, methods)
		}
	}
	return routes, nil
}

// resolveRefMethods 解析 file.yaml#/paths/<key> 形态的引用，返回该路径项
// 的方法键集合。
func resolveRefMethods(root, baseDir, ref string) (map[string]yaml.Node, error) {
	parts := strings.SplitN(ref, "#", 2)
	if len(parts) != 2 {
		return nil, os.ErrInvalid
	}
	target := filepath.Clean(filepath.Join(root, baseDir, parts[0]))
	doc, err := loadYAML(target)
	if err != nil {
		return nil, err
	}
	frag := strings.Split(strings.Trim(parts[1], "/"), "/")
	node := doc[frag[0]]
	for _, seg := range frag[1:] {
		if node.IsZero() {
			return nil, os.ErrNotExist
		}
		var m map[string]yaml.Node
		if err := node.Decode(&m); err != nil {
			return nil, err
		}
		next, ok := m[seg]
		if !ok {
			return nil, os.ErrNotExist
		}
		node = next
	}
	var body map[string]yaml.Node
	if err := node.Decode(&body); err != nil {
		return nil, err
	}
	return body, nil
}

// collectMethods 把路径项下的 HTTP 方法键登记为路由。
func collectMethods(routes map[string]bool, path string, body map[string]yaml.Node) {
	for method := range body {
		switch strings.ToUpper(method) {
		case "GET", "POST", "PUT", "PATCH", "DELETE":
			routes[strings.ToUpper(method)+" "+path] = true
		}
	}
}

// loadYAML 读取并把文件解码为顶层映射节点。
func loadYAML(path string) (map[string]yaml.Node, error) {
	//nolint:gosec // G304: 路径来自固定 spec 清单与已解析的 $ref 目标，非用户输入。
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc map[string]yaml.Node
	if err := yaml.Unmarshal(content, &doc); err != nil {
		return nil, err
	}
	return doc, nil
}
