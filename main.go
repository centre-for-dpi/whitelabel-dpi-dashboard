// Command whitelabel-dpi-dashboard serves a configurable public status
// dashboard for digital public infrastructure.
//
// Everything it needs is compiled in: default configuration, templates,
// stylesheet, fonts and htmx. A deployment that wants to customise anything
// points -config at a directory and overrides only the files it cares about.
package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"math"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/config"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/i18n"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/layout"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/model"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/persist"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/render"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/server"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/source"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/source/pull"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/source/seed"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/store"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/store/sqlstore"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/widget"
)

// coverage:ignore -- flag parsing and os.Exit; the work is all in run.
func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "dashboard:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	opts := parseFlags(args)

	// The container healthcheck runs the binary itself rather than shipping
	// curl into a scratch image, which would double its size for one request.
	if opts.healthcheck {
		return healthcheck(opts)
	}

	a, err := build(opts)
	if err != nil {
		return err
	}
	defer func() { _ = a.Close() }()

	if opts.validateOnly {
		fmt.Printf("configuration is valid: %d services, %d locales, %d sections\n",
			len(a.source.Snapshot().Services), len(a.locales.Locales()), a.sections)
		return nil
	}
	return serve(a, opts)
}

type options struct {
	configDir    string
	addr         string
	validateOnly bool
	healthcheck  bool
}

func parseFlags(args []string) options {
	o := options{configDir: os.Getenv("DPI_CONFIG_DIR")}

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-config", "--config":
			if i+1 < len(args) {
				i++
				o.configDir = args[i]
			}
		case "-addr", "--addr":
			if i+1 < len(args) {
				i++
				o.addr = args[i]
			}
		case "-validate", "--validate":
			o.validateOnly = true
		case "-healthcheck", "--healthcheck":
			o.healthcheck = true
		}
	}
	return o
}

// healthcheck probes a running instance over the loopback interface.
func healthcheck(o options) error {
	addr := o.addr
	if addr == "" {
		addr = os.Getenv("DPI_ADDR")
	}
	if addr == "" {
		addr = ":8080"
	}
	if strings.HasPrefix(addr, ":") {
		addr = "127.0.0.1" + addr
	}

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://" + addr + "/readyz")
	if err != nil {
		return fmt.Errorf("not reachable: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("not ready: %s", resp.Status)
	}
	return nil
}

// app is everything assembled and validated.
type app struct {
	cfg      config.Config
	locales  *i18n.Catalogue
	source   server.Snapshots
	poller   *pull.Driver
	store    store.Store
	recorder *persist.Recorder
	handler  http.Handler
	sections int
	log      *slog.Logger
}

// Close releases what build opened. Safe on a partially built app.
func (a *app) Close() error {
	if a == nil || a.store == nil {
		return nil
	}
	return a.store.Close()
}

func build(o options) (*app, error) {
	// layout.yaml and sources.yaml live in the same directory but are parsed by
	// their own packages; the loader is told so it does not reject them.
	cfg, err := config.Load(configFS, o.configDir, os.LookupEnv,
		layout.FileLayout, "sources.yaml")
	if err != nil {
		return nil, err
	}

	locales, err := loadLocales(o.configDir)
	if err != nil {
		return nil, err
	}

	registry := widget.Default()

	rawLayout, err := readConfigFile(o.configDir, layout.FileLayout)
	if err != nil {
		return nil, err
	}
	page, err := layout.Parse(rawLayout, registry, cfg)
	if err != nil {
		return nil, err
	}

	renderer, err := render.New(webFS, registry)
	if err != nil {
		return nil, err
	}

	log := newLogger(cfg.App.Log)

	sources, err := loadSources(o.configDir)
	if err != nil {
		return nil, err
	}
	driver, poller, err := buildSource(o.configDir, cfg, sources, log)
	if err != nil {
		return nil, err
	}

	// The store sits between the source and the server. Neither knows: the
	// source publishes snapshots as it always did, the server asks for one as it
	// always did, and what changes is that history now survives a restart.
	//
	// -validate gets an in-memory store regardless of what is configured.
	// Opening the real one would connect and migrate, and a configuration check
	// running in CI must not alter a production schema — nor fail because the
	// database happens to be unreachable from wherever the check runs.
	st, err := openStore(cfg.App.Storage, log, o.validateOnly)
	if err != nil {
		return nil, err
	}
	recorder, src := recordSource(driver, st, cfg, log)

	srv, err := server.New(server.Options{
		Config:   cfg,
		Layout:   page,
		Registry: registry,
		Renderer: renderer,
		Locales:  locales,
		Icons:    render.NewIcons(cfg.Icons),
		Source:   src,
		Static:   webFS,
		Log:      log,

		// Seed mode serves the demonstration data as an HTTP endpoint too, so
		// the shipped pull configuration has something real to poll. Switching
		// to pull against your own service is then one URL change, against a
		// path that has already been exercised.
		//
		// Both of these ask what kind of driver is underneath, so they are given
		// the driver rather than the recorder wrapping it — a wrapper answers
		// "no" to every such question, and the route simply would not appear.
		ExampleUpstream: exampleUpstream(driver),
		Ingest:          ingestOptions(driver, src, sources, log),
	})
	if err != nil {
		return nil, err
	}

	sections := 0
	for _, p := range page.Pages {
		sections += len(p.Sections)
	}

	return &app{
		cfg: cfg, locales: locales, source: src, poller: poller,
		store: st, recorder: recorder,
		handler: srv, sections: sections, log: log,
	}, nil
}

// openStore opens the configured backend.
//
// The DSN comes from the environment via config expansion, never from a literal
// in a config file — a password in a config file ends up in a git history.
//
// dryRun substitutes an in-memory store: the configuration is still validated,
// but nothing connects and nothing migrates.
func openStore(sc config.Storage, log *slog.Logger, dryRun bool) (store.Store, error) {
	if sc.Driver == config.DriverMemory {
		log.Info("storage is in memory; locally rolled-up history will not survive a restart")
		return store.NewMemory(), nil
	}
	if dryRun {
		return store.NewMemory(), nil
	}

	st, err := sqlstore.Open(sqlstore.Config{
		Driver:          sc.Driver,
		DSN:             sc.DSN,
		MaxOpenConns:    sc.MaxOpenConns,
		MaxIdleConns:    sc.MaxIdleConns,
		ConnMaxLifetime: sc.ConnMaxLifetime.Std(),
	})
	if err != nil {
		return nil, err
	}

	// Migrating at startup rather than as a separate deploy step: the schema is
	// owned entirely by this binary, and a single artefact that needs a second
	// command run before it works is not one-click.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := st.Migrate(ctx); err != nil {
		_ = st.Close()
		return nil, fmt.Errorf("migrating %s storage: %w", sc.Driver, err)
	}

	log.Info("storage ready", "driver", sc.Driver, "retentionDays", sc.History.RetentionDays)
	return st, nil
}

// recordSource puts the store in front of the source.
//
// It returns a SinkRecorder when the source is writable, so the ingest route is
// still registered for a push deployment — and so a push is made durable before
// the collector is told it was accepted.
func recordSource(src server.Snapshots, st store.Store, cfg config.Config, log *slog.Logger) (*persist.Recorder, server.Snapshots) {
	opts := persist.Options{
		Source:  src,
		Store:   st,
		Storage: cfg.App.Storage,
		Domain:  cfg.Domain,
		Log:     log,
	}

	if sink, ok := src.(server.Sink); ok {
		r := persist.NewSink(opts, sink)
		return r.Recorder, r
	}
	r := persist.New(opts)
	return r, r
}

// loadSources reads sources.yaml.
func loadSources(dir string) (source.Config, error) {
	raw, err := readConfigFile(dir, "sources.yaml")
	if err != nil {
		return source.Config{}, err
	}

	expanded, err := config.ExpandEnv(raw, os.LookupEnv)
	if err != nil {
		return source.Config{}, fmt.Errorf("sources.yaml: %w", err)
	}

	var c source.Config
	dec := yaml.NewDecoder(bytes.NewReader(expanded))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return source.Config{}, fmt.Errorf("sources.yaml: %w", err)
	}

	if errs := c.Validate(); len(errs) > 0 {
		msgs := make([]string, len(errs))
		for i, e := range errs {
			msgs[i] = e.Error()
		}
		return source.Config{}, fmt.Errorf("sources.yaml: %s", strings.Join(msgs, "; "))
	}
	return c, nil
}

// buildSource constructs the configured driver.
//
// The server never learns which one is running: it asks for a snapshot and
// renders whatever it gets, which is what makes switching a config change.
func buildSource(dir string, cfg config.Config, sc source.Config, log *slog.Logger) (server.Snapshots, *pull.Driver, error) {
	switch sc.Driver {
	case source.DriverPull:
		d, err := pull.New(pull.Options{Config: sc.Pull, Domain: cfg.Domain, Log: log})
		if err != nil {
			return nil, nil, err
		}
		return d, d, nil

	case source.DriverPush:
		// Empty until a collector pushes. Seeding it would mean the dashboard
		// showed invented data until the first real payload arrived, which is
		// exactly the confusion a status page must not create.
		return &staticSource{}, nil, nil

	default:
		src, err := loadSeed(dir, cfg, sc.Seed)
		if err != nil {
			return nil, nil, err
		}
		return src, nil, nil
	}
}

// exampleUpstream renders the current snapshot in the pull contract's shape.
//
// Returning nil when there is nothing to serve leaves the route unregistered,
// so a deployment already polling its own service does not also expose a copy
// of its data under a demonstration path.
func exampleUpstream(src server.Snapshots) func() any {
	holder, ok := src.(*staticSource)
	if !ok {
		return nil
	}
	return func() any { return upstreamShape(holder.Snapshot()) }
}

// readConfigFile prefers the override directory, falling back to what is
// embedded — the same rule config.Load applies to its own files.
func readConfigFile(dir, name string) ([]byte, error) {
	if dir != "" {
		if raw, err := os.ReadFile(filepath.Join(dir, name)); err == nil {
			return raw, nil
		}
	}
	raw, err := configFS.ReadFile("config/" + name)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", name, err)
	}
	return raw, nil
}

// loadLocales reads every bundle, embedded and overridden.
//
// A deployment adding config/locales/pt.yaml gets Portuguese with no code
// change and no registration step: the directory listing is the registry.
func loadLocales(dir string) (*i18n.Catalogue, error) {
	bundles := map[string]i18n.Bundle{}

	add := func(name string, raw []byte) error {
		var b i18n.Bundle
		if err := yaml.Unmarshal(raw, &b); err != nil {
			return fmt.Errorf("parsing locale %s: %w", name, err)
		}
		if b.Locale == "" {
			return fmt.Errorf("locale %s declares no locale code", name)
		}
		bundles[b.Locale] = b
		return nil
	}

	entries, err := fs.ReadDir(configFS, "config/locales")
	if err != nil {
		return nil, fmt.Errorf("reading embedded locales: %w", err)
	}
	for _, e := range entries {
		raw, err := configFS.ReadFile("config/locales/" + e.Name())
		if err != nil {
			return nil, err
		}
		if err := add(e.Name(), raw); err != nil {
			return nil, err
		}
	}

	if dir != "" {
		overrides, err := os.ReadDir(filepath.Join(dir, "locales"))
		if err == nil {
			for _, e := range overrides {
				if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
					continue
				}
				raw, err := os.ReadFile(filepath.Join(dir, "locales", e.Name()))
				if err != nil {
					return nil, err
				}
				if err := add(e.Name(), raw); err != nil {
					return nil, err
				}
			}
		}
	}

	// English first, so it is the base every other locale falls back to key by
	// key. The rest follow in a stable order for the language selector.
	var ordered []i18n.Bundle
	if en, ok := bundles["en"]; ok {
		ordered = append(ordered, en)
		delete(bundles, "en")
	}
	codes := make([]string, 0, len(bundles))
	for k := range bundles {
		codes = append(codes, k)
	}
	slices.Sort(codes)
	for _, code := range codes {
		ordered = append(ordered, bundles[code])
	}

	if len(ordered) == 0 {
		return nil, errors.New("no locale bundles found")
	}
	return i18n.NewCatalogue(ordered[0].Locale, ordered...)
}

// staticSource holds one snapshot, swapped atomically.
//
// Readers never lock, so render latency does not depend on how often the data
// refreshes — the property that matters once a poller is writing to it on a
// timer.
type staticSource struct {
	snap atomic.Pointer[model.Snapshot]
}

func (s *staticSource) Snapshot() model.Snapshot {
	if v := s.snap.Load(); v != nil {
		return *v
	}
	return model.Snapshot{}
}

func (s *staticSource) Store(snap model.Snapshot) { s.snap.Store(&snap) }

// loadSeed generates the demonstration dataset.
//
// A fresh deploy renders a full dashboard immediately rather than an empty page
// that gives no sense of what this is for.
func loadSeed(dir string, cfg config.Config, sc source.SeedConfig) (*staticSource, error) {
	name := sc.Catalogue
	if name == "" {
		name = "examples/seed-catalogue.yaml"
	}
	raw, err := readExampleFile(dir, strings.TrimPrefix(name, "examples/"))
	if err != nil {
		return nil, err
	}
	var cat seed.Catalogue
	if err := yaml.Unmarshal(raw, &cat); err != nil {
		return nil, fmt.Errorf("parsing the seed catalogue: %w", err)
	}

	opts := seed.DefaultOptions(time.Now().UTC())
	opts.HistoryDays = cfg.App.Storage.History.RetentionDays

	src := &staticSource{}
	src.Store(seed.Generate(cat, cfg.Domain, opts))
	return src, nil
}

func readExampleFile(dir, name string) ([]byte, error) {
	if dir != "" {
		if raw, err := os.ReadFile(filepath.Join(dir, name)); err == nil {
			return raw, nil
		}
	}
	raw, err := examplesFS.ReadFile("examples/" + name)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", name, err)
	}
	return raw, nil
}

func newLogger(c config.Log) *slog.Logger {
	level := slog.LevelInfo
	switch c.Level {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}

	opts := &slog.HandlerOptions{Level: level}
	if c.Format == "json" {
		return slog.New(slog.NewJSONHandler(os.Stderr, opts))
	}
	return slog.New(slog.NewTextHandler(os.Stderr, opts))
}

func serve(a *app, o options) error {
	addr := a.cfg.App.Server.Addr
	if o.addr != "" {
		addr = o.addr
	}

	srv := &http.Server{
		Addr:         addr,
		Handler:      a.handler,
		ReadTimeout:  a.cfg.App.Server.ReadTimeout.Std(),
		WriteTimeout: a.cfg.App.Server.WriteTimeout.Std(),
		IdleTimeout:  a.cfg.App.Server.IdleTimeout.Std(),
	}

	// Shut down on a signal rather than being killed mid-response: an
	// orchestrator sends SIGTERM and then SIGKILL a few seconds later, and the
	// gap is what in-flight requests get to finish in.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// One poll before serving, so the first reader sees data rather than an
	// empty page, then the schedule takes over.
	if a.poller != nil {
		if err := a.poller.PollOnce(ctx, time.Now); err != nil {
			// Not fatal: the dashboard starts, reports its data as stale, and
			// recovers when the upstream does. Refusing to start would turn an
			// upstream hiccup into an outage of the thing reporting on it.
			a.log.Warn("the first poll failed; starting anyway", "err", err)
		}
		go a.poller.Run(ctx, time.Now)
	}

	// After the first poll, so the restored history is merged against real data
	// rather than against an empty snapshot that the first sync then replaces.
	a.recorder.Restore(ctx)
	a.recorder.SyncOnce(ctx)
	go a.recorder.Run(ctx)

	errs := make(chan error, 1)
	go func() {
		a.log.Info("listening",
			"addr", addr,
			"services", len(a.source.Snapshot().Services),
			"locales", len(a.locales.Locales()),
			"storage", a.cfg.App.Storage.Driver)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errs <- err
		}
	}()

	select {
	case err := <-errs:
		return err
	case <-ctx.Done():
		a.log.Info("shutting down")
		grace, cancel := context.WithTimeout(context.Background(), a.cfg.App.Server.ShutdownGrace.Std())
		defer cancel()
		return srv.Shutdown(grace)
	}
}

// ingestOptions enables the push endpoint when the deployment has configured
// it and the source can actually be written to.
//
// An unset token leaves the route unregistered rather than open: an
// unauthenticated ingest lets anyone rewrite the dashboard, which is worse than
// having no dashboard.
//
// Whether the source is writable is asked of the driver, but what receives the
// push is the recorder in front of it — so a payload is persisted before the
// collector is told it was accepted. Asking the recorder both questions would
// also work today; asking the driver keeps the test meaningful if a wrapper is
// ever added that is writable when the thing it wraps is not.
func ingestOptions(driver, src server.Snapshots, sc source.Config, log *slog.Logger) server.IngestOptions {
	if sc.Driver != source.DriverPush {
		return server.IngestOptions{}
	}
	if _, ok := driver.(server.Sink); !ok {
		return server.IngestOptions{}
	}
	sink, ok := src.(server.Sink)
	if !ok {
		return server.IngestOptions{}
	}

	token, _ := os.LookupEnv(sc.Push.TokenEnvVar)
	if token == "" {
		log.Warn("the push driver is selected but its token is not set, so the ingest endpoint is disabled",
			"envVar", sc.Push.TokenEnvVar)
		return server.IngestOptions{}
	}

	return server.IngestOptions{
		Sink:         sink,
		Token:        token,
		MaxBodyBytes: sc.Push.BodyLimit(),
	}
}

// upstreamShape renders a snapshot the way the example upstream reports it.
//
// Deliberately NOT the dashboard's own format: ratios rather than percentages,
// enum casing rather than taxonomy ids, its own field names. If the example
// spoke the dashboard's language the shipped mapping would be an identity
// function and would demonstrate nothing.
func upstreamShape(snap model.Snapshot) any {
	services := make([]map[string]any, 0, len(snap.Services))

	for _, sv := range snap.Services {
		record := map[string]any{
			"serviceId":       sv.ID,
			"displayName":     sv.NameTermID,
			"summary":         sv.DescTermID,
			"category":        enumCase(sv.CategoryID),
			"region":          enumCase(sv.RegionID),
			"operator":        enumCase(sv.ProviderID),
			"tier":            strings.ToUpper(sv.Scope),
			"sla":             map[string]any{"uptimePct": ratio(sv.Metrics.Availability), "errorPct": round6(sv.Metrics.ErrorRate / 100)},
			"latency":         map[string]any{"p50Ms": sv.Metrics.LatencyP50},
			"requests":        map[string]any{"total": sv.Metrics.Volume.Total, "succeeded": sv.Metrics.Volume.Success},
			"observedAgeSecs": sv.Metrics.StaleSeconds,
		}
		if sv.Maintenance.Active {
			m := map[string]any{"inProgress": true, "reason": sv.Maintenance.ReasonTermID}
			if !sv.Maintenance.Until.IsZero() {
				m["endsAt"] = sv.Maintenance.Until.Format(time.RFC3339)
			}
			record["maintenance"] = m
		}

		daily := make([]map[string]any, 0, len(sv.History))
		for _, p := range sv.History {
			daily = append(daily, map[string]any{
				"ts":     p.Day.Format("2006-01-02"),
				"uptime": ratio(p.Availability),
				"errPct": round6(p.ErrorRate / 100),
				"count":  p.Volume,
				"p50Ms":  p.LatencyP50,
			})
		}
		record["daily"] = daily

		services = append(services, record)
	}

	return map[string]any{
		"generatedAt": snap.GeneratedAt.Format(time.RFC3339),
		"data":        map[string]any{"services": services},
	}
}

// ratio converts a percentage to the [0,1] form the example upstream reports.
// An absent reading stays absent: reporting it as 0 would claim a total outage.
func ratio(v model.OptFloat) any {
	if !v.Valid {
		return nil
	}
	return round6(v.Value / 100)
}

// round6 trims the binary-floating-point tail, so the example upstream does not
// report 0.005500000000000001 and imply a precision nobody is claiming.
func round6(v float64) float64 { return math.Round(v*1e6) / 1e6 }

// enumCase turns a taxonomy id into the upper-case enum a typical API sends, so
// the shipped mapping has a real translation to perform.
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
