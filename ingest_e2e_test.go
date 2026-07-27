package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	dpiv1 "github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/gen/dpi/v1"
)

const token = "t0ken"

// pushDashboard builds the real binary configured for push, so these tests
// exercise the same wiring a deployment gets.
func pushDashboard(t *testing.T) http.Handler {
	t.Helper()

	dir := t.TempDir()
	raw, err := configFS.ReadFile("config/sources.yaml")
	if err != nil {
		t.Fatal(err)
	}
	src := strings.Replace(string(raw), "driver: seed", "driver: push", 1)
	if err := os.WriteFile(filepath.Join(dir, "sources.yaml"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DPI_INGEST_TOKEN", token)

	a, err := build(options{configDir: dir})
	if err != nil {
		t.Fatalf("push configuration does not start: %v", err)
	}
	return a.handler
}

func post(t *testing.T, h http.Handler, body string, headers ...string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/ingest", strings.NewReader(body))
	req.Header.Set("authorization", "Bearer "+token)
	req.Header.Set("content-type", "application/json")
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decodeResponse(t *testing.T, rec *httptest.ResponseRecorder) *dpiv1.IngestResponse {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("= %d: %s", rec.Code, rec.Body.String())
	}
	var resp dpiv1.IngestResponse
	if err := protojson.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("the response is not an IngestResponse: %v\n%s", err, rec.Body.String())
	}
	return &resp
}

const twoServices = `{"mode":"INGEST_MODE_REPLACE","sourceId":"test","services":[
  {"id":"aadhaar","nameTermId":"Aadhaar","categoryId":"cat.identity","regionId":"reg.national","scope":"national",
   "metrics":{"availability":99.91,"errorRate":0.4,"volume":{"total":"2900000","success":"2888400"}}},
  {"id":"pan","nameTermId":"PAN","categoryId":"cat.identity","regionId":"reg.national","scope":"national",
   "metrics":{"availability":97.2,"errorRate":3.1}}
]}`

func TestPushStartsEmpty(t *testing.T) {
	// Seeding a push deployment would show invented data until the first real
	// payload arrived, which is exactly the confusion a status page must not
	// create.
	h := pushDashboard(t)

	if got := rows(get(t, h, "/")); got != 0 {
		t.Errorf("got %d rows before any push", got)
	}
	// And it says it is not ready, so a rolling deploy does not cut traffic to
	// an empty dashboard.
	if got := status(t, h, "/readyz"); got != http.StatusServiceUnavailable {
		t.Errorf("/readyz = %d, want 503 before any data arrives", got)
	}
}

func TestACollectorWithCurlAndJSONCanPush(t *testing.T) {
	// The promise in examples/README.md: protobuf is never an obligation.
	h := pushDashboard(t)

	resp := decodeResponse(t, post(t, h, twoServices))

	if resp.GetAccepted() != 2 || resp.GetRejected() != 0 {
		t.Fatalf("accepted %d, rejected %d", resp.GetAccepted(), resp.GetRejected())
	}
	if resp.GetReceivedAt() == nil {
		t.Error("no receipt time")
	}
	if got := rows(get(t, h, "/")); got != 2 {
		t.Errorf("got %d rows after the push", got)
	}
	if got := status(t, h, "/readyz"); got != http.StatusOK {
		t.Errorf("/readyz = %d after data arrived", got)
	}
}

func TestStatusIsDerivedFromThePushedNumbers(t *testing.T) {
	// A collector reports observations; the dashboard decides verdicts. A
	// service at 97.2% is a major outage whatever its operator would prefer to
	// call it.
	h := pushDashboard(t)
	post(t, h, twoServices)

	body := get(t, h, "/")
	if !strings.Contains(body, "1 Operational") || !strings.Contains(body, "1 Major outage") {
		t.Errorf("verdict does not follow from the pushed numbers:\n%s",
			firstMatch(body, `class="w w-summary[^"]*">[^<]+`))
	}
}

func TestPushedServicesWithNoAvailabilityReadAsUnknown(t *testing.T) {
	// Not as an outage. "We cannot tell" and "it is down" are different claims.
	h := pushDashboard(t)
	post(t, h, `{"services":[{"id":"quiet","categoryId":"cat.identity","regionId":"reg.national","scope":"national","metrics":{"errorRate":0.1}}]}`)

	if !strings.Contains(get(t, h, "/"), "1 Unknown") {
		t.Error("a service with no availability reading was not reported as unknown")
	}
}

func TestBinaryProtobufIsAcceptedToo(t *testing.T) {
	// The same message, for teams that want a generated client.
	h := pushDashboard(t)

	var req dpiv1.IngestRequest
	if err := protojson.Unmarshal([]byte(twoServices), &req); err != nil {
		t.Fatal(err)
	}
	body, err := proto.Marshal(&req)
	if err != nil {
		t.Fatal(err)
	}

	httpReq := httptest.NewRequest(http.MethodPost, "/api/v1/ingest", bytes.NewReader(body))
	httpReq.Header.Set("authorization", "Bearer "+token)
	httpReq.Header.Set("content-type", "application/x-protobuf")
	httpReq.Header.Set("accept", "application/x-protobuf")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httpReq)

	if rec.Code != http.StatusOK {
		t.Fatalf("= %d: %s", rec.Code, rec.Body.String())
	}
	var resp dpiv1.IngestResponse
	if err := proto.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("the response is not binary protobuf: %v", err)
	}
	if resp.GetAccepted() != 2 {
		t.Errorf("accepted %d", resp.GetAccepted())
	}
}

func TestUpsertLeavesUnmentionedServicesAlone(t *testing.T) {
	// What lets several collectors each own a slice of the estate.
	h := pushDashboard(t)
	post(t, h, twoServices)

	post(t, h, `{"mode":"INGEST_MODE_UPSERT","services":[
	  {"id":"pan","nameTermId":"PAN","categoryId":"cat.identity","regionId":"reg.national","scope":"national",
	   "metrics":{"availability":99.99,"errorRate":0.01}}
	]}`)

	if got := rows(get(t, h, "/")); got != 2 {
		t.Errorf("got %d rows; the unmentioned service was dropped", got)
	}
	// And the mentioned one was updated.
	if !strings.Contains(get(t, h, "/"), "2 Operational") {
		t.Error("the upserted service's status was not recomputed")
	}
}

func TestReplaceDropsWhatIsAbsent(t *testing.T) {
	// The collector owns the whole picture.
	h := pushDashboard(t)
	post(t, h, twoServices)

	post(t, h, `{"mode":"INGEST_MODE_REPLACE","services":[
	  {"id":"pan","categoryId":"cat.identity","regionId":"reg.national","scope":"national","metrics":{"availability":99.99}}
	]}`)

	if got := rows(get(t, h, "/")); got != 1 {
		t.Errorf("got %d rows, want only what the payload named", got)
	}
}

func TestPartialPayloadsArePartiallyAccepted(t *testing.T) {
	// One malformed record in a batch of two hundred must not reject the other
	// hundred and ninety-nine.
	h := pushDashboard(t)

	resp := decodeResponse(t, post(t, h, `{"services":[
	  {"id":"good","categoryId":"cat.identity","regionId":"reg.national","scope":"national"},
	  {"id":"badCategory","categoryId":"cat.invented"},
	  {"id":"badScope","scope":"galactic"},
	  {"id":"impossible","metrics":{"availability":140}},
	  {"id":"", "categoryId":"cat.identity"}
	]}`))

	if resp.GetAccepted() != 1 {
		t.Errorf("accepted %d, want the one good record", resp.GetAccepted())
	}
	if resp.GetRejected() != 4 {
		t.Errorf("rejected %d, want 4", resp.GetRejected())
	}
	// The response says exactly which and why, so a collector's own tests can
	// assert on it.
	joined := ""
	for _, e := range resp.GetErrors() {
		joined += e.GetServiceId() + ":" + e.GetField() + ":" + e.GetMessage() + "\n"
	}
	for _, want := range []string{"cat.invented", "galactic", "outside 0-100", "required"} {
		if !strings.Contains(joined, want) {
			t.Errorf("errors omit %q:\n%s", want, joined)
		}
	}
}

func TestMoreSuccessesThanRequestsIsRejected(t *testing.T) {
	h := pushDashboard(t)

	resp := decodeResponse(t, post(t, h, `{"services":[
	  {"id":"impossible","metrics":{"volume":{"total":"100","success":"200"}}}
	]}`))

	if resp.GetRejected() != 1 {
		t.Errorf("rejected %d, want 1", resp.GetRejected())
	}
}

// --- authentication ----------------------------------------------------------

func TestIngestRequiresACredential(t *testing.T) {
	h := pushDashboard(t)

	for _, tc := range []struct{ name, auth string }{
		{"none", ""},
		{"wrong token", "Bearer wrong"},
		{"not a bearer scheme", "Basic " + token},
		{"the token without the scheme", token},
		// A prefix of the real token, which a timing attack would try.
		{"a prefix of the token", "Bearer t0k"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/ingest", strings.NewReader(twoServices))
			if tc.auth != "" {
				req.Header.Set("authorization", tc.auth)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Errorf("= %d, want 401", rec.Code)
			}
			// The rejection says nothing about why, which would help a caller
			// guess at a valid credential.
			if strings.Contains(rec.Body.String(), token) {
				t.Error("the response echoes the expected token")
			}
		})
	}
	// And nothing was stored.
	if got := rows(get(t, h, "/")); got != 0 {
		t.Errorf("got %d rows after only unauthorised attempts", got)
	}
}

func TestTheIngestRouteIsAbsentWhenPushIsNotConfigured(t *testing.T) {
	// A deployment on seed or pull does not expose an endpoint it never meant
	// to. An endpoint that exists and rejects everything is still one to probe.
	h := dashboard(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/ingest", strings.NewReader(twoServices))
	req.Header.Set("authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("= %d, want the route not to exist", rec.Code)
	}
}

// --- malformed input ----------------------------------------------------------

func TestMalformedPayloadsAreRefusedClearly(t *testing.T) {
	h := pushDashboard(t)

	for _, tc := range []struct{ name, body, want string }{
		{"not JSON", "<xml/>", "not a valid IngestRequest"},
		{"empty", "", "empty"},
		{"an unknown field", `{"services":[{"id":"a","metricz":{}}]}`, "not a valid"},
		{"an array instead of an object", `[{"id":"a"}]`, "not a valid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := post(t, h, tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("= %d, want 400: %s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tc.want) {
				t.Errorf("error does not mention %q: %s", tc.want, rec.Body.String())
			}
		})
	}
}

func TestAnOversizedPayloadIsRefused(t *testing.T) {
	// A runaway collector must not exhaust memory.
	h := pushDashboard(t)

	var b strings.Builder
	b.WriteString(`{"services":[`)
	for i := range 400000 {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`{"id":"padding-service-with-a-long-identifier-`)
		b.WriteString(strings.Repeat("x", 60))
		b.WriteString(`"}`)
	}
	b.WriteString(`]}`)

	rec := post(t, h, b.String())
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("= %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "limit") {
		t.Errorf("error does not mention the limit: %s", rec.Body.String())
	}
}

func TestAnEmptyServiceListIsAccepted(t *testing.T) {
	// A collector reporting "I have nothing to say" in replace mode is
	// legitimate, and means the estate is empty rather than that the request
	// was wrong.
	h := pushDashboard(t)

	resp := decodeResponse(t, post(t, h, `{"mode":"INGEST_MODE_REPLACE","services":[]}`))
	if resp.GetAccepted() != 0 || resp.GetRejected() != 0 {
		t.Errorf("accepted %d rejected %d", resp.GetAccepted(), resp.GetRejected())
	}
}

func TestTheResponseIsStableJSON(t *testing.T) {
	// protojson varies its whitespace on purpose, which makes a response
	// awkward to assert on in a collector's own tests.
	h := pushDashboard(t)

	first := post(t, h, twoServices).Body.String()
	var decoded map[string]any
	if err := json.Unmarshal([]byte(first), &decoded); err != nil {
		t.Fatalf("the response is not valid JSON: %v", err)
	}
	if !strings.HasSuffix(first, "\n") {
		t.Error("the response does not end with a newline")
	}
	if !strings.Contains(first, "\n  ") {
		t.Error("the response is not indented")
	}
}

func firstMatch(body, pattern string) string {
	if m := regexp.MustCompile(pattern).FindString(body); m != "" {
		return m
	}
	return "(no match)"
}
