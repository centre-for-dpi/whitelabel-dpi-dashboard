package a11y

import (
	"strings"
	"testing"
)

// page wraps a body fragment in the minimum valid document, so a test about one
// rule is not also a test about <title> and lang.
func page(body string) string {
	return `<!doctype html><html lang="en"><head><title>T</title></head><body>` + body + `</body></html>`
}

func rulesFound(t *testing.T, doc string) map[string]string {
	t.Helper()
	problems, err := Audit(doc)
	if err != nil {
		t.Fatalf("Audit: %v", err)
	}
	out := map[string]string{}
	for _, p := range problems {
		out[p.Rule] = p.Msg
		if p.SC == "" {
			t.Errorf("problem %q has no success criterion", p.Rule)
		}
		if !strings.Contains(p.String(), p.Rule) {
			t.Errorf("String() for %q does not mention the rule", p.Rule)
		}
	}
	return out
}

func mustFlag(t *testing.T, rule, doc string) {
	t.Helper()
	if _, ok := rulesFound(t, doc)[rule]; !ok {
		t.Errorf("%s not reported for:\n%s", rule, doc)
	}
}

func mustNotFlag(t *testing.T, rule, doc string) {
	t.Helper()
	if msg, ok := rulesFound(t, doc)[rule]; ok {
		t.Errorf("%s wrongly reported (%s) for:\n%s", rule, msg, doc)
	}
}

func TestAuditRejectsMalformedInput(t *testing.T) {
	// The parser is extremely tolerant, so this documents that Audit does not
	// itself panic on the pathological cases rather than that it errors.
	for _, doc := range []string{"", "<", "<html", "<<<>>>", "<p><div></p></div>"} {
		if _, err := Audit(doc); err != nil {
			t.Errorf("Audit(%q) errored: %v", doc, err)
		}
	}
}

func TestDocumentLevelRules(t *testing.T) {
	mustFlag(t, "html-lang", `<!doctype html><html><head><title>T</title></head><body></body></html>`)
	mustFlag(t, "html-lang", `<!doctype html><html lang="  "><head><title>T</title></head><body></body></html>`)
	mustNotFlag(t, "html-lang", page(""))

	mustFlag(t, "document-title", `<!doctype html><html lang="en"><head></head><body></body></html>`)
	mustFlag(t, "document-title", `<!doctype html><html lang="en"><head><title>  </title></head><body></body></html>`)
	mustNotFlag(t, "document-title", page(""))

	mustFlag(t, "html-dir", `<!doctype html><html lang="en" dir="sideways"><head><title>T</title></head><body></body></html>`)
	for _, dir := range []string{"ltr", "rtl", "auto"} {
		mustNotFlag(t, "html-dir",
			`<!doctype html><html lang="en" dir="`+dir+`"><head><title>T</title></head><body></body></html>`)
	}
}

func TestAriaIdrefsMustResolve(t *testing.T) {
	for _, ref := range []string{"aria-labelledby", "aria-describedby", "aria-controls", "aria-owns"} {
		mustFlag(t, "aria-idref", page(`<div `+ref+`="ghost"></div>`))
		mustNotFlag(t, "aria-idref", page(`<p id="ghost">x</p><div `+ref+`="ghost"></div>`))
	}
	// A space-separated list is checked member by member.
	mustFlag(t, "aria-idref", page(`<p id="a">x</p><div aria-labelledby="a b"></div>`))
	mustNotFlag(t, "aria-idref", page(`<p id="a">x</p><p id="b">y</p><div aria-labelledby="a b"></div>`))
}

func TestAriaLabelNeedsARoleThatAcceptsIt(t *testing.T) {
	// The bug this rule exists for: a bare span.
	mustFlag(t, "aria-label-role", page(`<span aria-label="up 1">▲</span>`))
	mustFlag(t, "aria-label-role", page(`<div aria-label="nope"></div>`))
	// A role that does not take a name.
	mustFlag(t, "aria-label-role", page(`<div role="presentation" aria-label="nope"></div>`))

	// Natively labelable elements, and roles that do accept a name.
	mustNotFlag(t, "aria-label-role", page(`<button aria-label="Close">×</button>`))
	mustNotFlag(t, "aria-label-role", page(`<nav aria-label="Scope"></nav>`))
	mustNotFlag(t, "aria-label-role", page(`<span role="img" aria-label="a chart"></span>`))
	mustNotFlag(t, "aria-label-role", page(`<div role="radiogroup" aria-label="Scope"></div>`))
	// The fix the templates now use.
	mustNotFlag(t, "aria-label-role", page(`<span><span aria-hidden="true">▲</span><span class="sr-only">up 1</span></span>`))
}

func TestPositiveTabindexIsRejected(t *testing.T) {
	mustFlag(t, "tabindex-positive", page(`<a href="#" tabindex="1">x</a>`))
	mustNotFlag(t, "tabindex-positive", page(`<a href="#" tabindex="0">x</a>`))
	mustNotFlag(t, "tabindex-positive", page(`<div tabindex="-1"></div>`))
	// A non-numeric tabindex is the browser's problem, not this rule's.
	mustNotFlag(t, "tabindex-positive", page(`<div tabindex="banana"></div>`))
}

func TestNamesOnInteractiveElements(t *testing.T) {
	mustFlag(t, "link-name", page(`<a href="/x"><span aria-hidden="true">→</span></a>`))
	mustNotFlag(t, "link-name", page(`<a href="/x">Go</a>`))
	mustNotFlag(t, "link-name", page(`<a href="/x" aria-label="Go"></a>`))
	mustNotFlag(t, "link-name", page(`<a href="/x" title="Go"></a>`))
	mustNotFlag(t, "link-name", page(`<a href="/x"><img src="i.png" alt="Go"></a>`))
	mustNotFlag(t, "link-name", page(`<a href="/x"><span aria-hidden="true">→</span><span class="sr-only">Go</span></a>`))
	// An anchor without href is not a link and needs no name.
	mustNotFlag(t, "link-name", page(`<a></a>`))

	mustFlag(t, "button-name", page(`<button></button>`))
	mustFlag(t, "button-name", page(`<button><span aria-hidden="true">×</span></button>`))
	mustNotFlag(t, "button-name", page(`<button>Close</button>`))
	mustNotFlag(t, "button-name", page(`<button aria-labelledby="l">×</button><p id="l">Close</p>`))
}

func TestFormControlsMustBeLabelled(t *testing.T) {
	mustFlag(t, "control-label", page(`<input type="text">`))
	mustFlag(t, "control-label", page(`<select><option>a</option></select>`))
	mustFlag(t, "control-label", page(`<textarea></textarea>`))

	mustNotFlag(t, "control-label", page(`<label for="q">S</label><input id="q" type="search">`))
	mustNotFlag(t, "control-label", page(`<label>S<input type="text"></label>`))
	mustNotFlag(t, "control-label", page(`<input type="text" aria-label="S">`))
	mustNotFlag(t, "control-label", page(`<p id="l">S</p><input type="text" aria-labelledby="l">`))

	// Types that carry their own name or are not user-facing.
	for _, typ := range []string{"hidden", "submit", "button", "reset"} {
		mustNotFlag(t, "control-label", page(`<input type="`+typ+`" value="Go">`))
	}
}

func TestImagesNeedAnAltDecision(t *testing.T) {
	mustFlag(t, "img-alt", page(`<img src="x.png">`))
	// An explicit empty alt is a decision, not an omission.
	mustNotFlag(t, "img-alt", page(`<img src="x.png" alt="">`))
	mustNotFlag(t, "img-alt", page(`<img src="x.png" alt="A thing">`))
}

func TestTablesNeedACaptionAndScopedHeaders(t *testing.T) {
	// A table's own cells are not its name — the bug this fixture pins.
	mustFlag(t, "table-caption", page(`<table><tr><td>data</td></tr></table>`))
	mustNotFlag(t, "table-caption", page(`<table><caption>C</caption><tr><td>d</td></tr></table>`))
	mustNotFlag(t, "table-caption", page(`<table aria-label="C"><tr><td>d</td></tr></table>`))

	mustFlag(t, "th-scope", page(`<table><caption>C</caption><tr><th>H</th></tr></table>`))
	mustNotFlag(t, "th-scope", page(`<table><caption>C</caption><tr><th scope="col">H</th></tr></table>`))
}

func TestDuplicateIDs(t *testing.T) {
	got := rulesFound(t, page(`<p id="x"></p><p id="x"></p>`))
	msg, ok := got["duplicate-id"]
	if !ok {
		t.Fatal("duplicate-id not reported")
	}
	if !strings.Contains(msg, "x") || !strings.Contains(msg, "2") {
		t.Errorf("message %q does not name the id and its count", msg)
	}
	mustNotFlag(t, "duplicate-id", page(`<p id="x"></p><p id="y"></p>`))
}

func TestHeadingOrder(t *testing.T) {
	mustFlag(t, "heading-order", page(`<h1>a</h1><h3>b</h3>`))
	mustNotFlag(t, "heading-order", page(`<h1>a</h1><h2>b</h2><h3>c</h3>`))
	// Coming back up any number of levels is fine; only skipping down is not.
	mustNotFlag(t, "heading-order", page(`<h1>a</h1><h2>b</h2><h3>c</h3><h2>d</h2>`))

	mustFlag(t, "heading-h1", page(`<h2>no h1 anywhere</h2>`))
	mustFlag(t, "heading-h1", page(`<h1>a</h1><h1>b</h1>`))
	mustNotFlag(t, "heading-h1", page(`<h1>a</h1><h2>b</h2>`))
	// A document with no headings at all is not making a claim about outline.
	mustNotFlag(t, "heading-h1", page(`<p>just prose</p>`))
}

func TestTermIDLeaks(t *testing.T) {
	prefixes := []string{"flt.", "dr.", "inc.", "cat.", "metric."}

	leaks, err := TermIDLeaks(page(`
	  <button>flt.search</button>
	  <input aria-label="flt.search" placeholder="flt.search" name="q">
	  <p class="empty">dr.inc.none</p>
	  <img src="x.png" alt="cat.health">
	`), prefixes)
	if err != nil {
		t.Fatal(err)
	}

	byWhere := map[string]string{}
	for _, l := range leaks {
		byWhere[l.Where+"/"+l.ID] = l.String()
	}
	for _, want := range []string{"text/flt.search", "aria-label/flt.search", "placeholder/flt.search", "text/dr.inc.none", "alt/cat.health"} {
		if _, ok := byWhere[want]; !ok {
			t.Errorf("missing leak %q; found %v", want, keysOf(byWhere))
		}
	}

	// Nested namespaces must report once, as the longest id.
	for k := range byWhere {
		if k == "text/inc.none" {
			t.Error("dr.inc.none was also reported as inc.none; nested namespaces should report once")
		}
	}
}

// Machine identifiers are not leaks. A checkbox whose value names a metric is
// telling the server which metric, not showing a string to a reader — and this
// distinction is why the rule looks at the parsed tree instead of the raw HTML.
func TestTermIDLeaksIgnoresMachineIdentifiers(t *testing.T) {
	prefixes := []string{"flt.", "metric.", "cat.", "dr."}

	leaks, err := TermIDLeaks(page(`
	  <input type="checkbox" name="cat" value="cat.health">
	  <a href="/?sort=metric.errorRate">Sort</a>
	  <div id="dr.panel" class="cat.thing" data-metric="metric.availability"></div>
	  <script>var x = "flt.search";</script>
	  <style>.x { content: "cat.health"; }</style>
	`), prefixes)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range leaks {
		t.Errorf("machine identifier reported as a leak: %s", l)
	}
}

func TestTermIDLeaksRequiresAWholeWord(t *testing.T) {
	prefixes := []string{"cat.", "dr."}
	leaks, err := TermIDLeaks(page(`<p>subcat.identity and hydr.aulic and cat. alone</p>`), prefixes)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range leaks {
		t.Errorf("reported %s, which is not a term id", l)
	}
}

func TestTermIDLeaksRejectsMalformedDocuments(t *testing.T) {
	if _, err := TermIDLeaks("<p>flt.x", []string{"flt."}); err != nil {
		t.Errorf("TermIDLeaks on a fragment errored: %v", err)
	}
}

func TestOutlineTruncatesLoudly(t *testing.T) {
	long := strings.Repeat("y", 500)
	problems, err := Audit(page(`<span aria-label="x" title="` + long + `"></span>`))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range problems {
		if len(p.HTML) > 210 {
			t.Errorf("outline is %d chars; it should be truncated", len(p.HTML))
		}
	}
}

func TestProblemStringWithoutAnElement(t *testing.T) {
	p := Problem{Rule: "document-title", SC: "2.4.2 Page Titled", Msg: "no title"}
	s := p.String()
	if strings.Contains(s, "\n") {
		t.Errorf("a problem with no element should be one line, got %q", s)
	}
}

func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
