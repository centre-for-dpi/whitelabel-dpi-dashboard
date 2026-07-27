package main

import (
	"math"
	"strings"
	"time"

	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/model"
)

// The upstream fixture is deliberately NOT the dashboard's own wire format.
//
// It is shaped like somebody else's API: ratios rather than percentages,
// upper-case enum values rather than taxonomy ids, "succeeded" rather than
// "success", an age in seconds rather than a timestamp. That is the whole
// point. If the example upstream already spoke the dashboard's language, the
// mapping block in sources.yaml would be an identity function and would teach a
// reader nothing about integrating a real service.
//
// Every difference below corresponds to something the mapping has to do:
//
//	uptimePct        a ratio          -> ratioToPercent
//	category         "IDENTITY"       -> enumMap onto cat.identity
//	region           "NATIONAL"       -> enumMap onto reg.national
//	observedAgeSecs  a relative age   -> mapped straight onto staleSeconds
//	daily[]          nested history   -> the history block
type upstreamDocument struct {
	GeneratedAt string          `json:"generatedAt"`
	Data        upstreamPayload `json:"data"`
}

type upstreamPayload struct {
	Services []upstreamService `json:"services"`
}

type upstreamService struct {
	ServiceID   string `json:"serviceId"`
	DisplayName string `json:"displayName"`
	Summary     string `json:"summary"`
	Category    string `json:"category"`
	Region      string `json:"region"`
	Operator    string `json:"operator"`
	Tier        string `json:"tier"`

	SLA      upstreamSLA      `json:"sla"`
	Latency  upstreamLatency  `json:"latency"`
	Requests upstreamRequests `json:"requests"`

	ObservedAgeSecs int64 `json:"observedAgeSecs"`

	Maintenance *upstreamMaintenance `json:"maintenance,omitempty"`
	Daily       []upstreamDaily      `json:"daily"`
}

type upstreamSLA struct {
	// Ratios in [0,1], not percentages. A collector that reports 0.9991 and a
	// dashboard that renders 99.91% is the single most common integration
	// mistake, so the example makes it explicit.
	UptimePct *float64 `json:"uptimePct"`
	ErrorPct  float64  `json:"errorPct"`
}

type upstreamLatency struct {
	P50Ms int32 `json:"p50Ms"`
}

type upstreamRequests struct {
	Total     int64 `json:"total"`
	Succeeded int64 `json:"succeeded"`
}

type upstreamMaintenance struct {
	InProgress bool   `json:"inProgress"`
	EndsAt     string `json:"endsAt,omitempty"`
	Reason     string `json:"reason,omitempty"`
}

type upstreamDaily struct {
	TS     string   `json:"ts"`
	Uptime *float64 `json:"uptime"`
	ErrPct float64  `json:"errPct"`
	Count  int64    `json:"count"`
	P50Ms  int32    `json:"p50Ms"`
}

// toUpstream renders a generated snapshot as a foreign API would report it.
func toUpstream(snap model.Snapshot) upstreamDocument {
	doc := upstreamDocument{
		GeneratedAt: snap.GeneratedAt.Format(time.RFC3339),
		Data:        upstreamPayload{Services: make([]upstreamService, 0, len(snap.Services))},
	}

	for _, sv := range snap.Services {
		u := upstreamService{
			ServiceID:   sv.ID,
			DisplayName: displayName(sv.Key),
			Summary:     "Example service record for " + displayName(sv.Key) + ".",
			Category:    enumCase(sv.CategoryID),
			Region:      enumCase(sv.RegionID),
			Operator:    enumCase(sv.ProviderID),
			Tier:        strings.ToUpper(sv.Scope),
			SLA: upstreamSLA{
				UptimePct: ratio(sv.Metrics.Availability),
				ErrorPct:  ratioOf(sv.Metrics.ErrorRate),
			},
			Latency:         upstreamLatency{P50Ms: sv.Metrics.LatencyP50},
			Requests:        upstreamRequests{Total: sv.Metrics.Volume.Total, Succeeded: sv.Metrics.Volume.Success},
			ObservedAgeSecs: sv.Metrics.StaleSeconds,
		}

		if sv.Maintenance.Active {
			m := &upstreamMaintenance{InProgress: true, Reason: sv.Maintenance.ReasonTermID}
			if !sv.Maintenance.Until.IsZero() {
				m.EndsAt = sv.Maintenance.Until.Format(time.RFC3339)
			}
			u.Maintenance = m
		}

		for _, p := range sv.History {
			u.Daily = append(u.Daily, upstreamDaily{
				TS:     p.Day.Format("2006-01-02"),
				Uptime: ratio(p.Availability),
				ErrPct: ratioOf(p.ErrorRate),
				Count:  p.Volume,
				P50Ms:  p.LatencyP50,
			})
		}

		doc.Data.Services = append(doc.Data.Services, u)
	}
	return doc
}

// ratio converts a percentage to the [0,1] form the example upstream reports.
// An absent reading stays absent: reporting it as 0 would claim a total outage.
func ratio(v model.OptFloat) *float64 {
	if !v.Valid {
		return nil
	}
	r := ratioOf(v.Value)
	return &r
}

// ratioOf divides by a hundred and rounds away the binary-floating-point tail.
// Without this, 0.55 / 100 serialises as 0.005500000000000001, which looks like
// a precision claim nobody is making and makes the fixture diff noisily.
func ratioOf(percent float64) float64 {
	return math.Round(percent/100*1e6) / 1e6
}

// enumCase turns a taxonomy id into the upper-case enum a typical API would
// use, so the mapping has a real translation to perform.
func enumCase(id string) string {
	_, name, found := strings.Cut(id, ".")
	if !found {
		name = id
	}
	var b strings.Builder
	for i, r := range name {
		if i > 0 && r >= 'A' && r <= 'Z' {
			b.WriteByte('_')
		}
		b.WriteRune(r)
	}
	return strings.ToUpper(b.String())
}

// displayName turns a camelCase key into something a person would read, which
// is what a real API would send instead of a translation key.
func displayName(key string) string {
	var b strings.Builder
	for i, r := range key {
		switch {
		case i == 0:
			b.WriteString(strings.ToUpper(string(r)))
		case r >= 'A' && r <= 'Z':
			b.WriteByte(' ')
			b.WriteRune(r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
