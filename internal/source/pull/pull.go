// Package pull fetches service data from an upstream HTTP endpoint.
//
// It is the driver a deployment uses when it already exposes the numbers
// somewhere. Everything about the integration — the URL, the authentication,
// the interval, and the mapping from their field names to the dashboard's —
// lives in sources.yaml. No Go changes.
//
// Three behaviours matter more than the fetching:
//
//   - A failed poll keeps the last good snapshot. A status dashboard that
//     blanks out when its own upstream hiccups is worse than useless, because
//     it reports an outage that is its own.
//   - Failures back off. Hammering an upstream that is already struggling is
//     how a monitoring system becomes part of the incident.
//   - Polls are jittered. A fleet of dashboards started by the same deploy
//     would otherwise align their requests into a thundering herd.
package pull

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/config"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/mapping"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/model"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/rules"
)

// Doer is the HTTP client, injected so tests need no network.
type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

// Endpoint is one upstream, as written in config.
type Endpoint struct {
	ID              string            `yaml:"id"`
	URL             string            `yaml:"url"`
	Method          string            `yaml:"method"`
	Headers         map[string]string `yaml:"headers"`
	Auth            Auth              `yaml:"auth"`
	IntervalSeconds int               `yaml:"intervalSeconds"`
	TimeoutSeconds  int               `yaml:"timeoutSeconds"`
	// MaxBytes caps the response, so a misconfigured URL pointing at something
	// enormous cannot exhaust memory.
	MaxBytes int64 `yaml:"maxBytes"`

	mapping.Spec `yaml:",inline"`
}

// Auth is how to authenticate. The value always comes from the environment:
// a token in a config file ends up in a git history.
type Auth struct {
	Type   string `yaml:"type"`   // none | bearer | header | basic
	EnvVar string `yaml:"envVar"` // where to read the secret from
	Header string `yaml:"header"` // header name, for type: header
	User   string `yaml:"user"`   // for type: basic
}

// Config is the pull driver's whole configuration.
type Config struct {
	Endpoints []Endpoint `yaml:"endpoints"`
}

// Defaults applied where an endpoint leaves a value unset.
const (
	DefaultInterval = 60 * time.Second
	DefaultTimeout  = 10 * time.Second
	DefaultMaxBytes = 64 << 20
	// MaxBackoff caps the retry delay. Beyond a few minutes a dashboard has
	// stopped being live, and the staleness rules will have said so already.
	MaxBackoff = 5 * time.Minute
)

// Options configure a Driver.
type Options struct {
	Config Config
	// Domain supplies the thresholds every incoming service is evaluated
	// against. Without it the driver would publish services with no status.
	Domain config.Domain
	Client Doer
	Log    *slog.Logger
	// LookupEnv resolves auth secrets. Injected so tests need no environment.
	LookupEnv func(string) (string, bool)
	// Jitter returns a fraction in [0,1) used to spread polls apart. Injected
	// so a test's timing is deterministic.
	Jitter func() float64
}

// Driver polls a set of endpoints and publishes the merged result.
type Driver struct {
	endpoints []*compiledEndpoint
	client    Doer
	log       *slog.Logger
	lookupEnv func(string) (string, bool)
	jitter    func() float64
	domain    config.Domain

	snap atomic.Pointer[model.Snapshot]

	mu      sync.Mutex
	latest  map[string][]model.Service
	lastErr map[string]error
}

type compiledEndpoint struct {
	Endpoint
	mapper *mapping.Mapper
}

// New compiles the configuration, reporting every problem at once so a
// deployment fixes its mapping in one pass rather than one restart per error.
func New(o Options) (*Driver, error) {
	if len(o.Config.Endpoints) == 0 {
		return nil, fmt.Errorf("no endpoints configured")
	}
	if o.Client == nil {
		o.Client = &http.Client{}
	}
	if o.Log == nil {
		o.Log = slog.Default()
	}
	if o.LookupEnv == nil {
		o.LookupEnv = os.LookupEnv
	}
	if o.Jitter == nil {
		o.Jitter = defaultJitter
	}

	d := &Driver{
		domain:    o.Domain,
		client:    o.Client,
		log:       o.Log,
		lookupEnv: o.LookupEnv,
		jitter:    o.Jitter,
		latest:    map[string][]model.Service{},
		lastErr:   map[string]error{},
	}

	seen := map[string]bool{}
	var errs []string
	for i, e := range o.Config.Endpoints {
		name := e.ID
		if name == "" {
			name = fmt.Sprintf("endpoint %d", i)
			errs = append(errs, fmt.Sprintf("%s: no id", name))
		}
		if seen[e.ID] {
			errs = append(errs, fmt.Sprintf("%s: duplicate id", name))
		}
		seen[e.ID] = true

		if e.URL == "" {
			errs = append(errs, fmt.Sprintf("%s: no url", name))
		}
		if err := validateAuth(e.Auth); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", name, err))
		}

		m, err := mapping.Compile(e.Spec)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", name, err))
			continue
		}
		d.endpoints = append(d.endpoints, &compiledEndpoint{Endpoint: e, mapper: m})
	}

	if len(errs) > 0 {
		return nil, fmt.Errorf("pull configuration: %s", joinErrors(errs))
	}
	return d, nil
}

func validateAuth(a Auth) error {
	switch a.Type {
	case "", "none":
		return nil
	case "bearer", "basic":
		if a.EnvVar == "" {
			return fmt.Errorf("auth type %q needs an envVar; a token in a config file ends up in a git history", a.Type)
		}
	case "header":
		if a.EnvVar == "" || a.Header == "" {
			return fmt.Errorf("auth type \"header\" needs both header and envVar")
		}
	default:
		return fmt.Errorf("unknown auth type %q; expected none, bearer, header or basic", a.Type)
	}
	return nil
}

// Snapshot returns the most recent merged result, satisfying server.Snapshots.
func (d *Driver) Snapshot() model.Snapshot {
	if v := d.snap.Load(); v != nil {
		return *v
	}
	return model.Snapshot{}
}

// Run polls every endpoint until the context is cancelled.
//
// Each endpoint runs on its own schedule, so a slow upstream does not hold up a
// fast one and one failing endpoint does not stop the others.
func (d *Driver) Run(ctx context.Context, now func() time.Time) {
	var wg sync.WaitGroup
	for _, e := range d.endpoints {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d.loop(ctx, e, now)
		}()
	}
	wg.Wait()
}

func (d *Driver) loop(ctx context.Context, e *compiledEndpoint, now func() time.Time) {
	interval := e.interval()
	failures := 0

	for {
		if err := d.pollOnce(ctx, e, now); err != nil {
			failures++
			d.log.Warn("poll failed",
				"endpoint", e.ID, "err", err, "consecutiveFailures", failures)
		} else {
			failures = 0
		}

		wait := d.nextDelay(interval, failures)
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

// nextDelay is the interval, backed off on failure and jittered.
func (d *Driver) nextDelay(interval time.Duration, failures int) time.Duration {
	wait := interval
	if failures > 0 {
		// Exponential, so a struggling upstream is not hammered by the thing
		// monitoring it.
		backoff := time.Duration(float64(interval) * math.Pow(2, float64(min(failures, 10))))
		wait = min(backoff, MaxBackoff)
	}

	// Up to a tenth either side. A fleet started by one deploy would otherwise
	// align its requests into a thundering herd.
	spread := float64(wait) * 0.2 * (d.jitter() - 0.5)
	return time.Duration(float64(wait) + spread)
}

// PollOnce fetches every endpoint once, which is what a startup does before
// serving so the first reader sees data rather than an empty page.
func (d *Driver) PollOnce(ctx context.Context, now func() time.Time) error {
	var errs []string
	for _, e := range d.endpoints {
		if err := d.pollOnce(ctx, e, now); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", e.ID, err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s", joinErrors(errs))
	}
	return nil
}

func (d *Driver) pollOnce(ctx context.Context, e *compiledEndpoint, now func() time.Time) error {
	doc, err := d.fetch(ctx, e)
	if err != nil {
		d.recordFailure(e.ID, err)
		return err
	}

	result := e.mapper.Apply(doc)
	if len(result.Skipped) > 0 {
		// Reported rather than swallowed: a partial upstream failure that
		// silently shrinks the dashboard is the hardest kind to notice.
		d.log.Warn("records skipped",
			"endpoint", e.ID, "skipped", len(result.Skipped),
			"first", result.Skipped[0].Reason)
	}
	if len(result.Services) == 0 {
		err := fmt.Errorf("the response contained no readable services")
		d.recordFailure(e.ID, err)
		return err
	}

	// Status, trends and rank movement are derived here rather than accepted
	// from the upstream, so the rule shown on screen is provably the rule that
	// was applied — whichever driver the data arrived through.
	d.publish(e.ID, rules.Finalise(result.Services, d.domain, now()), now())
	return nil
}

func (d *Driver) fetch(ctx context.Context, e *compiledEndpoint) (any, error) {
	ctx, cancel := context.WithTimeout(ctx, e.timeout())
	defer cancel()

	method := e.Method
	if method == "" {
		method = http.MethodGet
	}
	req, err := http.NewRequestWithContext(ctx, method, e.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("building the request: %w", err)
	}
	req.Header.Set("accept", "application/json")
	for k, v := range e.Headers {
		req.Header.Set(k, v)
	}
	if err := d.applyAuth(req, e.Auth); err != nil {
		return nil, err
	}

	resp, err := d.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("requesting %s: %w", e.URL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s returned %s", e.URL, resp.Status)
	}

	// Capped, so a misconfigured URL pointing at something enormous cannot
	// exhaust memory.
	body, err := io.ReadAll(io.LimitReader(resp.Body, e.maxBytes()+1))
	if err != nil {
		return nil, fmt.Errorf("reading the response: %w", err)
	}
	if int64(len(body)) > e.maxBytes() {
		return nil, fmt.Errorf("the response exceeded maxBytes (%d)", e.maxBytes())
	}

	var doc any
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("the response is not JSON: %w", err)
	}
	return doc, nil
}

func (d *Driver) applyAuth(req *http.Request, a Auth) error {
	if a.Type == "" || a.Type == "none" {
		return nil
	}
	secret, ok := d.lookupEnv(a.EnvVar)
	if !ok || secret == "" {
		return fmt.Errorf("environment variable %q is not set, so the upstream cannot be authenticated", a.EnvVar)
	}

	switch a.Type {
	case "bearer":
		req.Header.Set("authorization", "Bearer "+secret)
	case "header":
		req.Header.Set(a.Header, secret)
	case "basic":
		req.SetBasicAuth(a.User, secret)
	}
	return nil
}

// publish merges one endpoint's result into the snapshot.
//
// Endpoints are merged rather than replacing each other, so a deployment can
// point several at different parts of its estate and see them as one dashboard.
func (d *Driver) publish(id string, services []model.Service, at time.Time) {
	d.mu.Lock()
	d.latest[id] = services
	delete(d.lastErr, id)

	merged := make([]model.Service, 0, len(services))
	seen := map[string]bool{}
	for _, e := range d.endpoints {
		for _, sv := range d.latest[e.ID] {
			// First endpoint to claim an id wins, in configured order, so an
			// overlap is resolved predictably rather than by whichever poll
			// happened to finish last.
			if seen[sv.ID] {
				continue
			}
			seen[sv.ID] = true
			merged = append(merged, sv)
		}
	}
	d.mu.Unlock()

	snap := model.Snapshot{Services: merged, GeneratedAt: at}
	d.snap.Store(&snap)
}

// recordFailure notes an error without disturbing the last good snapshot.
//
// This is the property that matters most: a dashboard that blanks out when its
// own upstream hiccups reports an outage that is its own. The data goes stale
// instead, and the staleness rules say so honestly.
func (d *Driver) recordFailure(id string, err error) {
	d.mu.Lock()
	d.lastErr[id] = err
	d.mu.Unlock()
}

// Health reports the last error per endpoint, for the readiness probe and logs.
func (d *Driver) Health() map[string]error {
	d.mu.Lock()
	defer d.mu.Unlock()

	out := make(map[string]error, len(d.lastErr))
	for k, v := range d.lastErr {
		out[k] = v
	}
	return out
}

func (e Endpoint) interval() time.Duration {
	if e.IntervalSeconds <= 0 {
		return DefaultInterval
	}
	return time.Duration(e.IntervalSeconds) * time.Second
}

func (e Endpoint) timeout() time.Duration {
	if e.TimeoutSeconds <= 0 {
		return DefaultTimeout
	}
	return time.Duration(e.TimeoutSeconds) * time.Second
}

func (e Endpoint) maxBytes() int64 {
	if e.MaxBytes <= 0 {
		return DefaultMaxBytes
	}
	return e.MaxBytes
}

// defaultJitter is deterministic enough for spreading polls and needs no
// cryptographic quality: it only has to differ between processes.
func defaultJitter() float64 {
	return float64(time.Now().UnixNano()%1000) / 1000
}

func joinErrors(errs []string) string {
	out := ""
	for i, e := range errs {
		if i > 0 {
			out += "; "
		}
		out += e
	}
	return out
}
