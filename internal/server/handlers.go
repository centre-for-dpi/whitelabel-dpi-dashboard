package server

import (
	"bytes"
	"net/http"

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
	RoleLabel   string
	PeriodLabel string
	LocaleLabel string
	ApplyLabel  string
	ThemeHref   string
	// NewTabText warns, in text a screen reader reads, that a link leaves the
	// site. The external icon is aria-hidden like every glyph, so without this
	// the warning is visual only.
	NewTabText string

	// ChromeItems is the header bar, in configured order. The template switches
	// on Kind rather than knowing what the bar contains. ChromeStrip is the
	// second bar below it, and is empty unless the deployment declares one.
	ChromeItems []ChromeItemView
	ChromeStrip []ChromeItemView

	Scopes    []Choice
	Roles     []Choice
	Platforms []PlatformView
	Periods   []Choice
	Locales   []Choice
	Hidden    []widget.Hidden
	Footer    []FooterLink
	// FormAction is where the chrome bar submits. It is the address the reader is
	// already at rather than a hardcoded "/", so changing the language or the
	// period keeps an open drawer open instead of dismissing it.
	FormAction string

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
		// Without the tab, which named a panel of the drawer that is not there.
		http.Redirect(w, r, c.Href("/", paramTab, ""), http.StatusSeeOther)
		return
	}

	page, err := s.pageData(c)
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

// ChromeItemView is one item of the header bar, ready to render.
//
// One flat type with a Kind rather than an interface per kind: html/template
// cannot switch on a Go type, so a template would need a field to switch on
// anyway, and this keeps the shape of the bar visible in one place.
type ChromeItemView struct {
	Kind string

	// Label is the text, already resolved.
	Label string
	Icon  widget.Icon

	// Select.
	Name    string
	Options []Choice

	// Link, and the wordmark.
	Href     string
	External bool
	// ExternalIcon and NewTabText travel with the item rather than being read
	// off the page, so one template renders a bar item wherever the bar is.
	ExternalIcon widget.Icon
	NewTabText   string

	// Platforms.
	Platforms []PlatformView
}

// PlatformView is one exchange named in the strip.
type PlatformView struct {
	Name string
	Href string
	// Logo is the resolved asset URL, version stamp and all.
	Logo       string
	LogoWidth  int
	LogoHeight int
}

// pageTerm prefers a page's own term and falls back to the brand's, so adding a
// title to layout.yaml is opt-in and its absence changes nothing.
func pageTerm(pageTerm, fallback string) string {
	if pageTerm != "" {
		return pageTerm
	}
	return fallback
}

func (s *Server) pageData(c widget.Context) (PageData, error) {
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
		RoleLabel:    text.Text("chrome.role.label", nil),
		PeriodLabel:  text.Text("chrome.period.label", nil),
		LocaleLabel:  text.Text("chrome.locale.label", nil),
		ApplyLabel:   text.Text("chrome.apply", nil),
		NewTabText:   text.Text("chrome.newTab", nil),
	}

	// Every control below returns the reader to where they already are, drawer
	// included, rather than to the board.
	data.FormAction = here(c)

	// The toggle offers the theme you would switch TO, which is what the icon
	// has to depict for the control to make sense.
	if c.State.Theme == "dark" {
		data.ThemeIcon = icons.Icon("ui.themeLight")
		data.ThemeHref = c.Href(data.FormAction, paramTheme, "light")
	} else {
		data.ThemeIcon = icons.Icon("ui.themeDark")
		data.ThemeHref = c.Href(data.FormAction, paramTheme, "dark")
	}

	for _, scope := range d.Scopes {
		// Switching scope drops the region: a state filter means nothing in the
		// national view, and carrying it would leave a filter the reader can
		// neither see nor clear.
		data.Scopes = append(data.Scopes, Choice{
			Value:  scope,
			Label:  text.Text("chrome.scope."+scope, nil),
			Href:   c.Href("/", paramScope, scope, paramRegion, ""),
			Active: scope == c.State.Scope,
		})
	}

	// Switching role keeps the scope but drops the filters: a category chip
	// counted over issuers means something different over requestors, and a
	// status filter carried across would silently narrow the new board.
	for _, role := range d.Roles {
		data.Roles = append(data.Roles, Choice{
			Value: role.ID,
			Label: text.Text(role.TermID, nil),
			Href: c.Href("/",
				paramRole, role.ID, paramStatus, "", paramCat, "", paramSearch, ""),
			Active: role.ID == c.State.Role,
		})
	}

	for _, p := range d.Platforms {
		data.Platforms = append(data.Platforms, PlatformView{
			Name:       text.Text(p.TermID, nil),
			Href:       p.Href,
			Logo:       "/assets/" + p.Logo + "?v=" + data.AssetVersion,
			LogoWidth:  p.LogoWidth,
			LogoHeight: p.LogoHeight,
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

	// The bar, assembled in the order chrome.yaml lists. Built last because each
	// item draws on the choices above rather than recomputing them.
	data.ChromeItems = s.chromeBar(s.cfg.Chrome.Header, data, text, icons)
	data.ChromeStrip = s.chromeBar(s.cfg.Chrome.Strip, data, text, icons)

	// Everything the visible controls do not carry, so submitting the period
	// selector does not silently clear the filters.
	data.Hidden = hiddenState(c)

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
		drawer, err := s.drawerData(c)
		if err != nil {
			return PageData{}, err
		}
		data.Drawer = &drawer
	}
	return data, nil
}

// hiddenState is the state the chrome form must carry through a submit.
//
// Everything the reader has chosen except the two the bar renders as selects of
// its own — a hidden input and a <select> of the same name both submit, and the
// first one wins, which would pin the language to whatever it was when the page
// was rendered — and except `tab`, which the form's action carries in its path.
//
// It used to be assembled by hand from State, and the omissions showed: `role`
// is a link in the strip outside this form, so changing the language reset the
// board to the default role.
func hiddenState(c widget.Context) []widget.Hidden {
	return c.HiddenExcept(widget.ParamLang, widget.ParamPeriod, widget.ParamTab)
}

// here is the address the reader is at, as a path.
//
// A chrome control that hardcoded "/" dismissed whatever drawer was open the
// moment the reader changed the theme or the language. The drawer is part of
// what they were looking at, not something those controls were asked to close.
func here(c widget.Context) string {
	if c.Service != nil {
		return "/service/" + c.Service.ID
	}
	return "/"
}

// handleSection re-renders one band, which is what an HTMX swap asks for.
func (s *Server) handleSection(id string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sec, ok := s.layout.SectionByID(id)
		if !ok {
			http.NotFound(w, r)
			return
		}

		c := s.build(r)
		rendered, err := s.renderSection(sec, c)
		if err != nil {
			s.fail(w, r, err)
			return
		}

		var buf bytes.Buffer
		if err := s.render.Fragment(&buf, "section", rendered); err != nil {
			s.fail(w, r, err)
			return
		}
		s.pushURL(w, c, "/")
		s.write(w, buf.Bytes())
	}
}

// --- drawer ----------------------------------------------------------------

// DrawerData is the per-service panel.
//
// TabsOOB marks the tab strip for an out-of-band swap. Changing tab replaces the
// pane below the strip and nothing else, so the strip — which is above it, and
// inside a sticky header the swap must not disturb — travels alongside as its own
// swap rather than being re-rendered with the panel around it.
type DrawerData struct {
	TabsOOB      bool
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
//
// Href is the whole page at this tab, for a reader without script and for the
// address bar; PaneHref is the pane alone, which is all a tab change swaps.
type DrawerTab struct {
	ID       string
	Label    string
	Href     string
	PaneHref string
	Active   bool
}

// drawerContext builds the rendering context for a drawer fragment.
//
// The path carries the id; readState looks under /service/, so the request is
// rewritten to the address the reader would have used.
func (s *Server) drawerContext(r *http.Request) (widget.Context, bool) {
	rewritten := r.Clone(r.Context())
	rewritten.URL.Path = "/service/" + r.PathValue("id")

	c := s.build(rewritten)
	return c, c.Service != nil
}

func (s *Server) handleDrawer(w http.ResponseWriter, r *http.Request) {
	c, ok := s.drawerContext(r)
	if !ok {
		http.NotFound(w, r)
		return
	}

	drawer, err := s.drawerData(c)
	if err != nil {
		s.fail(w, r, err)
		return
	}

	var buf bytes.Buffer
	if err := s.render.Fragment(&buf, "drawer", drawer); err != nil {
		s.fail(w, r, err)
		return
	}
	s.pushURL(w, c, "/service/"+c.Service.ID)
	s.write(w, buf.Bytes())
}

// handleDrawerPane re-renders one tab of an open drawer.
//
// Everything above the tab strip — the service, its verdict, the rule that
// produced it — is the same whichever tab is showing, so a tab change used to
// rebuild a panel that had not changed, replaying its entry animation each time.
// This returns the pane and the strip alone; the strip travels out-of-band
// because it sits above the pane inside a sticky header rather than beside it.
func (s *Server) handleDrawerPane(w http.ResponseWriter, r *http.Request) {
	c, ok := s.drawerContext(r)
	if !ok {
		http.NotFound(w, r)
		return
	}

	drawer, err := s.drawerData(c)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	drawer.TabsOOB = true

	var buf bytes.Buffer
	if err := s.render.Fragment(&buf, "drawer-panes", drawer); err != nil {
		s.fail(w, r, err)
		return
	}
	s.pushURL(w, c, "/service/"+c.Service.ID)
	s.write(w, buf.Bytes())
}

// pushURL tells the client what address the fragment it is about to swap in
// corresponds to.
//
// hx-push-url="true" pushes the URL the request went to, which for a fragment is
// an internal address like /fragments/leaderboard — so filtering the board wrote
// a path into the reader's history that serves markup rather than a page. htmx
// checks this header before that attribute, so the server states the address
// instead, which it is the only party that knows.
func (s *Server) pushURL(w http.ResponseWriter, c widget.Context, path string) {
	params := c.ParamsWith()
	// `tab` names a panel of the drawer. On an address with no drawer in it, it
	// would describe something that is not on the screen.
	if c.Service == nil {
		params.Del(paramTab)
	}
	w.Header().Set("HX-Push-Url", widget.Link(path, params))
}

func (s *Server) drawerData(c widget.Context) (DrawerData, error) {
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
		// Closing drops the tab, which names a panel of the drawer, and keeps
		// everything else: the reader is going back to the board they came from,
		// not to a fresh one.
		CloseHref: c.Href("/", paramTab, ""),
	}

	// The rule that produced this verdict, in the deployment's own published
	// words. A reader who disagrees can see exactly what decided it.
	if term, ok := d.Thresholds.Prose[status]; ok {
		data.Rule = text.Text(term, rules.ProseParams(d.Thresholds.Values))
	}

	for _, tab := range s.layout.Drawer.Tabs {
		data.Tabs = append(data.Tabs, DrawerTab{
			ID:       tab.ID,
			Label:    text.Text(tab.TitleTermID, nil),
			Href:     c.Href("/service/"+sv.ID, paramTab, tab.ID),
			PaneHref: c.Href("/fragments/service/"+sv.ID+"/pane", paramTab, tab.ID),
			Active:   tab.ID == tabID,
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

// chromeItems turns the configured bar into view models, in order.
//
// Anything a kind needs has already been computed for the page — the scope
// choices, the period options, the theme href — so this only selects and labels.
// An unknown kind cannot reach here: config validation rejects it at startup,
// and skipping it silently rather than panicking means a hot reload that races
// validation degrades to a missing control instead of a blank page.
func (s *Server) chromeBar(bar config.ChromeBar, data PageData, text widget.TextResolver, icons widget.IconResolver) []ChromeItemView {
	out := make([]ChromeItemView, 0, len(bar.Items))
	for _, item := range bar.Items {
		v := ChromeItemView{Kind: string(item.Kind)}
		if item.IconKey != "" {
			v.Icon = icons.Icon(item.IconKey)
		}

		switch item.Kind {
		case config.ChromeWordmark:
			v.Label = data.Wordmark
			v.Href = "/"
			if item.IconKey == "" {
				v.Icon = data.BrandIcon
			}

		case config.ChromeScopeSwitch:
			v.Label = data.ScopeLabel
			v.Options = data.Scopes

		case config.ChromeSelect:
			switch item.State {
			case "period":
				v.Name, v.Label, v.Options = paramPeriod, data.PeriodLabel, data.Periods
			case "locale":
				v.Name, v.Label, v.Options = paramLang, data.LocaleLabel, data.Locales
			}
			// A configured label wins, so a deployment can rename a control
			// without renaming the term the rest of the page uses.
			if item.TermID != "" {
				v.Label = text.Text(item.TermID, nil)
			}

		case config.ChromeThemeToggle:
			v.Label = data.ThemeIcon.Label
			v.Icon = data.ThemeIcon
			v.Href = data.ThemeHref

		case config.ChromeLink:
			v.Label = text.Text(item.TermID, nil)
			v.Href = item.Href
			v.External = item.External
			v.ExternalIcon, v.NewTabText = data.ExternalIcon, data.NewTabText

		case config.ChromeRoleSwitch:
			v.Label = data.RoleLabel
			v.Options = data.Roles
			// A deployment reporting one side of the exchange is not offering a
			// choice, and a switch with one option is a control that does
			// nothing.
			if len(v.Options) < 2 {
				continue
			}

		case config.ChromePlatforms:
			v.Label = text.Text("chrome.platforms", nil)
			v.Platforms = data.Platforms
			if len(v.Platforms) == 0 {
				continue
			}
			v.ExternalIcon, v.NewTabText = data.ExternalIcon, data.NewTabText

		case config.ChromeSpacer:
			// Nothing to resolve; the template gives it the margin.

		default:
			continue
		}
		out = append(out, v)
	}
	return out
}
