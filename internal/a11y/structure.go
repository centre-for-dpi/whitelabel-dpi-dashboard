package a11y

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// Audit reports structural accessibility problems in a rendered document.
//
// This is the layer between the contrast check, which sees colours but no
// markup, and the browser suite, which sees everything but needs Chrome. What
// lives here is every rule decidable from the HTML alone: whether a control has
// a name, whether an aria reference resolves, whether headings descend in order.
// Those are the failures that appear and disappear as templates change, so they
// belong in the test run that gates a commit rather than in a nightly job.
//
// It is deliberately not a general-purpose auditor. Each rule below earned its
// place by having been broken in this repository at least once.
type Problem struct {
	Rule string // short identifier, stable enough to grep for
	SC   string // the success criterion at stake
	Msg  string
	HTML string // the offending element, truncated
}

func (p Problem) String() string {
	s := fmt.Sprintf("[%s | WCAG %s] %s", p.Rule, p.SC, p.Msg)
	if p.HTML != "" {
		s += "\n    " + p.HTML
	}
	return s
}

// Audit parses doc and returns every problem found, in document order per rule
// and then by rule name, so output is stable between runs.
func Audit(doc string) ([]Problem, error) {
	root, err := html.Parse(strings.NewReader(doc))
	if err != nil {
		return nil, fmt.Errorf("a11y: parsing document: %w", err)
	}

	a := &auditor{ids: map[string]int{}}
	a.index(root)
	a.walk(root)
	a.checkDocument(root)
	a.checkDuplicateIDs()
	a.checkHeadingOrder()

	sort.SliceStable(a.problems, func(i, j int) bool { return a.problems[i].Rule < a.problems[j].Rule })
	return a.problems, nil
}

type auditor struct {
	problems []Problem
	ids      map[string]int
	headings []int
	labelFor map[string]bool
}

func (a *auditor) report(rule, sc, msg string, n *html.Node) {
	p := Problem{Rule: rule, SC: sc, Msg: msg}
	if n != nil {
		p.HTML = outline(n)
	}
	a.problems = append(a.problems, p)
}

// index collects ids and label targets in one pass, because several rules need
// to look forward as well as back.
func (a *auditor) index(n *html.Node) {
	a.labelFor = map[string]bool{}
	var visit func(*html.Node)
	visit = func(n *html.Node) {
		if n.Type == html.ElementNode {
			if id := attr(n, "id"); id != "" {
				a.ids[id]++
			}
			if n.DataAtom == atom.Label {
				if f := attr(n, "for"); f != "" {
					a.labelFor[f] = true
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			visit(c)
		}
	}
	visit(n)
}

func (a *auditor) walk(n *html.Node) {
	if n.Type == html.ElementNode {
		a.checkNode(n)
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		a.walk(c)
	}
}

// rolesTakingLabel is the set of ARIA roles for which aria-label is permitted.
// The rule this enforces is narrower: aria-label on an element with no role at
// all is dropped by screen readers, so it is never the right way to name
// something. Naming the permitted set rather than the prohibited one keeps the
// check honest as ARIA grows.
var rolesTakingLabel = []string{
	"img", "button", "link", "dialog", "region", "navigation", "group",
	"radiogroup", "radio", "tab", "tablist", "tabpanel", "status", "alert",
	"search", "form", "banner", "contentinfo", "main", "complementary",
	"table", "grid", "list", "listitem", "menu", "menuitem", "checkbox",
	"switch", "slider", "spinbutton", "textbox", "combobox", "progressbar",
	"separator", "toolbar", "tooltip", "tree", "treeitem", "note",
}

// nativelyLabelable are elements whose own semantics accept a name, so
// aria-label on them is legitimate without an explicit role.
var nativelyLabelable = map[atom.Atom]bool{
	atom.A: true, atom.Button: true, atom.Input: true, atom.Select: true,
	atom.Textarea: true, atom.Img: true, atom.Table: true, atom.Form: true,
	atom.Nav: true, atom.Section: true, atom.Aside: true, atom.Main: true,
	atom.Header: true, atom.Footer: true, atom.Dialog: true, atom.Iframe: true,
	atom.Fieldset: true, atom.Details: true, atom.Area: true, atom.Audio: true,
	atom.Video: true, atom.Svg: true, atom.Ul: true, atom.Ol: true, atom.Li: true,
}

func (a *auditor) checkNode(n *html.Node) {
	// --- aria idrefs resolve ---------------------------------------------
	// A dangling reference is worse than none: the browser computes an empty
	// name and the element is announced as nothing at all.
	for _, ref := range []string{"aria-labelledby", "aria-describedby", "aria-controls", "aria-owns"} {
		for _, id := range strings.Fields(attr(n, ref)) {
			if a.ids[id] == 0 {
				a.report("aria-idref", "1.3.1 Info and Relationships",
					fmt.Sprintf("%s points at %q, which is not an id in this document", ref, id), n)
			}
		}
	}

	// --- aria-label needs a role that accepts it -------------------------
	if attr(n, "aria-label") != "" {
		role := attr(n, "role")
		switch {
		case role != "" && slices.Contains(rolesTakingLabel, role):
		case role == "" && nativelyLabelable[n.DataAtom]:
		case role != "":
			a.report("aria-label-role", "4.1.2 Name, Role, Value",
				fmt.Sprintf("aria-label on role=%q, which does not accept a name", role), n)
		default:
			a.report("aria-label-role", "4.1.2 Name, Role, Value",
				fmt.Sprintf("aria-label on <%s>, which has no role, so screen readers ignore it — use visually hidden text instead", n.Data), n)
		}
	}

	// --- positive tabindex ----------------------------------------------
	if ti := attr(n, "tabindex"); ti != "" {
		if v, err := strconv.Atoi(ti); err == nil && v > 0 {
			a.report("tabindex-positive", "2.4.3 Focus Order",
				fmt.Sprintf("tabindex=%d overrides document order; only 0 and -1 are safe", v), n)
		}
	}

	switch n.DataAtom {
	case atom.Img:
		// A missing alt makes the file name the announcement; alt="" is a
		// deliberate claim that the image is decorative, which is different.
		if !hasAttr(n, "alt") {
			a.report("img-alt", "1.1.1 Non-text Content", "<img> has no alt attribute", n)
		}

	case atom.Input, atom.Select, atom.Textarea:
		if t := strings.ToLower(attr(n, "type")); t == "hidden" || t == "submit" || t == "button" || t == "reset" {
			break
		}
		if !a.isLabelled(n) {
			a.report("control-label", "3.3.2 Labels or Instructions",
				fmt.Sprintf("<%s> has no label, aria-label, aria-labelledby or wrapping <label>", n.Data), n)
		}

	case atom.A:
		if hasAttr(n, "href") && accessibleName(n) == "" {
			a.report("link-name", "2.4.4 Link Purpose",
				"link has no discernible text", n)
		}

	case atom.Button:
		if accessibleName(n) == "" {
			a.report("button-name", "4.1.2 Name, Role, Value", "button has no discernible text", n)
		}

	case atom.Table:
		// explicitName rather than accessibleName: a table's contents are not
		// its name, and asking accessibleName would let any table with a cell
		// in it claim to be labelled.
		if !hasChildOfType(n, atom.Caption) && explicitName(n) == "" {
			a.report("table-caption", "1.3.1 Info and Relationships",
				"<table> has neither a <caption> nor an accessible name", n)
		}

	case atom.Th:
		// Without scope, a reader cannot tell whether a header governs its
		// column or its row, and cell announcements lose their heading.
		if attr(n, "scope") == "" {
			a.report("th-scope", "1.3.1 Info and Relationships", "<th> has no scope", n)
		}

	case atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6:
		a.headings = append(a.headings, int(n.Data[1]-'0'))
	}
}

// isLabelled covers every way a form control can legitimately get its name.
func (a *auditor) isLabelled(n *html.Node) bool {
	if attr(n, "aria-label") != "" || attr(n, "aria-labelledby") != "" {
		return true
	}
	if id := attr(n, "id"); id != "" && a.labelFor[id] {
		return true
	}
	// A wrapping <label> associates implicitly.
	for p := n.Parent; p != nil; p = p.Parent {
		if p.DataAtom == atom.Label {
			return true
		}
	}
	return false
}

func (a *auditor) checkDocument(root *html.Node) {
	htmlEl := findFirst(root, atom.Html)
	if htmlEl == nil {
		return
	}
	// 3.1.1 is the criterion a screen reader depends on to pick a voice. Without
	// it an Arabic page is read by an English synthesiser.
	if strings.TrimSpace(attr(htmlEl, "lang")) == "" {
		a.report("html-lang", "3.1.1 Language of Page", "<html> has no lang attribute", htmlEl)
	}
	if d := attr(htmlEl, "dir"); d != "" && d != "ltr" && d != "rtl" && d != "auto" {
		a.report("html-dir", "1.3.2 Meaningful Sequence",
			fmt.Sprintf("<html dir=%q> is not ltr, rtl or auto", d), htmlEl)
	}

	title := findFirst(root, atom.Title)
	if title == nil || strings.TrimSpace(textOf(title)) == "" {
		a.report("document-title", "2.4.2 Page Titled", "the document has no non-empty <title>", nil)
	}
}

func (a *auditor) checkDuplicateIDs() {
	var dup []string
	for id, n := range a.ids {
		if n > 1 {
			dup = append(dup, fmt.Sprintf("%s (%d times)", id, n))
		}
	}
	sort.Strings(dup)
	for _, d := range dup {
		// Duplicates break every aria reference to that id, silently: the
		// browser resolves the first and the rest point at the wrong thing.
		a.report("duplicate-id", "4.1.1 Parsing", "id is not unique: "+d, nil)
	}
}

func (a *auditor) checkHeadingOrder() {
	h1s := 0
	prev := 0
	for _, level := range a.headings {
		if level == 1 {
			h1s++
		}
		if prev != 0 && level > prev+1 {
			a.report("heading-order", "1.3.1 Info and Relationships",
				fmt.Sprintf("heading level jumps from h%d to h%d, so a reader navigating by heading loses the outline", prev, level), nil)
		}
		prev = level
	}
	if h1s == 0 && len(a.headings) > 0 {
		a.report("heading-h1", "1.3.1 Info and Relationships", "the document has headings but no <h1>", nil)
	}
	if h1s > 1 {
		a.report("heading-h1", "1.3.1 Info and Relationships",
			fmt.Sprintf("the document has %d <h1> elements; a reader cannot tell which names the page", h1s), nil)
	}
}

// announcedAttrs are the attributes whose values a reader or a screen reader
// actually receives.
//
// Deliberately not value, name, id, class, href or data-*: those carry machine
// identifiers, and a checkbox whose value is "metric.errorRate" is correct — it
// is naming a metric to the server, not showing a string to anyone.
var announcedAttrs = []string{"aria-label", "aria-placeholder", "alt", "title", "placeholder"}

// Leak is a term id that reached the reader instead of being resolved to text.
type Leak struct {
	ID    string
	Where string // "text" or the attribute it appeared in
	HTML  string
}

func (l Leak) String() string {
	return fmt.Sprintf("%s (in %s) %s", l.ID, l.Where, l.HTML)
}

// TermIDLeaks finds unresolved term ids in a rendered document.
//
// The i18n resolver returns the id itself when a term is missing anywhere, which
// is a deliberate choice — literal text in a config file simply works — but it
// means a typo is rendered rather than reported. "flt.search" is not a label,
// and nothing else in the system will ever tell you.
//
// Only text nodes and the attributes that become announcements are examined, and
// script and style contents are skipped.
func TermIDLeaks(doc string, prefixes []string) ([]Leak, error) {
	root, err := html.Parse(strings.NewReader(doc))
	if err != nil {
		return nil, fmt.Errorf("a11y: parsing document: %w", err)
	}

	var out []Leak
	seen := map[string]bool{}
	add := func(id, where string, n *html.Node) {
		key := id + "\x00" + where
		if seen[key] {
			return
		}
		seen[key] = true
		l := Leak{ID: id, Where: where}
		if n != nil {
			l.HTML = outline(n)
		}
		out = append(out, l)
	}

	var visit func(*html.Node)
	visit = func(n *html.Node) {
		switch {
		case n.Type == html.ElementNode && (n.DataAtom == atom.Script || n.DataAtom == atom.Style):
			return
		case n.Type == html.TextNode:
			for _, id := range termIDsIn(n.Data, prefixes) {
				add(id, "text", n.Parent)
			}
		case n.Type == html.ElementNode:
			for _, key := range announcedAttrs {
				v := attr(n, key)
				if v == "" {
					continue
				}
				for _, id := range termIDsIn(v, prefixes) {
					add(id, key, n)
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			visit(c)
		}
	}
	visit(root)
	return out, nil
}

// termIDsIn reports dotted identifiers in s that begin with one of the given
// namespace prefixes and stand as whole words.
//
// Namespaces nest — "inc." is a prefix of the tail of "dr.inc.none" — so a match
// starting inside an earlier one is dropped. Without that, one leak is reported
// as two and the longer, more useful id is the one that looks redundant.
func termIDsIn(s string, prefixes []string) []string {
	type match struct {
		start, end int
		id         string
	}
	var candidates []match

	for _, prefix := range prefixes {
		from := 0
		for {
			i := strings.Index(s[from:], prefix)
			if i < 0 {
				break
			}
			at := from + i
			from = at + len(prefix)

			// Must start a word, or "subcat.identity" would match "cat.".
			if at > 0 && isIdentByte(s[at-1]) {
				continue
			}
			end := from
			for end < len(s) && (isIdentByte(s[end]) || s[end] == '.') {
				end++
			}
			id := strings.TrimRight(s[at:end], ".")
			// A bare prefix is a word, not an id: "dr." alone is not "dr.close".
			if len(id) <= len(prefix) {
				continue
			}
			candidates = append(candidates, match{at, at + len(id), id})
		}
	}

	// Earliest start wins, and on a tie the longer span, so "dr.inc.none"
	// claims the run and the "inc.none" inside it is not reported as a second
	// leak. Sorting by prefix length instead would let a longer prefix that
	// happens to sit further in — "inc." inside "dr.inc.none" — win the span.
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].start != candidates[j].start {
			return candidates[i].start < candidates[j].start
		}
		return candidates[i].end > candidates[j].end
	})

	var out []string
	claimedTo := -1
	for _, c := range candidates {
		if c.start < claimedTo {
			continue
		}
		out = append(out, c.id)
		claimedTo = c.end
	}
	return out
}

func isIdentByte(b byte) bool {
	return b == '-' || b == '_' ||
		(b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

// --- node helpers ----------------------------------------------------------

func attr(n *html.Node, name string) string {
	for _, a := range n.Attr {
		if a.Key == name {
			return a.Val
		}
	}
	return ""
}

func hasAttr(n *html.Node, name string) bool {
	for _, a := range n.Attr {
		if a.Key == name {
			return true
		}
	}
	return false
}

// accessibleName is a deliberate approximation: aria-label, aria-labelledby,
// title, alt on a descendant image, or the element's own text with
// aria-hidden subtrees removed.
//
// It is not the full accessible-name computation, which needs CSS and the
// browser's own tree. It is enough to catch the failure that matters — a control
// with nothing to announce — and the browser layer checks the rest.
func accessibleName(n *html.Node) string {
	if v := strings.TrimSpace(attr(n, "aria-label")); v != "" {
		return v
	}
	if v := strings.TrimSpace(attr(n, "aria-labelledby")); v != "" {
		return v
	}
	if v := strings.TrimSpace(visibleText(n)); v != "" {
		return v
	}
	if v := strings.TrimSpace(attr(n, "title")); v != "" {
		return v
	}
	// An icon-only control is often an image with a label of its own.
	var found string
	var visit func(*html.Node)
	visit = func(c *html.Node) {
		if found != "" {
			return
		}
		if c.Type == html.ElementNode {
			if c.DataAtom == atom.Img {
				if v := strings.TrimSpace(attr(c, "alt")); v != "" {
					found = v
					return
				}
			}
			if v := strings.TrimSpace(attr(c, "aria-label")); v != "" {
				found = v
				return
			}
		}
		for k := c.FirstChild; k != nil; k = k.NextSibling {
			visit(k)
		}
	}
	visit(n)
	return found
}

// explicitName is a name given deliberately, excluding the element's own
// contents. Used where contents are the data rather than the label — a table,
// a region — and would otherwise mask a missing name.
func explicitName(n *html.Node) string {
	for _, k := range []string{"aria-label", "aria-labelledby", "title"} {
		if v := strings.TrimSpace(attr(n, k)); v != "" {
			return v
		}
	}
	return ""
}

// visibleText is the element's text with aria-hidden subtrees skipped, which is
// what a screen reader would read.
func visibleText(n *html.Node) string {
	var b strings.Builder
	var visit func(*html.Node)
	visit = func(c *html.Node) {
		if c.Type == html.ElementNode && attr(c, "aria-hidden") == "true" {
			return
		}
		if c.Type == html.TextNode {
			b.WriteString(c.Data)
		}
		for k := c.FirstChild; k != nil; k = k.NextSibling {
			visit(k)
		}
	}
	visit(n)
	return strings.Join(strings.Fields(b.String()), " ")
}

func textOf(n *html.Node) string {
	var b strings.Builder
	var visit func(*html.Node)
	visit = func(c *html.Node) {
		if c.Type == html.TextNode {
			b.WriteString(c.Data)
		}
		for k := c.FirstChild; k != nil; k = k.NextSibling {
			visit(k)
		}
	}
	visit(n)
	return b.String()
}

func findFirst(n *html.Node, a atom.Atom) *html.Node {
	if n.Type == html.ElementNode && n.DataAtom == a {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if got := findFirst(c, a); got != nil {
			return got
		}
	}
	return nil
}

func hasChildOfType(n *html.Node, a atom.Atom) bool {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && c.DataAtom == a {
			return true
		}
	}
	return false
}

// outline renders the element's opening tag, so a failure identifies the
// element without dumping its whole subtree.
func outline(n *html.Node) string {
	var b strings.Builder
	b.WriteString("<")
	b.WriteString(n.Data)
	for _, a := range n.Attr {
		b.WriteString(" ")
		b.WriteString(a.Key)
		if a.Val != "" {
			v := a.Val
			if len(v) > 60 {
				v = v[:57] + "..."
			}
			b.WriteString(`="`)
			b.WriteString(v)
			b.WriteString(`"`)
		}
	}
	b.WriteString(">")
	s := b.String()
	if len(s) > 200 {
		s = s[:197] + "..."
	}
	return s
}
