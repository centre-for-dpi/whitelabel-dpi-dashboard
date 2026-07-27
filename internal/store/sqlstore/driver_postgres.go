//go:build !nopostgres

package sqlstore

import (
	// pgx's database/sql adapter. pgx is pure Go, and the stdlib adapter is used
	// rather than its native interface so one implementation covers all four
	// backends instead of Postgres needing its own.
	_ "github.com/jackc/pgx/v5/stdlib"
)

func init() { register("postgres", "pgx") }
