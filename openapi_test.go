package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/apispec"
	dpiv1 "github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/gen/dpi/v1"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/server"
)

// The published document is committed so a clean clone can hand it to a code
// generator, and so the binary can embed and serve it. That only holds if it
// stays in step with the wire contracts, so drift is a test failure rather than
// something an integrator discovers by writing a client against a field that
// changed shape.
func TestGeneratedOpenAPIIsUpToDate(t *testing.T) {
	files, err := apispec.All(os.DirFS("."))
	if err != nil {
		t.Fatalf("generating the document: %v", err)
	}

	for _, f := range files {
		path := filepath.Join("api", f.Name)
		committed, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("%s is missing; run `make openapi` and commit the result", path)
			continue
		}
		if string(committed) != string(f.JSON) {
			t.Errorf("%s is out of date; run `make openapi` and commit the result", path)
		}
	}
}

// --- the document and the router agree -------------------------------------

func committedDocument(t *testing.T) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("api", apispec.FileName))
	if err != nil {
		t.Fatalf("reading the committed document: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("the committed document is not valid JSON: %v", err)
	}
	return doc
}

func routes(t *testing.T, h http.Handler) []server.Route {
	t.Helper()
	srv, ok := h.(*server.Server)
	if !ok {
		t.Fatalf("the dashboard handler is %T, not a *server.Server", h)
	}
	return srv.Routes()
}

// The claim this whole document makes is that it describes the whole surface.
// Checked in both directions, because either half alone is satisfiable by doing
// nothing: a route nobody documented is invisible to an integrator, and a
// documented path that is not a route sends them somewhere that answers 404.
func TestEveryRouteIsDocumented(t *testing.T) {
	paths, _ := committedDocument(t)["paths"].(map[string]any)

	for _, rt := range routes(t, dashboard(t)) {
		if rt.Spec == "" {
			t.Errorf("%s %s names no path in the document", rt.Method, rt.Pattern)
			continue
		}
		item, ok := paths[rt.Spec].(map[string]any)
		if !ok {
			t.Errorf("%s %s is documented as %q, which the document does not describe",
				rt.Method, rt.Pattern, rt.Spec)
			continue
		}
		if _, ok := item[strings.ToLower(rt.Method)]; !ok {
			t.Errorf("the document describes %q but not its %s operation", rt.Spec, rt.Method)
		}
	}
}

func TestEveryDocumentedOperationIsARoute(t *testing.T) {
	// Every route this configuration could register, whether or not it did:
	// the document covers the whole surface, and says per operation which parts
	// of it this deployment switched off.
	served := map[string]bool{}
	for _, rt := range routes(t, dashboard(t)) {
		served[strings.ToLower(rt.Method)+" "+rt.Spec] = true
	}
	for _, rt := range routes(t, pushDashboard(t)) {
		served[strings.ToLower(rt.Method)+" "+rt.Spec] = true
	}

	paths, _ := committedDocument(t)["paths"].(map[string]any)
	for path, item := range paths {
		operations, _ := item.(map[string]any)
		for method := range operations {
			if !served[method+" "+path] {
				t.Errorf("the document describes %s %s, which no route serves", strings.ToUpper(method), path)
			}
		}
	}
}

// --- the worked examples work ----------------------------------------------

// examplesFor returns the named request examples for one operation.
func examplesFor(t *testing.T, path, method string) map[string]any {
	t.Helper()
	paths, _ := committedDocument(t)["paths"].(map[string]any)
	item, _ := paths[path].(map[string]any)
	op, _ := item[method].(map[string]any)
	body, _ := op["requestBody"].(map[string]any)
	content, _ := body["content"].(map[string]any)
	json, _ := content["application/json"].(map[string]any)
	out, _ := json["examples"].(map[string]any)
	if len(out) == 0 {
		t.Fatalf("%s %s carries no request examples", strings.ToUpper(method), path)
	}
	return out
}

func exampleValue(t *testing.T, examples map[string]any, name string) []byte {
	t.Helper()
	entry, ok := examples[name].(map[string]any)
	if !ok {
		t.Fatalf("no example named %q", name)
	}
	raw, err := json.Marshal(entry["value"])
	if err != nil {
		t.Fatalf("re-encoding the %s example: %v", name, err)
	}
	return raw
}

// A worked example that does not work is worse than none: it is a wrong answer
// to the first question every integrator asks. Each one is sent at the real
// handler, which is also what keeps the trimming that makes them readable from
// quietly making them invalid.
func TestEveryIngestExampleIsAccepted(t *testing.T) {
	h := pushDashboard(t)

	for name := range examplesFor(t, "/api/v1/ingest", "post") {
		t.Run(name, func(t *testing.T) {
			rec := post(t, h, string(exampleValue(t, examplesFor(t, "/api/v1/ingest", "post"), name)))
			resp := decodeResponse(t, rec)
			if resp.GetAccepted() == 0 {
				t.Errorf("the %s example was accepted for nothing", name)
			}
			if resp.GetRejected() != 0 {
				t.Errorf("the %s example had %d records rejected: %v",
					name, resp.GetRejected(), resp.GetErrors())
			}
		})
	}
}

func TestEveryPullPreviewExampleIsAccepted(t *testing.T) {
	h := dashboard(t)

	for name := range examplesFor(t, "/api/v1/pull/preview", "post") {
		t.Run(name, func(t *testing.T) {
			body := exampleValue(t, examplesFor(t, "/api/v1/pull/preview", "post"), name)
			req := httptest.NewRequest(http.MethodPost, "/api/v1/pull/preview", bytes.NewReader(body))
			req.Header.Set("content-type", "application/json")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("= %d: %s", rec.Code, rec.Body.String())
			}
			var out struct {
				Services []map[string]any `json:"services"`
				Skipped  []any            `json:"skipped"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
				t.Fatalf("the response is not JSON: %v", err)
			}
			if len(out.Services) == 0 {
				t.Errorf("the %s example maps to no services", name)
			}
			if len(out.Skipped) != 0 {
				t.Errorf("the %s example skipped %d records", name, len(out.Skipped))
			}
			// The point of the endpoint: a mapping that reads every field it was
			// asked to can still produce a board of unknowns, so the example has
			// to prove the derivation as well as the reading.
			for _, sv := range out.Services {
				if sv["status"] == "" || sv["status"] == nil {
					t.Errorf("the %s example derives no status for %v", name, sv["id"])
				}
			}
		})
	}
}

// --- the served document describes this deployment --------------------------

func servedDocument(t *testing.T, h http.Handler) map[string]any {
	t.Helper()
	body := get(t, h, "/api/openapi.json")
	var doc map[string]any
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("the served document is not valid JSON: %v", err)
	}
	return doc
}

func operationDescription(t *testing.T, doc map[string]any, path, method string) string {
	t.Helper()
	paths, _ := doc["paths"].(map[string]any)
	item, _ := paths[path].(map[string]any)
	op, _ := item[method].(map[string]any)
	desc, _ := op["description"].(string)
	return desc
}

// The committed document describes the whole surface; the served one has to be
// honest about which of it this configuration actually registers. Without that,
// the reference on a seed deployment would offer an ingest endpoint that is not
// there, and the first thing an integrator tried would 404 with no explanation.
func TestTheServedDocumentSaysWhatThisDeploymentRegisters(t *testing.T) {
	const unavailable = "Not available on this deployment"

	seeded := servedDocument(t, dashboard(t))
	if got := operationDescription(t, seeded, "/api/v1/ingest", "post"); !strings.Contains(got, unavailable) {
		t.Error("under driver: seed the document does not say the ingest endpoint is absent")
	}
	if !strings.Contains(operationDescription(t, seeded, "/api/v1/ingest", "post"), "driver: push") {
		t.Error("the note does not say which line would enable it")
	}
	if got := operationDescription(t, seeded, "/api/v1/services", "get"); strings.Contains(got, unavailable) {
		t.Error("the read API is registered everywhere but is marked absent")
	}

	pushed := servedDocument(t, pushDashboard(t))
	if got := operationDescription(t, pushed, "/api/v1/ingest", "post"); strings.Contains(got, unavailable) {
		t.Error("under driver: push the ingest endpoint is marked absent")
	}
}

// A relative server URL, so the console's requests go back to whatever origin
// served the page. An absolute one would mean the server guessing its own
// external name, which behind a proxy it cannot do.
func TestTheServedDocumentPointsAtThisOrigin(t *testing.T) {
	servers, _ := servedDocument(t, dashboard(t))["servers"].([]any)
	if len(servers) != 1 {
		t.Fatalf("the served document declares %d servers, want 1", len(servers))
	}
	entry, _ := servers[0].(map[string]any)
	if entry["url"] != "/" {
		t.Errorf("the served document points at %q, want a relative %q", entry["url"], "/")
	}
}

// --- the new endpoints ------------------------------------------------------

func TestTheReadAPIReturnsWhatWasPushed(t *testing.T) {
	// The contract dashboard.proto has always described, and until now never
	// served. What goes in through ingest has to come back out.
	h := pushDashboard(t)
	if resp := decodeResponse(t, post(t, h, twoServices)); resp.GetAccepted() != 2 {
		t.Fatalf("the fixture was not accepted: %v", resp)
	}

	var out struct {
		Services []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"services"`
		GeneratedAt string `json:"generatedAt"`
	}
	if err := json.Unmarshal([]byte(get(t, h, "/api/v1/services")), &out); err != nil {
		t.Fatalf("the response is not JSON: %v", err)
	}
	if len(out.Services) != 2 {
		t.Fatalf("read back %d services, want 2", len(out.Services))
	}
	if out.GeneratedAt == "" {
		t.Error("the response does not say when the snapshot was taken")
	}
	// Derived on the way out, not carried through from the payload — the ingest
	// message has no field for it.
	for _, sv := range out.Services {
		if sv.Status == "" || sv.Status == "STATUS_UNSPECIFIED" {
			t.Errorf("%s came back with no derived status", sv.ID)
		}
	}
}

func TestThePullPreviewReportsWhatAMappingWouldDo(t *testing.T) {
	h := dashboard(t)

	preview := func(t *testing.T, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/pull/preview", strings.NewReader(body))
		req.Header.Set("content-type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	t.Run("a record with no id is reported rather than dropped silently", func(t *testing.T) {
		rec := preview(t, `{"document":[{"svc":"a"},{"other":1}],"mapping":{"map":{"id":"$.svc"}}}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("= %d: %s", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), `"reason": "no id"`) {
			t.Errorf("the skipped record is not explained:\n%s", rec.Body.String())
		}
	})

	t.Run("a ratio where a percentage was expected shows up as the derived status", func(t *testing.T) {
		// The failure this endpoint exists to catch: the mapping reads every
		// field it was asked to, and the board still comes out wrong.
		raw := preview(t, `{"document":[{"svc":"a","up":0.992}],"mapping":{"map":{"id":"$.svc","metrics.availability":"$.up"}}}`)
		if !strings.Contains(raw.Body.String(), "STATUS_MAJOR") {
			t.Errorf("an availability of 0.992%% is not reported as an outage:\n%s", raw.Body.String())
		}
		fixed := preview(t, `{"document":[{"svc":"a","up":0.992}],"mapping":{"map":{"id":"$.svc","metrics.availability":"$.up"},
		    "transform":{"metrics.availability":[{"fn":"ratioToPercent"}]}}}`)
		if !strings.Contains(fixed.Body.String(), "STATUS_PARTIAL") {
			t.Errorf("the corrected mapping does not produce a partial outage:\n%s", fixed.Body.String())
		}
	})

	t.Run("a mapping that will not compile says which field", func(t *testing.T) {
		rec := preview(t, `{"document":[{"svc":"a"}],"mapping":{"map":{"nope":"$.svc"}}}`)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("= %d, want 400", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), `unknown field "nope"`) {
			t.Errorf("the error does not name the field:\n%s", rec.Body.String())
		}
	})

	t.Run("no url is fetched", func(t *testing.T) {
		// The document travels in the body. An endpoint that took a URL and
		// retrieved it would let anyone use the dashboard to make requests from
		// inside its own network.
		rec := preview(t, `{"url":"http://169.254.169.254/","mapping":{"map":{"id":"$.svc"}}}`)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("= %d, want 400: a url field must not be accepted at all", rec.Code)
		}
	})
}

// --- the console ------------------------------------------------------------

func TestTheConsoleIsServedEverywhere(t *testing.T) {
	// A deployment carries its own documentation, whichever driver it runs.
	for _, h := range []http.Handler{dashboard(t), pushDashboard(t)} {
		body := get(t, h, "/api")
		if !strings.Contains(body, `spec-url="/api/openapi.json"`) {
			t.Error("the console does not point at the served document")
		}
		// Both defaults reach off this origin, and the page must not.
		if !strings.Contains(body, `load-fonts="false"`) {
			t.Error("the console would fetch a typeface from a third party")
		}
		// The dashboard's whole story is that it works without script.
		if !strings.Contains(body, "<noscript>") || !strings.Contains(body, "curl") {
			t.Error("the console says nothing useful without JavaScript")
		}
	}
}

func TestTheConsoleRendererIsServedFromTheBinary(t *testing.T) {
	h := dashboard(t)

	req := httptest.NewRequest(http.MethodGet, "/assets/api-reference.js", nil)
	req.Header.Set("accept-encoding", "gzip")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("= %d", rec.Code)
	}
	if got := rec.Header().Get("content-encoding"); got != "gzip" {
		t.Errorf("content-encoding = %q, want gzip: the asset is stored compressed", got)
	}
	if got := rec.Header().Get("vary"); !strings.Contains(got, "accept-encoding") {
		t.Errorf("vary = %q; two clients asking for this URL get different bytes", got)
	}

	// A client that does not accept gzip gets it decompressed rather than
	// something it cannot read: how the asset is stored is not part of the
	// contract.
	plain := httptest.NewRecorder()
	h.ServeHTTP(plain, httptest.NewRequest(http.MethodGet, "/assets/api-reference.js", nil))
	if plain.Header().Get("content-encoding") != "" {
		t.Error("a client that did not ask for gzip was sent gzip")
	}
	if !bytes.Contains(plain.Body.Bytes(), []byte("rapi-doc")) {
		t.Error("the decompressed asset is not the renderer")
	}
}

// The read API answers in whatever the caller asked for, the same way the ingest
// response does. A team with curl and a shell script never needs to learn
// protobuf exists; a team that has already generated a client should not have to
// parse JSON to use it.
func TestTheReadAPINegotiatesItsWireFormat(t *testing.T) {
	h := pushDashboard(t)
	if resp := decodeResponse(t, post(t, h, twoServices)); resp.GetAccepted() != 2 {
		t.Fatalf("the fixture was not accepted: %v", resp)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/services", nil)
	req.Header.Set("accept", "application/x-protobuf")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("= %d", rec.Code)
	}
	if got := rec.Header().Get("content-type"); got != "application/x-protobuf" {
		t.Errorf("content-type = %q", got)
	}
	var out dpiv1.ListServicesResponse
	if err := proto.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("the response is not a ListServicesResponse: %v", err)
	}
	if len(out.GetServices()) != 2 {
		t.Errorf("read back %d services, want 2", len(out.GetServices()))
	}
}
