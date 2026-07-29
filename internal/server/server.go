package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/config"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/i18n"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/layout"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/model"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/query"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/render"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/theme"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/widget"
)

// Snapshots supplies the current state of the world.
//
// An interface, so the server is the same whether the data came from a poller,
// an ingest endpoint or the seeded generator. Swapping the source is a wiring
// change in main, not a change here.
type Snapshots interface {
	Snapshot() model.Snapshot
}

// Clock is injected so that "4 minutes ago" is testable.
type Clock interface{ Now() time.Time }

// Options are everything the server needs to run.
type Options struct {
	Config   config.Config
	Layout   layout.Layout
	Registry *widget.Registry
	Renderer *render.Renderer
	Locales  *i18n.Catalogue
	Icons    config.Icons
	Source   Snapshots
	Static   fs.FS
	Clock    Clock
	Log      *slog.Logger

	// ExampleUpstream, when set, serves the current data in the shape the pull
	// contract expects. Seed mode supplies it so the shipped pull
	// configuration has something real to poll: switching to your own service
	// is then one URL change, against a path already proven to work.
	ExampleUpstream func() any

	// Ingest, when its Sink is set, enables the push endpoint. Left unset the
	// route is not registered at all, so a deployment that has not configured
	// push does not expose an endpoint it never meant to.
	Ingest IngestOptions
}

// Server serves the dashboard.
type Server struct {
	cfg     config.Config
	layout  layout.Layout
	reg     *widget.Registry
	render  *Renderer
	locales *i18n.Catalogue
	// iconSet is the configured set, not a compiled resolver: an icon's
	// accessible name is announced to a reader, so it has to be resolved in that
	// reader's locale, which means once per request rather than once at startup.
	iconSet  config.Icons
	source   Snapshots
	clock    Clock
	log      *slog.Logger
	mux      *http.ServeMux
	themeCSS []byte
	// assetVersion busts the cache when the config changes, so a restyled
	// deployment does not serve a stale stylesheet from a reader's cache.
	assetVersion string
	static       fs.FS
	example      func() any
	ingest       IngestOptions
}

// Renderer is the subset of render.Renderer the server needs, named separately
// so a test can substitute one.
type Renderer = render.Renderer

// New wires up the routes.
func New(o Options) (*Server, error) {
	if o.Clock == nil {
		o.Clock = systemClock{}
	}
	if o.Log == nil {
		o.Log = slog.Default()
	}

	css := theme.CSS(o.Config.Theme)
	s := &Server{
		cfg:          o.Config,
		layout:       o.Layout,
		reg:          o.Registry,
		render:       o.Renderer,
		locales:      o.Locales,
		iconSet:      o.Icons,
		source:       o.Source,
		clock:        o.Clock,
		log:          o.Log,
		mux:          http.NewServeMux(),
		themeCSS:     []byte(css),
		assetVersion: assetFingerprint(css, o.Static),
		static:       o.Static,
		example:      o.ExampleUpstream,
		ingest:       o.Ingest,
	}

	s.routes()
	return s, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.mux.ServeHTTP(w, r) }

func (s *Server) routes() {
	s.mux.HandleFunc("GET /{$}", s.handlePage)
	s.mux.HandleFunc("GET /service/{id}", s.handlePage)

	// One fragment route per swappable section, named by the layout rather than
	// hardcoded — a deployment that adds a section gets its route for free.
	for _, id := range s.layout.SwapTargets() {
		s.mux.HandleFunc("GET /fragments/"+id, s.handleSection(id))
	}
	// Two granularities: the whole drawer, for opening one, and the pane alone,
	// for changing which tab of an open one is showing.
	s.mux.HandleFunc("GET /fragments/service/{id}", s.handleDrawer)
	s.mux.HandleFunc("GET /fragments/service/{id}/pane", s.handleDrawerPane)

	// Registered only when push is configured. An endpoint that exists but
	// rejects everything is still an endpoint someone can probe.
	if s.ingest.Sink != nil && s.ingest.Token != "" {
		s.mux.HandleFunc("POST /api/v1/ingest", s.handleIngest)
	}

	if s.example != nil {
		s.mux.HandleFunc("GET /__example/upstream/services", s.handleExampleUpstream)
	}

	s.mux.HandleFunc("GET /assets/theme.css", s.handleThemeCSS)
	s.mux.Handle("GET /assets/", s.handleStatic())

	s.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/plain; charset=utf-8")
		fmt.Fprintln(w, "ok")
	})
	s.mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		// Ready means there is something to serve. A dashboard with no data is
		// not yet useful, and saying so keeps a rolling deploy from cutting
		// traffic over to an empty page.
		if len(s.source.Snapshot().Services) == 0 {
			http.Error(w, "no data yet", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("content-type", "text/plain; charset=utf-8")
		fmt.Fprintln(w, "ready")
	})
}

// --- rendering context -----------------------------------------------------

// build assembles everything the widgets need for one request.
func (s *Server) build(r *http.Request) widget.Context {
	st := s.readState(r)
	text := s.locales.For(st.Locale)
	snap := s.source.Snapshot()

	view := query.View{
		Domain:  s.cfg.Domain,
		Labeler: labeler{text: text},
		Days:    query.DaysFor(s.cfg.Domain, st.Period),
	}

	// Two populations, because two questions are being asked. The verdict asks
	// whether the country's services are working, which is the supply side
	// whatever the reader is currently looking at; the board asks how one side
	// of the exchange is doing, ranked against its own kind.
	scoped := view.Scope(view.Role(snap.Services, s.cfg.Domain.DefaultRole), st.Scope, st.Region)
	roleScoped := scoped
	if st.Role != s.cfg.Domain.DefaultRole {
		roleScoped = view.Scope(view.Role(snap.Services, st.Role), st.Scope, st.Region)
	}

	ranked := view.Rank(roleScoped)
	ranks := query.Ranks(ranked)

	filtered := view.Apply(roleScoped, query.Filter{
		Statuses:   st.Statuses,
		Categories: st.Categories,
		Search:     st.Search,
		IDs:        st.IDs,
	})
	ordered := view.Order(filtered, st.Sort, st.Dir, ranks)

	c := widget.Context{
		Config:     s.cfg,
		Demand:     view.Scope(callers(snap.Services), st.Scope, st.Region),
		View:       view,
		Snapshot:   snap,
		Scoped:     scoped,
		RoleScoped: roleScoped,
		Filtered:   filtered,
		Ordered:    ordered,
		Ranks:      ranks,
		State:      st,
		Params:     readParams(r),
		Text:       text,
		Icons:      render.NewIcons(s.iconSet, text),
		Now:        s.clock.Now(),
	}

	if st.DrawerID != "" {
		for i := range snap.Services {
			if snap.Services[i].ID == st.DrawerID {
				c.Service = &snap.Services[i]
				break
			}
		}
	}
	return c
}

// labeler adapts the text resolver to what query needs for search and ordering.
type labeler struct{ text *i18n.Resolver }

func (l labeler) Name(s model.Service) string { return l.text.Text(s.NameTermID, nil) }

// Haystack is what a search matches against.
//
// Deliberately more than the name: a reader searching "transport" or "Kerala"
// is looking for a service they cannot name, which is precisely when search
// matters most.
func (l labeler) Haystack(s model.Service) string {
	return strings.Join([]string{
		l.text.Text(s.NameTermID, nil),
		l.text.Text(s.DescTermID, nil),
		l.text.Text(s.CategoryID, nil),
		l.text.Text(s.RegionID, nil),
	}, " ")
}

// Compare orders names the way the reader's language does, which is not the way
// bytes do in most of the world.
func (l labeler) Compare(a, b string) int { return strings.Compare(a, b) }

// --- widget rendering ------------------------------------------------------

// renderSection builds and renders every widget in a section.
func (s *Server) renderSection(sec layout.Section, c widget.Context) (render.Section, error) {
	out := render.Section{ID: sec.ID, Swap: sec.Swap, Grid: sec.Grid}

	if sec.Heading != nil {
		title := sec.Heading.TitleTermID
		if sec.Heading.Scoped {
			title += "." + c.State.Scope
		}
		if v, ok := sec.Heading.Variants[c.State.SignalTab]; ok {
			title = v
		}
		out.Heading = &render.HeadingBlock{
			Title: c.Text.Text(title, nil),
			Level: headingLevel(sec.Heading.Level),
			Class: sec.Heading.Class,
			ID:    sec.ID + "-title",
		}
		if sec.Heading.EyebrowTermID != "" {
			out.Heading.Eyebrow = c.Text.Text(sec.Heading.EyebrowTermID, nil)
		}
	}

	for _, w := range sec.Aside {
		rendered, err := s.renderWidgets(w, c)
		if err != nil {
			return render.Section{}, err
		}
		out.Aside = append(out.Aside, rendered...)
	}
	for _, w := range sec.Widgets {
		rendered, err := s.renderWidgets(w, c)
		if err != nil {
			return render.Section{}, err
		}
		out.Widgets = append(out.Widgets, rendered...)
	}
	groupColumns(&out, sec.Grid)
	return out, nil
}

// groupColumns partitions a columned section's widgets into its declared tracks.
//
// Widgets keep their configured order within a column, and a widget naming no
// column joins the first — which is what "the rest of the section" means when
// only one part has been pulled out of the flow. Validation has already
// rejected a name no column declares, so nothing can be dropped here.
func groupColumns(out *render.Section, g layout.Grid) {
	if len(g.Columns) == 0 {
		return
	}

	at := make(map[string]int, len(g.Columns))
	for i, col := range g.Columns {
		at[col.Name] = i
		out.Columns = append(out.Columns, render.RenderedColumn{
			Name: col.Name, Basis: col.Basis, Row: col.Direction == layout.DirectionRow,
		})
	}
	for _, w := range out.Widgets {
		i, ok := at[w.Column]
		if !ok {
			i = 0
		}
		out.Columns[i].Widgets = append(out.Columns[i].Widgets, w)
	}
}

func headingLevel(l int) int {
	if l < 1 || l > 6 {
		return 2
	}
	return l
}

// renderWidgets renders one layout entry, which may expand into several when it
// repeats over a configured collection.
func (s *Server) renderWidgets(w layout.Widget, c widget.Context) ([]render.Rendered, error) {
	binds := s.expand(w, c)

	out := make([]render.Rendered, 0, len(binds))
	for _, b := range binds {
		instance := c
		instance.Bind = b

		view, err := s.reg.Build(w.Type, instance, w.Options)
		if err != nil {
			return nil, fmt.Errorf("building %s: %w", w.Type, err)
		}

		var buf bytes.Buffer
		if err := s.render.Widget(&buf, w.Type, view); err != nil {
			return nil, fmt.Errorf("rendering %s: %w", w.Type, err)
		}
		out = append(out, render.Rendered{
			Type: w.Type, Column: w.Column, Span: w.Span,
			HTML: template.HTML(buf.String()),
		})
	}
	return out, nil
}

// expand turns a repeatOver into one binding per item, so adding a metric to
// domain.yaml adds a tile without anyone editing the layout.
func (s *Server) expand(w layout.Widget, c widget.Context) []widget.Bind {
	if w.RepeatOver == "" {
		return []widget.Bind{w.Bind}
	}

	var out []widget.Bind
	switch w.RepeatOver {
	case "config.metrics.drawer":
		for _, m := range c.Config.Domain.Metrics {
			if !m.ShowInDrawer {
				continue
			}
			b := w.Bind
			b.Metric = m.ID
			out = append(out, b)
		}
	case "config.metrics.leaderboard":
		for _, m := range c.Config.Domain.Metrics {
			if !m.ShowInLeaderboard {
				continue
			}
			b := w.Bind
			b.Metric = m.ID
			out = append(out, b)
		}
	default:
		out = append(out, w.Bind)
	}
	return out
}

// --- assets ----------------------------------------------------------------

// handleExampleUpstream serves the demonstration data as a foreign API would.
//
// It exists so that a fresh deploy can exercise the real pull driver end to
// end — mapping, transforms and all — against a real HTTP endpoint, rather than
// leaving that whole path untried until someone points it at production.
func (s *Server) handleExampleUpstream(w http.ResponseWriter, r *http.Request) {
	body, err := json.MarshalIndent(s.example(), "", "  ")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.Header().Set("content-type", "application/json; charset=utf-8")
	w.Header().Set("cache-control", "no-store")
	_, _ = w.Write(body)
}

func (s *Server) handleThemeCSS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("content-type", "text/css; charset=utf-8")
	// Immutable because the URL carries a fingerprint of the content; a config
	// change produces a different URL rather than a stale cache.
	w.Header().Set("cache-control", "public, max-age=31536000, immutable")
	w.Header().Set("etag", `"`+s.assetVersion+`"`)
	http.ServeContent(w, r, "theme.css", time.Time{}, bytes.NewReader(s.themeCSS))
}

func (s *Server) handleStatic() http.Handler {
	sub, err := fs.Sub(s.static, "web/static")
	if err != nil {
		// coverage:ignore -- the embedded tree is fixed at build time, so this
		// can only fail if the embed directive itself is wrong, which would
		// have failed to compile.
		return http.NotFoundHandler()
	}
	return http.StripPrefix("/assets/", cacheForever(http.FileServer(http.FS(sub))))
}

func cacheForever(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("cache-control", "public, max-age=31536000, immutable")
		h.ServeHTTP(w, r)
	})
}

// assetFingerprint is the version stamped onto every asset URL.
//
// It must cover everything served under that version, not just the generated
// stylesheet. Assets go out as immutable with a year-long max-age, so anything
// the fingerprint does not cover is one a returning reader will never fetch
// again: a fix to app.js would ship, the deployment would restart, and browsers
// that had been there before would keep running last month's script against
// this month's markup.
//
// Walking the embedded tree is cheap and happens once at startup, and the tree
// is fixed at build time, so this cannot drift from what handleStatic serves.
func assetFingerprint(css string, static fs.FS) string {
	var b strings.Builder
	b.WriteString(css)

	if static != nil {
		// WalkDir visits in lexical order, so the fingerprint is deterministic
		// and two identical builds agree.
		_ = fs.WalkDir(static, "web/static", func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			data, err := fs.ReadFile(static, path)
			if err != nil {
				return nil
			}
			b.WriteString(path)
			b.Write(data)
			return nil
		})
	}
	return fingerprint(b.String())
}

// fingerprint is a short content hash, used to bust asset caches.
func fingerprint(s string) string {
	// FNV-1a: not cryptographic, and does not need to be — it only has to
	// change when the content changes.
	var h uint64 = 14695981039346656037
	for i := range len(s) {
		h ^= uint64(s[i])
		h *= 1099511628211
	}
	return fmt.Sprintf("%x", h)[:12]
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

// link builds an in-app URL carrying the reader's state. It lives in the widget
// package because the builders assemble the same links from the same state.
func link(path string, params url.Values) string { return widget.Link(path, params) }

// callers are the services that consume another. They are the demand side
// whatever role they carry, which is what the opportunity rules need: a finding
// about issuance nobody requests is about both populations at once.
func callers(services []model.Service) []model.Service {
	out := make([]model.Service, 0, len(services))
	for _, sv := range services {
		if sv.CallsKey != "" {
			out = append(out, sv)
		}
	}
	return out
}
