package sqlstore

import "testing"

// The dialects are the only place the four backends differ, so they are worth
// asserting directly rather than only through the contract suite: a wrong
// placeholder shows up there as an opaque driver error, and here as the actual
// string that was wrong.

func TestPlaceholders(t *testing.T) {
	for _, tc := range []struct {
		dialect Dialect
		want    []string
	}{
		{SQLite{}, []string{"?", "?", "?"}},
		// The one difference that reaches every statement rather than just the
		// writes, and the reason nothing here hand-writes a placeholder.
		{Postgres{}, []string{"$1", "$2", "$3"}},
		{MySQL{}, []string{"?", "?", "?"}},
	} {
		t.Run(tc.dialect.Name(), func(t *testing.T) {
			for i, want := range tc.want {
				if got := tc.dialect.Placeholder(i + 1); got != want {
					t.Errorf("Placeholder(%d) = %q, want %q", i+1, got, want)
				}
			}
		})
	}
}

func TestQuotingIsPerDialect(t *testing.T) {
	// `key` is a reserved word in MySQL, which is why every identifier is
	// quoted rather than only the ones that look risky.
	for _, tc := range []struct {
		dialect Dialect
		want    string
	}{
		{SQLite{}, `"key"`},
		{Postgres{}, `"key"`},
		{MySQL{}, "`key`"},
	} {
		if got := tc.dialect.Quote("key"); got != tc.want {
			t.Errorf("%s: Quote(key) = %s, want %s", tc.dialect.Name(), got, tc.want)
		}
	}
}

func TestUpsert(t *testing.T) {
	for _, tc := range []struct {
		name    string
		dialect Dialect
		want    string
	}{
		{
			name:    "sqlite uses excluded",
			dialect: SQLite{},
			want: `INSERT INTO "services" ("id", "key") VALUES (?, ?)` +
				` ON CONFLICT ("id") DO UPDATE SET "key" = excluded."key"`,
		},
		{
			name:    "postgres numbers its placeholders",
			dialect: Postgres{},
			want: `INSERT INTO "services" ("id", "key") VALUES ($1, $2)` +
				` ON CONFLICT ("id") DO UPDATE SET "key" = excluded."key"`,
		},
		{
			// VALUES() rather than the newer alias form, which MariaDB and
			// MySQL 5.7 both reject.
			name:    "mysql keys off the unique index",
			dialect: MySQL{},
			want: "INSERT INTO `services` (`id`, `key`) VALUES (?, ?)" +
				" ON DUPLICATE KEY UPDATE `key` = VALUES(`key`)",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := insert(tc.dialect, "services",
				[]string{"id", "key"}, []string{"id"}, []string{"key"})
			if got != tc.want {
				t.Errorf("\n got: %s\nwant: %s", got, tc.want)
			}
		})
	}
}

func TestUpsertWithNothingToUpdate(t *testing.T) {
	// The caller asked for an upsert, so a conflicting row should be left alone
	// rather than rejected. MySQL has no DO NOTHING and needs a self-assignment
	// to say the same thing.
	for _, tc := range []struct {
		dialect Dialect
		want    string
	}{
		{SQLite{}, ` ON CONFLICT ("version") DO NOTHING`},
		{Postgres{}, ` ON CONFLICT ("version") DO NOTHING`},
		{MySQL{}, " ON DUPLICATE KEY UPDATE `version` = `version`"},
	} {
		t.Run(tc.dialect.Name(), func(t *testing.T) {
			if got := tc.dialect.UpsertSuffix([]string{"version"}, nil); got != tc.want {
				t.Errorf("\n got: %s\nwant: %s", got, tc.want)
			}
		})
	}
}

func TestMySQLUpsertWithNoConflictColumn(t *testing.T) {
	// Nothing to name and nothing to update: the clause is omitted rather than
	// emitted as a syntax error.
	if got := (MySQL{}).UpsertSuffix(nil, nil); got != "" {
		t.Errorf("got %q, want an empty clause", got)
	}
}

func TestInsertWithoutConflictIsAPlainInsert(t *testing.T) {
	got := insert(Postgres{}, "samples", []string{"service_id", "ts"}, nil, nil)
	want := `INSERT INTO "samples" ("service_id", "ts") VALUES ($1, $2)`
	if got != want {
		t.Errorf("\n got: %s\nwant: %s", got, want)
	}
}

func TestSelect(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		where, order, expect string
	}{
		{
			name:   "bare",
			expect: `SELECT "id", "day" FROM "history_daily"`,
		},
		{
			name:   "with a where",
			where:  cmp(Postgres{}, "day", ">=", 1),
			expect: `SELECT "id", "day" FROM "history_daily" WHERE "day" >= $1`,
		},
		{
			name:   "with a where and an order",
			where:  cmp(Postgres{}, "day", ">=", 1),
			order:  `"day"`,
			expect: `SELECT "id", "day" FROM "history_daily" WHERE "day" >= $1 ORDER BY "day"`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := selectAll(Postgres{}, "history_daily", []string{"id", "day"}, tc.where, tc.order)
			if got != tc.expect {
				t.Errorf("\n got: %s\nwant: %s", got, tc.expect)
			}
		})
	}
}

func TestDialectFor(t *testing.T) {
	for _, tc := range []struct{ driver, want string }{
		{"sqlite", "sqlite"},
		{"postgres", "postgres"},
		{"mysql", "mysql"},
		// MariaDB shares MySQL's dialect and its migrations.
		{"mariadb", "mysql"},
	} {
		d, err := dialectFor(tc.driver)
		if err != nil {
			t.Fatalf("dialectFor(%q): %v", tc.driver, err)
		}
		if d.Name() != tc.want {
			t.Errorf("dialectFor(%q) = %q, want %q", tc.driver, d.Name(), tc.want)
		}
	}

	if _, err := dialectFor("oracle"); err == nil {
		t.Error("dialectFor accepted a driver with no dialect")
	}
}
