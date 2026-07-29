package server

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/apimap"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/mapping"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/rules"
)

// The API surface a deployment integrates against, and the console that
// documents it.
//
// Three endpoints and two pages, and the relationship between them is the point:
// push writes, the read API reads back what was written, and the pull preview
// answers the question a poller cannot be asked interactively — "given this
// document and this mapping, what would you make of it?". A deployment can
// compare the two feed models by running both rather than by reading about them.

// handleServices is the read API.
//
// The contract dashboard.proto has always described. It is registered
// unconditionally and unauthenticated because it returns exactly what the public
// HTML already shows: gating the machine-readable form of a page anyone can load
// would be theatre, not access control.
func (s *Server) handleServices(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("cache-control", "no-store")
	s.writeProto(w, r, apimap.SnapshotToProto(s.source.Snapshot()))
}

// PullPreviewRequest is the dry-run body: an upstream's document, and the
// mapping under consideration.
//
// The mapping is the same type sources.yaml declares, with JSON tags mirroring
// its YAML keys, so the block moves between the two by copy and paste.
type PullPreviewRequest struct {
	Document any          `json:"document"`
	Mapping  mapping.Spec `json:"mapping"`
}

// PullPreviewResponse is what the dashboard would make of it.
//
// Services are raw JSON because they are encoded by protojson rather than by
// encoding/json — the same spelling the read API uses.
type PullPreviewResponse struct {
	Services []json.RawMessage `json:"services"`
	// Skipped names the records the mapping could not read. A poller logs these
	// and carries on; here they are the answer.
	Skipped []PullPreviewSkip `json:"skipped,omitempty"`
}

// PullPreviewSkip is one record the mapping could not read.
type PullPreviewSkip struct {
	Index  int    `json:"index"`
	Reason string `json:"reason"`
}

// handlePullPreview compiles a mapping, runs it over a document, and evaluates
// the result.
//
// Pull is a contract the dashboard consumes rather than an endpoint it serves,
// so until now the only way to find out whether a mapping worked was to edit
// sources.yaml, restart, and look at the board. This is the same three pure
// functions the poller uses — Compile, Apply, Finalise — with the fetch left out.
//
// Leaving the fetch out is deliberate: an endpoint that took a URL and retrieved
// it would let anyone use the dashboard to make requests from inside its own
// network. The document travels in the body.
func (s *Server) handlePullPreview(w http.ResponseWriter, r *http.Request) {
	body, err := readBody(r, s.bodyLimit())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var req PullPreviewRequest
	dec := json.NewDecoder(bytes.NewReader(body))
	// A misspelled key is the commonest mistake in a mapping and the hardest to
	// see, so it is rejected rather than ignored — the same choice the ingest
	// endpoint makes about unknown fields.
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		http.Error(w, "the body is not a valid preview request: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Document == nil {
		http.Error(w, "no document; send the JSON your upstream would serve", http.StatusBadRequest)
		return
	}

	mapper, err := mapping.Compile(req.Mapping)
	if err != nil {
		// Compile's own text names the field and lists what was expected, which
		// is more use than anything this layer could add.
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	result := mapper.Apply(req.Document)

	// Finalise is what makes this worth running: the response carries the status
	// and the trends the dashboard would derive, not merely the fields that were
	// read. A mapping can succeed and still produce a board of unknowns.
	resp := PullPreviewResponse{Services: []json.RawMessage{}}
	for _, sv := range rules.Finalise(result.Services, s.cfg.Domain, s.clock.Now()) {
		// Through protojson rather than encoding/json, so a service here is
		// spelled exactly as one from the read API: int64 as strings, enums by
		// name, and none of the generated struct's internals.
		raw, err := protojson.Marshal(apimap.ServiceToProto(sv))
		// coverage:ignore -- unreachable: apimap builds the message, so it is
		// always well formed. Reported rather than dropped so that a future
		// field protojson cannot render fails here instead of silently.
		if err != nil {
			s.fail(w, r, err)
			return
		}
		resp.Services = append(resp.Services, canonicalise(raw))
	}
	for _, skip := range result.Skipped {
		resp.Skipped = append(resp.Skipped, PullPreviewSkip{Index: skip.Index, Reason: skip.Reason})
	}

	out, err := json.MarshalIndent(resp, "", "  ")
	// coverage:ignore -- unreachable: the response holds raw JSON already proven
	// valid above, and two integers.
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.Header().Set("content-type", contentJSON+"; charset=utf-8")
	w.Header().Set("cache-control", "no-store")
	_, _ = w.Write(append(out, '\n'))
}

// canonicalise re-encodes protojson output so its keys are ordered.
//
// protojson varies its whitespace on purpose to discourage byte comparison,
// which makes a response awkward to diff in a collector's tests. The same
// round-trip is what the ingest response and the seed fixtures already do.
func canonicalise(raw []byte) json.RawMessage {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return raw
	}
	// coverage:ignore -- unreachable: a value that just decoded from JSON
	// re-encodes. The fallback exists so the caller gets the message either way.
	out, err := json.Marshal(v)
	if err != nil {
		return raw
	}
	return out
}

// bodyLimit is the cap on a request body.
//
// The ingest limit governs both endpoints. A deployment that has not configured
// push still gets the default, so the preview is never uncapped.
func (s *Server) bodyLimit() int64 {
	if s.ingest.MaxBodyBytes > 0 {
		return s.ingest.MaxBodyBytes
	}
	return defaultBodyLimit
}

// defaultBodyLimit matches source.DefaultMaxBodyBytes. It is restated rather
// than imported because internal/source imports the pull driver, and the server
// has no business depending on either.
const defaultBodyLimit = 32 << 20

func readBody(r *http.Request, limit int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("reading the request body: %w", err)
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("the payload exceeds the %d byte limit", limit)
	}
	if len(body) == 0 {
		return nil, fmt.Errorf("the request body is empty")
	}
	return body, nil
}

// --- the published document -------------------------------------------------

// handleOpenAPI serves the committed document, overlaid with what is true of
// this deployment.
//
// Two operations are conditional on the source driver, so a document served
// unchanged would send an integrator at an endpoint that answers 404. The
// overlay is written into the operation's own description because that is the
// one channel every renderer displays; a custom extension would be invisible.
func (s *Server) handleOpenAPI(w http.ResponseWriter, r *http.Request) {
	doc, err := s.openAPIDocument()
	if err != nil {
		s.fail(w, r, err)
		return
	}
	body, err := json.MarshalIndent(doc, "", "  ")
	// coverage:ignore -- unreachable: doc came from json.Unmarshal, so every
	// value in it is one encoding/json produced.
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.Header().Set("content-type", "application/json; charset=utf-8")
	w.Header().Set("cache-control", "no-store")
	_, _ = w.Write(append(body, '\n'))
}

func (s *Server) openAPIDocument() (map[string]any, error) {
	var doc map[string]any
	if err := json.Unmarshal(s.openAPI, &doc); err != nil {
		return nil, fmt.Errorf("the embedded OpenAPI document is not valid JSON: %w", err)
	}

	// A relative server URL, so the console's requests go back to whatever origin
	// served the page. Naming an absolute address would mean the server guessing
	// its own external name, which behind a proxy it cannot do.
	doc["servers"] = []any{map[string]any{"url": "/", "description": "This deployment."}}

	paths, _ := doc["paths"].(map[string]any)
	for _, rt := range s.Routes() {
		if rt.Available {
			continue
		}
		item, _ := paths[rt.Spec].(map[string]any)
		op, _ := item[strings.ToLower(rt.Method)].(map[string]any)
		if op == nil {
			continue
		}
		desc, _ := op["description"].(string)
		op["description"] = unavailableNote(rt.Spec) + desc
	}
	return doc, nil
}

// unavailableNote says what is switched off and what would switch it on.
//
// Naming the line to change is the difference between a reader concluding the
// dashboard is broken and a reader fixing it in one edit.
func unavailableNote(specPath string) string {
	switch specPath {
	case "/api/v1/ingest":
		return "> **Not available on this deployment.** The push endpoint is registered only when " +
			"`config/sources.yaml` sets `driver: push` and the environment variable named by " +
			"`push.tokenEnvVar` holds a token. An ingest endpoint that exists but rejects everything " +
			"is still an endpoint someone can probe, so it is absent rather than closed.\n\n"
	case "/__example/upstream/services":
		return "> **Not available on this deployment.** The example upstream is served only when the " +
			"dashboard holds its own data — `driver: seed` or `driver: push`. Under `driver: pull` it " +
			"is absent, so a deployment cannot re-publish a copy of what it polls.\n\n"
	default:
		return "> **Not available on this deployment.**\n\n"
	}
}

// --- the console ------------------------------------------------------------

// apiReferenceAsset is the vendored renderer, stored gzipped.
//
// RapiDoc 9.3.8, MIT, committed the way htmx.min.js is: no package.json, no
// build step. To update it:
//
//	curl -sSL https://cdn.jsdelivr.net/npm/rapidoc@<version>/dist/rapidoc-min.js \
//	  | gzip -9 -n > web/static/api-reference.js.gz
//
// -n drops the timestamp, so the same release always produces the same bytes and
// the file does not churn in the history.
//
// Stored compressed because it unpacks to 863 KB and travels as 218 KB: on a
// twenty-two megabyte binary that is the difference between a documentation page
// costing a twenty-fifth of the artefact and a twentieth. The handler below
// hands the stored bytes straight to a browser.
//
// The choice of renderer is not only about size. The better-known alternative
// puts a "Powered by" credit, an AI assistant and a button that hands this
// deployment's own address to a hosted service with tracking parameters onto a
// page a government publishes about its own services, and offers no setting that
// removes any of it. This one renders the document and nothing else.
const apiReferenceAsset = "api-reference.js"

// APIConsoleData is the console shell.
type APIConsoleData struct {
	Title        string
	Locale       string
	Direction    string
	Theme        string
	Favicon      string
	AssetVersion string
	SpecURL      string
	ScriptURL    string

	SkipText     string
	BackLabel    string
	NoScriptText string

	// Colours and Fonts hand the renderer the deployment's own tokens, so a
	// restyled theme.yaml restyles the API reference with it rather than leaving
	// a third party's defaults sitting inside the dashboard's chrome. They are
	// passed as attributes rather than inherited, because the component draws
	// into a shadow root that ordinary rules cannot reach into.
	Colours APIColours
	Fonts   APIFonts

	// The three requests, as commands. Code rather than copy, which is why they
	// are here rather than in a locale file.
	CurlIngest   string
	CurlServices string
	CurlPreview  string
}

// APIColours is the handful of tokens the renderer takes as attributes.
type APIColours struct{ Bg, Fg, Card, MutedFg, Accent string }

// APIFonts is the two stacks it takes.
type APIFonts struct{ Body, Mono string }

func (s *Server) handleAPIConsole(w http.ResponseWriter, r *http.Request) {
	st := s.readState(r)
	text := s.locales.For(st.Locale)

	data := APIConsoleData{
		Title:        text.Text("api.title", nil),
		Locale:       st.Locale,
		Direction:    text.Direction(),
		Theme:        st.Theme,
		Favicon:      s.cfg.Brand.Favicon,
		AssetVersion: s.assetVersion,
		SpecURL:      "/api/openapi.json",
		ScriptURL:    "/assets/" + apiReferenceAsset + "?v=" + s.assetVersion,

		SkipText:     text.Text("api.skip", nil),
		BackLabel:    text.Text("api.back", nil),
		NoScriptText: text.Text("api.noscript", nil),

		Colours:      s.apiColours(st.Theme),
		Fonts:        s.apiFonts(),
		CurlIngest:   curlIngest,
		CurlServices: curlServices,
		CurlPreview:  curlPreview,
	}

	var buf bytes.Buffer
	// coverage:ignore -- unreachable: the template is embedded and parsed at
	// startup, and a missing block would have failed the renderer's own check.
	if err := s.render.Fragment(&buf, "api-console", data); err != nil {
		s.fail(w, r, err)
		return
	}
	w.Header().Set("content-type", "text/html; charset=utf-8")
	w.Header().Set("cache-control", "no-store")
	_, _ = w.Write(buf.Bytes())
}

// apiColours reads the five tokens the renderer needs out of theme.yaml.
//
// The renderer takes colours as attributes rather than as custom properties, so
// unlike everything else here they cannot simply be inherited — they have to be
// looked up. Which half of the palette depends on the reader's choice; with no
// choice recorded the light one is used, because that is what the stylesheet
// falls back to when the operating system has no opinion either.
func (s *Server) apiColours(theme string) APIColours {
	palette := s.cfg.Theme.Light
	if theme == "dark" {
		palette = s.cfg.Theme.Dark
	}
	at := func(token, fallback string) string {
		if v, ok := palette[token]; ok && v != "" {
			return v
		}
		return fallback
	}
	return APIColours{
		Bg:      at("--bg", "#ffffff"),
		Fg:      at("--fg", "#000000"),
		Card:    at("--card", "#ffffff"),
		MutedFg: at("--muted-fg", "#666666"),
		Accent:  at("--accent", "#0055aa"),
	}
}

// apiFonts is the type the reference is set in.
//
// The body stack comes from theme.yaml so the reference reads as part of the
// dashboard. The monospace one does not: no deployment configures a code face,
// and a request body is code.
func (s *Server) apiFonts() APIFonts {
	body := s.cfg.Theme.Fonts.Body.Stack
	if body == "" {
		body = "system-ui, sans-serif"
	}
	return APIFonts{Body: body, Mono: "ui-monospace, SFMono-Regular, Menlo, Consolas, monospace"}
}

const (
	curlIngest = `curl -X POST "$DASHBOARD/api/v1/ingest" \
  -H "authorization: Bearer $DPI_INGEST_TOKEN" \
  -H "content-type: application/json" \
  --data @examples/push/payload.json`

	curlServices = `curl "$DASHBOARD/api/v1/services"`

	curlPreview = `curl -X POST "$DASHBOARD/api/v1/pull/preview" \
  -H "content-type: application/json" \
  --data '{
    "document": [ { "svc": "aadhaar", "up": 0.9992 } ],
    "mapping": {
      "map": { "id": "$.svc", "metrics.availability": "$.up" },
      "transform": { "metrics.availability": [ { "fn": "ratioToPercent" } ] }
    }
  }'`
)

// handleAPIReference serves the vendored bundle from the gzip it is stored as.
//
// Nearly every client accepts gzip and gets the bytes handed straight over. The
// one that does not is decompressed here rather than being sent something it
// cannot read, because a stored-compressed asset is a storage decision and must
// not become part of the contract.
func (s *Server) handleAPIReference(w http.ResponseWriter, r *http.Request) {
	raw, err := fs.ReadFile(s.static, "web/static/"+apiReferenceAsset+".gz")
	if err != nil {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("content-type", "text/javascript; charset=utf-8")
	w.Header().Set("cache-control", "public, max-age=31536000, immutable")
	w.Header().Set("etag", `"`+s.assetVersion+`"`)
	// Vary, because two clients asking for the same URL get different bytes.
	w.Header().Set("vary", "accept-encoding")

	if strings.Contains(r.Header.Get("accept-encoding"), "gzip") {
		w.Header().Set("content-encoding", "gzip")
		http.ServeContent(w, r, apiReferenceAsset, time.Time{}, bytes.NewReader(raw))
		return
	}

	zr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	defer zr.Close()
	plain, err := io.ReadAll(zr)
	// coverage:ignore -- unreachable: the gzip header already parsed, and the
	// body behind it is a committed file rather than a stream that can be cut.
	if err != nil {
		s.fail(w, r, err)
		return
	}
	http.ServeContent(w, r, apiReferenceAsset, time.Time{}, bytes.NewReader(plain))
}

// --- shared writers ---------------------------------------------------------

// writeProto replies in whatever the caller asked for.
func (s *Server) writeProto(w http.ResponseWriter, r *http.Request, m proto.Message) {
	if strings.Contains(r.Header.Get("accept"), contentProto) {
		body, err := proto.Marshal(m)
		// coverage:ignore -- unreachable: the message is built here from the
		// snapshot, so it is always well formed.
		if err != nil {
			s.fail(w, r, err)
			return
		}
		w.Header().Set("content-type", contentProto)
		_, _ = w.Write(body)
		return
	}

	body, err := protojson.Marshal(m)
	// coverage:ignore -- unreachable, for the same reason as the branch above.
	if err != nil {
		s.fail(w, r, err)
		return
	}
	// Re-encoded for stable key ordering: protojson varies its whitespace on
	// purpose, which makes a response awkward to diff in a collector's tests.
	var canonical any
	_ = json.Unmarshal(body, &canonical)
	pretty, _ := json.MarshalIndent(canonical, "", "  ")

	w.Header().Set("content-type", contentJSON+"; charset=utf-8")
	_, _ = w.Write(append(pretty, '\n'))
}
