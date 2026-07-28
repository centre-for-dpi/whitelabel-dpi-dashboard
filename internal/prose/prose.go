// Package prose renders configurable body text that can emphasise words.
//
// The problem it solves: a dashboard's explanatory copy belongs to the
// deployment, and the sentences that explain what "downtime" and "outage" mean
// need particular words picked out — but a locale file is data, and data must
// never become markup. Handing config a `template.HTML` would mean any
// translator, or anyone who can edit a mounted config directory, can inject
// script into every page.
//
// So a locale string may use a closed set of inline tags, and this package parses
// them into a node tree the template renders as elements. Nothing config supplies
// ever reaches the browser as markup; the tags are instructions, not HTML.
//
// The approach it replaces is the prototype's: keep the sentence plain and name
// the word to highlight in a separate field, then find that word by substring
// match. That works in English and silently degrades everywhere else, because a
// translator cannot make "downtime" appear in a Hindi sentence and a match that
// fails emphasises nothing. Here the emphasis travels inside the sentence, so it
// lands on whichever word carries the meaning in that language.
package prose

import (
	"fmt"
	"strings"
)

// Kind is what a span of prose is.
type Kind string

const (
	// Plain is unmarked text.
	Plain Kind = "text"
	// Strong is strong importance: <strong>.
	Strong Kind = "strong"
	// Emphasis is stress emphasis: <em>.
	Emphasis Kind = "em"
	// Mark is a highlight, optionally in a status tone: <mark>.
	Mark Kind = "mark"
)

// Tags is the closed set a locale string may use, and the only one.
//
// Adding to this list is a deliberate act with a security consequence, which is
// why it is a variable in one place rather than a check spread across a parser.
var Tags = map[string]Kind{
	"strong": Strong,
	"em":     Emphasis,
	"mark":   Mark,
}

// Tones a mark may carry. These name the status palette, so a highlighted phrase
// can be tinted to match the verdict it is explaining without a stylesheet edit.
var Tones = []string{"ok", "partial", "major", "unknown", "maintenance", "accent", "neutral"}

// Span is one run of text with its treatment.
type Span struct {
	Kind Kind
	Text string
	// Tone is set only for Mark, and only to a member of Tones.
	Tone string
}

// Parse turns a configured string into spans.
//
// Unknown tags are an error rather than literal text. The alternative — render
// what you do not understand — is how "<script>" ends up on a page as a visible
// string in one release and as markup in the next, when someone widens the
// allowlist without revisiting the fallback.
func Parse(s string) ([]Span, error) {
	var spans []Span
	var text strings.Builder

	flush := func() {
		if text.Len() > 0 {
			spans = append(spans, Span{Kind: Plain, Text: text.String()})
			text.Reset()
		}
	}

	for i := 0; i < len(s); {
		if s[i] != '<' {
			text.WriteByte(s[i])
			i++
			continue
		}

		close := strings.IndexByte(s[i:], '>')
		if close < 0 {
			return nil, fmt.Errorf("unclosed %q at byte %d", "<", i)
		}
		raw := s[i+1 : i+close]

		if strings.HasPrefix(raw, "/") {
			return nil, fmt.Errorf("closing tag </%s> has no opening tag before it", strings.TrimPrefix(raw, "/"))
		}

		name, attrs := splitTag(raw)
		kind, ok := Tags[name]
		if !ok {
			return nil, fmt.Errorf("<%s> is not an allowed tag; use one of %s", name, allowedList())
		}

		// Find this tag's own closing tag. Nesting is not supported: emphasis
		// inside emphasis has no meaning here, and refusing it keeps the parser
		// small enough to be obviously correct.
		end := strings.Index(s[i+close+1:], "</"+name+">")
		if end < 0 {
			return nil, fmt.Errorf("<%s> is never closed", name)
		}
		inner := s[i+close+1 : i+close+1+end]
		if strings.ContainsRune(inner, '<') {
			return nil, fmt.Errorf("<%s> contains another tag; inline markup does not nest here", name)
		}

		tone, err := toneOf(name, attrs)
		if err != nil {
			return nil, err
		}

		flush()
		spans = append(spans, Span{Kind: kind, Text: inner, Tone: tone})
		i += close + 1 + end + len("</"+name+">")
	}
	flush()
	return spans, nil
}

// splitTag separates a tag name from its attribute text.
func splitTag(raw string) (name, attrs string) {
	raw = strings.TrimSpace(raw)
	if i := strings.IndexAny(raw, " \t"); i >= 0 {
		return strings.ToLower(raw[:i]), strings.TrimSpace(raw[i+1:])
	}
	return strings.ToLower(raw), ""
}

// toneOf reads the one attribute this vocabulary allows.
func toneOf(name, attrs string) (string, error) {
	if attrs == "" {
		return "", nil
	}
	if name != "mark" {
		return "", fmt.Errorf("<%s> takes no attributes", name)
	}
	value, ok := strings.CutPrefix(attrs, "tone=")
	if !ok {
		return "", fmt.Errorf(`<mark> takes only tone="…", not %q`, attrs)
	}
	tone := strings.Trim(value, `"'`)
	for _, allowed := range Tones {
		if tone == allowed {
			return tone, nil
		}
	}
	return "", fmt.Errorf("tone %q is not one of %s", tone, strings.Join(Tones, ", "))
}

func allowedList() string {
	names := make([]string, 0, len(Tags))
	for name := range Tags {
		names = append(names, "<"+name+">")
	}
	// Sorted so an error message is stable.
	for i := range names {
		for j := i + 1; j < len(names); j++ {
			if names[j] < names[i] {
				names[i], names[j] = names[j], names[i]
			}
		}
	}
	return strings.Join(names, ", ")
}

// Text is the prose with all markup removed, for a title attribute or a place
// that can hold only a string.
func Text(spans []Span) string {
	var b strings.Builder
	for _, s := range spans {
		b.WriteString(s.Text)
	}
	return b.String()
}

// Validate reports whether a configured string parses, for use at startup.
func Validate(s string) error {
	_, err := Parse(s)
	return err
}
