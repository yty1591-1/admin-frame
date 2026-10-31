package persistence

import (
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"

	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// mysqlErrDuplicateEntry 是 MySQL 唯一键冲突错误码（ER_DUP_ENTRY）。
const mysqlErrDuplicateEntry = 1062

// conflictOnDuplicate 把唯一键冲突转换为 40900，其他错误原样返回。
func conflictOnDuplicate(err error, message string) error {
	if err == nil {
		return nil
	}
	var mysqlErr *mysql.MySQLError
	if errors.As(err, &mysqlErr) && mysqlErr.Number == mysqlErrDuplicateEntry {
		return apperr.Conflict(message)
	}
	return err
}

// parseID 把字符串雪花 ID 转换为 BIGINT 列值。
//
// 只接受正十进制整数，拒绝空串、负数与溢出，避免 MySQL 宽松模式把
// 'account-1' 静默转成 0 造成主键污染。
func parseID(id string) (int64, error) {
	value, err := strconv.ParseInt(id, 10, 64)
	if err != nil || value <= 0 {
		return 0, apperr.BadRequest("id must be a positive snowflake id")
	}
	return value, nil
}

// escapeLikePattern 转义 LIKE 模式中的通配符，避免用户输入 % / _ 扩大结果集。
// MySQL 默认以反斜杠作为 LIKE 转义符。
func escapeLikePattern(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return replacer.Replace(value)
}

// nullTime 把可空时间转换为 driver 可接受的值。
func nullTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.UTC()
}

// nullString 把空串写为 NULL，避免 S1 可空列出现无意义的空串。
func nullString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

// nullBytes 把空字节切片写为 NULL，供可空密文列使用。
func nullBytes(value []byte) any {
	if len(value) == 0 {
		return nil
	}
	return value
}

// nullableTime 把 sql.NullTime 转换为可空指针（UTC）。
func nullableTime(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	utc := value.Time.UTC()
	return &utc
}

// scanRow 是 *sql.Row 与 *sql.Rows 的公共扫描面。
type scanRow interface {
	Scan(dest ...any) error
}

// resolveNotFound 把 sql.ErrNoRows 映射为 40400，其他错误原样返回。
func resolveNotFound(err error, message string) error {
	if errors.Is(err, sql.ErrNoRows) {
		return apperr.NotFound(message)
	}
	return err
}
