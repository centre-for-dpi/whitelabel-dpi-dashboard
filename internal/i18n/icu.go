package i18n

import (
	"strconv"
	"strings"
)

// Message is a parsed ICU message.
//
// Only the subset the dashboard actually needs is supported — interpolation,
// plural and select — because that is the subset a translator can be expected
// to write correctly without tooling. The parser is small enough to read, which
// matters: it is the piece that decides whether an untrusted-ish string from a
// locale file can do anything surprising, and the answer needs to be obviously
// no.
//
//	{name}                            interpolate
//	{n, plural, one{# day} other{# days}}   choose by number, # is the number
//	{v, select, up{rose} other{fell}} choose by value
//
// Anything unrecognised is emitted literally, so a malformed message renders as
// itself rather than vanishing or panicking. A missing translation is a
// cosmetic problem; a blank page is not.
type Message struct {
	parts []part
}

type part struct {
	// literal is emitted as-is when kind is partLiteral.
	literal string
	kind    partKind
	arg     string
	options map[string]Message
	// offset subtracts from the number before substituting #, for the "one
	// more" phrasings some languages need. Unused today, reserved by ICU.
	offset float64
}

type partKind int

const (
	partLiteral partKind = iota
	partArg
	partPlural
	partSelect
)

// Parse compiles a message. It never fails: anything it cannot understand
// becomes a literal.
func Parse(src string) Message {
	return parseMessage(src)
}

// parseMessage reads a whole message.
func parseMessage(src string) Message {
	var (
		msg Message
		lit strings.Builder
		i   int
	)
	flush := func() {
		if lit.Len() > 0 {
			msg.parts = append(msg.parts, part{literal: lit.String()})
			lit.Reset()
		}
	}

	for i < len(src) {
		switch src[i] {
		case '}':
			// Nested groups are consumed whole by readBraced, so any closing
			// brace reaching here is unmatched and therefore literal text.
			lit.WriteByte('}')
			i++

		case '\'':
			// ICU's escape: '' is a literal quote, and '{' protects a brace.
			if i+1 < len(src) && src[i+1] == '\'' {
				lit.WriteByte('\'')
				i += 2
				continue
			}
			if end := strings.IndexByte(src[i+1:], '\''); end >= 0 && i+1 < len(src) {
				lit.WriteString(src[i+1 : i+1+end])
				i += end + 2
				continue
			}
			lit.WriteByte('\'')
			i++

		case '{':
			body, next, ok := readBraced(src, i)
			if !ok {
				// Unterminated: the rest is literal rather than lost.
				lit.WriteString(src[i:])
				i = len(src)
				continue
			}
			flush()
			msg.parts = append(msg.parts, parseArg(body))
			i = next

		default:
			lit.WriteByte(src[i])
			i++
		}
	}
	flush()
	return msg
}

// readBraced returns the contents of the braced group starting at i.
func readBraced(src string, i int) (body string, next int, ok bool) {
	depth := 0
	for j := i; j < len(src); j++ {
		switch src[j] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return src[i+1 : j], j + 1, true
			}
		}
	}
	return "", 0, false
}

// parseArg turns the inside of a braced group into a part.
func parseArg(body string) part {
	name, rest, hasType := strings.Cut(body, ",")
	name = strings.TrimSpace(name)

	if !hasType {
		return part{kind: partArg, arg: name}
	}

	kind, options, _ := strings.Cut(rest, ",")
	switch strings.TrimSpace(kind) {
	case "plural":
		p := part{kind: partPlural, arg: name, options: map[string]Message{}}
		p.offset, options = readOffset(options)
		p.options = parseOptions(options)
		return p
	case "select":
		return part{kind: partSelect, arg: name, options: parseOptions(options)}
	default:
		// An unrecognised type is treated as plain interpolation, which is the
		// least surprising thing a translator could have meant.
		return part{kind: partArg, arg: name}
	}
}

// readOffset consumes an "offset:N" prefix if present.
func readOffset(src string) (float64, string) {
	trimmed := strings.TrimSpace(src)
	rest, ok := strings.CutPrefix(trimmed, "offset:")
	if !ok {
		return 0, src
	}
	numStr, tail, _ := strings.Cut(strings.TrimSpace(rest), " ")
	n, err := strconv.ParseFloat(numStr, 64)
	if err != nil {
		return 0, src
	}
	return n, tail
}

// parseOptions reads `key{message} key{message}` pairs.
func parseOptions(src string) map[string]Message {
	out := map[string]Message{}

	i := 0
	for i < len(src) {
		// Skip to the next key.
		for i < len(src) && (src[i] == ' ' || src[i] == '\n' || src[i] == '\t') {
			i++
		}
		start := i
		for i < len(src) && src[i] != '{' {
			i++
		}
		if i >= len(src) {
			break
		}
		key := strings.TrimSpace(src[start:i])

		// coverage:ignore -- readBraced cannot fail here: the body parseOptions
		// receives was itself produced by a successful readBraced, so its braces
		// are balanced by construction. The guard stays because relying on that
		// invariant silently would be worse than one unreachable line.
		body, next, ok := readBraced(src, i)
		if !ok {
			break
		}
		out[key] = parseMessage(body)
		i = next
	}
	return out
}
