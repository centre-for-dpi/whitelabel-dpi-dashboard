package sqlstore

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/store"
)

//go:embed all:migrations
var migrationFS embed.FS

// Embedded is the shipped migration set, compiled into the binary so a deploy
// is one file with nothing to copy alongside it.
type Embedded struct{}

// Migrations reads the ordered migrations for a dialect.
func (Embedded) Migrations(dialect string) ([]store.Migration, error) {
	return readMigrations(migrationFS, path.Join("migrations", dialect))
}

// readMigrations parses `NNNN_name.sql` files from a directory.
func readMigrations(fsys fs.FS, dir string) ([]store.Migration, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("reading migrations for %s: %w", path.Base(dir), err)
	}

	var out []store.Migration
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}

		version, name, ok := strings.Cut(strings.TrimSuffix(e.Name(), ".sql"), "_")
		if !ok {
			return nil, fmt.Errorf("migration %q is not named NNNN_name.sql", e.Name())
		}
		n, err := strconv.Atoi(version)
		if err != nil {
			return nil, fmt.Errorf("migration %q has a non-numeric version %q", e.Name(), version)
		}

		body, err := fs.ReadFile(fsys, path.Join(dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("reading migration %q: %w", e.Name(), err)
		}
		out = append(out, store.Migration{Version: n, Name: name, SQL: string(body)})
	}

	slices.SortFunc(out, func(a, b store.Migration) int { return a.Version - b.Version })

	for i := 1; i < len(out); i++ {
		if out[i].Version == out[i-1].Version {
			return nil, fmt.Errorf("two migrations share version %d: %q and %q",
				out[i].Version, out[i-1].Name, out[i].Name)
		}
	}
	return out, nil
}

// Migrate applies every migration not yet recorded, each in its own transaction.
//
// Per-migration transactions rather than one for the whole run: a failure part
// way leaves the applied ones applied and recorded, so the next start resumes
// rather than replaying work that already succeeded. Statements are split on
// semicolons at the end of a line, which is enough for schema DDL and would not
// be enough for a migration containing a stored procedure — none does, and the
// runner would need a real parser if one ever did.
func (s *Store) Migrate(ctx context.Context) error {
	if err := s.ensureMigrationsTable(ctx); err != nil {
		return err
	}

	applied, err := s.appliedVersions(ctx)
	if err != nil {
		return err
	}

	migrations, err := s.migrations.Migrations(s.dialect.Name())
	if err != nil {
		return err
	}

	for _, m := range migrations {
		if applied[m.Version] {
			continue
		}
		if err := s.applyMigration(ctx, m); err != nil {
			return fmt.Errorf("migration %04d_%s: %w", m.Version, m.Name, err)
		}
	}
	return nil
}

func (s *Store) ensureMigrationsTable(ctx context.Context) error {
	// Written per dialect rather than through the migration files, because it
	// is the table that records which migration files have run.
	var stmt string
	switch s.dialect.Name() {
	case "mysql":
		stmt = "CREATE TABLE IF NOT EXISTS `schema_migrations` (" +
			"`version` BIGINT NOT NULL, `name` VARCHAR(191) NOT NULL DEFAULT ''," +
			"`applied_at` BIGINT NOT NULL DEFAULT 0, PRIMARY KEY (`version`))" +
			" ENGINE=InnoDB DEFAULT CHARSET=utf8mb4"
	default:
		stmt = `CREATE TABLE IF NOT EXISTS "schema_migrations" (` +
			`"version" BIGINT NOT NULL PRIMARY KEY, "name" TEXT NOT NULL DEFAULT '',` +
			`"applied_at" BIGINT NOT NULL DEFAULT 0)`
	}

	if _, err := s.db.ExecContext(ctx, stmt); err != nil {
		return fmt.Errorf("creating schema_migrations: %w", err)
	}
	return nil
}

func (s *Store) appliedVersions(ctx context.Context) (map[int]bool, error) {
	rows, err := s.db.QueryContext(ctx,
		selectAll(s.dialect, "schema_migrations", []string{"version"}, "", ""))
	if err != nil {
		return nil, fmt.Errorf("reading schema_migrations: %w", err)
	}
	defer rows.Close()

	applied := map[int]bool{}
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return nil, fmt.Errorf("reading schema_migrations: %w", err)
		}
		applied[v] = true
	}
	return applied, rows.Err()
}

func (s *Store) applyMigration(ctx context.Context, m store.Migration) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		for _, stmt := range splitStatements(m.SQL) {
			if _, err := tx.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("%w\n  in statement: %s", err, firstLine(stmt))
			}
		}

		_, err := tx.ExecContext(ctx,
			insert(s.dialect, "schema_migrations",
				[]string{"version", "name", "applied_at"},
				[]string{"version"}, nil),
			m.Version, m.Name, s.now().Unix())
		return err
	})
}

// splitStatements breaks a migration file into executable statements.
//
// Split on a semicolon that ends a line, so a semicolon inside a string literal
// or a comment does not cut a statement in half.
func splitStatements(body string) []string {
	var (
		out     []string
		current strings.Builder
	)
	for line := range strings.Lines(body) {
		trimmed := strings.TrimRight(line, " \t\r\n")
		if strings.HasPrefix(strings.TrimSpace(trimmed), "--") {
			continue
		}

		current.WriteString(line)
		if strings.HasSuffix(trimmed, ";") {
			if stmt := strings.TrimSpace(current.String()); stmt != ";" && stmt != "" {
				out = append(out, strings.TrimSuffix(stmt, ";"))
			}
			current.Reset()
		}
	}

	// A trailing statement with no terminating semicolon still counts.
	if stmt := strings.TrimSpace(current.String()); stmt != "" {
		out = append(out, stmt)
	}
	return out
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	if len(line) > 80 {
		return line[:77] + "..."
	}
	return line
}
