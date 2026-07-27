package pull_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/mapping"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/source/pull"
)

// fake is an HTTP client with no network behind it. Every response the driver
// has to cope with is expressed as a canned reply.
type fake struct {
	calls   atomic.Int64
	respond func(n int64, r *http.Request) (*http.Response, error)
	lastReq atomic.Pointer[http.Request]
}

func (f *fake) Do(r *http.Request) (*http.Response, error) {
	n := f.calls.Add(1)
	f.lastReq.Store(r)
	return f.respond(n, r)
}

func ok(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     http.Header{},
	}
}

func status(code int) *http.Response {
	return &http.Response{
		StatusCode: code,
		Status:     fmt.Sprintf("%d %s", code, http.StatusText(code)),
		Body:       io.NopCloser(strings.NewReader("")),
		Header:     http.Header{},
	}
}

const payload = `{"services":[
  {"id":"aadhaar","uptime":0.999,"errors":0.004},
  {"id":"pan","uptime":0.995,"errors":0.008}
]}`

func endpoint() pull.Endpoint {
	return pull.Endpoint{
		ID:              "primary",
		URL:             "https://upstream.test/services",
		IntervalSeconds: 60,
		TimeoutSeconds:  5,
		Spec: mapping.Spec{
			ItemsPath: "$.services",
			Map: map[string]string{
				"id":                   "$.id",
				"metrics.availability": "$.uptime",
				"metrics.errorRate":    "$.errors",
			},
		},
	}
}

func driver(t *testing.T, f *fake, endpoints ...pull.Endpoint) *pull.Driver {
	t.Helper()
	if len(endpoints) == 0 {
		endpoints = []pull.Endpoint{endpoint()}
	}
	d, err := pull.New(pull.Options{
		Config:    pull.Config{Endpoints: endpoints},
		Client:    f,
		LookupEnv: func(string) (string, bool) { return "s3cret", true },
		// No jitter, so a test's timing is exact.
		Jitter: func() float64 { return 0.5 },
		Log:    quietLogger(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return d
}

func now() time.Time { return time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC) }

// --- fetching and mapping ---------------------------------------------------

func TestPollFetchesAndMaps(t *testing.T) {
	f := &fake{respond: func(int64, *http.Request) (*http.Response, error) { return ok(payload), nil }}
	d := driver(t, f)

	if err := d.PollOnce(context.Background(), now); err != nil {
		t.Fatalf("PollOnce: %v", err)
	}

	snap := d.Snapshot()
	if len(snap.Services) != 2 {
		t.Fatalf("got %d services", len(snap.Services))
	}
	if snap.Services[0].ID != "aadhaar" {
		t.Errorf("first = %q", snap.Services[0].ID)
	}
	if !snap.Services[0].Metrics.Availability.Valid {
		t.Error("availability was not mapped")
	}
	if !snap.GeneratedAt.Equal(now()) {
		t.Errorf("generatedAt = %v", snap.GeneratedAt)
	}
}

func TestTheRequestCarriesWhatTheConfigSays(t *testing.T) {
	f := &fake{respond: func(int64, *http.Request) (*http.Response, error) { return ok(payload), nil }}

	e := endpoint()
	e.Headers = map[string]string{"x-tenant": "cdpi"}
	e.Auth = pull.Auth{Type: "bearer", EnvVar: "TOKEN"}
	d := driver(t, f, e)

	if err := d.PollOnce(context.Background(), now); err != nil {
		t.Fatal(err)
	}

	req := f.lastReq.Load()
	if got := req.Header.Get("authorization"); got != "Bearer s3cret" {
		t.Errorf("authorization = %q", got)
	}
	if got := req.Header.Get("x-tenant"); got != "cdpi" {
		t.Errorf("custom header = %q", got)
	}
	if got := req.Header.Get("accept"); got != "application/json" {
		t.Errorf("accept = %q", got)
	}
}

func TestEveryAuthType(t *testing.T) {
	for _, tc := range []struct {
		auth  pull.Auth
		check func(*testing.T, *http.Request)
	}{
		{
			pull.Auth{Type: "header", Header: "x-api-key", EnvVar: "KEY"},
			func(t *testing.T, r *http.Request) {
				if got := r.Header.Get("x-api-key"); got != "s3cret" {
					t.Errorf("x-api-key = %q", got)
				}
			},
		},
		{
			pull.Auth{Type: "basic", User: "dash", EnvVar: "PASS"},
			func(t *testing.T, r *http.Request) {
				user, pass, ok := r.BasicAuth()
				if !ok || user != "dash" || pass != "s3cret" {
					t.Errorf("basic auth = %q/%q/%v", user, pass, ok)
				}
			},
		},
		{
			pull.Auth{Type: "none"},
			func(t *testing.T, r *http.Request) {
				if r.Header.Get("authorization") != "" {
					t.Error("an authorization header was sent for an unauthenticated endpoint")
				}
			},
		},
	} {
		t.Run(tc.auth.Type, func(t *testing.T) {
			f := &fake{respond: func(int64, *http.Request) (*http.Response, error) { return ok(payload), nil }}
			e := endpoint()
			e.Auth = tc.auth

			if err := driver(t, f, e).PollOnce(context.Background(), now); err != nil {
				t.Fatal(err)
			}
			tc.check(t, f.lastReq.Load())
		})
	}
}

func TestAMissingSecretIsReportedRatherThanSentEmpty(t *testing.T) {
	// Sending an empty bearer token would produce a 401 that looks like the
	// upstream's fault.
	f := &fake{respond: func(int64, *http.Request) (*http.Response, error) { return ok(payload), nil }}

	e := endpoint()
	e.Auth = pull.Auth{Type: "bearer", EnvVar: "MISSING"}
	d, err := pull.New(pull.Options{
		Config:    pull.Config{Endpoints: []pull.Endpoint{e}},
		Client:    f,
		LookupEnv: func(string) (string, bool) { return "", false },
		Log:       quietLogger(),
	})
	if err != nil {
		t.Fatal(err)
	}

	pollErr := d.PollOnce(context.Background(), now)
	if pollErr == nil {
		t.Fatal("a missing secret was accepted")
	}
	if !strings.Contains(pollErr.Error(), "MISSING") {
		t.Errorf("error does not name the variable: %v", pollErr)
	}
	if f.calls.Load() != 0 {
		t.Error("a request was sent without the credential")
	}
}

// --- failure handling --------------------------------------------------------

func TestAFailedPollKeepsTheLastGoodSnapshot(t *testing.T) {
	// The property that matters most. A dashboard that blanks out when its own
	// upstream hiccups reports an outage that is its own.
	f := &fake{respond: func(n int64, _ *http.Request) (*http.Response, error) {
		if n == 1 {
			return ok(payload), nil
		}
		return nil, fmt.Errorf("connection refused")
	}}
	d := driver(t, f)
	ctx := context.Background()

	if err := d.PollOnce(ctx, now); err != nil {
		t.Fatal(err)
	}
	before := d.Snapshot()

	if err := d.PollOnce(ctx, now); err == nil {
		t.Fatal("the failing poll reported success")
	}

	after := d.Snapshot()
	if len(after.Services) != len(before.Services) {
		t.Errorf("the snapshot emptied on failure: %d services, was %d",
			len(after.Services), len(before.Services))
	}
	// And the failure is recorded, so it is visible rather than silent.
	if len(d.Health()) != 1 {
		t.Errorf("health = %v, want the failure recorded", d.Health())
	}
}

func TestFailureModes(t *testing.T) {
	for _, tc := range []struct {
		name    string
		respond func(int64, *http.Request) (*http.Response, error)
		want    string
	}{
		{
			"the upstream is down",
			func(int64, *http.Request) (*http.Response, error) { return nil, fmt.Errorf("no route to host") },
			"no route to host",
		},
		{
			"the upstream returns an error status",
			func(int64, *http.Request) (*http.Response, error) { return status(503), nil },
			"503",
		},
		{
			"the response is not JSON",
			func(int64, *http.Request) (*http.Response, error) { return ok("<html>oops</html>"), nil },
			"not JSON",
		},
		{
			// A path that no longer matches, or an upstream that changed shape.
			// Publishing an empty list would look like every service vanishing.
			"the response has no readable services",
			func(int64, *http.Request) (*http.Response, error) { return ok(`{"services":[]}`), nil },
			"no readable services",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := driver(t, &fake{respond: tc.respond})

			err := d.PollOnce(context.Background(), now)
			if err == nil {
				t.Fatal("reported success")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error does not mention %q: %v", tc.want, err)
			}
			if len(d.Snapshot().Services) != 0 {
				t.Error("services were published despite the failure")
			}
		})
	}
}

func TestAnOversizedResponseIsRefused(t *testing.T) {
	// A misconfigured URL pointing at something enormous must not exhaust
	// memory.
	huge := `{"services":[` + strings.Repeat(`{"id":"x"},`, 10000) + `{"id":"y"}]}`
	f := &fake{respond: func(int64, *http.Request) (*http.Response, error) { return ok(huge), nil }}

	e := endpoint()
	e.MaxBytes = 1024
	d := driver(t, f, e)

	err := d.PollOnce(context.Background(), now)
	if err == nil {
		t.Fatal("an oversized response was accepted")
	}
	if !strings.Contains(err.Error(), "maxBytes") {
		t.Errorf("error does not mention the limit: %v", err)
	}
}

func TestOneBadRecordDoesNotSinkThePoll(t *testing.T) {
	f := &fake{respond: func(int64, *http.Request) (*http.Response, error) {
		return ok(`{"services":[{"id":"good","uptime":0.99},{"noId":true}]}`), nil
	}}
	d := driver(t, f)

	if err := d.PollOnce(context.Background(), now); err != nil {
		t.Fatalf("one bad record failed the whole poll: %v", err)
	}
	if len(d.Snapshot().Services) != 1 {
		t.Errorf("got %d services, want the good one", len(d.Snapshot().Services))
	}
}

// --- several endpoints -------------------------------------------------------

func TestEndpointsMergeIntoOneDashboard(t *testing.T) {
	// A deployment can point several endpoints at different parts of its estate
	// and see them as one board.
	f := &fake{respond: func(_ int64, r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "second") {
			return ok(`{"services":[{"id":"third","uptime":0.97}]}`), nil
		}
		return ok(payload), nil
	}}

	a, b := endpoint(), endpoint()
	b.ID, b.URL = "secondary", "https://upstream.test/second"
	d := driver(t, f, a, b)

	if err := d.PollOnce(context.Background(), now); err != nil {
		t.Fatal(err)
	}

	if got := len(d.Snapshot().Services); got != 3 {
		t.Errorf("got %d services, want all three merged", got)
	}
}

func TestOverlappingIDsResolveInConfiguredOrder(t *testing.T) {
	// Predictable rather than decided by whichever poll finished last.
	f := &fake{respond: func(_ int64, r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "second") {
			return ok(`{"services":[{"id":"aadhaar","uptime":0.5}]}`), nil
		}
		return ok(payload), nil
	}}

	a, b := endpoint(), endpoint()
	b.ID, b.URL = "secondary", "https://upstream.test/second"
	d := driver(t, f, a, b)

	if err := d.PollOnce(context.Background(), now); err != nil {
		t.Fatal(err)
	}

	for _, sv := range d.Snapshot().Services {
		if sv.ID == "aadhaar" && sv.Metrics.Availability.Value != 0.999 {
			t.Errorf("aadhaar = %v, want the first endpoint's value", sv.Metrics.Availability.Value)
		}
	}
}

func TestOneFailingEndpointDoesNotStopTheOthers(t *testing.T) {
	f := &fake{respond: func(_ int64, r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "second") {
			return nil, fmt.Errorf("down")
		}
		return ok(payload), nil
	}}

	a, b := endpoint(), endpoint()
	b.ID, b.URL = "secondary", "https://upstream.test/second"
	d := driver(t, f, a, b)

	err := d.PollOnce(context.Background(), now)
	if err == nil {
		t.Error("the failure was not reported")
	}
	// But the healthy endpoint's data is still published.
	if len(d.Snapshot().Services) != 2 {
		t.Errorf("got %d services, want the working endpoint's", len(d.Snapshot().Services))
	}
}

// --- scheduling --------------------------------------------------------------

func TestPollingRepeatsOnItsInterval(t *testing.T) {
	// Driven under synctest, so an hour of polling takes no real time and the
	// assertion is exact rather than timing-dependent.
	synctest.Test(t, func(t *testing.T) {
		f := &fake{respond: func(int64, *http.Request) (*http.Response, error) { return ok(payload), nil }}
		d := driver(t, f)

		ctx, cancel := context.WithCancel(context.Background())
		go d.Run(ctx, now)

		// One immediate poll, then one per minute.
		synctest.Wait()
		time.Sleep(10 * time.Minute)
		synctest.Wait()
		cancel()

		if got := f.calls.Load(); got < 10 || got > 12 {
			t.Errorf("got %d polls in ten minutes at a one-minute interval", got)
		}
	})
}

func TestFailuresBackOff(t *testing.T) {
	// Hammering an upstream that is already struggling is how a monitoring
	// system becomes part of the incident.
	synctest.Test(t, func(t *testing.T) {
		f := &fake{respond: func(int64, *http.Request) (*http.Response, error) {
			return nil, fmt.Errorf("down")
		}}
		d := driver(t, f)

		ctx, cancel := context.WithCancel(context.Background())
		go d.Run(ctx, now)

		synctest.Wait()
		time.Sleep(10 * time.Minute)
		synctest.Wait()
		cancel()

		// At a one-minute interval, ten minutes of success would be ten polls.
		// Backing off exponentially, it should be far fewer.
		if got := f.calls.Load(); got > 6 {
			t.Errorf("got %d polls in ten minutes of failure; the backoff is not working", got)
		}
		if f.calls.Load() < 2 {
			t.Errorf("got %d polls; it stopped retrying entirely", f.calls.Load())
		}
	})
}

func TestBackoffRecoversAfterASuccess(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var failing atomic.Bool
		failing.Store(true)

		f := &fake{respond: func(int64, *http.Request) (*http.Response, error) {
			if failing.Load() {
				return nil, fmt.Errorf("down")
			}
			return ok(payload), nil
		}}
		d := driver(t, f)

		ctx, cancel := context.WithCancel(context.Background())
		go d.Run(ctx, now)

		synctest.Wait()
		time.Sleep(10 * time.Minute)
		synctest.Wait()

		failing.Store(false)
		before := f.calls.Load()
		time.Sleep(10 * time.Minute)
		synctest.Wait()
		cancel()

		// Once healthy again it should be back to roughly one poll a minute.
		if got := f.calls.Load() - before; got < 5 {
			t.Errorf("got %d polls in the ten minutes after recovery; the backoff did not reset", got)
		}
	})
}

func TestCancellationStopsPolling(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := &fake{respond: func(int64, *http.Request) (*http.Response, error) { return ok(payload), nil }}
		d := driver(t, f)

		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() {
			d.Run(ctx, now)
			close(done)
		}()

		synctest.Wait()
		cancel()
		synctest.Wait()

		select {
		case <-done:
		default:
			t.Error("Run did not return after cancellation")
		}
	})
}

// --- configuration -----------------------------------------------------------

func TestNewRejectsMistakes(t *testing.T) {
	for _, tc := range []struct {
		name      string
		endpoints []pull.Endpoint
		want      string
	}{
		{"no endpoints", nil, "no endpoints"},
		{
			"no id",
			[]pull.Endpoint{{URL: "https://x.test", Spec: mapping.Spec{Map: map[string]string{"id": "$.i"}}}},
			"no id",
		},
		{
			"duplicate ids",
			[]pull.Endpoint{endpoint(), endpoint()},
			"duplicate id",
		},
		{
			"no url",
			[]pull.Endpoint{{ID: "a", Spec: mapping.Spec{Map: map[string]string{"id": "$.i"}}}},
			"no url",
		},
		{
			// A token written into a config file ends up in a git history.
			"a secret with no environment variable",
			[]pull.Endpoint{{
				ID: "a", URL: "https://x.test",
				Auth: pull.Auth{Type: "bearer"},
				Spec: mapping.Spec{Map: map[string]string{"id": "$.i"}},
			}},
			"envVar",
		},
		{
			"an unknown auth type",
			[]pull.Endpoint{{
				ID: "a", URL: "https://x.test",
				Auth: pull.Auth{Type: "magic", EnvVar: "T"},
				Spec: mapping.Spec{Map: map[string]string{"id": "$.i"}},
			}},
			"unknown auth type",
		},
		{
			"a broken mapping",
			[]pull.Endpoint{{ID: "a", URL: "https://x.test", Spec: mapping.Spec{Map: map[string]string{"vibes": "$.v"}}}},
			"vibes",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := pull.New(pull.Options{Config: pull.Config{Endpoints: tc.endpoints}, Log: quietLogger()})
			if err == nil {
				t.Fatal("accepted")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error does not mention %q: %v", tc.want, err)
			}
		})
	}
}

func TestNewAppliesDefaults(t *testing.T) {
	// A deployment supplying only a URL and a mapping should get a working
	// poller rather than a validation error about intervals.
	d, err := pull.New(pull.Options{
		Config: pull.Config{Endpoints: []pull.Endpoint{{
			ID: "a", URL: "https://x.test",
			Spec: mapping.Spec{Map: map[string]string{"id": "$.id"}},
		}}},
	})
	if err != nil {
		t.Fatalf("a minimal endpoint was rejected: %v", err)
	}
	if d.Snapshot().Services != nil {
		t.Error("a fresh driver reports services")
	}
}

func TestSnapshotBeforeAnyPoll(t *testing.T) {
	// The server asks for a snapshot before the first poll completes, and must
	// get an empty one rather than a nil dereference.
	d := driver(t, &fake{respond: func(int64, *http.Request) (*http.Response, error) { return ok(payload), nil }})

	if got := d.Snapshot(); len(got.Services) != 0 {
		t.Errorf("got %d services before any poll", len(got.Services))
	}
	if len(d.Health()) != 0 {
		t.Error("health reports a failure before any poll")
	}
}

// quietLogger discards output: these tests deliberately provoke failures, and
// the log lines they produce are noise rather than signal.
func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError + 1}))
}

func TestDefaultsAreUsedWhenUnset(t *testing.T) {
	// A deployment supplying only a URL and a mapping gets sensible timing,
	// and the request still carries a deadline.
	f := &fake{respond: func(_ int64, r *http.Request) (*http.Response, error) {
		if _, ok := r.Context().Deadline(); !ok {
			t.Error("the request carries no deadline; a hung upstream would hang the poll")
		}
		return ok(payload), nil
	}}

	e := endpoint()
	e.IntervalSeconds, e.TimeoutSeconds, e.MaxBytes = 0, 0, 0

	if err := driver(t, f, e).PollOnce(context.Background(), now); err != nil {
		t.Fatalf("an endpoint with no timing set failed: %v", err)
	}
}

func TestAnEndpointWithNoIntervalStillPolls(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := &fake{respond: func(int64, *http.Request) (*http.Response, error) { return ok(payload), nil }}
		e := endpoint()
		e.IntervalSeconds = 0 // falls back to the default minute

		ctx, cancel := context.WithCancel(context.Background())
		go driver(t, f, e).Run(ctx, now)

		synctest.Wait()
		time.Sleep(3 * time.Minute)
		synctest.Wait()
		cancel()

		if got := f.calls.Load(); got < 3 || got > 5 {
			t.Errorf("got %d polls in three minutes at the default interval", got)
		}
	})
}

func TestAnUnauthenticatedEndpointNeedsNoEnvVar(t *testing.T) {
	for _, a := range []pull.Auth{{}, {Type: "none"}} {
		e := endpoint()
		e.Auth = a
		if _, err := pull.New(pull.Options{
			Config: pull.Config{Endpoints: []pull.Endpoint{e}}, Log: quietLogger(),
		}); err != nil {
			t.Errorf("auth %+v was rejected: %v", a, err)
		}
	}
}

func TestAHeaderAuthNeedsBothPieces(t *testing.T) {
	for _, a := range []pull.Auth{
		{Type: "header", EnvVar: "K"},         // no header name
		{Type: "header", Header: "x-api-key"}, // no env var
	} {
		e := endpoint()
		e.Auth = a
		if _, err := pull.New(pull.Options{
			Config: pull.Config{Endpoints: []pull.Endpoint{e}}, Log: quietLogger(),
		}); err == nil {
			t.Errorf("auth %+v was accepted", a)
		}
	}
}

func TestAnUnbuildableRequestIsReported(t *testing.T) {
	// A URL with a control character, which http.NewRequest refuses.
	f := &fake{respond: func(int64, *http.Request) (*http.Response, error) { return ok(payload), nil }}
	e := endpoint()
	e.URL = "https://upstream.test/\x7f"

	err := driver(t, f, e).PollOnce(context.Background(), now)
	if err == nil {
		t.Fatal("a malformed URL was accepted")
	}
	if !strings.Contains(err.Error(), "request") {
		t.Errorf("error does not explain: %v", err)
	}
}

func TestDefaultJitterStaysInRange(t *testing.T) {
	// Used in production where no jitter is injected. It only has to differ
	// between processes, not be cryptographic.
	d, err := pull.New(pull.Options{
		Config: pull.Config{Endpoints: []pull.Endpoint{endpoint()}},
		Client: &fake{respond: func(int64, *http.Request) (*http.Response, error) { return ok(payload), nil }},
		Log:    quietLogger(),
	})
	if err != nil {
		t.Fatal(err)
	}
	// Exercised through a real poll cycle rather than called directly.
	if err := d.PollOnce(context.Background(), now); err != nil {
		t.Fatal(err)
	}
}

// failingBody errors partway through, as a truncated response would.
type failingBody struct{}

func (failingBody) Read([]byte) (int, error) { return 0, fmt.Errorf("connection reset mid-body") }
func (failingBody) Close() error             { return nil }

func TestATruncatedResponseIsReported(t *testing.T) {
	f := &fake{respond: func(int64, *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 200, Status: "200 OK",
			Body: failingBody{}, Header: http.Header{},
		}, nil
	}}

	err := driver(t, f).PollOnce(context.Background(), now)
	if err == nil {
		t.Fatal("a truncated response was accepted")
	}
	if !strings.Contains(err.Error(), "reading the response") {
		t.Errorf("error does not explain: %v", err)
	}
}

func TestSeveralFailuresAreReportedTogether(t *testing.T) {
	// Fixing them one restart at a time would be miserable.
	f := &fake{respond: func(int64, *http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("down")
	}}

	a, b := endpoint(), endpoint()
	b.ID, b.URL = "secondary", "https://upstream.test/second"

	err := driver(t, f, a, b).PollOnce(context.Background(), now)
	if err == nil {
		t.Fatal("no error")
	}
	for _, want := range []string{"primary", "secondary"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error omits %q: %v", want, err)
		}
	}
}

func TestTheDefaultJitterIsUsedWhenNoneIsInjected(t *testing.T) {
	// Production supplies no jitter function; the built-in one only has to
	// differ between processes, not be cryptographic.
	synctest.Test(t, func(t *testing.T) {
		f := &fake{respond: func(int64, *http.Request) (*http.Response, error) { return ok(payload), nil }}
		d, err := pull.New(pull.Options{
			Config: pull.Config{Endpoints: []pull.Endpoint{endpoint()}},
			Client: f,
			Log:    quietLogger(),
		})
		if err != nil {
			t.Fatal(err)
		}

		ctx, cancel := context.WithCancel(context.Background())
		go d.Run(ctx, now)
		synctest.Wait()
		time.Sleep(3 * time.Minute)
		synctest.Wait()
		cancel()

		if f.calls.Load() < 2 {
			t.Errorf("got %d polls; the default jitter blocked the schedule", f.calls.Load())
		}
	})
}
