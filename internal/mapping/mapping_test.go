package mapping_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/mapping"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/transform"
)

// upstream is shaped like somebody else's API — ratios, enum casing, its own
// field names — which is the case the mapping layer exists for.
const upstream = `{
  "data": {
    "services": [
      {
        "serviceId": "aadhaar",
        "displayName": "Aadhaar verification",
        "category": "IDENTITY",
        "region": "NATIONAL",
        "sla": {"uptimePct": 0.9991, "errorPct": 0.0062},
        "latency": {"p50Ms": 264},
        "requests": {"total": 2480492, "succeeded": 2465112},
        "observedAgeSecs": 195,
        "maintenance": {"inProgress": false},
        "daily": [
          {"ts": "2026-07-26", "uptime": 0.9989, "errPct": 0.0055, "count": 2088317},
          {"ts": "2026-07-25", "uptime": 0.9992, "errPct": 0.0051, "count": 2101880}
        ]
      },
      {
        "serviceId": "pan",
        "displayName": "PAN verification",
        "category": "IDENTITY",
        "sla": {"uptimePct": null, "errorPct": 0.01},
        "observedAgeSecs": 4000
      }
    ]
  }
}`

func spec() mapping.Spec {
	return mapping.Spec{
		ItemsPath: "$.data.services",
		Map: map[string]string{
			"id":                     "$.serviceId",
			"name":                   "$.displayName",
			"categoryId":             "$.category",
			"regionId":               "$.region",
			"metrics.availability":   "$.sla.uptimePct",
			"metrics.errorRate":      "$.sla.errorPct",
			"metrics.latencyP50":     "$.latency.p50Ms",
			"metrics.volume.total":   "$.requests.total",
			"metrics.volume.success": "$.requests.succeeded",
			"metrics.staleSeconds":   "$.observedAgeSecs",
			"maintenance.active":     "$.maintenance.inProgress",
		},
		Transform: map[string][]transform.Spec{
			"metrics.availability": {{Fn: "ratioToPercent"}},
			"metrics.errorRate":    {{Fn: "ratioToPercent"}},
			"categoryId":           {{Fn: "enumMap", Table: map[string]string{"IDENTITY": "cat.identity"}}},
			"regionId": {
				{Fn: "enumMap", Table: map[string]string{"NATIONAL": "reg.national"}},
				{Fn: "default", Value: "reg.national"},
			},
		},
		History: &mapping.HistorySpec{
			Path:         "$.daily[*]",
			Date:         "$.ts",
			Availability: "$.uptime",
			ErrorRate:    "$.errPct",
			Volume:       "$.count",
		},
	}
}

func compile(t *testing.T, s mapping.Spec) *mapping.Mapper {
	t.Helper()
	m, err := mapping.Compile(s)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	return m
}

func decode(t *testing.T, src string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(src), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func apply(t *testing.T, s mapping.Spec, src string) mapping.Result {
	t.Helper()
	return compile(t, s).Apply(decode(t, src))
}

func TestMapsAForeignAPIWithNoCode(t *testing.T) {
	// The headline claim: a YAML mapping is the whole integration.
	got := apply(t, spec(), upstream)

	if len(got.Services) != 2 {
		t.Fatalf("got %d services, want 2: %+v", len(got.Services), got.Skipped)
	}
	sv := got.Services[0]

	if sv.ID != "aadhaar" || sv.NameTermID != "Aadhaar verification" {
		t.Errorf("identity = %+v", sv)
	}
	// The ratio became a percentage, which is the single most common
	// integration mistake this layer exists to make declarative.
	if !sv.Metrics.Availability.Valid || sv.Metrics.Availability.Value != 99.91 {
		t.Errorf("availability = %+v, want 99.91", sv.Metrics.Availability)
	}
	if sv.Metrics.ErrorRate != 0.62 {
		t.Errorf("errorRate = %v, want 0.62", sv.Metrics.ErrorRate)
	}
	// The enum became a taxonomy id.
	if sv.CategoryID != "cat.identity" {
		t.Errorf("categoryId = %q", sv.CategoryID)
	}
	if sv.Metrics.LatencyP50 != 264 {
		t.Errorf("latency = %v", sv.Metrics.LatencyP50)
	}
	if sv.Metrics.Volume.Total != 2480492 || sv.Metrics.Volume.Success != 2465112 {
		t.Errorf("volume = %+v", sv.Metrics.Volume)
	}
	if sv.Metrics.StaleSeconds != 195 {
		t.Errorf("staleSeconds = %v", sv.Metrics.StaleSeconds)
	}
}

func TestAnUnreportedValueStaysUnreported(t *testing.T) {
	// The difference between "we cannot tell" and "it is down" is the whole
	// point of the status rules, so a null must not become a zero.
	got := apply(t, spec(), upstream)

	pan := got.Services[1]
	if pan.Metrics.Availability.Valid {
		t.Errorf("a null uptime became %v", pan.Metrics.Availability.Value)
	}
	// And a field the record omits entirely leaves the zero value rather than
	// being invented.
	if pan.Metrics.LatencyP50 != 0 {
		t.Errorf("an omitted latency became %v", pan.Metrics.LatencyP50)
	}
}

func TestDefaultFillsAnAbsence(t *testing.T) {
	// The second record has no region at all; the chain's `default` supplies
	// one, which is how an upstream that only serves one region is integrated.
	got := apply(t, spec(), upstream)

	if got.Services[1].RegionID != "reg.national" {
		t.Errorf("regionId = %q, want the default", got.Services[1].RegionID)
	}
}

func TestKeyDefaultsToTheID(t *testing.T) {
	// A key is only ever a shorter handle for the same thing, and an upstream
	// is unlikely to send both.
	got := apply(t, spec(), upstream)

	if got.Services[0].Key != "aadhaar" {
		t.Errorf("key = %q", got.Services[0].Key)
	}
}

func TestHistoryIsMappedAndOrdered(t *testing.T) {
	// The upstream sends the newest day first; the charts plot oldest first,
	// and an unordered chart is worse than a shorter one.
	got := apply(t, spec(), upstream)

	h := got.Services[0].History
	if len(h) != 2 {
		t.Fatalf("got %d history points, want 2", len(h))
	}
	if !h[0].Day.Before(h[1].Day) {
		t.Errorf("history is newest-first: %v then %v", h[0].Day, h[1].Day)
	}
	if !h[0].Availability.Valid || h[0].Availability.Value != 0.9992 {
		t.Errorf("history availability = %+v", h[0].Availability)
	}
	if h[0].Volume != 2101880 {
		t.Errorf("history volume = %v", h[0].Volume)
	}
	// Bucketed to a day, so points land in the same slot however precise the
	// upstream's timestamps are.
	if !h[0].Day.Equal(h[0].Day.Truncate(24 * time.Hour)) {
		t.Errorf("history point is not aligned to a day: %v", h[0].Day)
	}
}

func TestAServiceWithNoHistoryIsFine(t *testing.T) {
	// A deployment reporting only current state is a supported way to
	// integrate; the dashboard rolls up its own history from successive polls.
	got := apply(t, spec(), upstream)

	if len(got.Services[1].History) != 0 {
		t.Errorf("history was invented: %+v", got.Services[1].History)
	}
}

func TestHistoryPointsWithNoDateAreSkipped(t *testing.T) {
	// A point that cannot be placed in time would land somewhere arbitrary.
	src := `{"data":{"services":[{"serviceId":"a","daily":[
	  {"ts":"2026-07-26","uptime":0.99},
	  {"uptime":0.98},
	  {"ts":"not a date","uptime":0.97}
	]}]}}`

	got := apply(t, spec(), src)

	if n := len(got.Services[0].History); n != 1 {
		t.Errorf("got %d history points, want only the dated one", n)
	}
}

// --- resilience --------------------------------------------------------------

func TestOneBadRecordDoesNotSinkTheBatch(t *testing.T) {
	// A single malformed service must not empty the dashboard.
	src := `{"data":{"services":[
	  {"serviceId":"good","sla":{"uptimePct":0.999}},
	  {"noIdAtAll":true},
	  {"serviceId":"","sla":{"uptimePct":0.5}},
	  {"serviceId":"  ","sla":{"uptimePct":0.5}},
	  {"serviceId":"alsoGood","sla":{"uptimePct":0.998}}
	]}}`

	got := apply(t, spec(), src)

	if len(got.Services) != 2 {
		t.Errorf("got %d services, want the two good ones", len(got.Services))
	}
	if len(got.Skipped) != 3 {
		t.Errorf("got %d skips, want 3", len(got.Skipped))
	}
	// The skips are reported so a partial upstream failure is visible rather
	// than silently shrinking the dashboard.
	for _, s := range got.Skipped {
		if s.Reason == "" {
			t.Error("a skip carries no reason")
		}
	}
	if got.Skipped[0].Index != 1 {
		t.Errorf("skip index = %d, want the position in the batch", got.Skipped[0].Index)
	}
}

func TestIdentifiersAreTrimmed(t *testing.T) {
	src := `{"data":{"services":[{"serviceId":"  aadhaar  "}]}}`

	got := apply(t, spec(), src)

	if got.Services[0].ID != "aadhaar" {
		t.Errorf("id = %q", got.Services[0].ID)
	}
}

func TestAnEmptyOrUnreachableDocumentYieldsNothing(t *testing.T) {
	for _, src := range []string{
		`{"data":{"services":[]}}`,
		`{"data":{}}`,
		`{}`,
		`[]`,
		`"not a document"`,
	} {
		t.Run(src, func(t *testing.T) {
			got := apply(t, spec(), src)
			if len(got.Services) != 0 {
				t.Errorf("got %d services", len(got.Services))
			}
		})
	}
}

func TestADocumentThatIsItselfTheArray(t *testing.T) {
	// An API returning a bare array is common enough to support without an
	// itemsPath.
	s := spec()
	s.ItemsPath = ""
	s.History = nil

	got := apply(t, s, `[{"serviceId":"a"},{"serviceId":"b"}]`)

	if len(got.Services) != 2 {
		t.Fatalf("got %d services", len(got.Services))
	}
	if got.Services[1].ID != "b" {
		t.Errorf("second = %q", got.Services[1].ID)
	}
}

func TestAWildcardItemsPath(t *testing.T) {
	s := spec()
	s.ItemsPath = "$.data.services[*]"

	got := apply(t, s, upstream)

	if len(got.Services) != 2 {
		t.Errorf("got %d services", len(got.Services))
	}
}

// --- compilation -------------------------------------------------------------

func TestCompileRejectsMistakes(t *testing.T) {
	for _, tc := range []struct {
		name string
		spec mapping.Spec
		want string
	}{
		{
			"no mapping at all",
			mapping.Spec{},
			"mapping is empty",
		},
		{
			// Without an id there is nothing to upsert against and nothing to
			// link to.
			"no id",
			mapping.Spec{Map: map[string]string{"name": "$.n"}},
			"`id` is not mapped",
		},
		{
			"unknown field",
			mapping.Spec{Map: map[string]string{"id": "$.i", "vibes": "$.v"}},
			`unknown field "vibes"`,
		},
		{
			"malformed path",
			mapping.Spec{Map: map[string]string{"id": "$.items[a]"}},
			"is not a number",
		},
		{
			"malformed itemsPath",
			mapping.Spec{ItemsPath: "$.a[", Map: map[string]string{"id": "$.i"}},
			"itemsPath",
		},
		{
			"transform for an unknown field",
			mapping.Spec{
				Map:       map[string]string{"id": "$.i"},
				Transform: map[string][]transform.Spec{"vibes": {{Fn: "trim"}}},
			},
			"unknown field",
		},
		{
			"unknown transform",
			mapping.Spec{
				Map:       map[string]string{"id": "$.i"},
				Transform: map[string][]transform.Spec{"id": {{Fn: "vibes"}}},
			},
			"unknown transform",
		},
		{
			"history with no path",
			mapping.Spec{
				Map:     map[string]string{"id": "$.i"},
				History: &mapping.HistorySpec{Date: "$.ts"},
			},
			"no path to the daily series",
		},
		{
			// Without a date the points cannot be ordered or bucketed.
			"history with no date",
			mapping.Spec{
				Map:     map[string]string{"id": "$.i"},
				History: &mapping.HistorySpec{Path: "$.daily[*]"},
			},
			"cannot be placed in time",
		},
		{
			"history with a malformed path",
			mapping.Spec{
				Map:     map[string]string{"id": "$.i"},
				History: &mapping.HistorySpec{Path: "$.daily[", Date: "$.ts"},
			},
			"history",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := mapping.Compile(tc.spec)
			if err == nil {
				t.Fatal("accepted")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error does not mention %q: %v", tc.want, err)
			}
		})
	}
}

func TestCompileReportsEveryProblemAtOnce(t *testing.T) {
	// Fixing a mapping one error per restart is miserable.
	_, err := mapping.Compile(mapping.Spec{
		Map: map[string]string{"id": "$.i", "vibes": "$.v", "name": "$.n["},
	})

	if err == nil {
		t.Fatal("accepted")
	}
	for _, want := range []string{"vibes", "unterminated"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error set omits %q: %v", want, err)
		}
	}
}

func TestCompileAcceptsAWellFormedSpec(t *testing.T) {
	if _, err := mapping.Compile(spec()); err != nil {
		t.Errorf("a valid spec was rejected: %v", err)
	}
}

func TestFieldsAreDocumented(t *testing.T) {
	// The list is what validation checks against and what the reference
	// documents, so it must cover the model a deployment can actually populate.
	fields := mapping.Fields()

	for _, want := range []string{
		"id", "categoryId", "metrics.availability", "metrics.volume.total",
		"maintenance.active", "observedAt",
	} {
		if !contains(fields, want) {
			t.Errorf("%q is not a mappable field", want)
		}
	}
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

func TestObservedAtAndMaintenanceWindows(t *testing.T) {
	s := spec()
	s.Map["observedAt"] = "$.checkedAt"
	s.Map["maintenance.until"] = "$.maintenance.endsAt"
	s.Map["maintenance.reason"] = "$.maintenance.reason"
	s.History = nil

	got := apply(t, s, `{"data":{"services":[{
	  "serviceId":"a",
	  "checkedAt":"2026-07-27T12:00:00Z",
	  "maintenance":{"inProgress":true,"endsAt":"2026-07-27T18:00:00Z","reason":"maint.scheduled"}
	}]}}`)

	sv := got.Services[0]
	if !sv.ObservedAt.Equal(time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)) {
		t.Errorf("observedAt = %v", sv.ObservedAt)
	}
	if !sv.Maintenance.Active {
		t.Error("the maintenance window was not read")
	}
	if sv.Maintenance.ReasonTermID != "maint.scheduled" {
		t.Errorf("reason = %q", sv.Maintenance.ReasonTermID)
	}
	if sv.Maintenance.Until.IsZero() {
		t.Error("the window has no end time")
	}
}

func TestAnItemsPathPointingAtSomethingOtherThanAnArray(t *testing.T) {
	// An upstream that changed shape, or a path off by one key. Yielding
	// nothing is right; treating an object as a single record would produce one
	// nonsense service.
	s := spec()
	s.ItemsPath = "$.data"

	got := apply(t, s, upstream)

	if len(got.Services) != 0 {
		t.Errorf("got %d services from a path pointing at an object", len(got.Services))
	}
	if len(got.Skipped) != 1 {
		t.Errorf("got %d skips, want the one unreadable record reported", len(got.Skipped))
	}
}

func TestNoItemsPathAndNoArray(t *testing.T) {
	// An API that returns a bare object where an array was expected. Yielding
	// nothing is right: there is no record to read.
	s := spec()
	s.ItemsPath = ""
	s.History = nil

	if got := apply(t, s, `{"serviceId":"a"}`); len(got.Services) != 0 {
		t.Errorf("got %d services from an object with no itemsPath", len(got.Services))
	}
}
