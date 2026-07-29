package widget

import "net/url"

// The names the reader's selections travel under.
//
// They live beside State rather than in the server because State is what they
// name: State round-trips through the URL, and these are the spellings it round
// trips as. A builder assembling a link needs them as much as a handler reading
// one, and a link built without them is a link that forgets what the reader
// chose — which is how the drawer came to open in English on a French page.
const (
	ParamScope   = "scope"
	ParamRole    = "role"
	ParamID      = "id"
	ParamSignals = "signals"
	ParamRegion  = "region"
	ParamPeriod  = "period"
	ParamSearch  = "q"
	ParamStatus  = "status"
	ParamCat     = "cat"
	ParamSort    = "sort"
	ParamDir     = "dir"
	ParamLang    = "lang"
	ParamFilters = "filters"
	ParamTheme   = "theme"
	ParamTab     = "tab"
)

// Params is every parameter the dashboard reads, and the order anything derived
// from it is emitted in. Iterating this rather than the map is what keeps hidden
// fields in a stable order from one render to the next.
var Params = []string{
	ParamScope, ParamRole, ParamID, ParamSignals, ParamRegion, ParamPeriod,
	ParamSearch, ParamStatus, ParamCat, ParamSort, ParamDir, ParamLang,
	ParamFilters, ParamTheme, ParamTab,
}

// Link builds an in-app URL carrying the reader's state.
//
// Empty parameters are dropped, so a shared link says only what the sender
// actually chose. A URL carrying every default communicates nothing.
func Link(path string, params url.Values) string {
	trimmed := url.Values{}
	for k, vs := range params {
		for _, v := range vs {
			if v != "" {
				trimmed.Add(k, v)
			}
		}
	}
	if len(trimmed) == 0 {
		return path
	}
	return path + "?" + trimmed.Encode()
}

// Hidden is one state-carrying field a form must submit for the reader not to
// lose a selection the form has no control for.
type Hidden struct {
	Name  string
	Value string
}

// Href builds an in-app URL that carries everything the reader has selected,
// with the given key/value pairs overridden. A pair whose value is empty removes
// that parameter, which is how a control that replaces a selection — a signal
// card standing in for the filters it describes — says so.
func (c Context) Href(path string, pairs ...string) string {
	return Link(path, c.ParamsWith(pairs...))
}

// ParamsWith is Href's query, for a caller that needs to look at it rather than
// render it.
func (c Context) ParamsWith(pairs ...string) url.Values {
	out := url.Values{}
	for k, vs := range c.Params {
		out[k] = append([]string(nil), vs...)
	}
	for i := 0; i+1 < len(pairs); i += 2 {
		if pairs[i+1] == "" {
			out.Del(pairs[i])
			continue
		}
		out.Set(pairs[i], pairs[i+1])
	}
	return out
}

// HiddenExcept is the reader's state as form fields, minus the names the calling
// form renders as real controls.
//
// The exclusions are not decoration. A hidden input and a <select> of the same
// name both submit, and the first one wins — so a form carrying its own language
// selector as a hidden field as well would pin the language to whatever it was
// when the page was rendered.
func (c Context) HiddenExcept(names ...string) []Hidden {
	skip := make(map[string]bool, len(names))
	for _, n := range names {
		skip[n] = true
	}
	var out []Hidden
	for _, name := range Params {
		if skip[name] {
			continue
		}
		for _, v := range c.Params[name] {
			if v != "" {
				out = append(out, Hidden{Name: name, Value: v})
			}
		}
	}
	return out
}
