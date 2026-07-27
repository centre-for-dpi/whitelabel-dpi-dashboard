// Package apimap converts between the generated protobuf wire types and the
// hand-written domain types in package model.
//
// It exists so that wire-compatibility concerns stay at the boundary: nothing
// downstream of here handles a *timestamppb.Timestamp or a protoimpl-laden
// struct, and nothing upstream of here needs to know how the domain represents
// an absent value.
//
// Two normalisations are applied deliberately, both so that round-tripping is
// an identity rather than merely approximate:
//
//   - Empty and nil collections are indistinguishable on the wire, so decoding
//     always settles on nil.
//   - A zero time.Time means "no such moment" and encodes as an absent
//     timestamp, not as the proto epoch.
package apimap

import (
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	dpiv1 "github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/gen/dpi/v1"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/model"
)

// --- scalars ---------------------------------------------------------------

func timeToProto(t time.Time) *timestamppb.Timestamp {
	if t.IsZero() {
		return nil
	}
	return timestamppb.New(t)
}

func timeFromProto(t *timestamppb.Timestamp) time.Time {
	if t == nil {
		return time.Time{}
	}
	return t.AsTime()
}

func optFloatToProto(o model.OptFloat) *float64 {
	if !o.Valid {
		return nil
	}
	v := o.Value
	return &v
}

func optFloatFromProto(v *float64) model.OptFloat {
	if v == nil {
		return model.NoFloat()
	}
	return model.Float(*v)
}

// StatusToProto encodes a domain status. An unrecognised value encodes as
// UNSPECIFIED rather than silently becoming a real status.
func StatusToProto(s model.Status) dpiv1.Status {
	switch s {
	case model.StatusOperational:
		return dpiv1.Status_STATUS_OPERATIONAL
	case model.StatusPartial:
		return dpiv1.Status_STATUS_PARTIAL
	case model.StatusMajor:
		return dpiv1.Status_STATUS_MAJOR
	case model.StatusUnknown:
		return dpiv1.Status_STATUS_UNKNOWN
	case model.StatusMaintenance:
		return dpiv1.Status_STATUS_MAINTENANCE
	default:
		return dpiv1.Status_STATUS_UNSPECIFIED
	}
}

// StatusFromProto decodes a wire status. Values this build does not recognise —
// including UNSPECIFIED, and anything a newer peer might send — decode to
// StatusUnknown, which is the honest reading of "we cannot tell".
func StatusFromProto(s dpiv1.Status) model.Status {
	switch s {
	case dpiv1.Status_STATUS_OPERATIONAL:
		return model.StatusOperational
	case dpiv1.Status_STATUS_PARTIAL:
		return model.StatusPartial
	case dpiv1.Status_STATUS_MAJOR:
		return model.StatusMajor
	case dpiv1.Status_STATUS_MAINTENANCE:
		return model.StatusMaintenance
	default:
		return model.StatusUnknown
	}
}

// DirectionToProto encodes a trend direction.
func DirectionToProto(d model.Direction) dpiv1.TrendDirection {
	switch d {
	case model.DirectionFlat:
		return dpiv1.TrendDirection_TREND_DIRECTION_FLAT
	case model.DirectionUp:
		return dpiv1.TrendDirection_TREND_DIRECTION_UP
	case model.DirectionDown:
		return dpiv1.TrendDirection_TREND_DIRECTION_DOWN
	default:
		return dpiv1.TrendDirection_TREND_DIRECTION_UNSPECIFIED
	}
}

// DirectionFromProto decodes a trend direction. Unrecognised values decode to
// flat: claiming no movement is safer than inventing a rise or a fall.
func DirectionFromProto(d dpiv1.TrendDirection) model.Direction {
	switch d {
	case dpiv1.TrendDirection_TREND_DIRECTION_UP:
		return model.DirectionUp
	case dpiv1.TrendDirection_TREND_DIRECTION_DOWN:
		return model.DirectionDown
	default:
		return model.DirectionFlat
	}
}

// ErrorClassToProto encodes an error class.
func ErrorClassToProto(c model.ErrorClass) dpiv1.ErrorClass {
	switch c {
	case model.ErrorClassServer:
		return dpiv1.ErrorClass_ERROR_CLASS_SERVER
	case model.ErrorClassClient:
		return dpiv1.ErrorClass_ERROR_CLASS_CLIENT
	case model.ErrorClassNetwork:
		return dpiv1.ErrorClass_ERROR_CLASS_NETWORK
	default:
		return dpiv1.ErrorClass_ERROR_CLASS_UNSPECIFIED
	}
}

// ErrorClassFromProto decodes an error class. Unrecognised values decode to
// network, the class that attributes the fault to neither peer.
func ErrorClassFromProto(c dpiv1.ErrorClass) model.ErrorClass {
	switch c {
	case dpiv1.ErrorClass_ERROR_CLASS_SERVER:
		return model.ErrorClassServer
	case dpiv1.ErrorClass_ERROR_CLASS_CLIENT:
		return model.ErrorClassClient
	default:
		return model.ErrorClassNetwork
	}
}

// --- component messages ----------------------------------------------------

func metricsToProto(m model.Metrics) *dpiv1.Metrics {
	return &dpiv1.Metrics{
		Availability: optFloatToProto(m.Availability),
		ErrorRate:    m.ErrorRate,
		LatencyP50Ms: m.LatencyP50,
		StaleSeconds: m.StaleSeconds,
		Volume: &dpiv1.Volume{
			Total:   m.Volume.Total,
			Success: m.Volume.Success,
		},
	}
}

func metricsFromProto(p *dpiv1.Metrics) model.Metrics {
	if p == nil {
		return model.Metrics{}
	}
	return model.Metrics{
		Availability: optFloatFromProto(p.Availability),
		ErrorRate:    p.GetErrorRate(),
		LatencyP50:   p.GetLatencyP50Ms(),
		StaleSeconds: p.GetStaleSeconds(),
		Volume: model.Volume{
			Total:   p.GetVolume().GetTotal(),
			Success: p.GetVolume().GetSuccess(),
		},
	}
}

// maintenanceToProto omits an entirely empty window. Most services are not
// under maintenance most of the time, and emitting a bare "maintenance": {} on
// each of them is both noise across a full snapshot and a misleading hint that
// some window exists.
func maintenanceToProto(m model.Maintenance) *dpiv1.Maintenance {
	if m == (model.Maintenance{}) {
		return nil
	}
	return &dpiv1.Maintenance{
		Active:       m.Active,
		Until:        timeToProto(m.Until),
		ReasonTermId: m.ReasonTermID,
	}
}

func maintenanceFromProto(p *dpiv1.Maintenance) model.Maintenance {
	if p == nil {
		return model.Maintenance{}
	}
	return model.Maintenance{
		Active:       p.GetActive(),
		Until:        timeFromProto(p.GetUntil()),
		ReasonTermID: p.GetReasonTermId(),
	}
}

func historyToProto(h []model.HistoryPoint) []*dpiv1.HistoryPoint {
	if len(h) == 0 {
		return nil
	}
	out := make([]*dpiv1.HistoryPoint, len(h))
	for i, p := range h {
		out[i] = &dpiv1.HistoryPoint{
			Day:          timeToProto(p.Day),
			Availability: optFloatToProto(p.Availability),
			ErrorRate:    p.ErrorRate,
			LatencyP50Ms: p.LatencyP50,
			Volume:       p.Volume,
			Samples:      p.Samples,
		}
	}
	return out
}

func historyFromProto(p []*dpiv1.HistoryPoint) []model.HistoryPoint {
	if len(p) == 0 {
		return nil
	}
	out := make([]model.HistoryPoint, len(p))
	for i, h := range p {
		out[i] = model.HistoryPoint{
			Day:          timeFromProto(h.GetDay()),
			Availability: optFloatFromProto(h.Availability),
			ErrorRate:    h.GetErrorRate(),
			LatencyP50:   h.GetLatencyP50Ms(),
			Volume:       h.GetVolume(),
			Samples:      h.GetSamples(),
		}
	}
	return out
}

func incidentsToProto(in []model.Incident) []*dpiv1.Incident {
	if len(in) == 0 {
		return nil
	}
	out := make([]*dpiv1.Incident, len(in))
	for i, inc := range in {
		var events []*dpiv1.IncidentEvent
		if len(inc.Events) > 0 {
			events = make([]*dpiv1.IncidentEvent, len(inc.Events))
			for j, e := range inc.Events {
				events[j] = &dpiv1.IncidentEvent{Type: e.Type, At: timeToProto(e.At)}
			}
		}
		out[i] = &dpiv1.Incident{
			Id:         inc.ID,
			ServiceId:  inc.ServiceID,
			Severity:   StatusToProto(inc.Severity),
			OpenedAt:   timeToProto(inc.OpenedAt),
			ClosedAt:   timeToProto(inc.ClosedAt),
			Open:       inc.Open,
			NoteTermId: inc.NoteTermID,
			Events:     events,
		}
	}
	return out
}

func incidentsFromProto(p []*dpiv1.Incident) []model.Incident {
	if len(p) == 0 {
		return nil
	}
	out := make([]model.Incident, len(p))
	for i, inc := range p {
		var events []model.IncidentEvent
		if len(inc.GetEvents()) > 0 {
			events = make([]model.IncidentEvent, len(inc.GetEvents()))
			for j, e := range inc.GetEvents() {
				events[j] = model.IncidentEvent{Type: e.GetType(), At: timeFromProto(e.GetAt())}
			}
		}
		out[i] = model.Incident{
			ID:         inc.GetId(),
			ServiceID:  inc.GetServiceId(),
			Severity:   StatusFromProto(inc.GetSeverity()),
			OpenedAt:   timeFromProto(inc.GetOpenedAt()),
			ClosedAt:   timeFromProto(inc.GetClosedAt()),
			Open:       inc.GetOpen(),
			NoteTermID: inc.GetNoteTermId(),
			Events:     events,
		}
	}
	return out
}

func errorsToProto(in []model.ErrorBucket) []*dpiv1.ErrorBucket {
	if len(in) == 0 {
		return nil
	}
	out := make([]*dpiv1.ErrorBucket, len(in))
	for i, e := range in {
		out[i] = &dpiv1.ErrorBucket{
			Code:   e.Code,
			TermId: e.TermID,
			Class:  ErrorClassToProto(e.Class),
			Count:  e.Count,
			Share:  e.Share,
			Trend:  DirectionToProto(e.Trend),
		}
	}
	return out
}

func errorsFromProto(p []*dpiv1.ErrorBucket) []model.ErrorBucket {
	if len(p) == 0 {
		return nil
	}
	out := make([]model.ErrorBucket, len(p))
	for i, e := range p {
		out[i] = model.ErrorBucket{
			Code:   e.GetCode(),
			TermID: e.GetTermId(),
			Class:  ErrorClassFromProto(e.GetClass()),
			Count:  e.GetCount(),
			Share:  e.GetShare(),
			Trend:  DirectionFromProto(e.GetTrend()),
		}
	}
	return out
}

func trendsToProto(in map[string]model.Trend) map[string]*dpiv1.Trend {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]*dpiv1.Trend, len(in))
	for k, t := range in {
		out[k] = &dpiv1.Trend{
			Delta:      t.Delta,
			Direction:  DirectionToProto(t.Direction),
			PeriodDays: t.PeriodDays,
		}
	}
	return out
}

func trendsFromProto(p map[string]*dpiv1.Trend) map[string]model.Trend {
	if len(p) == 0 {
		return nil
	}
	out := make(map[string]model.Trend, len(p))
	for k, t := range p {
		out[k] = model.Trend{
			Delta:      t.GetDelta(),
			Direction:  DirectionFromProto(t.GetDirection()),
			PeriodDays: t.GetPeriodDays(),
		}
	}
	return out
}

// --- services --------------------------------------------------------------

// ServiceToProto encodes a service for the read API, including the derived
// status, trends and rank movement.
func ServiceToProto(s model.Service) *dpiv1.Service {
	return &dpiv1.Service{
		Id:           s.ID,
		Key:          s.Key,
		NameTermId:   s.NameTermID,
		DescTermId:   s.DescTermID,
		CategoryId:   s.CategoryID,
		RegionId:     s.RegionID,
		ProviderId:   s.ProviderID,
		Scope:        s.Scope,
		Status:       StatusToProto(s.Status),
		Metrics:      metricsToProto(s.Metrics),
		Maintenance:  maintenanceToProto(s.Maintenance),
		History:      historyToProto(s.History),
		Incidents:    incidentsToProto(s.Incidents),
		Errors:       errorsToProto(s.Errors),
		Trends:       trendsToProto(s.Trends),
		RankMovement: s.RankMovement,
		ObservedAt:   timeToProto(s.ObservedAt),
	}
}

// ServiceFromProto decodes a service from the read API.
func ServiceFromProto(p *dpiv1.Service) model.Service {
	if p == nil {
		return model.Service{}
	}
	return model.Service{
		ID:           p.GetId(),
		Key:          p.GetKey(),
		NameTermID:   p.GetNameTermId(),
		DescTermID:   p.GetDescTermId(),
		CategoryID:   p.GetCategoryId(),
		RegionID:     p.GetRegionId(),
		ProviderID:   p.GetProviderId(),
		Scope:        p.GetScope(),
		Status:       statusFromProtoPreservingAbsence(p.GetStatus()),
		Metrics:      metricsFromProto(p.GetMetrics()),
		Maintenance:  maintenanceFromProto(p.GetMaintenance()),
		History:      historyFromProto(p.GetHistory()),
		Incidents:    incidentsFromProto(p.GetIncidents()),
		Errors:       errorsFromProto(p.GetErrors()),
		Trends:       trendsFromProto(p.GetTrends()),
		RankMovement: p.GetRankMovement(),
		ObservedAt:   timeFromProto(p.GetObservedAt()),
	}
}

// statusFromProtoPreservingAbsence keeps an unset status distinguishable from a
// reported one. StatusFromProto maps UNSPECIFIED to StatusUnknown because that
// is the right reading for a service whose state could not be determined; but a
// Service message that simply never had its status populated should decode to
// the empty string, so that the zero value round-trips as the zero value and
// the rules engine can tell "not yet evaluated" from "evaluated as unknown".
func statusFromProtoPreservingAbsence(s dpiv1.Status) model.Status {
	if s == dpiv1.Status_STATUS_UNSPECIFIED {
		return ""
	}
	return StatusFromProto(s)
}

// SnapshotToProto encodes a whole snapshot as the read API's response.
func SnapshotToProto(s model.Snapshot) *dpiv1.ListServicesResponse {
	var services []*dpiv1.Service
	if len(s.Services) > 0 {
		services = make([]*dpiv1.Service, len(s.Services))
		for i, svc := range s.Services {
			services[i] = ServiceToProto(svc)
		}
	}
	return &dpiv1.ListServicesResponse{
		Services:    services,
		GeneratedAt: timeToProto(s.GeneratedAt),
	}
}

// SnapshotFromProto decodes the read API's response.
func SnapshotFromProto(p *dpiv1.ListServicesResponse) model.Snapshot {
	if p == nil {
		return model.Snapshot{}
	}
	var services []model.Service
	if len(p.GetServices()) > 0 {
		services = make([]model.Service, len(p.GetServices()))
		for i, svc := range p.GetServices() {
			services[i] = ServiceFromProto(svc)
		}
	}
	return model.Snapshot{
		Services:    services,
		GeneratedAt: timeFromProto(p.GetGeneratedAt()),
	}
}

// --- ingest ----------------------------------------------------------------

// ServiceToIngestProto encodes a service as a deployment would report it. The
// derived fields — status, trends, rank movement — have no home in the ingest
// message and are dropped: a deployment reports observations, the dashboard
// decides verdicts.
func ServiceToIngestProto(s model.Service) *dpiv1.IngestService {
	return &dpiv1.IngestService{
		Id:          s.ID,
		Key:         s.Key,
		NameTermId:  s.NameTermID,
		DescTermId:  s.DescTermID,
		CategoryId:  s.CategoryID,
		RegionId:    s.RegionID,
		ProviderId:  s.ProviderID,
		Scope:       s.Scope,
		Metrics:     metricsToProto(s.Metrics),
		Maintenance: maintenanceToProto(s.Maintenance),
		History:     historyToProto(s.History),
		Incidents:   incidentsToProto(s.Incidents),
		Errors:      errorsToProto(s.Errors),
		ObservedAt:  timeToProto(s.ObservedAt),
	}
}

// IngestServiceToModel decodes a reported service. Status, Trends and
// RankMovement come back zeroed and are filled in later by the rules engine.
func IngestServiceToModel(p *dpiv1.IngestService) model.Service {
	if p == nil {
		return model.Service{}
	}
	return model.Service{
		ID:          p.GetId(),
		Key:         p.GetKey(),
		NameTermID:  p.GetNameTermId(),
		DescTermID:  p.GetDescTermId(),
		CategoryID:  p.GetCategoryId(),
		RegionID:    p.GetRegionId(),
		ProviderID:  p.GetProviderId(),
		Scope:       p.GetScope(),
		Metrics:     metricsFromProto(p.GetMetrics()),
		Maintenance: maintenanceFromProto(p.GetMaintenance()),
		History:     historyFromProto(p.GetHistory()),
		Incidents:   incidentsFromProto(p.GetIncidents()),
		Errors:      errorsFromProto(p.GetErrors()),
		ObservedAt:  timeFromProto(p.GetObservedAt()),
	}
}
