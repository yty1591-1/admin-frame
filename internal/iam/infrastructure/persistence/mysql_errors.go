package persistence

import (
	"errors"
	"strconv"

	"github.com/go-sql-driver/mysql"

	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// mysqlErrDuplicateEntry 是 MySQL 唯一键冲突错误码（ER_DUP_ENTRY）。
const mysqlErrDuplicateEntry = 1062

// mysqlErrNoReferencedRow 是 MySQL 外键引用不存在的错误码（ER_NO_REFERENCED_ROW_2）。
const mysqlErrNoReferencedRow = 1452

// isDuplicateEntry 判断错误是否为唯一键冲突，用于把数据库约束映射为 40900。
//
// 只识别 1062，不吞掉外键 / 数据截断等其他错误：
// 后者代表调用方或 schema 问题，必须原样向上暴露，不能被误报成「资源已存在」。
func isDuplicateEntry(err error) bool {
	var mysqlErr *mysql.MySQLError
	return errors.As(err, &mysqlErr) && mysqlErr.Number == mysqlErrDuplicateEntry
}

// conflictOnDuplicate 把唯一键冲突转换为稳定业务冲突错误，其他错误原样返回。
func conflictOnDuplicate(err error, message string) error {
	if err == nil {
		return nil
	}
	if isDuplicateEntry(err) {
		return apperr.Conflict(message)
	}
	return err
}

// conflictOnReferencedRow 把外键引用失败转换为业务冲突，避免暴露为 500。
//
// 应用层已做存在性预检；此处兜底并发删除父节点或权限点导致的外键失败。
func conflictOnReferencedRow(err error, message string) error {
	if err == nil {
		return nil
	}
	var mysqlErr *mysql.MySQLError
	if errors.As(err, &mysqlErr) && mysqlErr.Number == mysqlErrNoReferencedRow {
		return apperr.Conflict(message)
	}
	return err
}

// parseID 把字符串雪花 ID 转换为 BIGINT 列值。
//
// 端口契约为 string（见 domain 仓储接口），数据库为 BIGINT：
// 只接受正十进制整数，拒绝空串、负数、带前缀或溢出的值，
// 避免 MySQL 在宽松模式下把 'role-1' 静默转成 0 造成主键污染。
func parseID(id string) (int64, error) {
	value, err := strconv.ParseInt(id, 10, 64)
	if err != nil || value <= 0 {
		return 0, apperr.BadRequest("id must be a positive snowflake id")
	}
	return value, nil
}
