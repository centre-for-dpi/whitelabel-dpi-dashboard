// Package sqlstore implements the store contract over database/sql.
//
// One implementation covers SQLite, Postgres, MySQL and MariaDB. The queries
// are written once and differ only through a Dialect, which supplies the three
// things the four databases genuinely disagree about: how a placeholder is
// spelled, how an upsert is spelled, and how an identifier is quoted.
//
// There is no query builder and no ORM. About fifteen statements exist in total,
// which is few enough to write honestly and few enough to read.
package sqlstore

import (
	"fmt"
	"strconv"
	"strings"
)

// Dialect is the per-database SQL flavour.
type Dialect interface {
	// Name is the dialect's identifier, and the migrations directory to read.
	Name() string

	// Placeholder renders the nth bind parameter, one-indexed.
	Placeholder(n int) string

	// UpsertSuffix renders the clause that turns an INSERT into an upsert,
	// updating the given columns when the conflict columns already match.
	UpsertSuffix(conflict, update []string) string

	// Quote renders an identifier safely for this database.
	Quote(ident string) string
}

// --- SQLite -----------------------------------------------------------------

// SQLite speaks the standard-ish dialect the other two are compared against.
type SQLite struct{}

func (SQLite) Name() string              { return "sqlite" }
func (SQLite) Placeholder(int) string    { return "?" }
func (SQLite) Quote(ident string) string { return `"` + ident + `"` }

func (d SQLite) UpsertSuffix(conflict, update []string) string {
	return conflictSuffix(d, conflict, update)
}

// --- Postgres ---------------------------------------------------------------

// Postgres numbers its placeholders, which is the one difference that reaches
// every single statement rather than just the writes.
type Postgres struct{}

func (Postgres) Name() string              { return "postgres" }
func (Postgres) Placeholder(n int) string  { return "$" + strconv.Itoa(n) }
func (Postgres) Quote(ident string) string { return `"` + ident + `"` }

func (d Postgres) UpsertSuffix(conflict, update []string) string {
	return conflictSuffix(d, conflict, update)
}

// conflictSuffix renders the ON CONFLICT form that SQLite and Postgres share.
func conflictSuffix(d Dialect, conflict, update []string) string {
	quoted := make([]string, len(conflict))
	for i, c := range conflict {
		quoted[i] = d.Quote(c)
	}

	if len(update) == 0 {
		// A conflicting row that updates nothing should be left alone rather
		// than rejected: the caller asked for an upsert, not an insert.
		return " ON CONFLICT (" + strings.Join(quoted, ", ") + ") DO NOTHING"
	}

	sets := make([]string, len(update))
	for i, c := range update {
		sets[i] = fmt.Sprintf("%s = excluded.%s", d.Quote(c), d.Quote(c))
	}
	return " ON CONFLICT (" + strings.Join(quoted, ", ") + ") DO UPDATE SET " + strings.Join(sets, ", ")
}

// --- MySQL ------------------------------------------------------------------

// MySQL covers MariaDB too. Its upsert keys off whichever unique index the row
// collides with rather than off named columns, so the conflict list is unused —
// which also means the caller must have declared that index, and every table
// here does.
type MySQL struct{}

func (MySQL) Name() string              { return "mysql" }
func (MySQL) Placeholder(int) string    { return "?" }
func (MySQL) Quote(ident string) string { return "`" + ident + "`" }

func (d MySQL) UpsertSuffix(conflict, update []string) string {
	if len(update) == 0 {
		// MySQL has no DO NOTHING. Assigning a column to itself is the
		// idiomatic no-op, and needs a column to name — hence the conflict
		// list, which is otherwise unused here.
		if len(conflict) == 0 {
			return ""
		}
		c := d.Quote(conflict[0])
		return " ON DUPLICATE KEY UPDATE " + c + " = " + c
	}

	sets := make([]string, len(update))
	for i, c := range update {
		// VALUES() is deprecated in MySQL 8.0.20+ in favour of an alias, but
		// the alias form is a syntax error on MariaDB and on MySQL 5.7. This
		// build supports both, so it uses the form both understand.
		sets[i] = fmt.Sprintf("%s = VALUES(%s)", d.Quote(c), d.Quote(c))
	}
	return " ON DUPLICATE KEY UPDATE " + strings.Join(sets, ", ")
}

// --- helpers ----------------------------------------------------------------

// dialectFor resolves a driver name to its dialect.
func dialectFor(driver string) (Dialect, error) {
	switch driver {
	case "sqlite":
		return SQLite{}, nil
	case "postgres":
		return Postgres{}, nil
	case "mysql", "mariadb":
		return MySQL{}, nil
	}
	return nil, fmt.Errorf("no SQL dialect for driver %q", driver)
}

// insert renders an INSERT, optionally as an upsert.
//
// Every write in this package goes through here, so placeholder numbering is
// done once rather than at fifteen call sites — the sort of detail that works
// on SQLite and breaks on Postgres because a hand-written $3 was a $2.
func insert(d Dialect, table string, cols []string, conflict, update []string) string {
	quoted := make([]string, len(cols))
	marks := make([]string, len(cols))
	for i, c := range cols {
		quoted[i] = d.Quote(c)
		marks[i] = d.Placeholder(i + 1)
	}

	stmt := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)",
		d.Quote(table), strings.Join(quoted, ", "), strings.Join(marks, ", "))

	if len(conflict) > 0 {
		stmt += d.UpsertSuffix(conflict, update)
	}
	return stmt
}

// selectAll renders a SELECT of the given columns, with an optional WHERE whose
// placeholders continue the numbering.
func selectAll(d Dialect, table string, cols []string, where string, order string) string {
	quoted := make([]string, len(cols))
	for i, c := range cols {
		quoted[i] = d.Quote(c)
	}

	stmt := fmt.Sprintf("SELECT %s FROM %s", strings.Join(quoted, ", "), d.Quote(table))
	if where != "" {
		stmt += " WHERE " + where
	}
	if order != "" {
		stmt += " ORDER BY " + order
	}
	return stmt
}

// cmp renders a comparison against a bind parameter, e.g. `ts` < $1.
func cmp(d Dialect, col, op string, n int) string {
	return d.Quote(col) + " " + op + " " + d.Placeholder(n)
}
