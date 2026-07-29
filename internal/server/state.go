// Package server turns HTTP requests into rendered pages.
//
// It is the imperative shell: it reads requests, assembles the plain-data
// Context the pure widget builders need, and writes the result. No decision
// about what the data means is made here.
//
// Two rules shape the routing. Every control is a real link or form with a real
// GET target, so the dashboard works with JavaScript disabled; HTMX only
// upgrades a full page load into a partial swap. And the URL carries the whole
// reader-visible state, so a shared link reproduces exactly what the sender was
// looking at.
package server

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/config"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/query"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/widget"
)

// URL parameter names. These are the demo's own, kept deliberately: links
// already shared against the prototype continue to work.
//
// They are declared beside State, in the widget package, because the builders
// assemble the same links these handlers read. The local names are kept so the
// handlers below read as they always did.
const (
	paramScope   = widget.ParamScope
	paramRole    = widget.ParamRole
	paramID      = widget.ParamID
	paramSignals = widget.ParamSignals
	paramRegion  = widget.ParamRegion
	paramPeriod  = widget.ParamPeriod
	paramSearch  = widget.ParamSearch
	paramStatus  = widget.ParamStatus
	paramCat     = widget.ParamCat
	paramSort    = widget.ParamSort
	paramDir     = widget.ParamDir
	paramLang    = widget.ParamLang
	paramFilters = widget.ParamFilters
	paramTheme   = widget.ParamTheme
	paramTab     = widget.ParamTab
)

// readParams is the reader's selections as they travel: every parameter the
// dashboard knows that this request carried, and nothing it does not.
//
// readState is the validated reading of the same query, and is what decides what
// to show. This is what gets handed on. The two are separate because they answer
// different questions: readState fills in a default for anything missing, which
// is right for rendering and wrong for a link — a URL stating every default says
// nothing about what the reader actually chose.
func readParams(r *http.Request) url.Values {
	q := r.URL.Query()
	out := url.Values{}
	for _, name := range widget.Params {
		for _, v := range q[name] {
			if v != "" {
				out.Add(name, v)
			}
		}
	}
	return out
}

// readState reconstructs the reader's selections from a request.
//
// Every value is checked against what the deployment actually declares, and
// anything unrecognised falls back to a default rather than being carried
// through. A hand-edited URL or a bookmark surviving a config change should
// produce a sensible page, not an empty one.
func (s *Server) readState(r *http.Request) widget.State {
	q := r.URL.Query()
	d := s.cfg.Domain

	st := widget.State{
		Scope:      oneOf(q.Get(paramScope), d.Scopes, d.DefaultScope),
		Role:       query.RoleFor(d, q.Get(paramRole)),
		Period:     periodOr(q.Get(paramPeriod), d),
		Search:     strings.TrimSpace(q.Get(paramSearch)),
		Sort:       q.Get(paramSort),
		Dir:        q.Get(paramDir),
		Statuses:   knownValues(q[paramStatus], config.Statuses),
		Categories: knownValues(q[paramCat], categoryIDs(d)),
		// Not checked against a known set: the ids come from a signal card and
		// name whichever services its rule found. An id that no longer exists
		// simply matches nothing, which is the right reading of a stale link.
		IDs:       splitValues(q[paramID]),
		SignalTab: query.SignalTabFor(d, q.Get(paramSignals)),
		DrawerTab: q.Get(paramTab),
		// Only "open" counts, so a stray value collapses rather than throwing.
		FiltersOpen: q.Get(paramFilters) == "open",
	}

	st.Region = regionOr(q.Get(paramRegion), st.Scope, d)
	st.Locale = s.locales.Match(q.Get(paramLang), r.Header.Get("Accept-Language"))
	st.Theme = readTheme(q.Get(paramTheme), r)

	if st.Sort == "" {
		st.Sort = query.SortRank
	}
	if st.Dir != query.Asc && st.Dir != query.Desc {
		st.Dir = query.DefaultDirection(st.Sort)
	}

	// A drawer opened by path rather than by query, so /service/aadhaar is a
	// real address a reader can bookmark and send.
	if id, ok := strings.CutPrefix(r.URL.Path, "/service/"); ok && id != "" {
		st.DrawerID = strings.Trim(id, "/")
	}
	return st
}

// readTheme prefers an explicit choice, then the cookie the toggle sets.
//
// The cookie is what lets the server render the right theme in the first
// response. Leaving it to the client means the page arrives light and flips
// dark a moment later, which is worse than having no toggle.
func readTheme(explicit string, r *http.Request) string {
	if explicit == "light" || explicit == "dark" {
		return explicit
	}
	if c, err := r.Cookie("theme"); err == nil {
		if c.Value == "light" || c.Value == "dark" {
			return c.Value
		}
	}
	// Empty means "no opinion", which lets the stylesheet follow the operating
	// system rather than overriding it with a guess.
	return ""
}

func oneOf(got string, allowed []string, fallback string) string {
	for _, a := range allowed {
		if a == got {
			return got
		}
	}
	return fallback
}

func periodOr(got string, d config.Domain) string {
	for _, p := range d.Periods {
		if p.ID == got {
			return got
		}
	}
	return d.DefaultPeriod
}

// regionOr keeps the region consistent with the scope.
//
// Selecting a state and then switching to the national view would otherwise
// leave a region filter applied that the reader can no longer see or clear.
func regionOr(got, scope string, d config.Domain) string {
	if scope == d.DefaultScope {
		return ""
	}
	for _, r := range d.Taxonomy.Regions {
		if r.ID == got && (r.Scope == scope || r.Scope == d.DefaultScope) {
			return got
		}
	}
	return ""
}

// knownValues keeps only the values the deployment declares, and accepts both
// repeated parameters and one comma-separated parameter — the first is what a
// form submits, the second is what a shared link tends to carry.
func knownValues(raw []string, allowed []string) []string {
	var out []string
	seen := map[string]bool{}

	for _, group := range raw {
		for _, v := range strings.Split(group, ",") {
			v = strings.TrimSpace(v)
			if v == "" || seen[v] {
				continue
			}
			for _, a := range allowed {
				if a == v {
					seen[v] = true
					out = append(out, v)
					break
				}
			}
		}
	}
	return out
}

func categoryIDs(d config.Domain) []string {
	out := make([]string, 0, len(d.Taxonomy.Categories))
	for _, c := range d.Taxonomy.Categories {
		out = append(out, c.ID)
	}
	return out
}

// splitValues expands comma-joined repeats into a flat list, dropping blanks.
//
// The same shape knownValues accepts, without the closed set: some parameters
// carry ids the config has never heard of and cannot check.
func splitValues(raw []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, group := range raw {
		for _, v := range strings.Split(group, ",") {
			if v = strings.TrimSpace(v); v == "" || seen[v] {
				continue
			}
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}
