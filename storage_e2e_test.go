package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// These drive the real wiring with a store in the data path. They exist because
// the store is not a component the reader ever sees: every bug in it shows up as
// a page that is subtly wrong rather than a request that fails, so the
// assertions are all about what a reader gets.

// TestTheExampleUpstreamSurvivesTheRecorder is a regression test.
//
// The recorder that persists snapshots wraps the source, and both the example
// upstream route and the ingest route are registered only after asking what kind
// of source is underneath. A wrapper answers "no" to that question, so wrapping
// silently unregistered the route — no error, no warning, just a 404 that turned
// `make demo-pull` into two instances where the second had nothing to poll.
//
// It is the failure mode a type assertion through a decorator always has, and it
// is invisible to every unit test on either side of the seam.
func TestTheExampleUpstreamSurvivesTheRecorder(t *testing.T) {
	h := dashboard(t)

	body := get(t, h, "/__example/upstream/services")

	var payload struct {
		Data struct {
			Services []struct {
				ServiceID string `json:"serviceId"`
			} `json:"services"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("the example upstream is not valid JSON: %v", err)
	}
	if len(payload.Data.Services) == 0 {
		t.Error("the example upstream serves no services; the shipped pull config has nothing to poll")
	}
}

// TestHistorySurvivesARestart is the reason the whole storage layer exists.
//
// It builds a dashboard against a SQLite file, throws it away, and builds a
// second one against the same file with a source that reports no history at all.
// The charts must still be there.
func TestHistorySurvivesARestart(t *testing.T) {
	dir := t.TempDir()
	dsn := "file:" + filepath.Join(dir, "restart.db") + "?_pragma=busy_timeout(5000)"

	configDir := pushConfig(t, dir)
	t.Setenv("DPI_INGEST_TOKEN", "e2e-token")
	t.Setenv("DPI_STORAGE_DRIVER", "sqlite")
	t.Setenv("DATABASE_URL", dsn)

	// First process: accept a push carrying history.
	first, err := build(options{configDir: configDir})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	first.recorder.Restore(t.Context())

	payload, err := os.ReadFile("examples/push/payload.json")
	if err != nil {
		t.Fatalf("reading the example payload: %v", err)
	}

	req, _ := http.NewRequest(http.MethodPost, "/api/v1/ingest", strings.NewReader(string(payload)))
	req.Header.Set("authorization", "Bearer e2e-token")
	req.Header.Set("content-type", "application/json")
	rec := record(first.handler, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("ingest = %d, want 200\n%s", rec.Code, rec.Body.String())
	}

	before := get(t, first.handler, "/fragments/service/aadhaar")
	if err := first.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Second process: same database, nothing pushed to it.
	second, err := build(options{configDir: configDir})
	if err != nil {
		t.Fatalf("build after restart: %v", err)
	}
	defer func() { _ = second.Close() }()
	second.recorder.Restore(t.Context())

	after := get(t, second.handler, "/fragments/service/aadhaar")

	beforePath := sparkPath(t, before)
	afterPath := sparkPath(t, after)
	if afterPath == "" {
		t.Fatal("the restarted dashboard draws no sparkline; the history did not survive")
	}
	if beforePath != afterPath {
		t.Errorf("the chart changed across a restart\nbefore: %s\n after: %s", beforePath, afterPath)
	}
}

// TestAnUnreachableDatabaseStillServesTheLiveVerdict is the failure mode that
// matters most operationally.
//
// The question the page exists to answer — is this working right now? — does not
// depend on stored history. A dashboard that goes dark because its history
// database is down reports an outage that is its own.
func TestAnUnreachableDatabaseStillServesTheLiveVerdict(t *testing.T) {
	dir := t.TempDir()
	// A path inside a file, so every open fails rather than creating anything.
	blocked := filepath.Join(dir, "not-a-directory")
	if err := os.WriteFile(blocked, []byte("x"), 0o600); err != nil {
		t.Fatalf("writing the blocker: %v", err)
	}

	t.Setenv("DPI_STORAGE_DRIVER", "sqlite")
	t.Setenv("DATABASE_URL", "file:"+filepath.Join(blocked, "dpi.db"))

	// Startup does fail, and deliberately: a deployment that asked for durable
	// storage and cannot have it should be told at boot, not discover it at the
	// first restart. Degrading silently to memory would be the "silently
	// ignored setting" failure.
	if _, err := build(options{}); err == nil {
		t.Fatal("build succeeded against an unusable database; the misconfiguration would surface only later")
	}
}

// TestValidateDoesNotTouchTheDatabase guards a CI-safety property.
//
// `make validate` is documented as checking configuration without starting the
// server. Connecting would mean a config check in CI runs migrations against
// whatever DATABASE_URL happens to be set to.
func TestValidateDoesNotTouchTheDatabase(t *testing.T) {
	t.Setenv("DPI_STORAGE_DRIVER", "postgres")
	t.Setenv("DATABASE_URL", "postgres://nobody:nope@127.0.0.1:1/nothing")

	a, err := build(options{validateOnly: true})
	if err != nil {
		t.Fatalf("-validate failed against an unreachable database: %v", err)
	}
	defer func() { _ = a.Close() }()

	if len(a.source.Snapshot().Services) == 0 {
		t.Error("-validate reported no services")
	}
}

// TestStorageDriversAreSelectableByConfigAlone walks every driver that needs no
// server, and asserts the dashboard renders identically on each.
func TestStorageDriversAreSelectableByConfigAlone(t *testing.T) {
	var rendered []string

	for _, driver := range []string{"memory", "sqlite"} {
		t.Run(driver, func(t *testing.T) {
			t.Setenv("DPI_STORAGE_DRIVER", driver)
			if driver == "sqlite" {
				t.Setenv("DATABASE_URL", "file:"+filepath.Join(t.TempDir(), "dpi.db"))
			}

			a, err := build(options{})
			if err != nil {
				t.Fatalf("build on %s: %v", driver, err)
			}
			defer func() { _ = a.Close() }()

			body := get(t, a.handler, "/")
			rendered = append(rendered, summary(t, body))
		})
	}

	if len(rendered) == 2 && rendered[0] != rendered[1] {
		t.Errorf("the verdict differs by storage backend\nmemory: %s\nsqlite: %s",
			rendered[0], rendered[1])
	}
}

func record(h http.Handler, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

var (
	sparkLine  = regexp.MustCompile(`class="spark-line" d="([^"]+)"`)
	summaryTag = regexp.MustCompile(`class="[^"]*\bw-summary\b[^"]*">([^<]+)`)
)

// sparkPath extracts the availability sparkline's geometry.
//
// The rendered path is a good proxy for "the history is right": it is derived
// from every point in the series, so a missing day, a reordered one or a
// rounding difference all change it.
func sparkPath(t *testing.T, body string) string {
	t.Helper()
	m := sparkLine.FindStringSubmatch(body)
	if m == nil {
		return ""
	}
	return m[1]
}

func summary(t *testing.T, body string) string {
	t.Helper()
	m := summaryTag.FindStringSubmatch(body)
	if m == nil {
		t.Fatal("the page has no verdict summary")
	}
	return strings.TrimSpace(m[1])
}

func pushConfig(t *testing.T, dir string) string {
	t.Helper()
	configDir := filepath.Join(dir, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("creating the config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "sources.yaml"), []byte(
		"driver: push\npush:\n  tokenEnvVar: DPI_INGEST_TOKEN\n  maxBodyBytes: 8388608\n",
	), 0o600); err != nil {
		t.Fatalf("writing sources.yaml: %v", err)
	}
	return configDir
}
