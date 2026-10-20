package main

import (
	// 注册 MySQL database/sql 驱动（注册名为 mysql）。
	_ "github.com/go-sql-driver/mysql"
	// 注册 PostgreSQL database/sql 驱动（pgx stdlib 注册名为 pgx）。
	_ "github.com/jackc/pgx/v5/stdlib"
)
