package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Bundle is a set of configuration files keyed by base name. Keeping the bundle
// in memory rather than reading from disk is what makes Parse a pure function,
// and therefore what makes the whole validation surface testable without a
// filesystem.
type Bundle map[string][]byte

// Well-known file names in a bundle.
const (
	FileApp    = "app.yaml"
	FileBrand  = "brand.yaml"
	FileDomain = "domain.yaml"
	FileTheme  = "theme.yaml"
	FileIcons  = "icons.yaml"
)

// requiredFiles must all be present for a bundle to parse.
var requiredFiles = []string{FileApp, FileBrand, FileDomain, FileTheme, FileIcons}

// Statuses is the fixed status vocabulary. Config controls each status's label,
// icon, severity and thresholds, but not the set: the evaluation rules in
// package rules are structurally tied to these five outcomes.
var Statuses = []string{"operational", "partial", "major", "unknown", "maintenance"}

// Parse decodes and validates a whole bundle.
//
// Decoding is strict: unknown fields are errors, because a silently ignored
// `runtim:` leaves a deployment wondering why its setting had no effect.
// Validation continues past the first failure and reports everything it finds.
func Parse(b Bundle) (Config, error) {
	var cfg Config
	var errs Errors
	nodes := make(map[string]*yaml.Node, len(requiredFiles))

	targets := []struct {
		name string
		into any
	}{
		{FileApp, &cfg.App},
		{FileBrand, &cfg.Brand},
		{FileDomain, &cfg.Domain},
		{FileTheme, &cfg.Theme},
		{FileIcons, &cfg.Icons},
	}

	for _, t := range targets {
		raw, ok := b[t.name]
		if !ok {
			errs = append(errs, Error{File: t.name, Msg: "missing from the configuration bundle"})
			continue
		}
		if e := decodeStrict(t.name, raw, t.into); e != nil {
			errs = append(errs, *e)
			continue
		}
		var n yaml.Node
		if err := yaml.Unmarshal(raw, &n); err == nil {
			nodes[t.name] = &n
		}
	}

	// Structural failures are reported alone: validating a half-decoded config
	// would bury the real problem under a cascade of consequences.
	if len(errs) > 0 {
		return Config{}, errs
	}

	if err := validate(cfg, nodes).Err(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// decodeStrict decodes one file, rejecting unknown fields.
func decodeStrict(name string, raw []byte, into any) *Error {
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)

	err := dec.Decode(into)
	switch {
	case errors.Is(err, io.EOF):
		return &Error{File: name, Msg: "file is empty"}
	case err != nil:
		return DecodeError(name, err)
	}
	return nil
}

// DecodeError converts a yaml decode failure into a located Error.
//
// yaml.v3 reports type errors as free-form strings prefixed with "line N:", so
// the line is recovered from the text rather than from a structured field.
//
// It is exported because every package that decodes one of these files needs
// the same treatment, and a second copy of this parsing would drift.
func DecodeError(name string, err error) *Error {
	var typeErr *yaml.TypeError
	if errors.As(err, &typeErr) && len(typeErr.Errors) > 0 {
		line, msg := splitYAMLLinePrefix(typeErr.Errors[0])
		if len(typeErr.Errors) > 1 {
			msg = strings.Join(append([]string{msg}, typeErr.Errors[1:]...), "; ")
		}
		return &Error{File: name, Line: line, Msg: msg}
	}
	line, msg := splitYAMLLinePrefix(err.Error())
	return &Error{File: name, Line: line, Msg: msg}
}

// splitYAMLLinePrefix pulls the leading "line 12: " off a yaml.v3 message.
func splitYAMLLinePrefix(s string) (int, string) {
	const prefix = "line "
	rest, ok := strings.CutPrefix(s, prefix)
	if !ok {
		// Some messages are "yaml: line 12: ..." instead.
		if trimmed, ok2 := strings.CutPrefix(s, "yaml: "+prefix); ok2 {
			rest = trimmed
		} else {
			return 0, s
		}
	}
	numStr, msg, ok := strings.Cut(rest, ":")
	if !ok {
		return 0, s
	}
	n, err := strconv.Atoi(strings.TrimSpace(numStr))
	if err != nil {
		return 0, s
	}
	return n, strings.TrimSpace(msg)
}

// scanState tracks where in a YAML line the expander currently is, so that
// substitution respects the document's structure.
type scanState int

const (
	scanPlain  scanState = iota // a plain scalar, a key, or whitespace
	scanSingle                  // inside '...'
	scanDouble                  // inside "..."
	scanComment
)

// ExpandEnv substitutes ${VAR} and ${VAR:-default} references in a config file.
//
// An unset variable with no default is an error rather than an empty string: a
// config that parses but points at nothing fails later, further from the cause,
// and usually in production.
//
// `$$` escapes to a literal `$`, and a `$` not followed by `{` is left alone so
// that prices, regexes and shell snippets survive unharmed.
//
// Substitution respects YAML structure rather than treating the file as flat
// text:
//
//   - Comments are never expanded. Documenting a setting as "supply this with
//     ${DATABASE_URL}" is the natural thing to write, and it must not be
//     mistaken for a reference that has to resolve.
//   - Single-quoted scalars are left alone, matching YAML's own rule that they
//     are literal.
//   - Inside a double-quoted scalar, the substituted value is escaped, so a
//     password containing a quote cannot terminate the string and inject
//     structure into the document.
//   - A substituted value may not contain a newline, in any context, since
//     nothing legitimate needs one and it is the one character that could add
//     keys to the document.
func ExpandEnv(src []byte, lookup func(string) (string, bool)) ([]byte, error) {
	var out bytes.Buffer
	out.Grow(len(src))

	state := scanPlain
	var prev byte = '\n'

	for i := 0; i < len(src); {
		c := src[i]

		if c == '\n' {
			// Quoted scalars may span lines in YAML, but resetting here keeps a
			// single unbalanced quote from swallowing the rest of the file.
			state = scanPlain
			out.WriteByte(c)
			prev = c
			i++
			continue
		}

		switch state {
		case scanComment:
			out.WriteByte(c)
			prev = c
			i++
			continue

		case scanSingle:
			out.WriteByte(c)
			if c == '\'' {
				// '' is an escaped quote, not the end of the scalar.
				if i+1 < len(src) && src[i+1] == '\'' {
					out.WriteByte('\'')
					i += 2
					prev = '\''
					continue
				}
				state = scanPlain
			}
			prev = c
			i++
			continue

		case scanDouble:
			if c == '\\' && i+1 < len(src) {
				out.WriteByte(c)
				out.WriteByte(src[i+1])
				prev = src[i+1]
				i += 2
				continue
			}
			if c == '"' {
				state = scanPlain
				out.WriteByte(c)
				prev = c
				i++
				continue
			}

		case scanPlain:
			// YAML starts a comment at a # that begins a line or follows space.
			if c == '#' && (prev == '\n' || prev == ' ' || prev == '\t') {
				state = scanComment
				out.WriteByte(c)
				prev = c
				i++
				continue
			}
			if c == '\'' {
				state = scanSingle
				out.WriteByte(c)
				prev = c
				i++
				continue
			}
			if c == '"' {
				state = scanDouble
				out.WriteByte(c)
				prev = c
				i++
				continue
			}
		}

		if c != '$' {
			out.WriteByte(c)
			prev = c
			i++
			continue
		}
		if i+1 < len(src) && src[i+1] == '$' {
			out.WriteByte('$')
			prev = '$'
			i += 2
			continue
		}
		if i+1 >= len(src) || src[i+1] != '{' {
			out.WriteByte('$')
			prev = '$'
			i++
			continue
		}

		// Bound the search to this line: a stray "${" should not reach forward
		// and swallow an unrelated brace pages later.
		rest := src[i+2:]
		if nl := bytes.IndexByte(rest, '\n'); nl >= 0 {
			rest = rest[:nl]
		}
		end := bytes.IndexByte(rest, '}')
		if end < 0 {
			return nil, fmt.Errorf("unterminated ${ reference at byte %d", i)
		}
		ref := string(rest[:end])
		i += 2 + end + 1

		val, err := resolveRef(ref, lookup)
		if err != nil {
			return nil, err
		}
		if strings.ContainsAny(val, "\n\r") {
			return nil, fmt.Errorf("the value substituted for ${%s} contains a newline, which would change the structure of the document", ref)
		}
		if state == scanDouble {
			val = strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(val)
		}
		out.WriteString(val)
		prev = '}'
	}
	return out.Bytes(), nil
}

// resolveRef looks up NAME or NAME:-default.
func resolveRef(ref string, lookup func(string) (string, bool)) (string, error) {
	name, def, hasDefault := strings.Cut(ref, ":-")
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("empty variable name in ${%s}", ref)
	}

	if val, ok := lookup(name); ok {
		return val, nil
	}
	if hasDefault {
		return def, nil
	}
	return "", fmt.Errorf("environment variable %q is not set and has no default; write ${%s:-fallback} to supply one", name, name)
}
