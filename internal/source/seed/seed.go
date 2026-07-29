// Package seed generates demonstration data.
//
// It exists for two reasons. The first is that a fresh deploy should render
// something immediately, rather than an empty page that gives no sense of what
// the dashboard is for. The second, and more useful, is that the generated data
// is written out in exactly the shapes the pull driver and the ingest API
// expect — so a team wiring up their own service has a worked example of both
// wire formats to compare against, rather than only a schema.
//
// Generation is pure and seeded: the same catalogue and seed always produce the
// same dashboard. Two people looking at the same demo see the same thing, and
// tests can assert on it.
//
// Nothing here decides a status. The generated metrics are run through the same
// threshold evaluator the dashboard uses at runtime, so the demo demonstrates
// the real rule rather than a decorative label.
package seed

import (
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/config"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/model"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/rules"
)

// Catalogue is the example data the generator works from.
type Catalogue struct {
	Mix            Mix              `yaml:"mix"`
	RequestorMix   Mix              `yaml:"requestorMix"`
	Traffic        map[string]int64 `yaml:"traffic"`
	DefaultTraffic int64            `yaml:"defaultTraffic"`
	Services       []Entry          `yaml:"services"`
	Requestors     []Requestor      `yaml:"requestors"`
	StateExpansion []string         `yaml:"stateExpansion"`
}

// Mix is roughly how many services should land in each state. Everything not
// accounted for here is generated as healthy.
type Mix struct {
	Major       int `yaml:"major"`
	Partial     int `yaml:"partial"`
	Maintenance int `yaml:"maintenance"`
	Unknown     int `yaml:"unknown"`
}

// Entry is one service in the catalogue.
type Entry struct {
	Key            string `yaml:"key"`
	Category       string `yaml:"category"`
	Provider       string `yaml:"provider"`
	Scope          string `yaml:"scope"`
	StateInstances bool   `yaml:"stateInstances"`
}

// Requestor is one demand-side entry: an organisation calling a published API
// to complete a task for someone.
type Requestor struct {
	Key      string `yaml:"key"`
	Sector   string `yaml:"sector"`
	Category string `yaml:"category"`
	// Calls is the Key of the service consumed.
	Calls        string `yaml:"calls"`
	Subscription string `yaml:"subscription"`
	// StateInstances expands the requestor once per state, and States caps how
	// many. Coverage is uneven in the real roster — a lender operating in seven
	// states is not one operating in twelve — and a cap is how that is said.
	StateInstances bool `yaml:"stateInstances"`
	States         int  `yaml:"states"`
}

// Options tune the generator.
type Options struct {
	// Seed makes the output reproducible.
	Seed uint32
	// HistoryDays is how much past to invent. It should match the configured
	// retention, or the charts will have less to draw than they expect.
	HistoryDays int
	// Now anchors the generated timeline. It is a parameter rather than a call
	// to time.Now so that the same inputs always give the same output.
	Now time.Time
}

// DefaultOptions are what `make seed` uses.
func DefaultOptions(now time.Time) Options {
	return Options{Seed: 1000, HistoryDays: 90, Now: now}
}

// tier shapes the numbers generated for one service. It is not a status: the
// status is derived from the numbers afterwards.
type tier string

const (
	tierOperational tier = "operational"
	tierPartial     tier = "partial"
	tierMajor       tier = "major"
	tierMaintenance tier = "maintenance"
	tierUnknown     tier = "unknown"
)

// Generate builds a full snapshot.
//
// Both sides of the exchange land in the same list, distinguished by role. A
// requestor is the same record as the service it calls — same metrics, same
// history, same rules — because it is the same exchange seen from the other
// end, and the dashboard reads one role at a time.
func Generate(cat Catalogue, d config.Domain, opts Options) model.Snapshot {
	entries := expand(cat)
	tiers := assignTiers(len(entries), cat.Mix)

	services := make([]model.Service, 0, len(entries)+len(cat.Requestors))
	for i, e := range entries {
		services = append(services, generateService(e, tiers[i], cat, d, opts))
	}

	demand := expandRequestors(cat)
	demandTiers := assignTiers(len(demand), cat.RequestorMix)
	for i, e := range demand {
		services = append(services, generateRequestor(e, demandTiers[i], cat, d, opts))
	}

	return model.Snapshot{Services: services, GeneratedAt: opts.Now}
}

// instance is one catalogue entry after regional expansion.
type instance struct {
	Entry
	id     string
	region string
	scope  string
	index  int
}

// expand turns catalogue entries into concrete service instances, one per
// region for those marked stateInstances.
//
// The prototype truncated this list to a round number, which silently dropped
// two whole categories — their filter chips were present and always returned
// nothing. Everything the catalogue declares is generated here.
func expand(cat Catalogue) []instance {
	var out []instance
	for _, e := range cat.Services {
		if !e.StateInstances {
			out = append(out, instance{
				Entry: e, id: e.Key, region: "reg.national", scope: e.Scope, index: len(out),
			})
			continue
		}
		for _, region := range cat.StateExpansion {
			out = append(out, instance{
				Entry:  e,
				id:     e.Key + "__" + strings.TrimPrefix(region, "reg."),
				region: region,
				scope:  e.Scope,
				index:  len(out),
			})
		}
	}
	return out
}

// demandInstance is one requestor after regional expansion.
type demandInstance struct {
	Requestor
	id     string
	region string
	scope  string
	index  int
}

// expandRequestors is expand() for the demand side, with one difference: a
// requestor's state coverage is capped rather than complete. A lender operating
// in seven states is not one operating in twelve, and generating both the same
// way would flatten exactly the unevenness the demand view exists to show.
func expandRequestors(cat Catalogue) []demandInstance {
	var out []demandInstance
	for _, e := range cat.Requestors {
		if !e.StateInstances {
			out = append(out, demandInstance{
				Requestor: e, id: "req-" + e.Key,
				region: "reg.national", scope: "national", index: len(out),
			})
			continue
		}
		regions := cat.StateExpansion
		if e.States > 0 && e.States < len(regions) {
			regions = regions[:e.States]
		}
		for _, region := range regions {
			out = append(out, demandInstance{
				Requestor: e,
				id:        "req-" + e.Key + "__" + strings.TrimPrefix(region, "reg."),
				region:    region,
				scope:     "state",
				index:     len(out),
			})
		}
	}
	return out
}

// assignTiers spreads the non-healthy states evenly through the list.
//
// Evenly, rather than randomly or in a block, so that any view of the data — a
// single category, one region, the first page — contains some variety. A demo
// where the first twenty services are all healthy teaches the reader nothing
// about what an unhealthy one looks like.
func assignTiers(n int, mix Mix) []tier {
	out := make([]tier, n)
	for i := range out {
		out[i] = tierOperational
	}
	if n == 0 {
		return out
	}

	// Fixed order, so the result does not depend on map iteration.
	for _, t := range []struct {
		tier  tier
		count int
	}{
		{tierMajor, mix.Major},
		{tierPartial, mix.Partial},
		{tierMaintenance, mix.Maintenance},
		{tierUnknown, mix.Unknown},
	} {
		place(out, t.tier, t.count)
	}
	return out
}

func place(out []tier, t tier, count int) {
	n := len(out)
	if count <= 0 || n == 0 {
		return
	}
	count = min(count, n)
	stride := float64(n) / float64(count)

	for i := range count {
		idx := int(float64(i)*stride + stride/2)
		// Walk forward to the next free slot, so tiers placed later do not
		// overwrite earlier ones.
		for range n {
			if out[idx%n] == tierOperational {
				break
			}
			idx++
		}
		out[idx%n] = t
	}
}

func generateService(in instance, t tier, cat Catalogue, d config.Domain, opts Options) model.Service {
	// Seeded per service, so adding a service to the catalogue does not change
	// the numbers of every service after it.
	r := newRNG(opts.Seed + uint32(in.index)*97)

	m, maint := generateMetrics(r, t, in.Key, cat, opts.Now)
	history := generateHistory(r, m, t, opts)

	sv := model.Service{
		ID:          in.id,
		Key:         in.Key,
		NameTermID:  "svc." + in.Key + ".name",
		DescTermID:  "svc." + in.Key + ".desc",
		CategoryID:  in.Category,
		RegionID:    in.region,
		ProviderID:  in.Provider,
		Scope:       in.scope,
		Metrics:     m,
		Maintenance: maint,
		History:     history,
		Incidents:   generateIncidents(r, t, in.id, opts.Now),
		Errors:      generateErrors(r, t, m, sideIssuer),
		ObservedAt:  opts.Now,
		RoleID:      roleIssuer,
	}

	// The status comes from the same evaluator the dashboard uses at runtime,
	// so the demo demonstrates the published rule rather than asserting a label.
	sv.Status = rules.Evaluate(sv.Metrics, sv.Maintenance, d.Thresholds)
	sv.Trends = trendsOf(history)
	sv.RankMovement = int32(r.intBetween(-3, 3))
	return sv
}

// The roles the shipped example declares. A deployment can name its own; these
// are what the generator produces, and what config/domain.yaml lists.
const (
	roleIssuer    = "issuer"
	roleRequestor = "requestor"
)

func trendsOf(history []model.HistoryPoint) map[string]model.Trend {
	out := map[string]model.Trend{}
	for _, field := range []string{
		config.FieldAvailability,
		config.FieldErrorRate,
		config.FieldLatencyP50,
		config.FieldVolume,
	} {
		out[field] = rules.Trend(history, field, 30)
	}
	return out
}

// generateRequestor builds one demand-side record.
//
// Seeded from a different offset than the supply side, so that adding or
// removing a service does not renumber every requestor and redraw half the
// demo.
func generateRequestor(in demandInstance, t tier, cat Catalogue, d config.Domain, opts Options) model.Service {
	r := newRNG(opts.Seed + 6700 + uint32(in.index)*131)

	m, maint := generateMetrics(r, t, in.Key, cat, opts.Now)
	history := generateHistory(r, m, t, opts)
	errs := generateErrors(r, t, m, sideRequestor)

	sv := model.Service{
		ID:         in.id,
		Key:        in.Key,
		NameTermID: "req." + in.Key + ".name",
		DescTermID: "req." + in.Key + ".desc",
		CategoryID: in.Category,
		RegionID:   in.region,
		// A requestor is run by its sector, so that is what stands where a
		// service's provider does.
		ProviderID:       in.Sector,
		Scope:            in.scope,
		RoleID:           roleRequestor,
		SectorID:         in.Sector,
		CallsKey:         in.Calls,
		SubscriptionType: in.Subscription,
		OwnErrorShare:    ownErrorShare(errs),
		Metrics:          m,
		Maintenance:      maint,
		History:          history,
		Incidents:        generateIncidents(r, t, in.id, opts.Now),
		Errors:           errs,
		ObservedAt:       opts.Now,
	}

	sv.Status = rules.Evaluate(sv.Metrics, sv.Maintenance, d.Thresholds)
	sv.Trends = trendsOf(history)
	sv.RankMovement = int32(r.intBetween(-3, 3))
	return sv
}

// ownErrorShare is the percent of a requestor's errors that are its own: a
// request it malformed rather than a response that failed. Telling a requestor
// its error rate without telling it whose errors they are gives it a number it
// cannot act on.
func ownErrorShare(errs []model.ErrorBucket) float64 {
	var own, all int64
	for _, e := range errs {
		all += e.Count
		if e.Class != model.ErrorClassServer {
			own += e.Count
		}
	}
	if all == 0 {
		return 0
	}
	return math.Round(float64(own)/float64(all)*1000) / 10
}

func generateMetrics(r *rng, t tier, key string, cat Catalogue, now time.Time) (model.Metrics, model.Maintenance) {
	var (
		availability = model.NoFloat()
		errorRate    float64
		latency      int32
		stale        = r.intBetween(20, 240)
		maint        model.Maintenance
	)

	switch t {
	case tierOperational:
		availability = model.Float(round2(r.between(99.62, 99.98)))
		errorRate = round2(r.between(0.04, 0.88))
		latency = int32(r.intBetween(110, 285))

	case tierPartial:
		// One of the two ways to be partial, so the demo shows both a service
		// that is dropping requests and one that is merely erroring.
		if r.float() > 0.5 {
			availability = model.Float(round2(r.between(99.5, 99.9)))
			errorRate = round2(r.between(1.12, 1.9))
		} else {
			availability = model.Float(round2(r.between(99.08, 99.44)))
			errorRate = round2(r.between(0.6, 0.95))
		}
		latency = int32(r.intBetween(280, 470))

	case tierMajor:
		availability = model.Float(round2(r.between(97.2, 98.85)))
		errorRate = round2(r.between(1.9, 4.8))
		latency = int32(r.intBetween(420, 920))

	case tierMaintenance:
		availability = model.Float(round2(r.between(99.4, 99.9)))
		errorRate = round2(r.between(0.1, 0.9))
		latency = int32(r.intBetween(130, 300))
		maint = model.Maintenance{
			Active:       true,
			Until:        now.Add(time.Duration(r.between(0.5, 4) * float64(time.Hour))),
			ReasonTermID: "maint.scheduled",
		}

	case tierUnknown:
		// No availability reading at all, and a stale observation. Either alone
		// is enough; together they show both halves of the rule.
		errorRate = round2(r.between(0.2, 1.2))
		latency = int32(r.intBetween(150, 400))
		stale = r.intBetween(1100, 7200)
	}

	base, ok := cat.Traffic[key]
	if !ok {
		base = int64(r.between(float64(cat.DefaultTraffic)/6, float64(cat.DefaultTraffic)*3.5))
	}
	total := int64(float64(base) * r.between(0.85, 1.15))

	// Successes follow the error rate, so the two numbers agree with each other
	// rather than being invented separately.
	successRate := 1 - errorRate/100
	if !availability.Valid {
		successRate *= 0.97
	}
	success := int64(float64(total) * successRate)

	return model.Metrics{
		Availability: availability,
		ErrorRate:    errorRate,
		LatencyP50:   latency,
		StaleSeconds: stale,
		Volume:       model.Volume{Total: total, Success: success},
	}, maint
}

func generateHistory(r *rng, m model.Metrics, t tier, opts Options) []model.HistoryPoint {
	days := opts.HistoryDays
	if days <= 0 {
		return nil
	}

	baseAvail := m.Availability.Or(99.4)
	baseErr := m.ErrorRate
	if baseErr == 0 {
		baseErr = 0.5
	}
	baseVol := float64(m.Volume.Total)

	// A recent bad patch for the services that are currently struggling, so the
	// sparkline shows the incident the status is reporting rather than a flat
	// line that contradicts it.
	incidentDay := -1
	switch t {
	case tierMajor:
		incidentDay = int(r.between(1, 4))
	case tierPartial:
		incidentDay = int(r.between(1, 9))
	}

	out := make([]model.HistoryPoint, 0, days)
	day := opts.Now.UTC().Truncate(24 * time.Hour)

	for d := days - 1; d >= 0; d-- {
		av := baseAvail + r.between(-0.18, 0.18)
		er := math.Max(0.02, baseErr+r.between(-0.25, 0.35))
		vol := baseVol * r.between(0.72, 1.18) * (1 + 0.12*math.Sin(float64(d)/6))

		if d == incidentDay || d == incidentDay-1 {
			av -= r.between(0.8, 2.6)
			er += r.between(1.2, 3.5)
		}

		// A slow drift, so that a thirty-day trend has something to report.
		if t == tierMajor || t == tierPartial {
			av -= float64(days-d) * 0.004
		} else {
			av += float64(days-d) * 0.0009
		}

		point := model.HistoryPoint{
			Day:        day.AddDate(0, 0, -d),
			ErrorRate:  round2(math.Max(0.02, er)),
			Volume:     int64(vol),
			LatencyP50: int32(float64(m.LatencyP50) * r.between(0.85, 1.2)),
			Samples:    96,
		}
		// A service that is not reporting now was not reporting then either.
		if m.Availability.Valid {
			point.Availability = model.Float(round2(math.Min(99.99, av)))
		}
		out = append(out, point)
	}
	return out
}

var incidentNotes = map[tier][]string{
	tierMajor:   {"incident.note.errorSpike", "incident.note.upstreamTimeout", "incident.note.dbFailover"},
	tierPartial: {"incident.note.latencyDegraded", "incident.note.partialRegion", "incident.note.rateLimited"},
}

func generateIncidents(r *rng, t tier, serviceID string, now time.Time) []model.Incident {
	var out []model.Incident

	add := func(severity model.Status, open bool) {
		openedAt := now.Add(-time.Duration(r.between(1.5, 14) * float64(time.Hour)))

		events := []model.IncidentEvent{
			{Type: "opened", At: openedAt},
			{Type: "acknowledged", At: openedAt.Add(time.Duration(r.between(4, 22) * float64(time.Minute)))},
		}
		var closedAt time.Time
		if !open || r.float() > 0.5 {
			events = append(events, model.IncidentEvent{
				Type: "mitigated",
				At:   openedAt.Add(time.Duration(r.between(40, 130) * float64(time.Minute))),
			})
		}
		if !open {
			closedAt = openedAt.Add(time.Duration(r.between(150, 340) * float64(time.Minute)))
			events = append(events, model.IncidentEvent{Type: "resolved", At: closedAt})
		}

		out = append(out, model.Incident{
			ID:         serviceID + "-inc-" + strconv.FormatInt(r.intBetween(100000, 999999), 36),
			ServiceID:  serviceID,
			Severity:   severity,
			OpenedAt:   openedAt,
			ClosedAt:   closedAt,
			Open:       open,
			NoteTermID: pick(r, incidentNotes[severityTier(severity)]),
			Events:     events,
		})
	}

	switch t {
	case tierMajor:
		add(model.StatusMajor, true)
		if r.float() > 0.55 {
			add(model.StatusPartial, false)
		}
	case tierPartial:
		add(model.StatusPartial, r.float() > 0.4)
	default:
		// A few healthy services carry a resolved incident, because a history
		// with no closed incidents at all reads as implausible.
		if r.float() > 0.85 {
			add(model.StatusPartial, false)
		}
	}

	// Newest first, which is the order the drawer reads them in.
	slices.SortStableFunc(out, func(a, b model.Incident) int { return b.OpenedAt.Compare(a.OpenedAt) })
	return out
}

func severityTier(s model.Status) tier {
	if s == model.StatusMajor {
		return tierMajor
	}
	return tierPartial
}

type errorCode struct {
	code   string
	termID string
	class  model.ErrorClass
}

var errorCodes = []errorCode{
	{"500", "err.500", model.ErrorClassServer},
	{"503", "err.503", model.ErrorClassServer},
	{"504", "err.504", model.ErrorClassServer},
	{"429", "err.429", model.ErrorClassClient},
	{"401", "err.401", model.ErrorClassClient},
	{"400", "err.400", model.ErrorClassClient},
	{"timeout", "err.timeout", model.ErrorClassNetwork},
}

// side is which end of the exchange an error breakdown is being generated for.
// They fail differently: a publisher's errors are mostly its own servers, a
// requestor's are mostly its own requests.
type side int

const (
	sideIssuer side = iota
	sideRequestor
)

func generateErrors(r *rng, t tier, m model.Metrics, sd side) []model.ErrorBucket {
	totalErrors := int64(float64(m.Volume.Total) * m.ErrorRate / 100)
	if totalErrors <= 0 {
		return nil
	}

	// Weighted so the breakdown tells a story consistent with the status: a
	// major outage is mostly the server's fault, a partial one is more often
	// rate limiting.
	weights := make([]float64, len(errorCodes))
	var sum float64
	for i, c := range errorCodes {
		w := r.between(0.2, 1)
		switch {
		case t == tierMajor && c.class == model.ErrorClassServer:
			w *= 3.2
		case t == tierPartial && c.class == model.ErrorClassClient:
			w *= 2.4
		}
		// What a requestor sees is weighted the other way: most of what fails
		// on its side is a request it sent badly, not a response that broke.
		if sd == sideRequestor {
			switch c.class {
			case model.ErrorClassClient:
				w *= 3.0
			case model.ErrorClassServer:
				w *= 0.6
			}
		}
		weights[i] = w
		sum += w
	}

	out := make([]model.ErrorBucket, 0, len(errorCodes))
	for i, c := range errorCodes {
		share := weights[i] / sum
		count := int64(float64(totalErrors)*share + 0.5)
		if count == 0 {
			continue
		}
		out = append(out, model.ErrorBucket{
			Code:   c.code,
			TermID: c.termID,
			Class:  c.class,
			Count:  count,
			Share:  math.Round(share*1000) / 10,
			Trend:  randomDirection(r),
		})
	}

	// Largest first: the reader wants to know what is failing most.
	slices.SortStableFunc(out, func(a, b model.ErrorBucket) int { return int(b.Count - a.Count) })
	return out
}

func randomDirection(r *rng) model.Direction {
	switch v := r.float(); {
	case v > 0.6:
		return model.DirectionUp
	case v < 0.3:
		return model.DirectionDown
	default:
		return model.DirectionFlat
	}
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }
