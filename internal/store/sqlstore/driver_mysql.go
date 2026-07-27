//go:build !nomysql

package sqlstore

import (
	// One driver covers MySQL and MariaDB — they are wire-compatible, which is
	// why the config accepts both names and the dialect maps them to the same
	// SQL.
	_ "github.com/go-sql-driver/mysql"
)

func init() {
	register("mysql", "mysql")
	register("mariadb", "mysql")
}
