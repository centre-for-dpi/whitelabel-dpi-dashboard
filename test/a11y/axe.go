package a11y

import (
	_ "embed"
	"fmt"
	"strings"
)

// axeSource is axe-core, vendored so the suite has no network dependency and so
// the version that produced a conformance claim is the version in the repository.
// See axe-core.LICENSE for its terms and axe-core.VERSION for what is pinned.
//
//go:embed axe-core/axe.min.js
var axeSource string

// AxeTags are the rule sets a WCAG 2.2 AA claim rests on.
//
// wcag22aa is the reason for pinning a recent axe: it carries the 2.2 additions
// that can be checked automatically at all — target size, and the focus rules
// that are decidable without a human. The criteria 2.2 added that cannot be
// automated (2.4.11 Focus Not Obscured, 3.3.7 Redundant Entry) are asserted by
// hand in matrix_test.go instead of being quietly counted as passes.
var AxeTags = []string{"wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa"}

// Violation is one rule axe found broken.
type Violation struct {
	ID      string   `json:"id"`
	Impact  string   `json:"impact"`
	Help    string   `json:"help"`
	Tags    []string `json:"tags"`
	Nodes   []Node   `json:"nodes"`
	HelpURL string   `json:"helpUrl"`
}

// Node is one element a rule failed on.
type Node struct {
	Target  []string `json:"target"`
	Summary string   `json:"summary"`
	HTML    string   `json:"html"`
}

// Result is one axe run.
type Result struct {
	Violations []Violation `json:"violations"`
	Incomplete []Violation `json:"incomplete"`
	Passes     int         `json:"passes"`
	Engine     string      `json:"engine"`
}

// Axe injects axe-core and runs it over the whole document.
func (p *Page) Axe() (*Result, error) {
	if _, err := p.Eval(axeSource); err != nil {
		return nil, fmt.Errorf("injecting axe-core: %w", err)
	}
	script := fmt.Sprintf(`axe.run(document, {
		runOnly: {type: "tag", values: %s},
		resultTypes: ["violations", "incomplete"]
	}).then(r => ({
		violations: r.violations.map(summarise),
		incomplete: r.incomplete.map(summarise),
		passes: r.passes ? r.passes.length : 0,
		engine: r.testEngine.version
	}));
	function summarise(v) {
		return {
			id: v.id, impact: v.impact, help: v.help, helpUrl: v.helpUrl,
			tags: v.tags.filter(t => /^wcag/.test(t)),
			nodes: v.nodes.slice(0, 8).map(n => ({
				target: n.target.map(String),
				summary: (n.failureSummary || "").split("\n").filter(Boolean).join(" | "),
				html: n.html.slice(0, 240)
			}))
		};
	}`, jsString2(AxeTags))

	var out Result
	if err := p.EvalInto(script, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Report renders violations for a test failure message.
func (r *Result) Report() string {
	var b strings.Builder
	for _, v := range r.Violations {
		fmt.Fprintf(&b, "\n  [%s] %s — %s  {%s}\n", v.Impact, v.ID, v.Help, strings.Join(v.Tags, ","))
		for _, n := range v.Nodes {
			fmt.Fprintf(&b, "      at %s\n", strings.Join(n.Target, " "))
			if n.Summary != "" {
				fmt.Fprintf(&b, "         %s\n", n.Summary)
			}
			if n.HTML != "" {
				fmt.Fprintf(&b, "         %s\n", n.HTML)
			}
		}
		if v.HelpURL != "" {
			fmt.Fprintf(&b, "      %s\n", v.HelpURL)
		}
	}
	return b.String()
}

// IncompleteReport lists what axe could not decide. These are not failures — axe
// is honest about the cases it cannot resolve, usually a background it cannot
// compute — but a run where the incomplete list grows is worth a look, so the
// suite prints it rather than discarding it.
func (r *Result) IncompleteReport() string {
	var b strings.Builder
	for _, v := range r.Incomplete {
		fmt.Fprintf(&b, "  %s (%d nodes) — %s\n", v.ID, len(v.Nodes), v.Help)
	}
	return b.String()
}

func jsString2(v []string) string {
	quoted := make([]string, len(v))
	for i, s := range v {
		quoted[i] = jsString(s)
	}
	return "[" + strings.Join(quoted, ",") + "]"
}
