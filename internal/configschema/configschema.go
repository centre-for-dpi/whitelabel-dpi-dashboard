// Package configschema declares the JSON Schema for each configuration file.
//
// It is separate from cmd/schemagen so that the definitions can be tested and
// so the drift check can regenerate them without shelling out: `make schema`
// writing something different from what is committed is a build failure, not a
// discovery someone makes months later.
package configschema

import (
	"fmt"

	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/config"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/jsonschema"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/layout"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/widget"
)

// durationPattern matches a Go duration string. It is deliberately strict about
// requiring a unit: config rejects a bare "30" because it is ambiguous between
// seconds and nanoseconds, and the editor should say so before startup does.
const durationPattern = `^-?([0-9]+(\.[0-9]+)?(ns|us|µs|ms|s|m|h))+$`

var durationTypes = map[string]string{"Duration": durationPattern}

// File pairs a schema with the config file it describes.
type File struct {
	// Name is the schema's own file name, e.g. "app.schema.json".
	Name string
	// Describes is the config file it applies to, e.g. "app.yaml".
	Describes string
	// JSON is the rendered schema.
	JSON []byte
}

// spec is one schema to render. Kept separate from All so that render can be
// exercised with a type it cannot describe — the error path exists for whoever
// later adds an undescribable field to config, and it should be tested rather
// than assumed.
type spec struct {
	name      string
	describes string
	value     any
	opts      jsonschema.Options
}

// All renders every schema.
func All() ([]File, error) {
	return render([]spec{
		{"app.schema.json", config.FileApp, config.App{}, appOptions()},
		{"brand.schema.json", config.FileBrand, config.Brand{}, brandOptions()},
		{"domain.schema.json", config.FileDomain, config.Domain{}, domainOptions()},
		{"theme.schema.json", config.FileTheme, config.Theme{}, themeOptions()},
		{"icons.schema.json", config.FileIcons, config.Icons{}, iconsOptions()},
		{"layout.schema.json", layout.FileLayout, layout.Layout{}, layoutOptions()},
	})
}

func render(specs []spec) ([]File, error) {
	out := make([]File, 0, len(specs))
	for _, s := range specs {
		raw, err := jsonschema.Generate(s.value, s.opts)
		if err != nil {
			return nil, fmt.Errorf("generating %s: %w", s.name, err)
		}
		out = append(out, File{Name: s.name, Describes: s.describes, JSON: raw})
	}
	return out, nil
}

func appOptions() jsonschema.Options {
	return jsonschema.Options{
		Title:       "Dashboard application configuration",
		Description: "Process-level configuration: how to serve, where to store, how to log.",
		StringTypes: durationTypes,
		Enums: map[string][]string{
			"client.runtime": {config.RuntimeVanilla, config.RuntimeAlpine},
			"log.level":      {"debug", "info", "warn", "error"},
			"log.format":     {"text", "json"},
			"storage.driver": {
				config.DriverSQLite,
				config.DriverPostgres,
				config.DriverMySQL,
				config.DriverMariaDB,
				config.DriverMemory,
			},
		},
		Descriptions: map[string]string{
			"server.addr":                             "Listen address, e.g. \":8080\".",
			"server.shutdownGrace":                    "How long in-flight requests get to finish after SIGTERM.",
			"server.baseURL":                          "Absolute base for generated links. Empty derives it per request.",
			"client.runtime":                          "Which browser-side runtime the page loads. Only \"vanilla\" is implemented.",
			"storage.driver":                          "All database drivers are pure Go, so CGO_ENABLED=0 builds still work. \"memory\" keeps nothing across restarts.",
			"storage.dsn":                             "Connection string. Required for postgres, mysql and mariadb.",
			"storage.history.retentionDays":           "How far back the charts can look.",
			"storage.history.rawSampleRetentionHours": "How long raw per-poll samples are kept before rollup discards them.",
			"storage.history.rollupIntervalMinutes":   "How often raw samples are folded into daily buckets.",
		},
	}
}

func brandOptions() jsonschema.Options {
	return jsonschema.Options{
		Title:       "Dashboard brand configuration",
		Description: "The visible identity: what the dashboard calls itself and who it credits.",
		Descriptions: map[string]string{
			"wordmarkTermId":  "Term id for the dashboard's name. Untranslated text works too: an id with no matching key renders as itself.",
			"iconKey":         "Which entry in icons.yaml to render beside the wordmark.",
			"footer.external": "Marks the link as leaving the site, which adds an indicator and rel=noopener.",
		},
	}
}

func domainOptions() jsonschema.Options {
	return jsonschema.Options{
		Title:       "Dashboard domain contract",
		Description: "What is measured, how it is grouped, and what counts as working. Swap this file and a locale bundle to retarget the dashboard.",
		Enums: map[string][]string{
			"metrics.field":              {config.FieldAvailability, config.FieldErrorRate, config.FieldLatencyP50, config.FieldVolume},
			"metrics.unit":               {config.UnitPercent, config.UnitMillisecond, config.UnitCount},
			"metrics.direction":          {config.DirectionHigherIsBetter, config.DirectionLowerIsBetter, config.DirectionNeutral},
			"thresholds.evaluationOrder": config.Statuses,
			"statusModel.order":          config.Statuses,
			"signals.kind": {
				config.SignalBelowTargetDays,
				config.SignalErrorRisingCategories,
				config.SignalLongestOpenIncident,
				config.SignalMaintenanceActive,
			},
			"signals.filter.status": config.Statuses,
		},
		Descriptions: map[string]string{
			"onboardedDenominator":                "The denominator of the coverage line: \"104 of 812 services onboarded\".",
			"periods.days":                        "Lookback in days used for trend deltas.",
			"metrics.field":                       "Which observation supplies the value. Explicit so a metric can be renamed for its audience without breaking the binding.",
			"metrics.target":                      "The value this metric is held to. Null means no target; a metric shown in the leaderboard needs either a target or a framing.",
			"metrics.direction":                   "Which way is good. Decides whether a rising trend is coloured as improvement or regression.",
			"thresholds.evaluationOrder":          "First match wins. Maintenance and staleness come first: a planned window or a missing reading explains more than a number that was never observed.",
			"thresholds.values.staleSecondsAbove": "An observation older than this is treated as no observation at all.",
			"thresholds.prose":                    "Term ids for the published rule text. Each receives the threshold values as ICU parameters, so the sentence on screen quotes the live numbers.",
			"statusModel.severity":                "Higher sorts first in the default ranking: worst news at the top.",
			"signals.ruleTermId":                  "Every signal card prints the rule it applied, so this is required.",
		},
	}
}

func themeOptions() jsonschema.Options {
	return jsonschema.Options{
		Title:       "Dashboard theme tokens",
		Description: "Design tokens only. Declares values; adds no selectors. Values containing { } ; < > /* */ or newlines are rejected, since they would break out of the generated stylesheet.",
		Descriptions: map[string]string{
			"light":                 "Applied by default and whenever the reader has explicitly chosen light.",
			"dark":                  "Applied when the operating system asks for dark, unless the reader has chosen light.",
			"tokens":                "Values that do not change between light and dark, such as radii and easing.",
			"fonts.body.stack":      "CSS font stack. Templates reference var(--font-body), never a family name.",
			"fonts.body.faces":      "Self-hosted faces to declare. Omit entirely to use only system fonts.",
			"fonts.body.faces.file": "File under /assets/fonts/. Set either this or url, not both.",
			"fonts.body.faces.url":  "Absolute URL. A strict Content-Security-Policy may block it.",
		},
	}
}

func iconsOptions() jsonschema.Options {
	return jsonschema.Options{
		Title:       "Dashboard icon set",
		Description: "Semantic icons. Templates reference icons by role, never by glyph, so the whole set can be swapped without touching markup.",
		Descriptions: map[string]string{
			"icons": "Keyed by role, e.g. \"status.major\". Each entry sets exactly one of glyph or svg.",
		},
	}
}

func layoutOptions() jsonschema.Options {
	return jsonschema.Options{
		Title:       "Dashboard layout",
		Description: "Which sections exist, in what order, with which widgets bound to which data. Validated at startup against the widget registry and your own domain.yaml.",
		Enums: map[string][]string{
			"pages.sections.widgets.type":        widget.Default().Types(),
			"pages.sections.widgets.bind.source": widget.Sources(),
			"pages.sections.widgets.repeatOver":  layout.RepeatSources,
			"drawer.tabs.widgets.type":           widget.Default().Types(),
			"drawer.tabs.widgets.bind.source":    widget.Sources(),
			"drawer.tabs.widgets.repeatOver":     layout.RepeatSources,
		},
		Descriptions: map[string]string{
			"pages.sections.id":                 "Becomes a DOM id and a fragment route, so it must be unique across the layout.",
			"pages.sections.swap":               "Fragment route that re-renders just this band. Empty means the section never swaps on its own.",
			"pages.sections.grid.minColWidth":   "Narrowest a column may be before the grid reflows. Empty means one full-width column.",
			"pages.sections.heading.scoped":     "Append the current scope to the title term, so one heading reads differently per scope.",
			"pages.sections.widgets.options":    "Settings for this widget type. Unknown options are rejected at startup; run `make validate` to see what each accepts.",
			"pages.sections.widgets.repeatOver": "Render once per item of a configured collection, so adding a metric to domain.yaml adds a tile without touching the layout.",
			"pages.sections.widgets.bind.days":  "Override the reader's selected window. Leave unset to follow the period control.",
			"drawer.tabs.id":                    "First tab is the one opened when no tab is named.",
		},
	}
}
