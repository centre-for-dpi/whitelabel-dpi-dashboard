package prose

import (
	"strings"
	"testing"
)

func TestParsePlainText(t *testing.T) {
	spans, err := Parse("Downtime means the service cannot be reached.")
	if err != nil {
		t.Fatal(err)
	}
	if len(spans) != 1 || spans[0].Kind != Plain {
		t.Fatalf("got %+v, want one plain span", spans)
	}
	if spans[0].Text != "Downtime means the service cannot be reached." {
		t.Errorf("text was altered: %q", spans[0].Text)
	}
}

func TestParseEmphasis(t *testing.T) {
	spans, err := Parse(`<strong>Downtime</strong> means the service cannot be reached at all.`)
	if err != nil {
		t.Fatal(err)
	}
	if len(spans) != 2 {
		t.Fatalf("got %d spans, want 2: %+v", len(spans), spans)
	}
	if spans[0].Kind != Strong || spans[0].Text != "Downtime" {
		t.Errorf("first span is %+v, want strong \"Downtime\"", spans[0])
	}
	if spans[1].Kind != Plain || !strings.HasPrefix(spans[1].Text, " means") {
		t.Errorf("second span is %+v, want the rest as plain text", spans[1])
	}
}

// The emphasis travels inside the sentence, which is the whole point: a
// translator puts it on whichever word carries the meaning in their language,
// rather than on whichever word happens to match an English string.
func TestEmphasisCanSitAnywhereInTheSentence(t *testing.T) {
	for _, in := range []string{
		`<mark tone="major">Downtime</mark> means unreachable.`,
		`A service is <mark tone="major">down</mark> when nobody can reach it.`,
		`Nobody can reach it: it is <mark tone="major">down</mark>`,
	} {
		spans, err := Parse(in)
		if err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		var marks int
		for _, s := range spans {
			if s.Kind == Mark {
				marks++
				if s.Tone != "major" {
					t.Errorf("%q: tone is %q, want major", in, s.Tone)
				}
			}
		}
		if marks != 1 {
			t.Errorf("%q: found %d marks, want 1", in, marks)
		}
	}
}

func TestParseEveryAllowedTag(t *testing.T) {
	for tag, want := range Tags {
		spans, err := Parse("<" + tag + ">x</" + tag + ">")
		if err != nil {
			t.Fatalf("<%s>: %v", tag, err)
		}
		if len(spans) != 1 || spans[0].Kind != want {
			t.Errorf("<%s> produced %+v, want kind %q", tag, spans, want)
		}
	}
}

func TestParseEveryAllowedTone(t *testing.T) {
	for _, tone := range Tones {
		spans, err := Parse(`<mark tone="` + tone + `">x</mark>`)
		if err != nil {
			t.Fatalf("tone %q: %v", tone, err)
		}
		if spans[0].Tone != tone {
			t.Errorf("tone %q became %q", tone, spans[0].Tone)
		}
	}
	// Single quotes and no quotes are both tolerated: a YAML file makes quoting
	// awkward enough already.
	for _, form := range []string{`tone=ok`, `tone='ok'`, `tone="ok"`} {
		spans, err := Parse(`<mark ` + form + `>x</mark>`)
		if err != nil {
			t.Fatalf("%q: %v", form, err)
		}
		if spans[0].Tone != "ok" {
			t.Errorf("%q gave tone %q", form, spans[0].Tone)
		}
	}
}

// The security property this package exists for. Every one of these must be an
// error, not text and certainly not markup.
func TestParseRefusesAnythingOutsideTheAllowlist(t *testing.T) {
	for _, in := range []string{
		`<script>alert(1)</script>`,
		`<img src=x onerror=alert(1)>`,
		`<a href="javascript:alert(1)">x</a>`,
		`<div>x</div>`,
		`<STRONG onclick="x">y</STRONG>`,            // an attribute strong does not take
		`<mark tone="javascript:alert(1)">x</mark>`, // a tone that is not a tone
		`<mark class="x">y</mark>`,                  // an attribute that is not tone
		`<strong>unclosed`,                          // never closed
		`</strong>`,                                 // closing with no opening
		`<strong>a<em>b</em>c</strong>`,             // nesting
		`<strong`,                                   // unterminated tag
	} {
		if _, err := Parse(in); err == nil {
			t.Errorf("Parse(%q) succeeded; it must be refused", in)
		}
	}
}

// A refusal has to say what to do instead, because the person reading it is a
// translator or an integrator, not the author of this parser.
func TestErrorsNameTheAllowedVocabulary(t *testing.T) {
	_, err := Parse(`<div>x</div>`)
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{"<strong>", "<em>", "<mark>"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %s", err, want)
		}
	}

	_, err = Parse(`<mark tone="purple">x</mark>`)
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "ok") || !strings.Contains(err.Error(), "purple") {
		t.Errorf("error %q names neither the bad tone nor the allowed ones", err)
	}
}

func TestTextStripsMarkup(t *testing.T) {
	spans, err := Parse(`An <mark tone="major">outage</mark> means a queue somewhere.`)
	if err != nil {
		t.Fatal(err)
	}
	if got := Text(spans); got != "An outage means a queue somewhere." {
		t.Errorf("Text() = %q", got)
	}
}

func TestValidate(t *testing.T) {
	if err := Validate(`<strong>fine</strong>`); err != nil {
		t.Errorf("Validate rejected valid prose: %v", err)
	}
	if err := Validate(`<script>x</script>`); err == nil {
		t.Error("Validate accepted a script tag")
	}
}

func TestEmptyStringIsValidAndEmpty(t *testing.T) {
	spans, err := Parse("")
	if err != nil {
		t.Fatal(err)
	}
	if len(spans) != 0 {
		t.Errorf("Parse(\"\") = %+v, want no spans", spans)
	}
	if Text(nil) != "" {
		t.Error("Text(nil) is not empty")
	}
}

// ICU placeholders pass through untouched: prose is parsed after interpolation,
// so a brace here is just a character.
func TestBracesAreNotSpecial(t *testing.T) {
	spans, err := Parse("A total of {n} services.")
	if err != nil {
		t.Fatal(err)
	}
	if Text(spans) != "A total of {n} services." {
		t.Errorf("placeholders were altered: %q", Text(spans))
	}
}
