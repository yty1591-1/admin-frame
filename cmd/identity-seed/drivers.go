package main

// 注册 seed CLI 支持的数据库驱动（与 cmd/migrate 保持一致）。
//
// memory 驱动不对应真实数据库，故不在此注册。
import (
	_ "github.com/go-sql-driver/mysql" // mysql
	_ "github.com/jackc/pgx/v5/stdlib" // pgx（postgres）
)
