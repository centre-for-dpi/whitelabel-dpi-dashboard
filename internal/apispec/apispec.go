// Package apispec declares the dashboard's published API document.
//
// It is the counterpart of internal/configschema: that package declares what the
// configuration files are, this one declares what the HTTP surface is, and both
// are rendered to a committed file whose drift is a build failure. Schemas come
// from internal/openapi, which reads them off the protobuf descriptors; what is
// here is everything a descriptor cannot say — which paths exist, what they are
// for, what a request looks like and what comes back.
//
// It is separate from cmd/openapigen so the declarations can be tested, and so
// the drift check can regenerate them without shelling out: `make openapi`
// writing something different from what is committed is a build failure, not a
// discovery someone makes months later.
package apispec

import (
	"encoding/json"
	"fmt"
	"io/fs"

	"gopkg.in/yaml.v3"

	dpiv1 "github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/gen/dpi/v1"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/mapping"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/openapi"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/transform"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/widget"
)

// FileName is the committed document.
const FileName = "openapi.json"

// File is one generated artefact.
type File struct {
	Name      string
	Describes string
	JSON      []byte
}

// All renders the document.
//
// src is the repository root, because the worked examples are read from the
// fixtures that are already committed and already proven — examples/push/payload.json
// is asserted acceptable by the ingest contract, and the pull mapping is the one
// config/sources.yaml actually ships. An example invented here could go stale
// against the endpoint it claims to demonstrate; one read from a fixture cannot.
func All(src fs.FS) ([]File, error) {
	doc, err := document(src)
	if err != nil {
		return nil, err
	}
	raw, err := openapi.Marshal(doc)
	// coverage:ignore -- unreachable: the document holds only the maps, slices,
	// strings and numbers assembled below, every one of which encoding/json can
	// render. The error is returned rather than dropped so that a future
	// addition of something it cannot fails here instead of writing a truncated
	// file.
	if err != nil {
		return nil, err
	}
	return []File{{Name: FileName, Describes: "the HTTP surface", JSON: raw}}, nil
}

func document(src fs.FS) (map[string]any, error) {
	g := openapi.New(openapi.Options{
		Descriptions: descriptions(),
		Required: map[string][]string{
			// proto3 declares nothing required; this is the endpoint's own
			// validation, and it is the one field whose absence drops a record.
			"dpi.v1.IngestService": {"id"},
		},
	})

	ex, err := loadExamples(src)
	if err != nil {
		return nil, err
	}

	ingestReq := g.Ref(dpiv1.File_dpi_v1_ingest_proto.Messages().ByName("IngestRequest"))
	ingestResp := g.Ref(dpiv1.File_dpi_v1_ingest_proto.Messages().ByName("IngestResponse"))
	listResp := g.Ref(dpiv1.File_dpi_v1_dashboard_proto.Messages().ByName("ListServicesResponse"))
	service := g.Ref(dpiv1.File_dpi_v1_dashboard_proto.Messages().ByName("Service"))

	paths := map[string]any{
		"/api/v1/ingest":               ingestPath(ingestReq, ingestResp, ex),
		"/api/v1/services":             servicesPath(listResp),
		"/api/v1/pull/preview":         pullPreviewPath(service, ex),
		"/api/openapi.json":            openAPIPath(),
		"/api":                         consolePath(),
		"/__example/upstream/services": exampleUpstreamPath(),
		"/healthz":                     healthPath(),
		"/readyz":                      readyPath(),
	}
	for path, item := range htmlPaths() {
		paths[path] = item
	}

	doc := map[string]any{
		"openapi":           openapi.Version,
		"jsonSchemaDialect": openapi.Dialect,
		"info":              info(),
		"servers":           []any{map[string]any{"url": "/", "description": "This deployment."}},
		"tags":              tags(),
		"paths":             paths,
		"components": map[string]any{
			"schemas":    schemas(g),
			"parameters": readerParameters(),
			"securitySchemes": map[string]any{
				"ingestToken": map[string]any{
					"type": "http", "scheme": "bearer",
					"description": "The value of the environment variable named by `push.tokenEnvVar` " +
						"in `config/sources.yaml`, shipped as `DPI_INGEST_TOKEN`. Never a value from a " +
						"configuration file: a token in a config file ends up in a git history.",
				},
			},
		},
	}
	// coverage:ignore -- unreachable with the contracts as they stand: the
	// generator only reports a proto kind it cannot map or two messages sharing
	// a short name, and dpi.v1 has neither. Both are exercised in the
	// generator's own tests against a descriptor built for the purpose.
	if err := g.Err(); err != nil {
		return nil, err
	}
	return doc, nil
}

func info() map[string]any {
	return map[string]any{
		"title":   "White-label DPI dashboard",
		"version": "v1",
		"summary": "Feed a dashboard, read back what it holds, and try a pull mapping before committing to one.",
		"description": "Two ways to get data in, and they are alternatives rather than layers.\n\n" +
			"**Push** — your collector calls `POST /api/v1/ingest` whenever it has something to say. " +
			"It is the richer of the two: incidents and error buckets can only arrive this way, and " +
			"every record is validated against the deployment's own taxonomy, so a category nothing " +
			"declares is rejected rather than quietly becoming unreachable.\n\n" +
			"**Pull** — the dashboard polls an endpoint you already have, on a timer, and maps its " +
			"shape onto its own with a declarative mapping in `config/sources.yaml`. Nothing to build " +
			"and nothing to schedule, but it can carry only what a mapping can express. " +
			"`POST /api/v1/pull/preview` exists so that mapping can be worked out interactively " +
			"rather than by editing a file and restarting.\n\n" +
			"Exactly one of the two is active, chosen by `driver:` in `config/sources.yaml`, so an " +
			"operation this deployment does not register says so in its own description.\n\n" +
			"**What the dashboard decides for itself.** Status is never reported, only derived: a " +
			"collector sends observations and the dashboard evaluates them against the thresholds it " +
			"publishes. `status`, `trends` and `rankMovement` are absent from the ingest message " +
			"rather than ignored, so the contract states it plainly.",
		"license": map[string]any{"name": "Apache-2.0", "identifier": "Apache-2.0"},
	}
}

func tags() []any {
	return []any{
		map[string]any{"name": "Ingest", "description": "Getting observations into the dashboard."},
		map[string]any{"name": "Read", "description": "Getting them back out."},
		map[string]any{"name": "Tools", "description": "Working out a configuration before committing to it."},
		map[string]any{"name": "Operations", "description": "What a load balancer and an orchestrator ask."},
		map[string]any{"name": "Dashboard", "description": "The reader-facing pages, and the fragments they swap in. " +
			"HTML rather than JSON, and documented here because a surface that is only partly written " +
			"down is one nobody can reason about."},
	}
}

func schemas(g *openapi.Generator) map[string]any {
	out := g.Schemas()

	// The two bodies that are not protobuf. The preview endpoint takes a mapping,
	// and a mapping is a configuration structure rather than a wire message —
	// which is the point: what goes in the request is what goes in sources.yaml.
	out["PullPreviewRequest"] = map[string]any{
		"type": "object",
		"description": "An upstream's document, and the mapping under consideration. Nothing is " +
			"fetched: the document travels in the body, so this endpoint cannot be used to make the " +
			"dashboard issue requests from inside its own network.",
		"required": []any{"document", "mapping"},
		"properties": map[string]any{
			"document": map[string]any{
				"description": "The JSON your upstream would serve, verbatim.",
			},
			"mapping": map[string]any{"$ref": "#/components/schemas/MappingSpec"},
		},
		"additionalProperties": false,
	}
	out["MappingSpec"] = mappingSchema()
	out["TransformStep"] = transformSchema()
	out["HistoryMapping"] = historyMappingSchema()
	out["PullPreviewResponse"] = map[string]any{
		"type": "object",
		"description": "What the dashboard would make of it — including the status it would derive, " +
			"which is the part a mapping cannot be checked against by eye.",
		"required": []any{"services"},
		"properties": map[string]any{
			"services": map[string]any{
				"type":        "array",
				"description": "One per record the mapping read, evaluated exactly as a poll would evaluate it.",
				"items":       map[string]any{"$ref": "#/components/schemas/Service"},
			},
			"skipped": map[string]any{
				"type": "array",
				"description": "Records the mapping could not read. A poller logs these and carries on; " +
					"here they are the answer.",
				"items": map[string]any{
					"type":     "object",
					"required": []any{"index", "reason"},
					"properties": map[string]any{
						"index":  map[string]any{"type": "integer", "description": "Position in the source array, from zero."},
						"reason": map[string]any{"type": "string", "description": "Why it could not be read."},
					},
					"additionalProperties": false,
				},
			},
		},
		"additionalProperties": false,
	}
	return out
}

func mappingSchema() map[string]any {
	fields := make([]any, 0, len(mapping.Fields()))
	for _, f := range mapping.Fields() {
		fields = append(fields, f)
	}
	return map[string]any{
		"type": "object",
		"description": "The same structure `pull.endpoints[].` takes in `config/sources.yaml`, key for " +
			"key — so a block that works here can be pasted straight into the file, and a block from " +
			"the file can be pasted here to find out what it does.",
		"properties": map[string]any{
			"itemsPath": map[string]any{
				"type": "string",
				"description": "Path to the array of records, e.g. `$.data.services[*]`. Empty means the " +
					"document is itself the array. The syntax is deliberately small: `$.a.b`, " +
					"`$.items[0]`, `$.items[*]` and combinations. No filters, no recursive descent, no " +
					"expressions.",
			},
			"map": map[string]any{
				"type": "object",
				"description": "Dashboard field name to a path into each record. `id` must be mapped, " +
					"because without it services cannot be told apart.",
				"additionalProperties": map[string]any{"type": "string"},
				"propertyNames":        map[string]any{"enum": fields},
			},
			"transform": map[string]any{
				"type": "object",
				"description": "Field name to the chain applied after reading it. Chains run in order, " +
					"and an absent value skips every step except `default`.",
				"additionalProperties": map[string]any{
					"type":  "array",
					"items": map[string]any{"$ref": "#/components/schemas/TransformStep"},
				},
				"propertyNames": map[string]any{"enum": fields},
			},
			"history": map[string]any{"$ref": "#/components/schemas/HistoryMapping"},
		},
		"additionalProperties": false,
	}
}

func transformSchema() map[string]any {
	// Read from the package that implements them, so the document cannot offer a
	// transform that does not exist — the same reason the layout schema takes its
	// widget types from the registry.
	names := make([]any, 0)
	for _, n := range transform.Names() {
		names = append(names, n)
	}
	return map[string]any{
		"type":        "object",
		"description": "One step of a transform chain.",
		"required":    []any{"fn"},
		"properties": map[string]any{
			"fn": map[string]any{
				"type": "string", "enum": names,
				"description": "Which transform. A value that cannot be coerced to the type the " +
					"transform needs passes through untouched rather than becoming zero.",
			},
			"table": map[string]any{
				"type": "object", "additionalProperties": map[string]any{"type": "string"},
				"description": "For `enumMap`: the lookup. An unmapped value passes through unchanged, " +
					"so a table that misses a case leaves the original rather than emptying the field.",
			},
			"by": map[string]any{
				"type":        "number",
				"description": "For `multiply`, `divide` and `round`. Non-zero for the first two.",
			},
			"value": map[string]any{
				"type": "string",
				"description": "For `default` and `trimPrefix`. Note that `default` fires only for a " +
					"field that has a `map` entry: an unmapped field is absent, not defaulted.",
			},
		},
		"additionalProperties": false,
	}
}

func historyMappingSchema() map[string]any {
	str := func(desc string) map[string]any {
		return map[string]any{"type": "string", "description": desc}
	}
	return map[string]any{
		"type": "object",
		"description": "Where the nested daily series is, if the upstream offers one. Omit it and the " +
			"dashboard rolls up its own buckets from successive polls instead — which is why a pull " +
			"deployment need not supply history at all.",
		"properties": map[string]any{
			"path":         str("Path to the array of daily points, relative to each record."),
			"date":         str("Path to the day, within a point. Required: a point that cannot be placed in time is dropped."),
			"availability": str("Path to the day's availability."),
			"errorRate":    str("Path to the day's error rate."),
			"latencyP50":   str("Path to the day's median latency."),
			"volume":       str("Path to the day's request count."),
		},
		"additionalProperties": false,
	}
}

// --- the operations ---------------------------------------------------------

func ingestPath(req, resp map[string]any, ex examples) map[string]any {
	return map[string]any{
		"post": map[string]any{
			"operationId": "ingest",
			"tags":        []any{"Ingest"},
			"summary":     "Report observations",
			"description": "Send what you have observed. Everything else the dashboard shows is " +
				"derived from it.\n\n" +
				"Partial acceptance is deliberate: a malformed record in a batch of two hundred does " +
				"not reject the other hundred and ninety-nine. The response is `200` whenever the body " +
				"decoded, and says which records were dropped and why — so a collector checks " +
				"`rejected`, not the status code.\n\n" +
				"The complete worked payload is committed at `examples/push/payload.json`; the example " +
				"below is the first two records of it.",
			"security": []any{map[string]any{"ingestToken": []any{}}},
			"requestBody": map[string]any{
				"required": true,
				"content": map[string]any{
					"application/json": map[string]any{
						"schema":   req,
						"examples": ex.ingestRequests,
					},
					"application/x-protobuf": map[string]any{
						"schema": map[string]any{"type": "string", "format": "binary",
							"description": "A binary `dpi.v1.IngestRequest`."},
					},
				},
			},
			"responses": map[string]any{
				"200": jsonResponse("Decoded. Read `accepted` and `rejected` to find out what happened to it.",
					resp, ex.ingestResponses),
				"400": textResponse("The body could not be decoded: empty, over the size limit, not " +
					"valid JSON or protobuf, or carrying a field the contract does not declare."),
				"401": textResponse("No bearer token, or the wrong one. The response says nothing about " +
					"which, because telling an unauthenticated caller why their token was rejected " +
					"helps them guess at a valid one."),
			},
		},
	}
}

func servicesPath(resp map[string]any) map[string]any {
	return map[string]any{
		"get": map[string]any{
			"operationId": "listServices",
			"tags":        []any{"Read"},
			"summary":     "Read everything the dashboard holds",
			"description": "The same records the pages are drawn from, with the derived fields " +
				"included — `status`, `trends` and `rankMovement`, none of which any feed can set.\n\n" +
				"Unauthenticated, because it returns exactly what the public HTML already shows: " +
				"gating the machine-readable form of a page anyone can load would be theatre rather " +
				"than access control.",
			"responses": map[string]any{
				"200": map[string]any{
					"description": "The current snapshot.",
					"content": map[string]any{
						"application/json":       map[string]any{"schema": resp},
						"application/x-protobuf": map[string]any{"schema": map[string]any{"type": "string", "format": "binary"}},
					},
				},
			},
		},
	}
}

func pullPreviewPath(service map[string]any, ex examples) map[string]any {
	_ = service
	return map[string]any{
		"post": map[string]any{
			"operationId": "previewPullMapping",
			"tags":        []any{"Tools"},
			"summary":     "Try a pull mapping without deploying it",
			"description": "Pull is a contract the dashboard consumes rather than an endpoint it " +
				"serves, so a mapping normally cannot be tried at all: you edit `sources.yaml`, " +
				"restart, and look at the board. This runs the same three steps a poll runs — compile " +
				"the mapping, apply it to the document, evaluate the result — with the fetch left " +
				"out.\n\n" +
				"Leaving the fetch out is deliberate. An endpoint that accepted a URL and retrieved it " +
				"would let anyone use the dashboard to make requests from inside its own network, so " +
				"the document travels in the body.\n\n" +
				"The response carries the **derived** status, which is what makes it worth running: a " +
				"mapping can read every field it was asked to and still produce a board of unknowns, " +
				"because availability arrived as a ratio where a percentage was expected.\n\n" +
				"Registered whatever the configured driver — it touches no state.",
			"requestBody": map[string]any{
				"required": true,
				"content": map[string]any{
					"application/json": map[string]any{
						"schema":   map[string]any{"$ref": "#/components/schemas/PullPreviewRequest"},
						"examples": ex.previewRequests,
					},
				},
			},
			"responses": map[string]any{
				"200": jsonResponse("What the dashboard would make of the document.",
					map[string]any{"$ref": "#/components/schemas/PullPreviewResponse"}, nil),
				"400": textResponse("The body could not be read, or the mapping would not compile. The " +
					"message names the field and lists what was expected — an unknown field name, a " +
					"path that does not parse, a transform that needs an operand it was not given, or " +
					"`id` left unmapped."),
			},
		},
	}
}

func openAPIPath() map[string]any {
	return map[string]any{
		"get": map[string]any{
			"operationId": "openapi",
			"tags":        []any{"Tools"},
			"summary":     "This document",
			"description": "The committed document, overlaid with what is true of this particular " +
				"deployment: the server URL it is being served from, and a note on any operation the " +
				"configured driver does not register.",
			"responses": map[string]any{
				"200": map[string]any{
					"description": "An OpenAPI 3.1 document.",
					"content":     map[string]any{"application/json": map[string]any{"schema": map[string]any{"type": "object"}}},
				},
			},
		},
	}
}

func consolePath() map[string]any {
	return map[string]any{
		"get": map[string]any{
			"operationId": "apiConsole",
			"tags":        []any{"Tools"},
			"summary":     "The API console",
			"description": "This reference, rendered, with every request editable and sendable against " +
				"the running deployment. Served everywhere, so a deployment always carries its own " +
				"documentation.",
			"responses": map[string]any{"200": htmlResponse("The console.")},
		},
	}
}

func exampleUpstreamPath() map[string]any {
	return map[string]any{
		"get": map[string]any{
			"operationId": "exampleUpstream",
			"tags":        []any{"Read"},
			"summary":     "The same data in a foreign shape",
			"description": "Deliberately **not** the dashboard's own schema: ratios rather than " +
				"percentages, `IDENTITY` rather than `cat.identity`, and field names nothing here " +
				"chose. It exists so the shipped pull mapping has something real to poll, which makes " +
				"switching to your own service one URL change against a path already proven to " +
				"work.\n\n" +
				"Served when the dashboard holds its own data — `driver: seed` or `driver: push`. " +
				"Under `driver: pull` it is absent, so a deployment cannot re-publish a copy of what " +
				"it polls.",
			"responses": map[string]any{
				"200": map[string]any{
					"description": "An imaginary upstream's response.",
					"content":     map[string]any{"application/json": map[string]any{"schema": map[string]any{"type": "object"}}},
				},
			},
		},
	}
}

func healthPath() map[string]any {
	return map[string]any{
		"get": map[string]any{
			"operationId": "healthz",
			"tags":        []any{"Operations"},
			"summary":     "Liveness",
			"description": "Whether the process is running. It never inspects the data, so it stays " +
				"`200` for a dashboard that has nothing to show — which is what distinguishes it from " +
				"`/readyz`.",
			"responses": map[string]any{"200": textResponse("`ok`")},
		},
	}
}

func readyPath() map[string]any {
	return map[string]any{
		"get": map[string]any{
			"operationId": "readyz",
			"tags":        []any{"Operations"},
			"summary":     "Readiness",
			"description": "Whether there is anything to serve. A push deployment answers `503` until " +
				"its first successful ingest, which is what keeps a rolling deploy from cutting " +
				"traffic over to an empty page.",
			"responses": map[string]any{
				"200": textResponse("`ready`"),
				"503": textResponse("`no data yet`"),
			},
		},
	}
}

// htmlPaths documents the reader-facing surface.
//
// HTML rather than JSON, and here because the ask was the whole surface: a
// document that covered only the machine-readable half would leave the routes an
// integrator actually meets — the ones a link points at — written down nowhere
// but the router.
func htmlPaths() map[string]any {
	readerParams := []any{}
	for _, name := range widget.Params {
		readerParams = append(readerParams, map[string]any{"$ref": "#/components/parameters/" + name})
	}

	page := func(id, summary, desc string, extra ...any) map[string]any {
		return map[string]any{
			"get": map[string]any{
				"operationId": id,
				"tags":        []any{"Dashboard"},
				"summary":     summary,
				"description": desc,
				"parameters":  append(append([]any{}, extra...), readerParams...),
				"responses":   map[string]any{"200": htmlResponse("The rendered markup.")},
			},
		}
	}

	serviceID := map[string]any{
		"name": "id", "in": "path", "required": true,
		"description": "A service id, as returned by `GET /api/v1/services`.",
		"schema":      map[string]any{"type": "string"},
	}

	out := map[string]any{
		"/": page("dashboard", "The dashboard",
			"Every reader selection rides in the query string, so a shared link reproduces exactly "+
				"what the sender was looking at. Unrecognised values fall back to the deployment's "+
				"defaults rather than erroring: a hand-edited URL or a bookmark that outlived a config "+
				"change should produce a sensible page, not an empty one."),
		"/service/{id}": page("servicePage", "The dashboard with one service open",
			"The same page with the drawer open, so a service is a real address that can be "+
				"bookmarked and sent.", serviceID),
		"/fragments/{section}": page("sectionFragment", "One band of the page",
			"What an HTMX swap asks for. The section ids come from the deployment's `layout.yaml`; "+
				"as shipped they are `verdict`, `signals` and `leaderboard`. The response carries an "+
				"`HX-Push-Url` header naming the reader-facing address the fragment corresponds to.",
			map[string]any{
				"name": "section", "in": "path", "required": true,
				"description": "A section id declared in `layout.yaml`.",
				"schema":      map[string]any{"type": "string"},
			}),
		"/fragments/service/{id}": page("drawerFragment", "One service's drawer",
			"The whole panel, for opening one.", serviceID),
		"/fragments/service/{id}/pane": page("drawerPaneFragment", "One tab of an open drawer",
			"The pane below the tab strip, plus the strip itself as an out-of-band swap. Everything "+
				"above it is the same whichever tab is showing, so changing tab does not rebuild it.",
			serviceID),
	}

	out["/assets/theme.css"] = map[string]any{
		"get": map[string]any{
			"operationId": "themeCSS",
			"tags":        []any{"Dashboard"},
			"summary":     "The generated stylesheet",
			"description": "Built at startup from `config/theme.yaml`, never a file on disk. Served " +
				"immutable with a fingerprint for an ETag, so a restyled deployment produces a " +
				"different URL rather than a stale cache.",
			"responses": map[string]any{
				"200": map[string]any{"description": "CSS custom properties.",
					"content": map[string]any{"text/css": map[string]any{"schema": map[string]any{"type": "string"}}}},
				"304": map[string]any{"description": "Unchanged since the ETag the request carried."},
			},
		},
	}
	out["/assets/{path}"] = map[string]any{
		"get": map[string]any{
			"operationId": "asset",
			"tags":        []any{"Dashboard"},
			"summary":     "A static asset",
			"description": "The stylesheet, the script, htmx, the fonts and the API console's " +
				"renderer, all compiled into the binary. Served immutable for a year, which is safe " +
				"because every link carries a `?v=` fingerprint over the whole embedded tree.",
			"parameters": []any{map[string]any{
				"name": "path", "in": "path", "required": true,
				"description": "A path under `web/static`, e.g. `app.css`.",
				"schema":      map[string]any{"type": "string"},
			}},
			"responses": map[string]any{
				"200": map[string]any{"description": "The asset."},
				"404": map[string]any{"description": "No such asset."},
			},
		},
	}
	return out
}

// readerParameters is the reader's state, once.
//
// Every page and every fragment accepts the same set, because they are all
// renderings of the same selections — which is also why a link between them
// carries the lot.
func readerParameters() map[string]any {
	meaning := map[string]string{
		widget.ParamScope:   "Which population is on view. The deployment's declared scopes; as shipped `national` or `state`.",
		widget.ParamRole:    "Which side of the exchange: `issuer` or `requestor`.",
		widget.ParamID:      "Narrow the board to an explicit set of service ids, comma-separated or repeated. Set by a finding whose subject has nothing else in common.",
		widget.ParamSignals: "Which set of findings the signals band shows: `attention` or `opportunity`.",
		widget.ParamRegion:  "A region id. Only meaningful when the scope is sub-national.",
		widget.ParamPeriod:  "The window the figures and the ranking cover: `24h`, `7d`, `30d` or `90d` as shipped.",
		widget.ParamSearch:  "Free-text search over names and descriptions.",
		widget.ParamStatus:  "Keep only these statuses, comma-separated or repeated.",
		widget.ParamCat:     "Keep only these categories, comma-separated or repeated.",
		widget.ParamSort:    "Which column orders the board: `rank`, `name`, `status`, or a metric id.",
		widget.ParamDir:     "`asc` or `desc`.",
		widget.ParamLang:    "A locale code. Falls back to `Accept-Language`, then to the deployment's base locale.",
		widget.ParamFilters: "`open` keeps the narrow-screen filter panel expanded.",
		widget.ParamTheme:   "`light` or `dark`. Falls back to the `theme` cookie, then to the reader's system preference.",
		widget.ParamTab:     "Which tab of the open drawer is showing.",
	}

	out := map[string]any{}
	for _, name := range widget.Params {
		out[name] = map[string]any{
			"name": name, "in": "query", "required": false,
			"description": meaning[name] + " Unrecognised values fall back rather than erroring.",
			"schema":      map[string]any{"type": "string"},
		}
	}
	return out
}

// --- response helpers -------------------------------------------------------

func jsonResponse(desc string, schema map[string]any, examples map[string]any) map[string]any {
	content := map[string]any{"schema": schema}
	if len(examples) > 0 {
		content["examples"] = examples
	}
	return map[string]any{
		"description": desc,
		"content":     map[string]any{"application/json": content},
	}
}

// textResponse describes an error body.
//
// Errors are plain text rather than a JSON envelope, because that is what
// http.Error writes and inventing a schema for them here would document
// something the server does not send.
func textResponse(desc string) map[string]any {
	return map[string]any{
		"description": desc,
		"content":     map[string]any{"text/plain": map[string]any{"schema": map[string]any{"type": "string"}}},
	}
}

func htmlResponse(desc string) map[string]any {
	return map[string]any{
		"description": desc,
		"content":     map[string]any{"text/html": map[string]any{"schema": map[string]any{"type": "string"}}},
	}
}

// --- worked examples --------------------------------------------------------

type examples struct {
	ingestRequests  map[string]any
	ingestResponses map[string]any
	previewRequests map[string]any
}

func loadExamples(src fs.FS) (examples, error) {
	var ex examples

	payload, err := readJSON(src, "examples/push/payload.json")
	if err != nil {
		return ex, err
	}
	upstream, err := readJSON(src, "examples/upstream/services.json")
	if err != nil {
		return ex, err
	}
	spec, err := shippedMapping(src)
	if err != nil {
		return ex, err
	}

	ex.ingestRequests = map[string]any{
		"minimal": map[string]any{
			"summary": "The smallest useful payload",
			"description": "One service, one reading. Everything else the dashboard shows — the " +
				"status, the trend, the rank — is worked out from this.",
			"value": map[string]any{
				"mode":     "INGEST_MODE_UPSERT",
				"sourceId": "my-collector",
				"services": []any{map[string]any{
					"id":         "aadhaar",
					"key":        "aadhaar",
					"nameTermId": "Aadhaar",
					"categoryId": "cat.identity",
					"regionId":   "reg.national",
					"scope":      "national",
					"roleId":     "issuer",
					"metrics": map[string]any{
						"availability": 99.92,
						"errorRate":    0.62,
						"latencyP50Ms": 264,
						"staleSeconds": "195",
						"volume":       map[string]any{"total": "2480492", "success": "2465112"},
					},
					"observedAt": "2026-07-27T12:00:00Z",
				}},
			},
		},
		"seeded": map[string]any{
			"summary": "The shipped fixture",
			"description": "The first two records of `examples/push/payload.json`, history and error " +
				"buckets trimmed. The complete file is what `make demo-push` sends.",
			"value": trimIngest(payload, 2, 3),
		},
	}

	ex.ingestResponses = map[string]any{
		"accepted": map[string]any{
			"summary": "Everything stored",
			"value":   map[string]any{"accepted": 2, "receivedAt": "2026-07-27T12:00:04.113Z"},
		},
		"partial": map[string]any{
			"summary": "One record dropped, the rest stored",
			"description": "The status is still 200. A collector checks `rejected`, not the status " +
				"code — which is what lets one bad record out of two hundred stay one bad record.",
			"value": map[string]any{
				"accepted": 1, "rejected": 1,
				"errors": []any{
					map[string]any{"serviceId": "pension", "field": "categoryId",
						"message": `"cat.pensions" is not declared in domain.yaml`},
				},
				"receivedAt": "2026-07-27T12:00:04.113Z",
			},
		},
	}

	ex.previewRequests = map[string]any{
		"shipped": map[string]any{
			"summary": "The mapping this deployment ships",
			"description": "One record of `examples/upstream/services.json` with the mapping from " +
				"`config/sources.yaml`. Note what the transforms are for: the upstream reports " +
				"availability as a ratio and category as `IDENTITY`, and neither would mean anything " +
				"to the dashboard untranslated.",
			"value": map[string]any{
				"document": trimUpstream(upstream, 1, 3),
				"mapping":  spec,
			},
		},
		"minimal": map[string]any{
			"summary": "The smallest mapping that works",
			"description": "`id` is the only field a mapping must have — without it, records cannot " +
				"be told apart. Everything unmapped is absent rather than zero.",
			"value": map[string]any{
				"document": []any{map[string]any{"svc": "aadhaar", "up": 0.9992}},
				"mapping": map[string]any{
					"map": map[string]any{"id": "$.svc", "metrics.availability": "$.up"},
					"transform": map[string]any{
						"metrics.availability": []any{map[string]any{"fn": "ratioToPercent"}},
					},
				},
			},
		},
	}
	return ex, nil
}

func readJSON(src fs.FS, name string) (map[string]any, error) {
	raw, err := fs.ReadFile(src, name)
	if err != nil {
		return nil, fmt.Errorf("reading the worked example %s: %w", name, err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", name, err)
	}
	return out, nil
}

// shippedMapping is the pull mapping from config/sources.yaml.
//
// Read rather than restated, so the example cannot describe a mapping the
// deployment does not actually use.
func shippedMapping(src fs.FS) (mapping.Spec, error) {
	raw, err := fs.ReadFile(src, "config/sources.yaml")
	if err != nil {
		return mapping.Spec{}, fmt.Errorf("reading config/sources.yaml: %w", err)
	}
	var file struct {
		Pull struct {
			Endpoints []struct {
				mapping.Spec `yaml:",inline"`
			} `yaml:"endpoints"`
		} `yaml:"pull"`
	}
	if err := yaml.Unmarshal(raw, &file); err != nil {
		return mapping.Spec{}, fmt.Errorf("parsing config/sources.yaml: %w", err)
	}
	if len(file.Pull.Endpoints) == 0 {
		return mapping.Spec{}, fmt.Errorf("config/sources.yaml declares no pull endpoint to take the example mapping from")
	}
	return file.Pull.Endpoints[0].Spec, nil
}

// trimIngest keeps the first few records, with their series shortened.
//
// The committed payload is thirty kilobytes. Inlined whole it would be most of
// the document and unreadable in any renderer, and a worked example nobody reads
// is not a worked example. What is left is still a valid request, which the
// tests assert by sending it.
func trimIngest(doc map[string]any, records, series int) map[string]any {
	out := map[string]any{}
	for k, v := range doc {
		out[k] = v
	}
	list, _ := doc["services"].([]any)
	kept := make([]any, 0, records)
	for i, item := range list {
		if i >= records {
			break
		}
		sv, ok := item.(map[string]any)
		if !ok {
			continue
		}
		trimmed := map[string]any{}
		for k, v := range sv {
			trimmed[k] = v
		}
		trimmed["history"] = firstN(sv["history"], series)
		trimmed["errors"] = firstN(sv["errors"], series)
		kept = append(kept, trimmed)
	}
	out["services"] = kept
	return out
}

func trimUpstream(doc map[string]any, records, series int) map[string]any {
	out := map[string]any{}
	for k, v := range doc {
		out[k] = v
	}
	data, _ := doc["data"].(map[string]any)
	list, _ := data["services"].([]any)

	kept := make([]any, 0, records)
	for i, item := range list {
		if i >= records {
			break
		}
		sv, ok := item.(map[string]any)
		if !ok {
			continue
		}
		trimmed := map[string]any{}
		for k, v := range sv {
			trimmed[k] = v
		}
		trimmed["daily"] = firstN(sv["daily"], series)
		kept = append(kept, trimmed)
	}
	out["data"] = map[string]any{"services": kept}
	return out
}

func firstN(v any, n int) []any {
	list, _ := v.([]any)
	if len(list) > n {
		return list[:n]
	}
	if list == nil {
		return []any{}
	}
	return list
}
