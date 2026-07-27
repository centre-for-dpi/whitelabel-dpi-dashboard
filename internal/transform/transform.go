// Package transform converts upstream values into what the dashboard stores.
//
// It exists because no two APIs agree. One reports availability as 0.9991,
// another as 99.91; one calls a category "IDENTITY", another "cat.identity";
// one gives a timestamp, another an age in seconds. Without this, integrating
// each of them would mean writing Go.
//
// Every transform is named in config and applied in order:
//
//	metrics.availability:
//	  - { fn: ratioToPercent }
//	categoryId:
//	  - { fn: enumMap, table: { IDENTITY: cat.identity } }
//
// The set is closed and small. A config file that could express arbitrary
// computation would be a program, and a program in a config file cannot be
// validated at startup — which is the property that makes every other part of
// this dashboard's configuration trustworthy.
package transform

import (
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Value is one upstream value passing through a chain.
//
// Present distinguishes "the upstream reported null" from "the upstream
// reported zero", which the availability rules depend on: absence means
// unknown, zero means a total outage.
type Value struct {
	Raw     any
	Present bool
}

// Absent is what an unresolved path yields.
func Absent() Value { return Value{} }

// Of wraps a resolved value.
func Of(raw any) Value { return Value{Raw: raw, Present: raw != nil} }

// Number reads the value as a float, reporting whether it is one.
func (v Value) Number() (float64, bool) {
	if !v.Present {
		return 0, false
	}
	switch n := v.Raw.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	case string:
		// APIs that send large integers as strings are common enough that
		// refusing them would be pedantry.
		f, err := strconv.ParseFloat(strings.TrimSpace(n), 64)
		return f, err == nil
	case bool:
		if n {
			return 1, true
		}
		return 0, true
	}
	return 0, false
}

// Text reads the value as a string.
func (v Value) Text() (string, bool) {
	if !v.Present {
		return "", false
	}
	switch s := v.Raw.(type) {
	case string:
		return s, true
	case float64:
		return strconv.FormatFloat(s, 'f', -1, 64), true
	case bool:
		return strconv.FormatBool(s), true
	}
	return "", false
}

// Bool reads the value as a boolean.
func (v Value) Bool() (bool, bool) {
	if !v.Present {
		return false, false
	}
	switch b := v.Raw.(type) {
	case bool:
		return b, true
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(b))
		return parsed, err == nil
	case float64:
		return b != 0, true
	}
	return false, false
}

// Time reads the value as an instant, accepting the formats APIs actually send.
func (v Value) Time() (time.Time, bool) {
	if !v.Present {
		return time.Time{}, false
	}
	switch t := v.Raw.(type) {
	case string:
		for _, layout := range []string{
			time.RFC3339Nano, time.RFC3339,
			"2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02",
		} {
			if parsed, err := time.Parse(layout, strings.TrimSpace(t)); err == nil {
				return parsed.UTC(), true
			}
		}
	case float64:
		// Seconds or milliseconds since the epoch. The threshold is the year
		// 2286 in seconds, past which a value can only sensibly be millis.
		if t > 1e11 {
			return time.UnixMilli(int64(t)).UTC(), true
		}
		return time.Unix(int64(t), 0).UTC(), true
	}
	return time.Time{}, false
}

// Spec is one transform as written in config.
type Spec struct {
	Fn string `yaml:"fn"`
	// Table is the lookup for enumMap.
	Table map[string]string `yaml:"table"`
	// By is the operand for arithmetic transforms.
	By float64 `yaml:"by"`
	// Value is the fallback for `default`.
	Value string `yaml:"value"`
}

// Names lists every transform, for validation and documentation.
func Names() []string {
	return []string{
		"ratioToPercent", "percentToRatio",
		"multiply", "divide", "round",
		"secondsToMillis", "millisToSeconds",
		"enumMap", "lowercase", "uppercase", "trim", "trimPrefix",
		"default", "negate", "clampPercent",
	}
}

// Validate checks a chain at startup, so a mistyped transform stops the binary
// rather than silently passing values through untouched.
func Validate(specs []Spec) []error {
	var errs []error
	for i, s := range specs {
		if !slices.Contains(Names(), s.Fn) {
			errs = append(errs, fmt.Errorf("step %d: unknown transform %q; expected one of %v", i, s.Fn, Names()))
			continue
		}
		switch s.Fn {
		case "enumMap":
			if len(s.Table) == 0 {
				errs = append(errs, fmt.Errorf("step %d: enumMap needs a table", i))
			}
		case "multiply", "divide":
			if s.By == 0 {
				errs = append(errs, fmt.Errorf("step %d: %s needs a non-zero `by`", i, s.Fn))
			}
		}
	}
	return errs
}

// Apply runs a chain over a value.
//
// An absent value passes through untouched except by `default`, which exists
// precisely to fill one. Transforming a value that was never reported would
// manufacture data, which is the one thing a status dashboard must not do.
func Apply(v Value, specs []Spec) Value {
	for _, s := range specs {
		if !v.Present && s.Fn != "default" {
			continue
		}
		v = apply(v, s)
	}
	return v
}

func apply(v Value, s Spec) Value {
	switch s.Fn {
	case "ratioToPercent":
		return numeric(v, func(n float64) float64 { return n * 100 })
	case "percentToRatio":
		return numeric(v, func(n float64) float64 { return n / 100 })
	case "multiply":
		return numeric(v, func(n float64) float64 { return n * s.By })
	case "divide":
		return numeric(v, func(n float64) float64 { return n / s.By })
	case "negate":
		return numeric(v, func(n float64) float64 { return -n })
	case "round":
		places := math.Pow(10, s.By)
		return numeric(v, func(n float64) float64 { return math.Round(n*places) / places })
	case "secondsToMillis":
		return numeric(v, func(n float64) float64 { return n * 1000 })
	case "millisToSeconds":
		return numeric(v, func(n float64) float64 { return n / 1000 })

	case "clampPercent":
		// An upstream reporting 100.4% is not evidence of better than perfect
		// availability; it is a rounding artefact, and rendering it would look
		// like a bug in the dashboard rather than in the source.
		return numeric(v, func(n float64) float64 { return math.Min(math.Max(n, 0), 100) })

	case "enumMap":
		text, ok := v.Text()
		if !ok {
			return v
		}
		if mapped, ok := s.Table[text]; ok {
			return Of(mapped)
		}
		// An unmapped value passes through rather than becoming empty, so a new
		// category appearing upstream shows up as itself in the config error
		// rather than vanishing from the dashboard.
		return v

	case "lowercase":
		return textual(v, strings.ToLower)
	case "uppercase":
		return textual(v, strings.ToUpper)
	case "trim":
		return textual(v, strings.TrimSpace)
	case "trimPrefix":
		return textual(v, func(t string) string { return strings.TrimPrefix(t, s.Value) })

	case "default":
		if v.Present {
			return v
		}
		return Of(s.Value)
	}
	return v
}

func numeric(v Value, f func(float64) float64) Value {
	n, ok := v.Number()
	if !ok {
		return v
	}
	return Of(f(n))
}

func textual(v Value, f func(string) string) Value {
	t, ok := v.Text()
	if !ok {
		return v
	}
	return Of(f(t))
}
