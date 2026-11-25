package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// FlatValues 展平配置文件（含 include）为「yaml 路径 → 字符串」。
//
// 仅用于迁移期双读对比（CC-F-42），不参与运行时取值；合并顺序与 Loader 一致：
// 被 include 的文件先加载，当前文件覆盖先加载的值。数组按紧凑 JSON 编码，
// 与配置中心 ValueTypeJSON 的存储形式对齐；null 与非标量节点跳过。
func FlatValues(path string) (map[string]string, error) {
	merged := make(map[string]any)
	if err := mergeFileChain(path, &merged, nil); err != nil {
		return nil, err
	}
	flat := make(map[string]string)
	flattenNode("", merged, flat)
	return flat, nil
}

// mergeFileChain 按 include 顺序把文件链合并进 dst，语义与 Loader.loadFileChain 一致。
//
//nolint:gocritic // ptrToRefParam: 递归合并需要可变 map 指针，与 Loader 语义一致。
func mergeFileChain(path string, dst *map[string]any, chain []string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return readError(path, err)
	}
	for _, ancestor := range chain {
		if ancestor == abs {
			return decodeError(ErrMalformedFile, path, "include cycle detected")
		}
	}
	if len(chain) >= maxIncludeDepth {
		return decodeError(ErrMalformedFile, path,
			fmt.Sprintf("include depth exceeds %d", maxIncludeDepth))
	}

	data, err := os.ReadFile(path) // #nosec G304,G703 -- 路径来自运维配置，非请求输入。
	if err != nil {
		return readError(path, err)
	}
	includes, body, err := splitInclude(data, path)
	if err != nil {
		return err
	}
	for _, included := range includes {
		includedPath := included
		if !filepath.IsAbs(includedPath) {
			includedPath = filepath.Join(filepath.Dir(path), includedPath)
		}
		if err := mergeFileChain(includedPath, dst, append(chain, abs)); err != nil {
			return err
		}
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return nil
	}
	var current map[string]any
	if err := decodeYAML(body, &current, true); err != nil {
		return decodeErrorOf(path, err)
	}
	deepMerge(*dst, current)
	return nil
}

// deepMerge 递归合并 map：后者覆盖前者；非 map 节点整体替换。
func deepMerge(dst, src map[string]any) {
	for key, value := range src {
		if srcMap, ok := value.(map[string]any); ok {
			if dstMap, ok := dst[key].(map[string]any); ok {
				deepMerge(dstMap, srcMap)
				continue
			}
		}
		dst[key] = value
	}
}

// flattenNode 把嵌套 map 展平为点分路径；数组与标量落在叶子节点。
func flattenNode(prefix string, value any, out map[string]string) {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			childPrefix := key
			if prefix != "" {
				childPrefix = prefix + "." + key
			}
			flattenNode(childPrefix, child, out)
		}
	case []any:
		if prefix == "" {
			return
		}
		encoded, err := json.Marshal(typed)
		if err != nil {
			return
		}
		out[prefix] = string(encoded)
	case nil:
		return
	default:
		if prefix == "" {
			return
		}
		out[prefix] = scalarString(typed)
	}
}

// scalarString 把 YAML 标量转成配置中心存储的字符串形式。
func scalarString(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case bool:
		return strconv.FormatBool(typed)
	case int:
		return strconv.Itoa(typed)
	case int64:
		return strconv.FormatInt(typed, 10)
	case uint64:
		return strconv.FormatUint(typed, 10)
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	default:
		return strings.TrimSpace(fmt.Sprint(typed))
	}
}
