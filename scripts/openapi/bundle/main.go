// Command bundle 将多文件 OpenAPI 装配为单文件。
//
// 仅用于契约门禁比对与分发产物。规则：
//   - 外部 $ref 指向 components.* 时，把该文件全部组件提升到根 components，
//     引用改写为本地指针（同名同结构复用，冲突直接报错）；
//   - 外部 $ref 指向其它位置（如 path item）时就地内联；
//   - 本地 $ref（#/...）保持不变。
//
// 用法: go run ./scripts/openapi/bundle <entry.yaml> <out.yaml>
package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type bundler struct {
	root    *yaml.Node
	cache   map[string]*yaml.Node
	hoisted map[string]bool
}

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: bundle <entry.yaml> <out.yaml>")
		os.Exit(2)
	}
	entry, err := loadNode(os.Args[1])
	if err != nil {
		fail(err)
	}
	entryAbs, err := filepath.Abs(os.Args[1])
	if err != nil {
		fail(err)
	}
	b := &bundler{root: entry.Content[0], cache: map[string]*yaml.Node{}, hoisted: map[string]bool{}}
	b.cache[entryAbs] = entry
	if err := b.expand(b.root, filepath.Dir(entryAbs)); err != nil {
		fail(err)
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(entry); err != nil {
		fail(err)
	}
	if err := enc.Close(); err != nil {
		fail(err)
	}
	if err := os.WriteFile(os.Args[2], buf.Bytes(), 0o644); err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "bundle:", err)
	os.Exit(1)
}

func loadNode(path string) (*yaml.Node, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil, fmt.Errorf("%s: empty document", path)
	}
	return &doc, nil
}

func (b *bundler) load(abs string) (*yaml.Node, error) {
	if n, ok := b.cache[abs]; ok {
		return n, nil
	}
	n, err := loadNode(abs)
	if err != nil {
		return nil, err
	}
	b.cache[abs] = n
	return n, nil
}

func (b *bundler) expand(n *yaml.Node, baseDir string) error {
	switch n.Kind {
	case yaml.DocumentNode:
		return b.expand(n.Content[0], baseDir)
	case yaml.MappingNode:
		if ref, ok := refOf(n); ok && !strings.HasPrefix(ref, "#") {
			return b.handleRef(n, ref, baseDir)
		}
		for i := 1; i < len(n.Content); i += 2 {
			if err := b.expand(n.Content[i], baseDir); err != nil {
				return err
			}
		}
	case yaml.SequenceNode:
		for _, c := range n.Content {
			if err := b.expand(c, baseDir); err != nil {
				return err
			}
		}
	}
	return nil
}

func (b *bundler) handleRef(n *yaml.Node, ref, baseDir string) error {
	file, frag := splitRef(ref)
	abs := filepath.Clean(filepath.Join(baseDir, filepath.FromSlash(file)))
	if strings.HasPrefix(frag, "/components/") {
		if err := b.hoist(abs); err != nil {
			return err
		}
		n.Content = []*yaml.Node{scalar("$ref"), scalar("#" + frag)}
		return nil
	}
	doc, err := b.load(abs)
	if err != nil {
		return err
	}
	target := navigate(doc, frag)
	if target == nil {
		return fmt.Errorf("%s: ref %q not found", abs, ref)
	}
	clone := deepCopy(target)
	*n = *clone
	return b.expand(n, filepath.Dir(abs))
}

// hoist 把 abs 文件 components 下的全部条目提升到根文档，保证该文件的本地 $ref 仍可解析。
func (b *bundler) hoist(abs string) error {
	if b.hoisted[abs] {
		return nil
	}
	b.hoisted[abs] = true
	doc, err := b.load(abs)
	if err != nil {
		return err
	}
	src := lookup(doc.Content[0], "components")
	if src == nil || src.Kind != yaml.MappingNode {
		return nil
	}
	dir := filepath.Dir(abs)
	for i := 0; i < len(src.Content); i += 2 {
		section := src.Content[i].Value
		items := src.Content[i+1]
		if items.Kind != yaml.MappingNode {
			continue
		}
		dst := ensurePath(b.rootMap(), "components", section)
		for j := 0; j < len(items.Content); j += 2 {
			name := items.Content[j].Value
			value := items.Content[j+1]
			if existing := lookup(dst, name); existing != nil {
				if !sameNode(existing, value) {
					return fmt.Errorf("component conflict: %s/%s (%s)", section, name, abs)
				}
				continue
			}
			clone := deepCopy(value)
			if err := b.expand(clone, dir); err != nil {
				return err
			}
			dst.Content = append(dst.Content, scalar(name), clone)
		}
	}
	return nil
}

func (b *bundler) rootMap() *yaml.Node {
	return b.root
}

func refOf(n *yaml.Node) (string, bool) {
	if n.Kind != yaml.MappingNode {
		return "", false
	}
	for i := 0; i < len(n.Content); i += 2 {
		if n.Content[i].Value == "$ref" && n.Content[i+1].Kind == yaml.ScalarNode {
			return n.Content[i+1].Value, true
		}
	}
	return "", false
}

func splitRef(ref string) (file, frag string) {
	if i := strings.Index(ref, "#"); i >= 0 {
		return ref[:i], ref[i+1:]
	}
	return ref, ""
}

func navigate(doc *yaml.Node, frag string) *yaml.Node {
	cur := doc.Content[0]
	for _, seg := range strings.Split(strings.Trim(frag, "/"), "/") {
		if seg == "" {
			continue
		}
		if cur.Kind != yaml.MappingNode {
			return nil
		}
		cur = lookup(cur, seg)
		if cur == nil {
			return nil
		}
	}
	return cur
}

func lookup(n *yaml.Node, key string) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}

func ensurePath(n *yaml.Node, keys ...string) *yaml.Node {
	for _, key := range keys {
		next := lookup(n, key)
		if next == nil {
			next = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			n.Content = append(n.Content, scalar(key), next)
		}
		n = next
	}
	return n
}

func scalar(s string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s}
}

func deepCopy(n *yaml.Node) *yaml.Node {
	c := *n
	c.Content = nil
	for _, ch := range n.Content {
		c.Content = append(c.Content, deepCopy(ch))
	}
	return &c
}

func sameNode(a, b *yaml.Node) bool {
	var ab, bb bytes.Buffer
	ea := yaml.NewEncoder(&ab)
	ea.SetIndent(2)
	_ = ea.Encode(a)
	eb := yaml.NewEncoder(&bb)
	eb.SetIndent(2)
	_ = eb.Encode(b)
	return ab.String() == bb.String()
}
