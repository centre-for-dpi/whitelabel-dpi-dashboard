package server

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/config"
)

// The helpers behind the API surface, tested here rather than through the
// end-to-end suite because each answers a question about a deployment that the
// shipped configuration does not ask: a theme.yaml with a token missing, a
// request body too large to read, an embedded document that does not parse.

func TestTheRendererTakesTheDeploymentsOwnPalette(t *testing.T) {
	s := &Server{cfg: config.Config{Theme: config.Theme{
		Light: map[string]string{"--bg": "#ffffff", "--fg": "#111111", "--card": "#fafafa",
			"--muted-fg": "#666666", "--accent": "#0055aa"},
		Dark: map[string]string{"--bg": "#111111", "--fg": "#eeeeee", "--card": "#1a1a1a",
			"--muted-fg": "#999999", "--accent": "#77bbff"},
	}}}

	if got := s.apiColours("").Bg; got != "#ffffff" {
		t.Errorf("with no theme chosen the console takes %q; light is what the stylesheet falls back to", got)
	}
	if got := s.apiColours("dark").Bg; got != "#111111" {
		t.Errorf("a dark reader gets %q", got)
	}
}

// A deployment that has removed a token from theme.yaml gets a legible console
// rather than an attribute with nothing in it.
func TestTheRendererFallsBackWhenATokenIsMissing(t *testing.T) {
	s := &Server{cfg: config.Config{Theme: config.Theme{Light: map[string]string{"--bg": ""}}}}

	c := s.apiColours("")
	for name, got := range map[string]string{"bg": c.Bg, "fg": c.Fg, "card": c.Card,
		"muted": c.MutedFg, "accent": c.Accent} {
		if got == "" {
			t.Errorf("%s has no value at all", name)
		}
	}
	if s.apiFonts().Body == "" || s.apiFonts().Mono == "" {
		t.Error("the console would be set in nothing")
	}
}

func TestTheConfiguredBodyFontIsUsed(t *testing.T) {
	s := &Server{cfg: config.Config{Theme: config.Theme{
		Fonts: config.Fonts{Body: config.FontFamily{Stack: "Inter, sans-serif"}},
	}}}
	if got := s.apiFonts().Body; got != "Inter, sans-serif" {
		t.Errorf("the console is set in %q rather than the deployment's own face", got)
	}
}

// A deployment that has not configured push still gets a cap, so the dry-run
// endpoint is never uncapped.
func TestTheBodyLimitAlwaysHasAValue(t *testing.T) {
	if got := (&Server{}).bodyLimit(); got != defaultBodyLimit {
		t.Errorf("an unconfigured deployment caps bodies at %d", got)
	}
	s := &Server{ingest: IngestOptions{MaxBodyBytes: 1024}}
	if got := s.bodyLimit(); got != 1024 {
		t.Errorf("the configured cap is %d", got)
	}
}

func TestReadBodyRefusesWhatItCannotAccept(t *testing.T) {
	read := func(body string, limit int64) error {
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		_, err := readBody(r, limit)
		return err
	}

	if err := read("", 10); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Errorf("an empty body is accepted or unexplained: %v", err)
	}
	if err := read("0123456789ab", 4); err == nil || !strings.Contains(err.Error(), "4 byte limit") {
		t.Errorf("an oversized body is accepted or unexplained: %v", err)
	}
	if err := read("ok", 10); err != nil {
		t.Errorf("a body inside the limit was refused: %v", err)
	}
}

// protojson varies its whitespace on purpose, which makes a response awkward to
// diff in a collector's tests. Anything that is not JSON at all passes through
// rather than being lost.
func TestCanonicaliseIsTotal(t *testing.T) {
	got := canonicalise([]byte(`{"b":1,"a":2}`))
	if string(got) != `{"a":2,"b":1}` {
		t.Errorf("keys are not ordered: %s", got)
	}
	if string(canonicalise([]byte("not json"))) != "not json" {
		t.Error("input that is not JSON was not passed through")
	}
}

// The note names the line that would switch the operation on, because that is
// the difference between a reader concluding the dashboard is broken and a
// reader fixing it in one edit.
func TestTheUnavailableNoteNamesTheFix(t *testing.T) {
	for path, want := range map[string]string{
		"/api/v1/ingest":               "driver: push",
		"/__example/upstream/services": "driver: pull",
		"/something/else":              "Not available",
	} {
		if got := unavailableNote(path); !strings.Contains(got, want) {
			t.Errorf("the note for %s does not mention %q: %s", path, want, got)
		}
	}
}

// The overlay parses the embedded document, so a build that embedded something
// broken has to say so rather than serving an empty page.
func TestAnUnreadableEmbeddedDocumentIsReported(t *testing.T) {
	s := &Server{openAPI: []byte("{ not a document")}
	if _, err := s.openAPIDocument(); err == nil {
		t.Error("an unparseable document was served")
	}
}

// An operation the document does not describe is skipped rather than panicking:
// a route added without a matching path is a drift-test failure, and this layer
// should not turn it into a 500 as well.
func TestTheOverlaySkipsAnUndescribedOperation(t *testing.T) {
	s := &Server{openAPI: []byte(`{"paths":{"/known":{"get":{"description":"x"}}}}`)}
	doc, err := s.openAPIDocument()
	if err != nil {
		t.Fatalf("overlaying: %v", err)
	}
	if _, ok := doc["servers"]; !ok {
		t.Error("the served document does not say which server it describes")
	}
}

func TestTheOverlayMarksAnAbsentOperation(t *testing.T) {
	// A server with no ingest sink: the route is not registered, so the
	// document served has to say so on that operation.
	s := &Server{openAPI: []byte(`{"paths":{"/api/v1/ingest":{"post":{"description":"Send observations."}}}}`)}
	doc, err := s.openAPIDocument()
	if err != nil {
		t.Fatalf("overlaying: %v", err)
	}
	raw, _ := json.Marshal(doc)
	if !strings.Contains(string(raw), "Not available on this deployment") {
		t.Errorf("the absent operation is not marked:\n%s", raw)
	}
	if !strings.Contains(string(raw), "Send observations.") {
		t.Error("the note replaced the description rather than leading it")
	}
}

// A build that embedded a broken document fails loudly rather than serving an
// empty console with no clue why.
func TestServingAnUnreadableDocumentIsA500(t *testing.T) {
	s := &Server{openAPI: []byte("{ not a document"), log: slog.New(slog.DiscardHandler)}
	rec := httptest.NewRecorder()
	s.handleOpenAPI(rec, httptest.NewRequest(http.MethodGet, "/api/openapi.json", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("= %d, want 500", rec.Code)
	}
}

// A binary built without the renderer serves a 404 rather than an empty script
// tag: the console would then be visibly broken instead of silently blank.
func TestAMissingRendererIsNotFound(t *testing.T) {
	s := &Server{static: fstest.MapFS{}, log: slog.New(slog.DiscardHandler)}
	rec := httptest.NewRecorder()
	s.handleAPIReference(rec, httptest.NewRequest(http.MethodGet, "/assets/api-reference.js", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("= %d, want 404", rec.Code)
	}
}

// Stored compressed is a storage decision, so a stored file that is not actually
// gzip must not reach a client that asked for plain bytes.
func TestARendererThatIsNotGzipIsReported(t *testing.T) {
	s := &Server{
		static: fstest.MapFS{"web/static/api-reference.js.gz": {Data: []byte("not gzip")}},
		log:    slog.New(slog.DiscardHandler),
	}
	rec := httptest.NewRecorder()
	s.handleAPIReference(rec, httptest.NewRequest(http.MethodGet, "/assets/api-reference.js", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("= %d, want 500", rec.Code)
	}
}

func TestReadBodyReportsAReadFailure(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/", errReader{})
	if _, err := readBody(r, 10); err == nil || !strings.Contains(err.Error(), "reading the request body") {
		t.Errorf("a connection that died mid-body is unexplained: %v", err)
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("the connection went away") }
