package server

import (
	"bytes"
	"net/http"
	"net/url"

	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/config"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/layout"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/render"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/rules"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/widget"
)

// PageData is what the base template renders.
type PageData struct {
	Locale    string
	Direction string
	Theme     string
	Title     string
	Tagline   string
	// Wordmark is the brand name in the header, which the document title no
	// longer has to match.
	Wordmark     string
	Favicon      string
	SkipText     string
	AssetVersion string

	BrandIcon    widget.Icon
	ThemeIcon    widget.Icon
	ExternalIcon widget.Icon

	ScopeLabel  string
	PeriodLabel string
	LocaleLabel string
	ApplyLabel  string
	ThemeHref   string

	Scopes  []Choice
	Periods []Choice
	Locales []Choice
	Hidden  []HiddenField
	Footer  []FooterLink

	Sections []render.Section
	Drawer   *DrawerData
}

// Choice is one option in a control.
type Choice struct {
	Value  string
	Label  string
	Href   string
	Active bool
}

// HiddenField carries state through a form that does not display it, so
// changing the period does not silently discard the reader's filters.
type HiddenField struct{ Name, Value string }

// FooterLink is one credit.
type FooterLink struct {
	Label    string
	Href     string
	External bool
}

func (s *Server) handlePage(w http.ResponseWriter, r *http.Request) {
	c := s.build(r)

	// A path naming a service that no longer exists is a stale bookmark, not an
	// error worth a 404 page: the dashboard still has everything else to show.
	if c.State.DrawerID != "" && c.Service == nil {
		http.Redirect(w, r, link("/", r.URL.Query()), http.StatusSeeOther)
		return
	}

	page, err := s.pageData(c, r)
	if err != nil {
		s.fail(w, r, err)
		return
	}

	var buf bytes.Buffer
	if err := s.render.Page(&buf, page); err != nil {
		s.fail(w, r, err)
		return
	}
	s.write(w, buf.Bytes())
}

// pageTerm prefers a page's own term and falls back to the brand's, so adding a
// title to layout.yaml is opt-in and its absence changes nothing.
func pageTerm(pageTerm, fallback string) string {
	if pageTerm != "" {
		return pageTerm
	}
	return fallback
}

func (s *Server) pageData(c widget.Context, r *http.Request) (PageData, error) {
	d := s.cfg.Domain
	text := c.Text
	icons := c.Icons

	page, ok := s.layout.PageByPath("/")
	if !ok {
		return PageData{}, errNoPage
	}

	data := PageData{
		Locale:       c.State.Locale,
		Direction:    s.locales.For(c.State.Locale).Direction(),
		Theme:        c.State.Theme,
		Title:        text.Text(pageTerm(page.TitleTermID, s.cfg.Brand.WordmarkTermID), nil),
		Tagline:      text.Text(pageTerm(page.DescriptionTermID, s.cfg.Brand.TaglineTermID), nil),
		Wordmark:     text.Text(s.cfg.Brand.WordmarkTermID, nil),
		Favicon:      s.cfg.Brand.Favicon,
		SkipText:     text.Text("chrome.skip", nil),
		AssetVersion: s.assetVersion,
		BrandIcon:    icons.Icon(s.cfg.Brand.IconKey),
		ExternalIcon: icons.Icon("ui.external"),
		ScopeLabel:   text.Text("chrome.scope.label", nil),
		PeriodLabel:  text.Text("chrome.period.label", nil),
		LocaleLabel:  text.Text("chrome.locale.label", nil),
		ApplyLabel:   text.Text("chrome.apply", nil),
	}

	// The toggle offers the theme you would switch TO, which is what the icon
	// has to depict for the control to make sense.
	if c.State.Theme == "dark" {
		data.ThemeIcon = icons.Icon("ui.themeLight")
		data.ThemeHref = link("/", withParam(r.URL.Query(), paramTheme, "light"))
	} else {
		data.ThemeIcon = icons.Icon("ui.themeDark")
		data.ThemeHref = link("/", withParam(r.URL.Query(), paramTheme, "dark"))
	}

	for _, scope := range d.Scopes {
		// Switching scope drops the region: a state filter means nothing in the
		// national view, and carrying it would leave a filter the reader can
		// neither see nor clear.
		params := withParam(r.URL.Query(), paramScope, scope)
		params.Del(paramRegion)
		data.Scopes = append(data.Scopes, Choice{
			Value:  scope,
			Label:  text.Text("chrome.scope."+scope, nil),
			Href:   link("/", params),
			Active: scope == c.State.Scope,
		})
	}

	for _, p := range d.Periods {
		data.Periods = append(data.Periods, Choice{
			Value: p.ID, Label: text.Text(p.TermID, nil), Active: p.ID == c.State.Period,
		})
	}

	names := s.locales.Names()
	for _, code := range s.locales.Locales() {
		data.Locales = append(data.Locales, Choice{
			Value: code, Label: names[code], Active: code == c.State.Locale,
		})
	}

	// Everything the visible controls do not carry, so submitting the period
	// selector does not silently clear the filters.
	data.Hidden = hiddenState(c.State)

	for _, f := range s.cfg.Brand.Footer {
		data.Footer = append(data.Footer, FooterLink{
			Label: text.Text(f.TermID, nil), Href: f.Href, External: f.External,
		})
	}

	for _, sec := range page.Sections {
		rendered, err := s.renderSection(sec, c)
		if err != nil {
			return PageData{}, err
		}
		data.Sections = append(data.Sections, rendered)
	}

	if c.Service != nil {
		drawer, err := s.drawerData(c, r)
		if err != nil {
			return PageData{}, err
		}
		data.Drawer = &drawer
	}
	return data, nil
}

// hiddenState is the state the chrome form must carry through a submit.
func hiddenState(st widget.State) []HiddenField {
	var out []HiddenField
	add := func(name, value string) {
		if value != "" {
			out = append(out, HiddenField{Name: name, Value: value})
		}
	}
	add(paramScope, st.Scope)
	add(paramRegion, st.Region)
	add(paramSearch, st.Search)
	add(paramSort, st.Sort)
	add(paramDir, st.Dir)
	add(paramTheme, st.Theme)
	for _, v := range st.Statuses {
		out = append(out, HiddenField{Name: paramStatus, Value: v})
	}
	for _, v := range st.Categories {
		out = append(out, HiddenField{Name: paramCat, Value: v})
	}
	return out
}

// handleSection re-renders one band, which is what an HTMX swap asks for.
func (s *Server) handleSection(id string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sec, ok := s.layout.SectionByID(id)
		if !ok {
			http.NotFound(w, r)
			return
		}

		rendered, err := s.renderSection(sec, s.build(r))
		if err != nil {
			s.fail(w, r, err)
			return
		}

		var buf bytes.Buffer
		if err := s.render.Fragment(&buf, "section", rendered); err != nil {
			s.fail(w, r, err)
			return
		}
		s.write(w, buf.Bytes())
	}
}

// --- drawer ----------------------------------------------------------------

// DrawerData is the per-service panel.
type DrawerData struct {
	ID           string
	Name         string
	Description  string
	Category     string
	CategoryIcon widget.Icon
	Region       string
	StatusLabel  string
	StatusIcon   widget.Icon
	StatusTone   string
	RuleLabel    string
	Rule         string
	CloseHref    string
	CloseIcon    widget.Icon
	Tabs         []DrawerTab
	Grid         layout.Grid
	Widgets      []render.Rendered
}

// DrawerTab is one pane's button.
type DrawerTab struct {
	ID           string
	Label        string
	Href         string
	FragmentHref string
	Active       bool
}

func (s *Server) handleDrawer(w http.ResponseWriter, r *http.Request) {
	// The path carries the id; readState looks under /service/, so the request
	// is rewritten to the address the reader would have used.
	id := r.PathValue("id")
	rewritten := r.Clone(r.Context())
	rewritten.URL.Path = "/service/" + id

	c := s.build(rewritten)
	if c.Service == nil {
		http.NotFound(w, r)
		return
	}

	drawer, err := s.drawerData(c, r)
	if err != nil {
		s.fail(w, r, err)
		return
	}

	var buf bytes.Buffer
	if err := s.render.Fragment(&buf, "drawer", drawer); err != nil {
		s.fail(w, r, err)
		return
	}
	s.write(w, buf.Bytes())
}

func (s *Server) drawerData(c widget.Context, r *http.Request) (DrawerData, error) {
	sv := c.Service
	d := s.cfg.Domain
	status := string(sv.Status)
	text := c.Text
	icons := c.Icons

	tabID := c.State.DrawerTab
	if _, ok := s.layout.TabByID(tabID); !ok {
		tabID = s.layout.DefaultTab()
	}

	data := DrawerData{
		ID:           sv.ID,
		Name:         text.Text(sv.NameTermID, nil),
		Description:  text.Text(sv.DescTermID, nil),
		Category:     text.Text(sv.CategoryID, nil),
		CategoryIcon: icons.Icon(categoryIcon(d, sv.CategoryID)),
		Region:       text.Text(sv.RegionID, nil),
		StatusLabel:  text.Text(d.StatusModel.LabelTermID[status], nil),
		StatusIcon:   icons.Icon(d.StatusModel.IconKey[status]),
		StatusTone:   statusTone(status),
		RuleLabel:    text.Text("dr.why", nil),
		CloseIcon:    icons.Icon("ui.close"),
		CloseHref:    link("/", stripDrawer(r.URL.Query())),
	}

	// The rule that produced this verdict, in the deployment's own published
	// words. A reader who disagrees can see exactly what decided it.
	if term, ok := d.Thresholds.Prose[status]; ok {
		data.Rule = text.Text(term, rules.ProseParams(d.Thresholds.Values))
	}

	for _, tab := range s.layout.Drawer.Tabs {
		params := withParam(r.URL.Query(), paramTab, tab.ID)
		data.Tabs = append(data.Tabs, DrawerTab{
			ID:           tab.ID,
			Label:        text.Text(tab.TitleTermID, nil),
			Href:         link("/service/"+sv.ID, params),
			FragmentHref: link("/fragments/service/"+sv.ID, params),
			Active:       tab.ID == tabID,
		})
	}

	active, _ := s.layout.TabByID(tabID)
	data.Grid = active.Grid
	for _, w := range active.Widgets {
		rendered, err := s.renderWidgets(w, c)
		if err != nil {
			return DrawerData{}, err
		}
		data.Widgets = append(data.Widgets, rendered...)
	}
	return data, nil
}

// --- helpers ---------------------------------------------------------------

func withParam(q url.Values, key, value string) url.Values {
	out := url.Values{}
	for k, v := range q {
		out[k] = append([]string(nil), v...)
	}
	out.Set(key, value)
	return out
}

func stripDrawer(q url.Values) url.Values {
	out := withParam(q, paramTab, "")
	out.Del(paramTab)
	return out
}

func statusTone(status string) string {
	if status == "operational" {
		return "ok"
	}
	return status
}

func categoryIcon(d config.Domain, id string) string {
	for _, c := range d.Taxonomy.Categories {
		if c.ID == id {
			return c.IconKey
		}
	}
	return ""
}

func (s *Server) write(w http.ResponseWriter, body []byte) {
	w.Header().Set("content-type", "text/html; charset=utf-8")
	// A dashboard is only as good as its freshness, and a cached status page is
	// worse than no status page. Assets are cached hard; the page never is.
	w.Header().Set("cache-control", "no-store")
	_, _ = w.Write(body)
}

// fail logs and returns a plain error.
//
// Deliberately terse to the reader and detailed to the log: a rendering failure
// is a bug in this repository, and the reader can do nothing with the details.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	s.log.Error("render failed", "path", r.URL.Path, "err", err)
	http.Error(w, "the dashboard could not render this page", http.StatusInternalServerError)
}

var errNoPage = errNoPageErr{}

type errNoPageErr struct{}

func (errNoPageErr) Error() string {
	return "the layout declares no page at /, so there is nothing to serve"
}
