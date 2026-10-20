package migrate

import (
	"fmt"
	"strings"
)

// SplitStatements 将 SQL 脚本切分为可逐条执行的语句。
//
// 识别行注释（--）、块注释（/* */，支持嵌套）、单引号字符串（” 转义）、
// 双引号标识符（"" 转义）、反引号标识符（“ 转义）与 PostgreSQL dollar-quote
// （$$ ... $$ 或 $tag$ ... $tag$）。返回值已去除首尾空白，纯注释片段被丢弃。
//
// 本函数按 ANSI SQL 语义处理字符串（反斜杠不是转义符）；MySQL 迁移由 Runner 使用
// 反斜杠转义语义切分。脚本未闭合时返回 ErrMalformedSQL，绝不静默截断。
func SplitStatements(script string) ([]string, error) {
	return splitStatements(script, false)
}

// splitStatements 按方言选择是否启用反斜杠转义（MySQL 单/双引号字符串）。
func splitStatements(script string, backslashEscapes bool) ([]string, error) {
	var statements []string
	start, i, n := 0, 0, len(script)
	hasCode := false

	for i < n {
		c := script[i]
		switch {
		case c == '-' && i+1 < n && script[i+1] == '-':
			i = skipLineComment(script, i+2)
		case c == '#' && backslashEscapes:
			// MySQL 专有行注释；ANSIC 模式下 # 可能是运算符，不能误判。
			i = skipLineComment(script, i+1)
		case c == '/' && i+1 < n && script[i+1] == '*':
			next, err := skipBlockComment(script, i+2)
			if err != nil {
				return nil, err
			}
			i = next
		case c == '\'' || c == '"' || c == '`':
			next, err := skipQuoted(script, i, c, backslashEscapes && c != '`')
			if err != nil {
				return nil, err
			}
			hasCode = true
			i = next
		case c == '$':
			if delim, ok := dollarDelimiter(script, i); ok {
				next, err := skipDollarQuoted(script, i, delim)
				if err != nil {
					return nil, err
				}
				hasCode = true
				i = next
			} else {
				hasCode = true
				i++
			}
		case c == ';':
			if hasCode {
				statements = appendStatement(statements, script[start:i])
			}
			hasCode = false
			i++
			start = i
		default:
			if !isSpace(c) {
				hasCode = true
			}
			i++
		}
	}
	if hasCode {
		statements = appendStatement(statements, script[start:])
	}
	return statements, nil
}

func appendStatement(statements []string, raw string) []string {
	stmt := strings.TrimSpace(raw)
	if stmt == "" {
		return statements
	}
	return append(statements, stmt)
}

// skipLineComment 返回行注释之后（换行符之后或脚本末尾）的下标。
func skipLineComment(script string, from int) int {
	if idx := strings.IndexByte(script[from:], '\n'); idx >= 0 {
		return from + idx + 1
	}
	return len(script)
}

// skipBlockComment 跳过块注释（支持嵌套），from 指向 "/*" 之后。
func skipBlockComment(script string, from int) (int, error) {
	depth := 1
	for i := from; i < len(script); i++ {
		switch {
		case script[i] == '/' && i+1 < len(script) && script[i+1] == '*':
			depth++
			i++
		case script[i] == '*' && i+1 < len(script) && script[i+1] == '/':
			depth--
			i++
			if depth == 0 {
				return i + 1, nil
			}
		}
	}
	return 0, fmt.Errorf("%w: unterminated block comment", ErrMalformedSQL)
}

// skipQuoted 跳过引号包裹的字符串/标识符，from 指向起始引号。
func skipQuoted(script string, from int, quote byte, backslashEscapes bool) (int, error) {
	for i := from + 1; i < len(script); i++ {
		switch script[i] {
		case '\\':
			if backslashEscapes {
				i++ // 跳过被转义的字节；末尾反斜杠由循环结束后的错误分支处理
			}
		case quote:
			if i+1 < len(script) && script[i+1] == quote {
				i++ // 双写引号表示字面量
				continue
			}
			return i + 1, nil
		}
	}
	return 0, fmt.Errorf("%w: unterminated quote %q", ErrMalformedSQL, string(quote))
}

// dollarDelimiter 判断 i 处是否为 dollar-quote 起始标记，返回完整定界符（如 "$$"、"$body$"）。
func dollarDelimiter(script string, i int) (string, bool) {
	j := i + 1
	if j < len(script) && script[j] == '$' {
		return "$$", true
	}
	if j >= len(script) || !isTagStart(script[j]) {
		return "", false
	}
	for j < len(script) && isTagChar(script[j]) {
		j++
	}
	if j < len(script) && script[j] == '$' {
		return script[i : j+1], true
	}
	return "", false
}

// skipDollarQuoted 跳过 dollar-quote 块，from 指向起始定界符。
func skipDollarQuoted(script string, from int, delim string) (int, error) {
	idx := strings.Index(script[from+len(delim):], delim)
	if idx < 0 {
		return 0, fmt.Errorf("%w: unterminated dollar-quoted string %s", ErrMalformedSQL, delim)
	}
	return from + len(delim) + idx + len(delim), nil
}

func isTagStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isTagChar(c byte) bool {
	return isTagStart(c) || (c >= '0' && c <= '9')
}

func isSpace(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '\r', '\f', '\v':
		return true
	default:
		return false
	}
}
