// Package model holds the dashboard's domain types.
//
// These are hand-written value types rather than generated protobuf structs, on
// purpose. The whole rendering pipeline downstream of this package is pure —
// rules, query, chart and widget are total functions from data to data — and
// that discipline depends on types that are comparable with ==, cheap to write
// as literals in table-driven tests, and free of the pointer-heavy optionality
// and protoimpl internals that generated code carries.
//
// Wire types live in internal/gen/dpi/v1 and are converted at the boundary by
// internal/apimap, which keeps protobuf compatibility concerns from leaking into
// the domain logic.
package model

import "time"

// Status is the verdict for a service. The vocabulary is fixed: the threshold
// rules in package rules are structurally tied to these five outcomes.
type Status string

const (
	StatusUnknown     Status = "unknown"
	StatusOperational Status = "operational"
	StatusPartial     Status = "partial"
	StatusMajor       Status = "major"
	StatusMaintenance Status = "maintenance"
)

// Direction is the sense of a trend or of a single error bucket's movement.
type Direction string

const (
	DirectionFlat Direction = "flat"
	DirectionUp   Direction = "up"
	DirectionDown Direction = "down"
)

// ErrorClass groups error codes for colouring and aggregation. The string
// values match the CSS status tokens the templates look up.
type ErrorClass string

const (
	ErrorClassServer  ErrorClass = "5xx"
	ErrorClassClient  ErrorClass = "4xx"
	ErrorClassNetwork ErrorClass = "net"
)

// OptFloat is an optional float64.
//
// Availability is genuinely absent for services that have not reported, and
// that absence is load-bearing: it produces StatusUnknown rather than being
// coerced to 0, which would read as a total outage. A struct rather than a
// *float64 keeps the enclosing types comparable and keeps test fixtures terse.
type OptFloat struct {
	Value float64
	Valid bool
}

// Float returns a present OptFloat.
func Float(v float64) OptFloat { return OptFloat{Value: v, Valid: true} }

// NoFloat returns an absent OptFloat. It is the zero value, named for use at
// call sites where the absence is deliberate rather than incidental.
func NoFloat() OptFloat { return OptFloat{} }

// Or returns the contained value, or def when absent.
func (o OptFloat) Or(def float64) float64 {
	if !o.Valid {
		return def
	}
	return o.Value
}

// Volume is a request count and how many of those succeeded.
type Volume struct {
	Total   int64
	Success int64
}

// Metrics is one observation of a service.
type Metrics struct {
	Availability OptFloat // percent in [0,100]
	ErrorRate    float64  // percent in [0,100]
	LatencyP50   int32    // milliseconds
	StaleSeconds int64    // age of the underlying observation
	Volume       Volume
}

// Maintenance is a planned window. Active is authoritative; Until is advisory
// and may be zero even while active.
type Maintenance struct {
	Active       bool
	Until        time.Time
	ReasonTermID string
}

// HistoryPoint is one sealed daily bucket. Day is UTC midnight. Samples counts
// the raw observations folded in, and is zero for buckets supplied wholesale by
// an upstream.
type HistoryPoint struct {
	Day          time.Time
	Availability OptFloat
	ErrorRate    float64
	LatencyP50   int32
	Volume       int64
	Samples      int32
}

// IncidentEvent is one stage in an incident's lifecycle. Type is free-form and
// resolved for display as the term id "inc.<type>", so a deployment adds a
// stage by adding a locale key rather than by changing the wire contract.
type IncidentEvent struct {
	Type string
	At   time.Time
}

// Incident is an outage record. ClosedAt is zero while Open.
type Incident struct {
	ID         string
	ServiceID  string
	Severity   Status
	OpenedAt   time.Time
	ClosedAt   time.Time
	Open       bool
	NoteTermID string
	Events     []IncidentEvent
}

// ErrorBucket is one error code's contribution to a service's error volume.
type ErrorBucket struct {
	Code   string
	TermID string
	Class  ErrorClass
	Count  int64
	Share  float64 // percent of this service's total errors
	Trend  Direction
}

// Trend is a metric's movement over PeriodDays.
type Trend struct {
	Delta      float64
	Direction  Direction
	PeriodDays int32
}

// Trend map keys, matching the metric field names used across config and the
// wire contract.
const (
	TrendAvailability = "availability"
	TrendErrorRate    = "errorRate"
	TrendLatencyP50   = "latencyP50"
	TrendVolume       = "volume"
)

// Service is one onboarded service and everything the dashboard knows about it.
//
// Status, Trends and RankMovement are derived: they are recomputed from Metrics
// and History against the published thresholds rather than accepted from an
// upstream, so the rule shown on screen is provably the rule that was applied.
type Service struct {
	ID         string
	Key        string
	NameTermID string
	DescTermID string
	CategoryID string
	RegionID   string
	ProviderID string
	Scope      string

	Status      Status // derived
	Metrics     Metrics
	Maintenance Maintenance

	History   []HistoryPoint
	Incidents []Incident
	Errors    []ErrorBucket

	Trends       map[string]Trend // derived
	RankMovement int32            // derived
	ObservedAt   time.Time
}

// Sample is a single raw observation awaiting rollup into a HistoryPoint.
type Sample struct {
	ServiceID    string
	At           time.Time
	Availability OptFloat
	ErrorRate    float64
	LatencyP50   int32
	Volume       int64
}

// Snapshot is the complete dashboard state at a moment in time. It is swapped
// atomically, so readers never observe a partially updated set.
type Snapshot struct {
	Services    []Service
	GeneratedAt time.Time
}

// IngestRecord is one audit entry for a source driver's attempt to update state.
type IngestRecord struct {
	At        time.Time
	Driver    string
	SourceID  string
	OK        bool
	Err       string
	ItemCount int
}
