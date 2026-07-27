package main

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

// These tests drive the real binary's wiring — the same config, layout,
// templates and seeded data a `docker run` would use — through httptest. Each
// asserts a promise the dashboard makes to a reader, not an implementation
// detail, which is what lets the internals be rewritten underneath them.

func dashboard(t *testing.T) http.Handler {
	t.Helper()
	a, err := build(options{})
	if err != nil {
		t.Fatalf("the shipped configuration does not start: %v", err)
	}
	return a.handler
}

// get fetches a path and returns the body, failing on any status but 200.
func get(t *testing.T, h http.Handler, path string) string {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200\n%s", path, rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

func status(t *testing.T, h http.Handler, path string) int {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec.Code
}

var (
	serviceNames = regexp.MustCompile(`class="lb-title">([^<]+)`)
	rowCount     = regexp.MustCompile(`class="lb-row"`)
	metricValues = regexp.MustCompile(`class="cell-value tnum">([^<]+)`)
	termIDs      = regexp.MustCompile(`>([a-z]+\.[a-zA-Z0-9.]+)<`)
)

func names(body string) []string {
	var out []string
	for _, m := range serviceNames.FindAllStringSubmatch(body, -1) {
		out = append(out, m[1])
	}
	return out
}

func rows(body string) int { return len(rowCount.FindAllString(body, -1)) }

// --- the page renders ------------------------------------------------------

func TestTheDashboardRenders(t *testing.T) {
	body := get(t, dashboard(t), "/")

	for _, want := range []string{
		"working right now",     // the question at the top
		"Operational",           // the verdict
		"class=\"lb-row\"",      // the leaderboard
		"How status is decided", // the published rules
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the page does not contain %q", want)
		}
	}
	if got := rows(body); got == 0 {
		t.Error("the leaderboard is empty")
	}
}

func TestThePublishedRulesQuoteTheLiveThresholds(t *testing.T) {
	// The whole claim of the disclosure block is that the rule shown is the rule
	// applied. A sentence with the numbers missing quietly breaks that.
	body := get(t, dashboard(t), "/")

	for _, want := range []string{"99.00%", "99.50%", "2.00%", "1.00%", "15 minutes"} {
		if !strings.Contains(body, want) {
			t.Errorf("the published rules do not quote %q", want)
		}
	}
}

func TestNoTermIDLeaksToTheReader(t *testing.T) {
	// An unresolved term renders as its own id, which looks like a bug to a
	// reader and is one. Every page and every drawer tab is checked, because a
	// missing term usually hides on the panel nobody opened.
	h := dashboard(t)

	for _, path := range []string{
		"/", "/?scope=state", "/?status=major", "/?q=aadhaar",
		"/service/aadhaar",
		"/service/aadhaar?tab=errors",
		"/service/aadhaar?tab=traffic",
		"/service/aadhaar?tab=incidents",
	} {
		t.Run(path, func(t *testing.T) {
			for _, m := range termIDs.FindAllStringSubmatch(get(t, h, path), -1) {
				id := m[1]
				// Version strings and file names look like term ids but are not.
				if strings.Contains(id, "/") || strings.HasSuffix(id, ".js") {
					continue
				}
				t.Errorf("unresolved term id on the page: %q", id)
			}
		})
	}
}

// --- the period drives the leaderboard --------------------------------------

func TestChangingThePeriodReranksTheBoard(t *testing.T) {
	// The reason the control exists. A service that was poor last quarter and
	// excellent this week should lead over a short window and trail over a long
	// one; if the order never moves, the selector is decorative.
	h := dashboard(t)

	short := names(get(t, h, "/fragments/leaderboard?period=24h"))
	long := names(get(t, h, "/fragments/leaderboard?period=90d"))

	if len(short) == 0 || len(long) == 0 {
		t.Fatal("no rows to compare")
	}
	if strings.Join(short, "|") == strings.Join(long, "|") {
		t.Errorf("the order is identical at 24 hours and 90 days:\n%v", short)
	}
}

func TestChangingThePeriodRereadsTheFigures(t *testing.T) {
	// Rank and the numbers beside it have to move together. A board ordered by
	// ninety-day standing but showing this minute's availability invites the
	// reader to conclude the sort is broken.
	h := dashboard(t)

	short := metricValues.FindAllString(get(t, h, "/fragments/leaderboard?period=24h&sort=name"), 4)
	long := metricValues.FindAllString(get(t, h, "/fragments/leaderboard?period=90d&sort=name"), 4)

	if strings.Join(short, "|") == strings.Join(long, "|") {
		t.Errorf("the figures are identical at 24 hours and 90 days: %v", short)
	}
}

func TestTheLeaderboardNamesTheWindowItIsShowing(t *testing.T) {
	body := get(t, dashboard(t), "/fragments/leaderboard?period=7d")

	if !strings.Contains(body, "7 days") {
		t.Error("the leaderboard does not say which window its figures cover")
	}
}

// --- narrowing --------------------------------------------------------------

func TestFiltersNarrowTheBoard(t *testing.T) {
	h := dashboard(t)
	all := rows(get(t, h, "/fragments/leaderboard"))

	for _, tc := range []struct{ name, query string }{
		{"by status", "status=major"},
		{"by category", "cat=cat.identity"},
		{"by search", "q=aadhaar"},
		{"by two at once", "status=major&cat=cat.money"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := rows(get(t, h, "/fragments/leaderboard?"+tc.query))
			if got == 0 {
				t.Errorf("%s matched nothing", tc.query)
			}
			if got >= all {
				t.Errorf("%s returned %d of %d rows; it narrowed nothing", tc.query, got, all)
			}
		})
	}
}

func TestScopeSeparatesTheTwoViews(t *testing.T) {
	h := dashboard(t)

	national := names(get(t, h, "/fragments/leaderboard?scope=national"))
	state := names(get(t, h, "/fragments/leaderboard?scope=state"))

	if len(national) == 0 || len(state) == 0 {
		t.Fatal("one of the scopes is empty")
	}
	// The two views answer different questions, so no service belongs to both.
	inNational := map[string]bool{}
	for _, n := range national {
		inNational[n] = true
	}
	for _, n := range state {
		if inNational[n] {
			t.Errorf("%q appears in both the national and sub-national views", n)
		}
	}
}

func TestSortingReordersTheBoard(t *testing.T) {
	h := dashboard(t)

	asc := names(get(t, h, "/fragments/leaderboard?sort=name&dir=asc"))
	desc := names(get(t, h, "/fragments/leaderboard?sort=name&dir=desc"))

	if len(asc) < 2 {
		t.Fatal("not enough rows to sort")
	}
	if asc[0] == desc[0] {
		t.Errorf("ascending and descending both start with %q", asc[0])
	}
}

func TestRankIsIndependentOfFiltering(t *testing.T) {
	// "Rank 4" has to mean the same thing however the reader narrowed the view.
	h := dashboard(t)

	unfiltered := get(t, h, "/fragments/leaderboard?sort=name")
	filtered := get(t, h, "/fragments/leaderboard?sort=name&status=major")

	// Every rank in the filtered view must appear in the unfiltered one.
	rankOf := regexp.MustCompile(`<span class="tnum">(\d+)</span>`)
	all := map[string]bool{}
	for _, m := range rankOf.FindAllStringSubmatch(unfiltered, -1) {
		all[m[1]] = true
	}
	for _, m := range rankOf.FindAllStringSubmatch(filtered, -1) {
		if !all[m[1]] {
			t.Errorf("rank %s appears only when filtered; ranks are being recomputed", m[1])
		}
	}
}

// --- the drawer -------------------------------------------------------------

func TestTheDrawerOpensOnItsOwnAddress(t *testing.T) {
	// A service is a real, shareable URL rather than client-side state, so a
	// link to one reproduces what the sender was looking at.
	body := get(t, dashboard(t), "/service/aadhaar")

	if !strings.Contains(body, `id="drawer"`) {
		t.Fatal("no drawer rendered")
	}
	if !strings.Contains(body, "Why this status:") {
		t.Error("the drawer does not explain the verdict it is reporting")
	}
}

func TestEveryDrawerTabRenders(t *testing.T) {
	h := dashboard(t)

	for _, tc := range []struct{ tab, want string }{
		{"overview", `class="spark"`},
		{"errors", "w-barlist"},
		{"traffic", `class="bars"`},
		{"incidents", "w-timeline"},
	} {
		t.Run(tc.tab, func(t *testing.T) {
			body := get(t, h, "/fragments/service/aadhaar?tab="+tc.tab)
			if !strings.Contains(body, tc.want) {
				t.Errorf("the %s tab does not contain %q", tc.tab, tc.want)
			}
		})
	}
}

func TestEveryChartHasATableBehindIt(t *testing.T) {
	// A line on a screen is not accessible to a screen reader, not copyable and
	// not checkable. The table is what makes the figure usable.
	h := dashboard(t)

	for _, tab := range []string{"errors", "traffic"} {
		body := get(t, h, "/fragments/service/aadhaar?tab="+tab)
		if !strings.Contains(body, "w-datatable") || !strings.Contains(body, "<table>") {
			t.Errorf("the %s tab has a chart with no table behind it", tab)
		}
	}
}

func TestAStaleBookmarkStillShowsTheDashboard(t *testing.T) {
	// A service that no longer exists is a stale link, not an error worth a 404:
	// everything else is still worth showing.
	if got := status(t, dashboard(t), "/service/nonexistent"); got != http.StatusSeeOther {
		t.Errorf("GET /service/nonexistent = %d, want a redirect to the dashboard", got)
	}
}

// --- progressive enhancement ------------------------------------------------

func TestEveryControlWorksWithoutJavaScript(t *testing.T) {
	// HTMX upgrades a page load into a partial swap; it must not be what makes
	// the control work in the first place.
	body := get(t, dashboard(t), "/")

	if !strings.Contains(body, `<form class="controls" method="get"`) {
		t.Error("the chrome controls are not a real GET form")
	}
	if !strings.Contains(body, `method="get" action="/" data-dpi-filters`) {
		t.Error("the filter bar is not a real GET form")
	}
	// Filters are checkboxes, which a keyboard and a screen reader already
	// understand, rather than buttons carrying state in a class.
	if !strings.Contains(body, `type="checkbox" name="status"`) {
		t.Error("status filters are not real checkboxes")
	}
	if !strings.Contains(body, `<noscript>`) {
		t.Error("no submit button for readers without JavaScript")
	}
	// Sorting is a link, so it works before any script has loaded.
	if !strings.Contains(body, `class="th-sort" href="/?sort=`) {
		t.Error("column sorting is not a real link")
	}
}

func TestTheMarkupIsAccessible(t *testing.T) {
	body := get(t, dashboard(t), "/service/aadhaar")

	for _, want := range []string{
		`aria-sort=`,         // the table announces how it is sorted
		`aria-live="polite"`, // the result count announces itself when it changes
		`class="skip"`,       // skip to content
		`aria-labelledby="drawer-title"`,
		`<caption class="sr-only">`,
		`role="img" aria-label=`, // charts carry a text alternative
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the markup is missing %q", want)
		}
	}
}

func TestTheThemeFollowsTheReadersChoice(t *testing.T) {
	h := dashboard(t)

	if body := get(t, h, "/?theme=dark"); !strings.Contains(body, `data-theme="dark"`) {
		t.Error("an explicit dark choice was not applied server-side")
	}
	// With no choice at all the attribute is absent, which lets the stylesheet
	// follow the operating system rather than overriding it with a guess.
	if body := get(t, h, "/"); strings.Contains(body, `data-theme=`) {
		t.Error("a theme was asserted despite the reader expressing no preference")
	}

	// The cookie is what lets the first response already be right, instead of
	// arriving light and flipping dark a moment later.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: "theme", Value: "dark"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), `data-theme="dark"`) {
		t.Error("the theme cookie was ignored, so the page would flash on load")
	}
}

// --- hostile input -----------------------------------------------------------

func TestNonsenseParametersFallBackRatherThanBreak(t *testing.T) {
	// A hand-edited URL or a bookmark surviving a config change should produce a
	// sensible page, not an empty one or a 500.
	h := dashboard(t)

	for _, q := range []string{
		"?scope=galactic",
		"?period=eternity",
		"?status=catastrophic",
		"?cat=cat.invented",
		"?sort=nonsense&dir=sideways",
		"?region=reg.atlantis",
		"?lang=kl",
		"?theme=chartreuse",
		"?q=" + strings.Repeat("x", 5000),
		"?status=major&status=major&status=major",
	} {
		t.Run(q, func(t *testing.T) {
			if got := status(t, h, "/"+q); got != http.StatusOK {
				t.Errorf("GET /%s = %d, want 200", q, got)
			}
		})
	}
}

func TestSearchInputIsEscaped(t *testing.T) {
	// html/template escapes contextually, but the search term is the one value
	// that goes straight from the URL into an attribute, so it is worth an
	// explicit test.
	body := get(t, dashboard(t), `/?q=%22%3E%3Cscript%3Ealert(1)%3C/script%3E`)

	if strings.Contains(body, "<script>alert(1)</script>") {
		t.Fatal("a search term was reflected into the page unescaped")
	}
}

// --- operations --------------------------------------------------------------

func TestHealthAndReadiness(t *testing.T) {
	h := dashboard(t)

	if got := status(t, h, "/healthz"); got != http.StatusOK {
		t.Errorf("/healthz = %d", got)
	}
	// Ready means there is something to serve, so a rolling deploy does not cut
	// traffic to an empty dashboard.
	if got := status(t, h, "/readyz"); got != http.StatusOK {
		t.Errorf("/readyz = %d", got)
	}
}

func TestAssetsAreServedAndCached(t *testing.T) {
	h := dashboard(t)

	for _, path := range []string{
		"/assets/theme.css",
		"/assets/app.css",
		"/assets/app.js",
		"/assets/htmx.min.js",
		"/assets/fonts/outfit-latin.woff2",
	} {
		t.Run(path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("= %d, want 200", rec.Code)
			}
			if rec.Body.Len() == 0 {
				t.Error("is empty")
			}
			if cc := rec.Header().Get("cache-control"); !strings.Contains(cc, "immutable") {
				t.Errorf("cache-control = %q; assets carry a version and should cache hard", cc)
			}
		})
	}
}

func TestThePageItselfIsNeverCached(t *testing.T) {
	// A dashboard is only as good as its freshness, and a cached status page is
	// worse than no status page.
	rec := httptest.NewRecorder()
	dashboard(t).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if cc := rec.Header().Get("cache-control"); !strings.Contains(cc, "no-store") {
		t.Errorf("cache-control = %q, want no-store", cc)
	}
}

func TestGeneratedStylesheetCarriesTheConfiguredTokens(t *testing.T) {
	body := get(t, dashboard(t), "/assets/theme.css")

	for _, want := range []string{
		"--bg:#FAF9F5",
		`@media (prefers-color-scheme:dark)`,
		`:root[data-theme="dark"]`,
		"@font-face",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the generated stylesheet is missing %q", want)
		}
	}
}

func TestFragmentRoutesExistForEverySwappableSection(t *testing.T) {
	// Registered from the layout rather than hardcoded, so a deployment that
	// adds a section gets its route without touching Go.
	h := dashboard(t)

	for _, id := range []string{"verdict", "signals", "leaderboard"} {
		if got := status(t, h, "/fragments/"+id); got != http.StatusOK {
			t.Errorf("/fragments/%s = %d", id, got)
		}
	}
	if got := status(t, h, "/fragments/invented"); got != http.StatusNotFound {
		t.Errorf("/fragments/invented = %d, want 404", got)
	}
}

func TestFragmentsAreFragmentsNotWholePages(t *testing.T) {
	// An HTMX swap that returned a whole document would nest one inside another.
	body := get(t, dashboard(t), "/fragments/leaderboard")

	if strings.Contains(body, "<!doctype html>") || strings.Contains(body, "<body>") {
		t.Error("a fragment response contains a whole document")
	}
	if !strings.Contains(body, `id="leaderboard"`) {
		t.Error("the fragment is not the section it claims to be")
	}
}

func TestValidateModeStartsAndStops(t *testing.T) {
	// What `make validate` runs in CI: prove the configuration is coherent
	// without binding a port.
	if err := run([]string{"-validate"}); err != nil {
		t.Errorf("the shipped configuration does not validate: %v", err)
	}
}
