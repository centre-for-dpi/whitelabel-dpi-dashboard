package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protojson"

	dpiv1 "github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/gen/dpi/v1"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/model"
)

// The command runs from the repository root, so tests reach the real catalogue
// and config through relative paths — exercising exactly what `make seed` does.
const (
	repoRoot      = "../.."
	cataloguePath = repoRoot + "/examples/seed-catalogue.yaml"
	configPath    = repoRoot + "/config"
)

func generateInto(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := run(cataloguePath, configPath, dir); err != nil {
		t.Fatalf("run: %v", err)
	}
	return dir
}

func readUpstream(t *testing.T, dir string) upstreamDocument {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "upstream", "services.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc upstreamDocument
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("the upstream fixture is not valid JSON: %v", err)
	}
	return doc
}

func TestFixturesAreWritten(t *testing.T) {
	dir := generateInto(t)

	for _, name := range []string{"upstream/services.json", "push/payload.json"} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Errorf("%s was not written: %v", name, err)
			continue
		}
		if info.Size() == 0 {
			t.Errorf("%s is empty", name)
		}
	}
}

func TestFixturesAreReproducible(t *testing.T) {
	// They are committed, so `make seed` producing a different file on every run
	// would make the diff meaningless and the drift check impossible.
	a, b := generateInto(t), generateInto(t)

	for _, name := range []string{"upstream/services.json", "push/payload.json"} {
		x, err := os.ReadFile(filepath.Join(a, name))
		if err != nil {
			t.Fatal(err)
		}
		y, err := os.ReadFile(filepath.Join(b, name))
		if err != nil {
			t.Fatal(err)
		}
		if string(x) != string(y) {
			t.Errorf("%s differs between runs", name)
		}
	}
}

func TestFixturesStaySmallEnoughToRead(t *testing.T) {
	// Their job is to be read. A multi-megabyte fixture is worse documentation
	// than a short one and a needless addition to the repository.
	dir := generateInto(t)

	for _, name := range []string{"upstream/services.json", "push/payload.json"} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if info.Size() > 200*1024 {
			t.Errorf("%s is %d KB; it is meant to be an excerpt", name, info.Size()/1024)
		}
	}
}

func TestUpstreamFixtureShowsTheAwkwardCases(t *testing.T) {
	// A fixture of healthy services documents neither the null uptime nor the
	// maintenance object — precisely the two things an integrator gets wrong.
	doc := readUpstream(t, generateInto(t))

	var sawNullUptime, sawMaintenance bool
	for _, s := range doc.Data.Services {
		if s.SLA.UptimePct == nil {
			sawNullUptime = true
		}
		if s.Maintenance != nil && s.Maintenance.InProgress {
			sawMaintenance = true
		}
	}

	if !sawNullUptime {
		t.Error("no service reports a null uptime, so the fixture never shows how absence is expressed")
	}
	if !sawMaintenance {
		t.Error("no service is in maintenance, so the fixture never shows the maintenance object")
	}
}

func TestUpstreamFixtureIsShapedLikeSomebodyElsesAPI(t *testing.T) {
	// If the example upstream already spoke the dashboard's language, the
	// mapping in sources.yaml would be an identity function and would teach a
	// reader nothing.
	doc := readUpstream(t, generateInto(t))
	if len(doc.Data.Services) == 0 {
		t.Fatal("no services in the fixture")
	}
	s := doc.Data.Services[0]

	if s.SLA.UptimePct == nil {
		t.Fatal("expected the first service to report an uptime")
	}
	if v := *s.SLA.UptimePct; v > 1 {
		t.Errorf("uptime is %v; the example upstream should report a ratio, so ratioToPercent has work to do", v)
	}
	if s.Category != strings.ToUpper(s.Category) {
		t.Errorf("category %q is not enum-cased, so enumMap has nothing to translate", s.Category)
	}
	if strings.Contains(s.Category, ".") {
		t.Errorf("category %q is already a taxonomy id; the fixture is speaking the dashboard's language", s.Category)
	}
	if s.ObservedAgeSecs <= 0 {
		t.Error("no observation age, so the staleness rule cannot be demonstrated")
	}
	if len(s.Daily) == 0 {
		t.Error("no daily history, so the history mapping is undocumented")
	}
}

func TestRatiosAreRoundedCleanly(t *testing.T) {
	// 0.55/100 serialises as 0.005500000000000001 without rounding, which looks
	// like a precision claim nobody is making and makes the fixture diff noisily.
	raw, err := os.ReadFile(filepath.Join(generateInto(t), "upstream", "services.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "000000000") {
		t.Error("the fixture contains floating-point noise; ratios are not being rounded")
	}
}

func TestPushFixtureIsAcceptedByTheIngestContract(t *testing.T) {
	// The whole value of this file is that it can be POSTed as-is.
	raw, err := os.ReadFile(filepath.Join(generateInto(t), "push", "payload.json"))
	if err != nil {
		t.Fatal(err)
	}

	var req dpiv1.IngestRequest
	if err := protojson.Unmarshal(raw, &req); err != nil {
		t.Fatalf("the push fixture is not a valid IngestRequest: %v", err)
	}
	if len(req.GetServices()) == 0 {
		t.Fatal("the push fixture carries no services")
	}
	if req.GetMode() != dpiv1.IngestMode_INGEST_MODE_REPLACE {
		t.Errorf("mode = %v; a full snapshot should replace rather than merge", req.GetMode())
	}
	if req.GetSourceId() == "" {
		t.Error("no sourceId, so multiple collectors feeding one dashboard would be indistinguishable")
	}
}

func TestBothFixturesDescribeTheSameServices(t *testing.T) {
	// A team reading them side by side to choose an approach must be comparing
	// like with like.
	dir := generateInto(t)
	doc := readUpstream(t, dir)

	raw, err := os.ReadFile(filepath.Join(dir, "push", "payload.json"))
	if err != nil {
		t.Fatal(err)
	}
	var req dpiv1.IngestRequest
	if err := protojson.Unmarshal(raw, &req); err != nil {
		t.Fatal(err)
	}

	pull := map[string]bool{}
	for _, s := range doc.Data.Services {
		pull[s.ServiceID] = true
	}
	if len(pull) != len(req.GetServices()) {
		t.Fatalf("pull fixture has %d services, push fixture has %d", len(pull), len(req.GetServices()))
	}
	for _, s := range req.GetServices() {
		if !pull[s.GetId()] {
			t.Errorf("%q appears in the push fixture but not the pull one", s.GetId())
		}
	}
}

func TestExcerptCoversEveryStatusItCan(t *testing.T) {
	snap := model.Snapshot{Services: []model.Service{
		{ID: "op1", Status: model.StatusOperational},
		{ID: "op2", Status: model.StatusOperational},
		{ID: "op3", Status: model.StatusOperational},
		{ID: "maj", Status: model.StatusMajor},
		{ID: "unk", Status: model.StatusUnknown},
		{ID: "mnt", Status: model.StatusMaintenance},
	}}

	got := excerpt(snap, 4)

	if len(got.Services) != 4 {
		t.Fatalf("got %d services, want 4", len(got.Services))
	}
	seen := map[model.Status]bool{}
	for _, sv := range got.Services {
		seen[sv.Status] = true
	}
	for _, s := range []model.Status{model.StatusOperational, model.StatusMajor, model.StatusUnknown} {
		if !seen[s] {
			t.Errorf("excerpt omits %q despite room for it", s)
		}
	}
}

func TestExcerptPreservesCatalogueOrder(t *testing.T) {
	snap := model.Snapshot{Services: []model.Service{
		{ID: "a", Status: model.StatusOperational},
		{ID: "b", Status: model.StatusMajor},
		{ID: "c", Status: model.StatusOperational},
		{ID: "d", Status: model.StatusUnknown},
	}}

	got := excerpt(snap, 3)

	last := -1
	for _, sv := range got.Services {
		for i, orig := range snap.Services {
			if orig.ID == sv.ID {
				if i < last {
					t.Errorf("excerpt is out of catalogue order: %v", ids(got.Services))
				}
				last = i
			}
		}
	}
}

func TestExcerptLeavesSmallSnapshotsAlone(t *testing.T) {
	snap := model.Snapshot{Services: []model.Service{{ID: "a"}, {ID: "b"}}}

	if got := excerpt(snap, 6); len(got.Services) != 2 {
		t.Errorf("got %d, want both services untouched", len(got.Services))
	}
	if got := excerpt(snap, 0); len(got.Services) != 2 {
		t.Errorf("got %d, want both services untouched", len(got.Services))
	}
}

func ids(list []model.Service) []string {
	out := make([]string, len(list))
	for i, s := range list {
		out[i] = s.ID
	}
	return out
}

func TestNameAndEnumConversions(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"aadhaar", "Aadhaar"},
		{"gstFile", "Gst File"},
		{"cbse10", "Cbse10"},
		{"marriageCert", "Marriage Cert"},
	} {
		if got := displayName(tc.in); got != tc.want {
			t.Errorf("displayName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}

	for _, tc := range []struct{ in, want string }{
		{"cat.identity", "IDENTITY"},
		{"reg.national", "NATIONAL"},
		{"prov.uidai", "UIDAI"},
		{"cat.someThing", "SOME_THING"},
		{"nodots", "NODOTS"},
	} {
		if got := enumCase(tc.in); got != tc.want {
			t.Errorf("enumCase(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestRatioKeepsAbsenceAbsent(t *testing.T) {
	// Reporting an unreported service as 0 would claim a total outage.
	if got := ratio(model.NoFloat()); got != nil {
		t.Errorf("got %v, want nil", *got)
	}
	got := ratio(model.Float(99.91))
	if got == nil || *got != 0.9991 {
		t.Errorf("got %v, want 0.9991", got)
	}
	// A reported zero is a real reading and must survive.
	if got := ratio(model.Float(0)); got == nil || *got != 0 {
		t.Errorf("got %v, want a present zero", got)
	}
}

func TestUpstreamConversionCarriesMaintenanceWindows(t *testing.T) {
	until := time.Date(2026, time.July, 27, 18, 0, 0, 0, time.UTC)
	snap := model.Snapshot{Services: []model.Service{{
		ID:          "x",
		Key:         "x",
		Maintenance: model.Maintenance{Active: true, Until: until, ReasonTermID: "maint.scheduled"},
	}}}

	got := toUpstream(snap).Data.Services[0]

	if got.Maintenance == nil {
		t.Fatal("the maintenance window was dropped")
	}
	if !got.Maintenance.InProgress {
		t.Error("the window is not marked in progress")
	}
	if got.Maintenance.EndsAt != until.Format(time.RFC3339) {
		t.Errorf("endsAt = %q", got.Maintenance.EndsAt)
	}
}

func TestUpstreamConversionOmitsAnAbsentMaintenanceWindow(t *testing.T) {
	snap := model.Snapshot{Services: []model.Service{{ID: "x", Key: "x"}}}

	if got := toUpstream(snap).Data.Services[0]; got.Maintenance != nil {
		t.Errorf("got %+v, want no maintenance object", got.Maintenance)
	}
}

func TestMaintenanceWithoutAnEndTimeStillReports(t *testing.T) {
	// An upstream may know a window is open without knowing when it closes.
	snap := model.Snapshot{Services: []model.Service{{
		ID: "x", Maintenance: model.Maintenance{Active: true, ReasonTermID: "maint.emergency"},
	}}}

	got := toUpstream(snap).Data.Services[0]

	if got.Maintenance == nil || !got.Maintenance.InProgress {
		t.Fatal("the window was dropped for want of an end time")
	}
	if got.Maintenance.EndsAt != "" {
		t.Errorf("endsAt = %q, want empty", got.Maintenance.EndsAt)
	}
}

func TestRunReportsMissingInputs(t *testing.T) {
	dir := t.TempDir()

	if err := run(filepath.Join(dir, "nope.yaml"), configPath, dir); err == nil {
		t.Error("a missing catalogue was accepted")
	}
	if err := run(cataloguePath, filepath.Join(dir, "nope"), dir); err == nil {
		t.Error("a missing config directory was accepted")
	}
}

func TestRunReportsUnparsableInputs(t *testing.T) {
	dir := t.TempDir()

	bad := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(bad, []byte("services: [this is: not valid"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run(bad, configPath, dir); err == nil {
		t.Error("an unparsable catalogue was accepted")
	}

	empty := filepath.Join(dir, "empty.yaml")
	if err := os.WriteFile(empty, []byte("services: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run(empty, configPath, dir); err == nil {
		t.Error("a catalogue with no services was accepted")
	}

	badConfig := t.TempDir()
	if err := os.WriteFile(filepath.Join(badConfig, "domain.yaml"), []byte("scopes: [a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run(cataloguePath, badConfig, dir); err == nil {
		t.Error("an unparsable domain config was accepted")
	}
}

func TestRunReportsAnUnwritableOutputDirectory(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root, which can write regardless of mode")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Skipf("cannot make a directory read-only here: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	if err := run(cataloguePath, configPath, dir); err == nil {
		t.Error("an unwritable output directory was accepted")
	}
}

func TestWriteFileReportsAnUncreatableDirectory(t *testing.T) {
	// A path whose parent is a regular file cannot be created.
	base := t.TempDir()
	blocker := filepath.Join(base, "blocker")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := writeFile(filepath.Join(blocker, "nested", "out.json"), []byte("{}")); err == nil {
		t.Error("writing beneath a regular file was accepted")
	}
}

func TestExcerptStopsAtTheRequestedSize(t *testing.T) {
	// Fewer slots than statuses: the status sweep must stop rather than
	// overfilling.
	snap := model.Snapshot{Services: []model.Service{
		{ID: "a", Status: model.StatusOperational},
		{ID: "b", Status: model.StatusMajor},
		{ID: "c", Status: model.StatusUnknown},
		{ID: "d", Status: model.StatusMaintenance},
		{ID: "e", Status: model.StatusPartial},
	}}

	if got := excerpt(snap, 2); len(got.Services) != 2 {
		t.Errorf("got %d services, want 2", len(got.Services))
	}
}
