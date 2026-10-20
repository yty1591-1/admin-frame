package migrate

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Direction 表示迁移文件的执行方向。
type Direction string

const (
	// DirectionUp 表示前向迁移（.up.sql）。
	DirectionUp Direction = "up"
	// DirectionDown 表示回滚迁移（.down.sql）。
	DirectionDown Direction = "down"
)

// String 返回方向的可读名称。
func (d Direction) String() string { return string(d) }

// Migration 是一条已加载并校验通过的迁移。
//
// Version 对应文件名前缀（六位十进制，DATA-01）；Checksum 是 Up SQL 的 SHA-256 十六进制摘要，
// 用于检测历史漂移。Migration 值在加载后视为只读，可安全并发读取。
type Migration struct {
	// Version 迁移版本号（文件名 NNNNNN，> 0）。
	Version int64
	// Description 迁移描述（文件名 <desc>，小写蛇形）。
	Description string
	// Up 前向 SQL 原文。
	Up string
	// Down 回滚 SQL 原文；AllowMissingDown 时可为空。
	Down string
	// Checksum Up SQL 的 SHA-256（小写十六进制）。
	Checksum string
	// NoTx 为 true 时该迁移不在事务内执行（由 "-- +migrate no-transaction" 指令声明）。
	NoTx bool
}

// LoadOptions 控制迁移文件加载与校验行为；零值即启用全部严格校验（推荐）。
type LoadOptions struct {
	// AllowMissingDown 允许只有 .up.sql 的迁移（默认 false；生产必须成对，DATA-02）。
	AllowMissingDown bool
}

var migrationNamePattern = regexp.MustCompile(`^(\d{6})_([a-z0-9]+(?:_[a-z0-9]+)*)\.(up|down)\.sql$`)

// noTxPattern 匹配显式声明非事务执行的指令行，例如：
//
//	-- +migrate no-transaction
var noTxPattern = regexp.MustCompile(`(?mi)^[ \t]*--[ \t]*\+migrate[ \t]+no-transaction[ \t]*$`)

// ParseName 解析迁移文件名，返回版本号、描述与方向。
//
// 仅接受 DATA-01 规定的 `<NNNNNN>_<desc>.(up|down).sql`；版本号必须大于 0，
// 描述仅允许小写字母、数字与单下划线分隔。
func ParseName(name string) (version int64, description string, direction Direction, err error) {
	m := migrationNamePattern.FindStringSubmatch(name)
	if m == nil {
		return 0, "", "", fmt.Errorf("%w: bad migration file name %q (want NNNNNN_desc.up.sql)", ErrInvalidSource, name)
	}
	version, err = strconv.ParseInt(m[1], 10, 64)
	if err != nil || version <= 0 {
		return 0, "", "", fmt.Errorf("%w: bad migration version in %q", ErrInvalidSource, name)
	}
	return version, m[2], Direction(m[3]), nil
}

// LoadDir 从本地目录加载迁移（等价于 os.DirFS(dir) + LoadFS）。
func LoadDir(dir string, opts LoadOptions) ([]Migration, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("%w: stat %s: %w", ErrInvalidSource, dir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%w: %s is not a directory", ErrInvalidSource, dir)
	}
	return LoadFS(os.DirFS(dir), ".", opts)
}

// LoadFS 从任意 io/fs.FS（os.DirFS、embed.FS 等）的 dir 子目录加载迁移。
//
// 返回结果按 Version 升序排列。目录中的迁移文件必须严格符合 DATA-01，up/down 必须成对
// （除非 AllowMissingDown），up/down 描述必须一致，SQL 不得为空。
func LoadFS(fsys fs.FS, dir string, opts LoadOptions) ([]Migration, error) {
	if fsys == nil {
		return nil, fmt.Errorf("%w: nil filesystem", ErrInvalidSource)
	}
	dir = strings.TrimSpace(dir)
	if dir == "" {
		dir = "."
	}
	dir = path.Clean(dir)
	if !fs.ValidPath(dir) {
		return nil, fmt.Errorf("%w: invalid migration dir %q", ErrInvalidSource, dir)
	}
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("%w: read dir %s: %w", ErrInvalidSource, dir, err)
	}

	type pair struct {
		desc string
		up   string
		down string
		hasU bool
		hasD bool
	}
	pairs := make(map[int64]*pair, len(entries))
	order := make([]int64, 0, len(entries))

	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() {
			continue
		}
		if name == "README.md" || name == ".gitkeep" {
			continue
		}
		version, desc, direction, err := ParseName(name)
		if err != nil {
			return nil, err
		}
		raw, err := fs.ReadFile(fsys, path.Join(dir, name))
		if err != nil {
			return nil, fmt.Errorf("%w: read %s: %w", ErrInvalidSource, name, err)
		}
		p := pairs[version]
		if p == nil {
			p = &pair{desc: desc}
			pairs[version] = p
			order = append(order, version)
		} else if p.desc != desc {
			return nil, fmt.Errorf("%w: version %06d has mismatched descriptions %q and %q", ErrInvalidSource, version, p.desc, desc)
		}
		sqlText := string(raw)
		switch direction {
		case DirectionUp:
			if p.hasU {
				return nil, fmt.Errorf("%w: duplicate up migration for version %06d", ErrInvalidSource, version)
			}
			p.up, p.hasU = sqlText, true
		case DirectionDown:
			if p.hasD {
				return nil, fmt.Errorf("%w: duplicate down migration for version %06d", ErrInvalidSource, version)
			}
			p.down, p.hasD = sqlText, true
		}
	}

	sort.Slice(order, func(i, j int) bool { return order[i] < order[j] })
	migrations := make([]Migration, 0, len(order))
	for _, version := range order {
		p := pairs[version]
		if !p.hasU {
			return nil, fmt.Errorf("%w: down migration for version %06d has no up counterpart", ErrInvalidSource, version)
		}
		if strings.TrimSpace(p.up) == "" {
			return nil, fmt.Errorf("%w: empty up sql for version %06d", ErrInvalidSource, version)
		}
		if !p.hasD || strings.TrimSpace(p.down) == "" {
			if !opts.AllowMissingDown {
				return nil, fmt.Errorf("%w: version %06d (%s)", ErrMissingDown, version, p.desc)
			}
			p.down = ""
		}
		migrations = append(migrations, Migration{
			Version:     version,
			Description: p.desc,
			Up:          p.up,
			Down:        p.down,
			Checksum:    checksumSQL(p.up),
			NoTx:        noTxPattern.MatchString(p.up),
		})
	}
	return migrations, nil
}

// checksumSQL 返回脚本的 SHA-256 小写十六进制摘要。
func checksumSQL(script string) string {
	sum := sha256.Sum256([]byte(script))
	return hex.EncodeToString(sum[:])
}

// ValidateMigrations 校验一组已在内存中的迁移（版本唯一、升序、必填字段完整）。
//
// 从文件加载时这些约束已由 LoadFS 保证；直接构造 Migration 初始化的调用方可显式调用本函数。
func ValidateMigrations(migrations []Migration) error {
	seen := make(map[int64]struct{}, len(migrations))
	prev := int64(0)
	for _, m := range migrations {
		if m.Version <= 0 {
			return fmt.Errorf("%w: version must be > 0", ErrInvalidSource)
		}
		if m.Description == "" {
			return fmt.Errorf("%w: version %06d has empty description", ErrInvalidSource, m.Version)
		}
		if strings.TrimSpace(m.Up) == "" {
			return fmt.Errorf("%w: version %06d has empty up sql", ErrInvalidSource, m.Version)
		}
		if _, dup := seen[m.Version]; dup {
			return fmt.Errorf("%w: duplicate version %06d", ErrInvalidSource, m.Version)
		}
		if m.Version < prev {
			return fmt.Errorf("%w: migrations are not sorted by version", ErrInvalidSource)
		}
		seen[m.Version] = struct{}{}
		prev = m.Version
	}
	return nil
}
