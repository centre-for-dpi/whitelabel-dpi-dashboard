package apimap_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/apimap"
	dpiv1 "github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/gen/dpi/v1"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/model"
)

// ts builds a UTC timestamp. Fixtures stay in UTC because protobuf timestamps
// carry no zone, so a non-UTC fixture could never round-trip to itself.
func ts(y int, m time.Month, d, h, min int) time.Time {
	return time.Date(y, m, d, h, min, 0, 0, time.UTC)
}

// fullService populates every field, including both OptFloat states, so the
// round-trip test cannot pass by ignoring a field.
func fullService() model.Service {
	return model.Service{
		ID:         "aadhaar",
		Key:        "aadhaar",
		NameTermID: "svc.aadhaar.name",
		DescTermID: "svc.aadhaar.desc",
		CategoryID: "cat.identity",
		RegionID:   "reg.national",
		ProviderID: "prov.uidai",
		Scope:      "national",
		Status:     model.StatusPartial,
		Metrics: model.Metrics{
			Availability: model.Float(99.41),
			ErrorRate:    1.27,
			LatencyP50:   312,
			StaleSeconds: 45,
			Volume:       model.Volume{Total: 2900000, Success: 2863170},
		},
		Maintenance: model.Maintenance{
			Active:       true,
			Until:        ts(2026, time.July, 27, 18, 30),
			ReasonTermID: "maint.scheduled",
		},
		History: []model.HistoryPoint{
			{
				Day:          ts(2026, time.July, 25, 0, 0),
				Availability: model.Float(99.62),
				ErrorRate:    0.81,
				LatencyP50:   288,
				Volume:       2811004,
				Samples:      96,
			},
			{
				// Availability absent: the service reported nothing that day.
				Day:          ts(2026, time.July, 26, 0, 0),
				Availability: model.NoFloat(),
				ErrorRate:    1.44,
				LatencyP50:   401,
				Volume:       2733910,
				Samples:      88,
			},
		},
		Incidents: []model.Incident{
			{
				ID:         "inc-4k2p",
				ServiceID:  "aadhaar",
				Severity:   model.StatusMajor,
				OpenedAt:   ts(2026, time.July, 27, 9, 12),
				ClosedAt:   ts(2026, time.July, 27, 13, 40),
				Open:       false,
				NoteTermID: "incident.note.errorSpike",
				Events: []model.IncidentEvent{
					{Type: "opened", At: ts(2026, time.July, 27, 9, 12)},
					{Type: "acknowledged", At: ts(2026, time.July, 27, 9, 31)},
					{Type: "resolved", At: ts(2026, time.July, 27, 13, 40)},
				},
			},
			{
				// Open incident: ClosedAt must stay zero across the round-trip
				// rather than becoming the proto epoch.
				ID:         "inc-9x1a",
				ServiceID:  "aadhaar",
				Severity:   model.StatusPartial,
				OpenedAt:   ts(2026, time.July, 27, 15, 2),
				Open:       true,
				NoteTermID: "incident.note.rateLimited",
				Events: []model.IncidentEvent{
					{Type: "opened", At: ts(2026, time.July, 27, 15, 2)},
				},
			},
		},
		Errors: []model.ErrorBucket{
			{Code: "503", TermID: "err.503", Class: model.ErrorClassServer, Count: 18422, Share: 51.3, Trend: model.DirectionUp},
			{Code: "429", TermID: "err.429", Class: model.ErrorClassClient, Count: 9110, Share: 25.4, Trend: model.DirectionFlat},
			{Code: "timeout", TermID: "err.timeout", Class: model.ErrorClassNetwork, Count: 8377, Share: 23.3, Trend: model.DirectionDown},
		},
		Trends: map[string]model.Trend{
			model.TrendAvailability: {Delta: -0.21, Direction: model.DirectionDown, PeriodDays: 30},
			model.TrendErrorRate:    {Delta: 0.46, Direction: model.DirectionUp, PeriodDays: 30},
			model.TrendVolume:       {Delta: 12044, Direction: model.DirectionUp, PeriodDays: 30},
			model.TrendLatencyP50:   {Delta: 0, Direction: model.DirectionFlat, PeriodDays: 30},
		},
		RankMovement: -2,
		ObservedAt:   ts(2026, time.July, 27, 15, 44),
	}
}

func TestServiceRoundTripIsIdentity(t *testing.T) {
	want := fullService()

	got := apimap.ServiceFromProto(apimap.ServiceToProto(want))

	if !reflect.DeepEqual(got, want) {
		t.Errorf("round trip changed the service\n got: %#v\nwant: %#v", got, want)
	}
}

func TestSnapshotRoundTripIsIdentity(t *testing.T) {
	want := model.Snapshot{
		Services:    []model.Service{fullService()},
		GeneratedAt: ts(2026, time.July, 27, 15, 44),
	}

	got := apimap.SnapshotFromProto(apimap.SnapshotToProto(want))

	if !reflect.DeepEqual(got, want) {
		t.Errorf("round trip changed the snapshot\n got: %#v\nwant: %#v", got, want)
	}
}

func TestZeroServiceRoundTripIsIdentity(t *testing.T) {
	// The empty service exercises the absent-timestamp and absent-OptFloat
	// paths that the fully-populated fixture does not.
	var want model.Service

	got := apimap.ServiceFromProto(apimap.ServiceToProto(want))

	if !reflect.DeepEqual(got, want) {
		t.Errorf("round trip changed the zero service\n got: %#v\nwant: %#v", got, want)
	}
}

func TestAvailabilityAbsenceSurvivesRoundTrip(t *testing.T) {
	// Absence must not collapse to 0.0, which would read as a total outage
	// rather than as "not reported".
	svc := model.Service{Metrics: model.Metrics{Availability: model.NoFloat()}}

	p := apimap.ServiceToProto(svc)
	if p.GetMetrics().Availability != nil {
		t.Fatalf("absent availability encoded as present: %v", p.GetMetrics().GetAvailability())
	}

	got := apimap.ServiceFromProto(p).Metrics.Availability
	if got.Valid {
		t.Errorf("absent availability decoded as present with value %v", got.Value)
	}
}

func TestZeroAvailabilityIsDistinctFromAbsent(t *testing.T) {
	svc := model.Service{Metrics: model.Metrics{Availability: model.Float(0)}}

	p := apimap.ServiceToProto(svc)
	if p.GetMetrics().Availability == nil {
		t.Fatal("a reported 0% availability encoded as absent")
	}

	got := apimap.ServiceFromProto(p).Metrics.Availability
	if !got.Valid || got.Value != 0 {
		t.Errorf("got %+v, want present zero", got)
	}
}

func TestNilProtoDecodesToZeroValues(t *testing.T) {
	if got := apimap.ServiceFromProto(nil); !reflect.DeepEqual(got, model.Service{}) {
		t.Errorf("ServiceFromProto(nil) = %#v, want zero", got)
	}
	if got := apimap.SnapshotFromProto(nil); !reflect.DeepEqual(got, model.Snapshot{}) {
		t.Errorf("SnapshotFromProto(nil) = %#v, want zero", got)
	}
	if got := apimap.IngestServiceToModel(nil); !reflect.DeepEqual(got, model.Service{}) {
		t.Errorf("IngestServiceToModel(nil) = %#v, want zero", got)
	}
}

func TestEmptyCollectionsNormaliseToNil(t *testing.T) {
	// Empty and nil are indistinguishable on the wire, so decoding settles on
	// nil. Without this, round-trip equality would depend on which one a
	// caller happened to construct.
	svc := model.Service{
		History:   []model.HistoryPoint{},
		Incidents: []model.Incident{},
		Errors:    []model.ErrorBucket{},
		Trends:    map[string]model.Trend{},
	}

	got := apimap.ServiceFromProto(apimap.ServiceToProto(svc))

	if got.History != nil || got.Incidents != nil || got.Errors != nil || got.Trends != nil {
		t.Errorf("empty collections did not normalise to nil: %#v", got)
	}
}

func TestIngestDropsDerivedFields(t *testing.T) {
	// A deployment reports observations; the dashboard decides verdicts. The
	// ingest message has no field for these, so they must come back zeroed
	// rather than smuggled through.
	got := apimap.IngestServiceToModel(apimap.ServiceToIngestProto(fullService()))

	if got.Status != "" {
		t.Errorf("Status survived ingest as %q, want empty", got.Status)
	}
	if got.Trends != nil {
		t.Errorf("Trends survived ingest as %#v, want nil", got.Trends)
	}
	if got.RankMovement != 0 {
		t.Errorf("RankMovement survived ingest as %d, want 0", got.RankMovement)
	}
}

func TestIngestPreservesReportedFields(t *testing.T) {
	want := fullService()

	got := apimap.IngestServiceToModel(apimap.ServiceToIngestProto(want))

	// Everything a deployment legitimately reports must survive intact.
	want.Status = ""
	want.Trends = nil
	want.RankMovement = 0

	if !reflect.DeepEqual(got, want) {
		t.Errorf("ingest lost reported data\n got: %#v\nwant: %#v", got, want)
	}
}

func TestStatusMappingIsExhaustiveAndReversible(t *testing.T) {
	all := []model.Status{
		model.StatusUnknown,
		model.StatusOperational,
		model.StatusPartial,
		model.StatusMajor,
		model.StatusMaintenance,
	}
	seen := map[dpiv1.Status]bool{}

	for _, s := range all {
		p := apimap.StatusToProto(s)
		if p == dpiv1.Status_STATUS_UNSPECIFIED {
			t.Errorf("status %q maps to UNSPECIFIED", s)
		}
		if seen[p] {
			t.Errorf("status %q collides with an earlier status on %v", s, p)
		}
		seen[p] = true

		if got := apimap.StatusFromProto(p); got != s {
			t.Errorf("StatusFromProto(StatusToProto(%q)) = %q", s, got)
		}
	}
}

func TestUnrecognisedEnumsDecodeToSafeDefaults(t *testing.T) {
	// A newer peer may send an enum value this build does not know. Decoding
	// must degrade to the documented default rather than panic or invent one.
	if got := apimap.StatusFromProto(dpiv1.Status(999)); got != model.StatusUnknown {
		t.Errorf("unrecognised status decoded to %q, want %q", got, model.StatusUnknown)
	}
	if got := apimap.StatusFromProto(dpiv1.Status_STATUS_UNSPECIFIED); got != model.StatusUnknown {
		t.Errorf("unspecified status decoded to %q, want %q", got, model.StatusUnknown)
	}
	if got := apimap.DirectionFromProto(dpiv1.TrendDirection(999)); got != model.DirectionFlat {
		t.Errorf("unrecognised direction decoded to %q, want %q", got, model.DirectionFlat)
	}
	if got := apimap.ErrorClassFromProto(dpiv1.ErrorClass(999)); got != model.ErrorClassNetwork {
		t.Errorf("unrecognised error class decoded to %q, want %q", got, model.ErrorClassNetwork)
	}
}

func TestUnrecognisedModelValuesEncodeToUnspecified(t *testing.T) {
	if got := apimap.StatusToProto(model.Status("invented")); got != dpiv1.Status_STATUS_UNSPECIFIED {
		t.Errorf("invented status encoded to %v, want UNSPECIFIED", got)
	}
	if got := apimap.DirectionToProto(model.Direction("sideways")); got != dpiv1.TrendDirection_TREND_DIRECTION_UNSPECIFIED {
		t.Errorf("invented direction encoded to %v, want UNSPECIFIED", got)
	}
	if got := apimap.ErrorClassToProto(model.ErrorClass("3xx")); got != dpiv1.ErrorClass_ERROR_CLASS_UNSPECIFIED {
		t.Errorf("invented error class encoded to %v, want UNSPECIFIED", got)
	}
}

func TestDirectionAndErrorClassRoundTrip(t *testing.T) {
	for _, d := range []model.Direction{model.DirectionFlat, model.DirectionUp, model.DirectionDown} {
		if got := apimap.DirectionFromProto(apimap.DirectionToProto(d)); got != d {
			t.Errorf("direction %q round-tripped to %q", d, got)
		}
	}
	for _, c := range []model.ErrorClass{model.ErrorClassServer, model.ErrorClassClient, model.ErrorClassNetwork} {
		if got := apimap.ErrorClassFromProto(apimap.ErrorClassToProto(c)); got != c {
			t.Errorf("error class %q round-tripped to %q", c, got)
		}
	}
}

func TestSparsePayloadDecodesWithoutPanicking(t *testing.T) {
	// Anything ServiceToProto builds always populates the sub-messages, but a
	// hand-written payload need not. A shell script POSTing {"id":"x"} is a
	// supported way to use the ingest API, so omitted sub-messages must decode
	// to zero values rather than panic.
	sparse := &dpiv1.Service{Id: "x"}

	got := apimap.ServiceFromProto(sparse)

	if got.ID != "x" {
		t.Errorf("ID = %q, want %q", got.ID, "x")
	}
	if !reflect.DeepEqual(got.Metrics, model.Metrics{}) {
		t.Errorf("omitted metrics decoded to %#v, want zero", got.Metrics)
	}
	if !reflect.DeepEqual(got.Maintenance, model.Maintenance{}) {
		t.Errorf("omitted maintenance decoded to %#v, want zero", got.Maintenance)
	}
	if got.Metrics.Availability.Valid {
		t.Error("omitted metrics yielded a present availability")
	}

	sparseIngest := &dpiv1.IngestService{Id: "x"}
	if ing := apimap.IngestServiceToModel(sparseIngest); !reflect.DeepEqual(ing.Metrics, model.Metrics{}) {
		t.Errorf("omitted ingest metrics decoded to %#v, want zero", ing.Metrics)
	}
}

func TestZeroTimeMapsToAbsentTimestamp(t *testing.T) {
	// A zero time.Time means "no such moment". Encoding it as the proto epoch
	// would render as 1st January 1970 in the incident timeline.
	svc := model.Service{
		Incidents: []model.Incident{{ID: "i", Open: true}},
	}

	p := apimap.ServiceToProto(svc)
	if p.GetIncidents()[0].ClosedAt != nil {
		t.Error("zero ClosedAt encoded as a present timestamp")
	}
	if p.GetObservedAt() != nil {
		t.Error("zero ObservedAt encoded as a present timestamp")
	}

	got := apimap.ServiceFromProto(p)
	if !got.Incidents[0].ClosedAt.IsZero() {
		t.Errorf("absent ClosedAt decoded to %v, want zero", got.Incidents[0].ClosedAt)
	}
}
