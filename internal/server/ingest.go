package server

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/apimap"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/config"
	dpiv1 "github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/gen/dpi/v1"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/model"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/rules"
)

// Sink accepts pushed data. A source that can be written to implements it;
// one that polls does not, which is how the route is only registered when the
// deployment is actually configured for push.
type Sink interface {
	Snapshots
	Store(model.Snapshot)
}

// IngestOptions configure the push endpoint.
type IngestOptions struct {
	Sink Sink
	// Token is the bearer credential collectors must present. An empty token
	// disables the endpoint entirely rather than leaving it open: an
	// unauthenticated ingest lets anyone rewrite the dashboard.
	Token string
	// MaxBodyBytes caps a request, so a runaway collector cannot exhaust
	// memory.
	MaxBodyBytes int64
}

// Content types the endpoint accepts. Both carry the same message; a team with
// curl and a shell script never needs to learn protobuf exists.
const (
	contentJSON  = "application/json"
	contentProto = "application/x-protobuf"
)

func (s *Server) handleIngest(w http.ResponseWriter, r *http.Request) {
	if !s.authorised(r) {
		// A bare 401 with no detail: telling an unauthenticated caller why
		// their token was rejected helps them guess at a valid one.
		w.Header().Set("www-authenticate", `Bearer realm="ingest"`)
		http.Error(w, "unauthorised", http.StatusUnauthorized)
		return
	}

	req, err := s.decodeIngest(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	resp := s.applyIngest(req)
	s.writeProto(w, r, resp)
}

// authorised compares the bearer token in constant time.
//
// A byte-by-byte comparison leaks the token's prefix through timing, which over
// enough attempts is enough to recover it.
func (s *Server) authorised(r *http.Request) bool {
	if s.ingest.Token == "" {
		return false
	}
	presented, ok := strings.CutPrefix(r.Header.Get("authorization"), "Bearer ")
	if !ok {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(strings.TrimSpace(presented)), []byte(s.ingest.Token)) == 1
}

func (s *Server) decodeIngest(r *http.Request) (*dpiv1.IngestRequest, error) {
	limit := s.ingest.MaxBodyBytes
	body, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("reading the request body: %w", err)
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("the payload exceeds the %d byte limit", limit)
	}
	if len(body) == 0 {
		return nil, errors.New("the request body is empty")
	}

	var req dpiv1.IngestRequest
	mediaType, _, _ := strings.Cut(r.Header.Get("content-type"), ";")

	switch strings.TrimSpace(mediaType) {
	case contentProto:
		if err := proto.Unmarshal(body, &req); err != nil {
			return nil, fmt.Errorf("the body is not a valid protobuf IngestRequest: %w", err)
		}
	default:
		// JSON is the default, so a collector that sets no content type at all
		// still works. Protobuf is opt-in, never an obligation.
		if err := protojson.Unmarshal(body, &req); err != nil {
			return nil, fmt.Errorf("the body is not a valid IngestRequest: %w", err)
		}
	}
	return &req, nil
}

// applyIngest validates each service and stores the ones that pass.
//
// Partial acceptance on purpose: one malformed record in a batch of two hundred
// should not reject the other hundred and ninety-nine, and the response says
// exactly which ones were dropped and why.
func (s *Server) applyIngest(req *dpiv1.IngestRequest) *dpiv1.IngestResponse {
	now := s.clock.Now()
	resp := &dpiv1.IngestResponse{ReceivedAt: timestamppb.New(now)}

	accepted := make([]model.Service, 0, len(req.GetServices()))
	for _, in := range req.GetServices() {
		sv := apimap.IngestServiceToModel(in)

		if problems := s.validateIngested(sv); len(problems) > 0 {
			resp.Rejected++
			resp.Errors = append(resp.Errors, problems...)
			continue
		}
		accepted = append(accepted, sv)
	}
	resp.Accepted = int32(len(accepted))

	// Derived here, exactly as it is for the pull driver, so a pushed service's
	// status follows from its own numbers rather than from what the collector
	// preferred to call it.
	accepted = rules.Finalise(accepted, s.cfg.Domain, now)

	s.ingest.Sink.Store(s.merge(req.GetMode(), accepted, now))
	return resp
}

// merge applies the request's mode to the existing snapshot.
func (s *Server) merge(mode dpiv1.IngestMode, incoming []model.Service, now time.Time) model.Snapshot {
	if mode == dpiv1.IngestMode_INGEST_MODE_REPLACE {
		// The collector owns the whole picture, so anything absent is gone.
		return model.Snapshot{Services: incoming, GeneratedAt: now}
	}

	// Upsert. Anything not mentioned keeps its last known state, which is what
	// lets several collectors each own a slice of the estate.
	existing := s.ingest.Sink.Snapshot()
	byID := make(map[string]int, len(existing.Services))
	merged := make([]model.Service, len(existing.Services))
	copy(merged, existing.Services)

	for i, sv := range merged {
		byID[sv.ID] = i
	}
	for _, sv := range incoming {
		if i, ok := byID[sv.ID]; ok {
			merged[i] = sv
			continue
		}
		byID[sv.ID] = len(merged)
		merged = append(merged, sv)
	}
	return model.Snapshot{Services: merged, GeneratedAt: now}
}

// validateIngested checks a service against the deployment's own taxonomy.
//
// Reported rather than silently accepted: a service with a category no filter
// chip matches is invisible on the dashboard, which is a worse outcome than
// being told the id is wrong.
func (s *Server) validateIngested(sv model.Service) []*dpiv1.IngestError {
	var out []*dpiv1.IngestError
	fail := func(field, msg string) {
		out = append(out, &dpiv1.IngestError{ServiceId: sv.ID, Field: field, Message: msg})
	}

	if strings.TrimSpace(sv.ID) == "" {
		fail("id", "required")
		return out
	}

	d := s.cfg.Domain
	if sv.CategoryID != "" && !hasID(categoryIDs(d), sv.CategoryID) {
		fail("categoryId", fmt.Sprintf("%q is not declared in domain.yaml", sv.CategoryID))
	}
	if sv.RegionID != "" && !hasRegion(d, sv.RegionID) {
		fail("regionId", fmt.Sprintf("%q is not declared in domain.yaml", sv.RegionID))
	}
	if sv.Scope != "" && !hasID(d.Scopes, sv.Scope) {
		fail("scope", fmt.Sprintf("%q is not declared in domain.yaml", sv.Scope))
	}
	if sv.Metrics.Availability.Valid {
		v := sv.Metrics.Availability.Value
		switch {
		case v < 0 || v > 100:
			fail("metrics.availability", fmt.Sprintf(
				"%v is outside 0-100; availability is a percentage, not a ratio", v))

		case v > 0 && v <= 1:
			// Almost certainly a ratio sent where a percentage was expected —
			// the commonest integration mistake there is. But 0.9% availability
			// is a legal reading for a service that is genuinely on fire, and
			// rejecting a true catastrophe would be far worse than rendering a
			// suspicious number. Warned, not refused.
			s.log.Warn("availability looks like a ratio rather than a percentage",
				"service", sv.ID, "value", v,
				"hint", "the dashboard expects 99.91 for 99.91%, not 0.9991")
		}
	}
	if sv.Metrics.Volume.Success > sv.Metrics.Volume.Total {
		fail("metrics.volume", "more successes than requests")
	}
	return out
}

func hasID(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

func hasRegion(d config.Domain, want string) bool {
	for _, r := range d.Taxonomy.Regions {
		if r.ID == want {
			return true
		}
	}
	return false
}

// writeIngestResponse replies in whatever the caller asked for.
func (s *Server) writeIngestResponse(w http.ResponseWriter, r *http.Request, resp *dpiv1.IngestResponse) {
	if strings.Contains(r.Header.Get("accept"), contentProto) {
		body, err := proto.Marshal(resp)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		w.Header().Set("content-type", contentProto)
		_, _ = w.Write(body)
		return
	}

	body, err := protojson.Marshal(resp)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	// Re-encoded for stable key ordering: protojson varies its whitespace on
	// purpose, which makes a response awkward to diff in a collector's tests.
	var canonical any
	_ = json.Unmarshal(body, &canonical)
	pretty, _ := json.MarshalIndent(canonical, "", "  ")

	w.Header().Set("content-type", contentJSON+"; charset=utf-8")
	_, _ = w.Write(append(pretty, '\n'))
}
