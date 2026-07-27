package apimap_test

import (
	"encoding/json"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/apimap"
	dpiv1 "github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/gen/dpi/v1"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/model"
)

// canonicalJSON renders a proto message as stably-ordered JSON.
//
// protojson deliberately varies its whitespace to discourage byte comparison,
// so the output is re-encoded through encoding/json, which sorts object keys.
// What this pins is the field names and value encodings — exactly the parts of
// the contract an integrator writes against.
func canonicalJSON(t *testing.T, m proto.Message) string {
	t.Helper()
	raw, err := protojson.Marshal(m)
	if err != nil {
		t.Fatalf("protojson.Marshal: %v", err)
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("re-decoding protojson output: %v", err)
	}
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatalf("json.MarshalIndent: %v", err)
	}
	return string(out)
}

func TestJSONWireFormatIsStable(t *testing.T) {
	svc := model.Service{
		ID:         "aadhaar",
		Key:        "aadhaar",
		NameTermID: "svc.aadhaar.name",
		CategoryID: "cat.identity",
		RegionID:   "reg.national",
		Scope:      "national",
		Status:     model.StatusPartial,
		Metrics: model.Metrics{
			Availability: model.Float(99.41),
			ErrorRate:    1.27,
			LatencyP50:   312,
			StaleSeconds: 45,
			Volume:       model.Volume{Total: 2900000, Success: 2863170},
		},
		Errors: []model.ErrorBucket{
			{Code: "503", TermID: "err.503", Class: model.ErrorClassServer, Count: 18422, Share: 51.3, Trend: model.DirectionUp},
		},
		Trends: map[string]model.Trend{
			model.TrendAvailability: {Delta: -0.21, Direction: model.DirectionDown, PeriodDays: 30},
		},
		RankMovement: -2,
		ObservedAt:   time.Date(2026, time.July, 27, 15, 44, 0, 0, time.UTC),
	}

	// Note the int64 fields: proto3's JSON mapping encodes them as strings, so
	// "staleSeconds", "total", "success" and "count" are quoted while the int32
	// "latencyP50Ms" and "rankMovement" are not. Integrators need to know this,
	// which is precisely why it is pinned here.
	const want = `{
  "categoryId": "cat.identity",
  "errors": [
    {
      "class": "ERROR_CLASS_SERVER",
      "code": "503",
      "count": "18422",
      "share": 51.3,
      "termId": "err.503",
      "trend": "TREND_DIRECTION_UP"
    }
  ],
  "id": "aadhaar",
  "key": "aadhaar",
  "metrics": {
    "availability": 99.41,
    "errorRate": 1.27,
    "latencyP50Ms": 312,
    "staleSeconds": "45",
    "volume": {
      "success": "2863170",
      "total": "2900000"
    }
  },
  "nameTermId": "svc.aadhaar.name",
  "observedAt": "2026-07-27T15:44:00Z",
  "rankMovement": -2,
  "regionId": "reg.national",
  "scope": "national",
  "status": "STATUS_PARTIAL",
  "trends": {
    "availability": {
      "delta": -0.21,
      "direction": "TREND_DIRECTION_DOWN",
      "periodDays": 30
    }
  }
}`

	if got := canonicalJSON(t, apimap.ServiceToProto(svc)); got != want {
		t.Errorf("wire format changed.\n got:\n%s\n\nwant:\n%s", got, want)
	}
}

func TestZeroMaintenanceIsOmittedFromTheWire(t *testing.T) {
	// Every service would otherwise carry a bare "maintenance": {}, which is
	// noise across a full snapshot and implies a window that does not exist.
	p := apimap.ServiceToProto(model.Service{ID: "x"})
	if p.GetMaintenance() != nil {
		t.Errorf("zero maintenance encoded as %v, want absent", p.GetMaintenance())
	}

	active := apimap.ServiceToProto(model.Service{
		ID:          "x",
		Maintenance: model.Maintenance{Active: true, ReasonTermID: "maint.scheduled"},
	})
	if active.GetMaintenance() == nil {
		t.Fatal("an active maintenance window was dropped from the wire")
	}
	if !active.GetMaintenance().GetActive() {
		t.Error("maintenance encoded but Active was lost")
	}
}

func TestJSONPayloadWithoutProtoToolingIsAccepted(t *testing.T) {
	// The plan's promise is that protobuf never becomes an adoption tax: a team
	// with curl and a shell script must be able to push. This is the shape of
	// the minimal hand-written payload, so it is asserted to decode.
	const handWritten = `{
	  "mode": "INGEST_MODE_UPSERT",
	  "sourceId": "cron-collector",
	  "services": [{
	    "id": "aadhaar",
	    "categoryId": "cat.identity",
	    "regionId": "reg.national",
	    "scope": "national",
	    "metrics": {
	      "availability": 99.41,
	      "errorRate": 1.27,
	      "latencyP50Ms": 312,
	      "volume": {"total": "2900000", "success": "2863170"}
	    }
	  }]
	}`

	var req dpiv1.IngestRequest
	if err := protojson.Unmarshal([]byte(handWritten), &req); err != nil {
		t.Fatalf("a hand-written JSON payload was rejected: %v", err)
	}

	if req.GetMode() != dpiv1.IngestMode_INGEST_MODE_UPSERT {
		t.Errorf("mode = %v, want UPSERT", req.GetMode())
	}
	if got := len(req.GetServices()); got != 1 {
		t.Fatalf("decoded %d services, want 1", got)
	}

	svc := apimap.IngestServiceToModel(req.GetServices()[0])
	if svc.ID != "aadhaar" {
		t.Errorf("ID = %q, want aadhaar", svc.ID)
	}
	if !svc.Metrics.Availability.Valid || svc.Metrics.Availability.Value != 99.41 {
		t.Errorf("availability = %+v, want present 99.41", svc.Metrics.Availability)
	}
	if svc.Metrics.Volume.Total != 2900000 {
		t.Errorf("volume total = %d, want 2900000", svc.Metrics.Volume.Total)
	}
}

func TestUnknownJSONFieldsAreRejected(t *testing.T) {
	// A typo in a hand-written payload should fail loudly at the boundary
	// rather than silently storing a service with no metrics.
	const typo = `{"services": [{"id": "x", "metricz": {"errorRate": 1.0}}]}`

	var req dpiv1.IngestRequest
	if err := protojson.Unmarshal([]byte(typo), &req); err == nil {
		t.Error("a payload with an unknown field was accepted; typos would pass silently")
	}
}
