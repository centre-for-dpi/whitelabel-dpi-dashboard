package sqlstore

import (
	"strings"
	"testing"
	"testing/fstest"
)

func TestEmbeddedMigrationsExistForEveryDialect(t *testing.T) {
	// A dialect with no migrations directory fails at startup with a confusing
	// error about a missing table, long after the cause.
	for _, dialect := range []string{"sqlite", "postgres", "mysql"} {
		t.Run(dialect, func(t *testing.T) {
			got, err := (Embedded{}).Migrations(dialect)
			if err != nil {
				t.Fatalf("Migrations(%q): %v", dialect, err)
			}
			if len(got) == 0 {
				t.Fatalf("no migrations embedded for %q", dialect)
			}
			if got[0].Version != 1 {
				t.Errorf("first migration is version %d, want 1", got[0].Version)
			}
		})
	}
}

func TestEveryDialectDeclaresTheSameTables(t *testing.T) {
	// The four backends are meant to be interchangeable, which they cannot be
	// if one of them is missing a table. This catches the divergence at the
	// schema rather than at the query that needed it.
	want := map[string]bool{
		"services": true, "service_state": true, "samples": true,
		"history_daily": true, "incidents": true, "incident_events": true,
		"error_buckets": true,
	}

	for _, dialect := range []string{"sqlite", "postgres", "mysql"} {
		migrations, err := (Embedded{}).Migrations(dialect)
		if err != nil {
			t.Fatalf("Migrations(%q): %v", dialect, err)
		}

		var all strings.Builder
		for _, m := range migrations {
			all.WriteString(m.SQL)
		}
		body := all.String()

		for table := range want {
			// Backticked on MySQL, bare elsewhere.
			if !strings.Contains(body, table) {
				t.Errorf("%s declares no %q table", dialect, table)
			}
		}
	}
}

func TestUnknownDialectIsReported(t *testing.T) {
	if _, err := (Embedded{}).Migrations("oracle"); err == nil {
		t.Error("Migrations accepted a dialect with no directory")
	}
}

func TestReadMigrationsOrdersByVersion(t *testing.T) {
	// Directory listings are lexical, which orders 10 before 2. Applying
	// migrations in that order corrupts a schema in a way that is very hard to
	// unpick afterwards.
	fsys := fstest.MapFS{
		"m/0010_tenth.sql":  {Data: []byte("SELECT 10;")},
		"m/0002_second.sql": {Data: []byte("SELECT 2;")},
		"m/0001_first.sql":  {Data: []byte("SELECT 1;")},
		"m/README.md":       {Data: []byte("not a migration")},
	}

	got, err := readMigrations(fsys, "m")
	if err != nil {
		t.Fatalf("readMigrations: %v", err)
	}

	want := []int{1, 2, 10}
	if len(got) != len(want) {
		t.Fatalf("got %d migrations, want %d", len(got), len(want))
	}
	for i, v := range want {
		if got[i].Version != v {
			t.Errorf("position %d is version %d, want %d", i, got[i].Version, v)
		}
	}
	if got[0].Name != "first" {
		t.Errorf("name = %q, want %q", got[0].Name, "first")
	}
}

func TestReadMigrationsRejectsBadNames(t *testing.T) {
	for _, tc := range []struct {
		name    string
		file    string
		wantErr string
	}{
		{
			name:    "no version prefix",
			file:    "m/initial.sql",
			wantErr: "NNNN_name.sql",
		},
		{
			name:    "non-numeric version",
			file:    "m/vone_initial.sql",
			wantErr: "non-numeric version",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := readMigrations(fstest.MapFS{tc.file: {Data: []byte("SELECT 1;")}}, "m")
			if err == nil {
				t.Fatalf("readMigrations accepted %q", tc.file)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q does not mention %q", err, tc.wantErr)
			}
		})
	}
}

func TestReadMigrationsRejectsDuplicateVersions(t *testing.T) {
	// Two people adding a migration on separate branches. Whichever sorts
	// second would silently never run.
	_, err := readMigrations(fstest.MapFS{
		"m/0001_first.sql": {Data: []byte("SELECT 1;")},
		"m/0001_other.sql": {Data: []byte("SELECT 2;")},
	}, "m")

	if err == nil {
		t.Fatal("readMigrations accepted two migrations at the same version")
	}
	if !strings.Contains(err.Error(), "share version 1") {
		t.Errorf("error %q does not say which version collided", err)
	}
}

func TestSplitStatements(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want []string
	}{
		{
			name: "one statement per semicolon",
			in:   "CREATE TABLE a (x INT);\nCREATE TABLE b (y INT);\n",
			want: []string{"CREATE TABLE a (x INT)", "CREATE TABLE b (y INT)"},
		},
		{
			name: "a statement spanning lines stays whole",
			in:   "CREATE TABLE a (\n  x INT,\n  y INT\n);\n",
			want: []string{"CREATE TABLE a (\n  x INT,\n  y INT\n)"},
		},
		{
			// The reason comments are stripped: every migration here documents
			// its columns, and a semicolon in prose would cut a CREATE TABLE in
			// half.
			name: "comments are dropped",
			in:   "-- a note; with a semicolon\nCREATE TABLE a (x INT);\n",
			want: []string{"CREATE TABLE a (x INT)"},
		},
		{
			name: "an indented comment is dropped too",
			in:   "CREATE TABLE a (\n    -- why; this column exists\n    x INT\n);\n",
			want: []string{"CREATE TABLE a (\n    x INT\n)"},
		},
		{
			name: "a trailing statement with no semicolon still counts",
			in:   "CREATE TABLE a (x INT)",
			want: []string{"CREATE TABLE a (x INT)"},
		},
		{
			name: "an empty file yields nothing",
			in:   "\n\n-- only comments\n",
			want: nil,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := splitStatements(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("got %d statements %q, want %d", len(got), got, len(tc.want))
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("statement %d:\n got: %q\nwant: %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestShippedMigrationsSplitCleanly(t *testing.T) {
	// The shipped files are heavily commented, and those comments contain
	// semicolons. This asserts the splitter copes with the real ones rather
	// than only with the fixtures above.
	for _, dialect := range []string{"sqlite", "postgres", "mysql"} {
		migrations, err := (Embedded{}).Migrations(dialect)
		if err != nil {
			t.Fatalf("Migrations(%q): %v", dialect, err)
		}
		for _, m := range migrations {
			for _, stmt := range splitStatements(m.SQL) {
				if strings.HasPrefix(strings.TrimSpace(stmt), "--") {
					t.Errorf("%s/%04d: a statement is only a comment: %q", dialect, m.Version, stmt)
				}
				if !strings.Contains(strings.ToUpper(stmt), "CREATE") {
					t.Errorf("%s/%04d: unexpected statement %q", dialect, m.Version, firstLine(stmt))
				}
			}
		}
	}
}

func TestFirstLineTruncates(t *testing.T) {
	long := "CREATE TABLE " + strings.Repeat("x", 200)
	got := firstLine(long)
	if len(got) != 80 {
		t.Errorf("got %d characters, want 80", len(got))
	}
	if !strings.HasSuffix(got, "...") {
		t.Errorf("a truncated line should say so: %q", got)
	}
}
