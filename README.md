# White-label DPI Dashboard

[![ci](https://github.com/centre-for-dpi/whitelabel-dpi-dashboard/actions/workflows/ci.yml/badge.svg)](https://github.com/centre-for-dpi/whitelabel-dpi-dashboard/actions/workflows/ci.yml)

A public status dashboard for digital public infrastructure. One static binary,
no runtime dependencies, and everything a deployment needs to change lives in
config files rather than in code.

It answers one question first — **are these services working right now?** — and
then lets a reader find out why.

```
Are the national data sharing services working right now?
25 Operational · 4 Partial outage · 3 Major outage · 1 Unknown · 1 Under maintenance
178 of 812 national & state services onboarded · updated 4 minutes ago
```

## Accessibility

WCAG 2.2 AA, enforced by tests rather than asserted: contrast is computed from
your palette and fails startup, structure is checked over every route and locale
on every commit, and axe-core runs in a headless Chrome across 26 states. See
[`docs/accessibility.md`](docs/accessibility.md) for the claim and what is
deliberately out of scope, and [`docs/design-walkthrough.md`](docs/design-walkthrough.md)
for what the page shows and why.

## Try it

```sh
make run          # http://localhost:8080
```

Or without cloning anything:

```sh
docker run --rm -p 8080:8080 ghcr.io/centre-for-dpi/whitelabel-dpi-dashboard
```

The image is published from `main` by the pipeline above, for both `linux/amd64`
and `linux/arm64`. Nothing is pushed that has not built and been proven to serve.

Either way it starts on generated demonstration data — 178 services, ninety days
of history, every status represented — so the dashboard is worth looking at
before you have integrated anything.

Every deployment also serves its own API reference at
[`/api`](http://localhost:8080/api), with each request editable and sendable
against the instance in front of you — including a dry run that answers "given
this document and this mapping, what would you make of it?" before any of it
reaches a config file. See [docs/api.md](docs/api.md).

### Deploy it

[![Deploy to Render](https://render.com/images/deploy-to-render-button.svg)](https://render.com/deploy)
[![Deploy on Railway](https://railway.app/button.svg)](https://railway.app/new/template)

```sh
fly launch --copy-config      # Fly.io
docker compose up             # anywhere
```

Nothing needs mounting. The default configuration, templates, stylesheet, fonts
and htmx are all compiled into the binary, so a container with no volumes and no
environment produces a working dashboard.

## Feed it your data

Three ways, one config key. See [`examples/README.md`](examples/README.md) for
both wire formats side by side.

| | Who initiates | Good when |
|---|---|---|
| **Pull** | The dashboard, on a timer | You already expose an HTTP endpoint |
| **Push** | Your collector | Your systems are on a private network |
| **Seed** | Nobody | You are evaluating or developing |

### Pull: a mapping, not code

Point `config/sources.yaml` at your service and describe its fields. Your names
on the right, the dashboard's on the left:

```yaml
driver: pull
pull:
  endpoints:
    - id: primary
      url: https://status.example.gov/api/services
      itemsPath: $.data.services
      map:
        id:                   $.serviceId
        categoryId:           $.category
        metrics.availability: $.sla.uptimePct
      transform:
        # A collector reporting 0.9991 where the dashboard expects 99.91 is the
        # commonest integration mistake there is. It is one line here.
        metrics.availability: [{ fn: ratioToPercent }]
        categoryId:           [{ fn: enumMap, table: { IDENTITY: cat.identity } }]
```

`make demo-pull` proves the whole path end to end: it starts one instance
serving demonstration data as a deliberately foreign-shaped API, points a second
at it in pull mode, and shows that both produce identical verdicts.

### Push: curl is enough

```sh
curl -X POST http://localhost:8080/api/v1/ingest \
  -H "authorization: Bearer $DPI_INGEST_TOKEN" \
  -H "content-type: application/json" \
  --data @examples/push/payload.json
```

The contract is defined in [`api/dpi/v1/ingest.proto`](api/dpi/v1/ingest.proto)
and served as both JSON and binary protobuf, negotiated on `Content-Type`. The
proto exists so the contract is versioned and machine-checkable, and so teams
who want a generated client can have one. **It is never an obligation.**

What went in comes back out of `GET /api/v1/services`, with the derived status
alongside it. The whole surface — both feed models, the pages, the probes — is
described by [`api/openapi.json`](api/openapi.json), generated from those
contracts and checked by `make drift`, and rendered at `/api`.

## Keep the history

The dashboard's verdict is about right now, and needs no database. The **charts**
are about the last ninety days, and where those days come from depends on your
upstream:

- **It serves its own history.** Nothing to store. The default `memory` driver is
  a defensible permanent choice — a restart loses nothing, because the next poll
  brings it all back.
- **It serves only a current reading**, which is the common case and almost
  always the case for a push collector. The dashboard samples it, folds each
  completed day into a bucket, and *that* is worth keeping. On `memory`, a
  restart throws it away and starts the ninety days again.

Point it at a database and it survives. One key, no code:

```yaml
storage:
  driver: postgres          # sqlite | postgres | mysql | mariadb | memory
  dsn: ${DATABASE_URL}      # from the environment — it usually carries a password
```

Migrations run at startup, so there is no second command to remember. All three
drivers are pure Go, so `CGO_ENABLED=0` and the `scratch` image still hold
whichever you choose.

**The four are interchangeable, and that is tested rather than asserted.** One
contract suite — sixteen cases covering absence, upstream precedence, rollup
arithmetic and retention — runs against all four:

```sh
make test-db     # brings up Postgres, MySQL and MariaDB, runs the identical suite
```

SQLite needs no server and runs on every `make test`. Two decisions are worth
knowing about because they are the ones that could have gone wrong quietly:

- **Rollup arithmetic happens in Go, not as a `GROUP BY`.** `AVG` over an integer
  column returns a different type and rounds differently on each of the three
  databases, and integer division is `/` on two of them and `DIV` on the third.
  Aggregating in SQL would have meant slightly different charts depending on
  which backend you picked.
- **A missing reading is stored as `NULL`, never as `0`.** A service nobody has
  measured reads as *unknown*. Round-tripping that absence as a zero would turn
  it into a total outage on every restart.

The drivers are about a third of the binary. `make build-lite` drops all three
(22 MB → 14 MB); `make build-sqlite` keeps one.

## Make it yours

Every file below is embedded with a default. Put your own in a directory, point
`-config` at it, and override only what you care about — everything else keeps
falling back.

| File | What it decides |
|---|---|
| `brand.yaml` | The name, the wordmark, the credits |
| `theme.yaml` | Colours, fonts, radii — as tokens, in light and dark |
| `icons.yaml` | The icon set, by role. No template contains a glyph |
| `domain.yaml` | What is measured, how it is grouped, what counts as working |
| `layout.yaml` | Which sections exist, in what order, bound to which data |
| `locales/*.yaml` | Every word on the screen, in eight languages |
| `sources.yaml` | Where the data comes from |

```sh
mkdir mine && cp config/brand.yaml mine/
$EDITOR mine/brand.yaml
./whitelabel-dpi-dashboard -config mine
```

Each file carries a `$schema` header, so an editor with YAML language server
support gives you completion and inline validation as you type.

### The whole page is data

`layout.yaml` composes seventeen widget types into sections. Adding a metric
column, reordering the drawer tabs or dropping a section entirely are edits to
that file:

```yaml
- type: leaderboard-table
  bind: { source: services.filtered }
  options:
    columns: [rank, name, status, metric.availability, metric.errorRate]
```

It is validated in full at startup against the widget registry and your own
`domain.yaml`, with the line that caused each problem:

```
3 configuration errors:
  layout.yaml:8:13: pages[0].sections[0].widgets[0]: unknown widget type "haeding";
    this build provides [bar-chart bar-list coverage cta-button data-table …]
  layout.yaml:15:11: drawer.tabs[0].widgets[0]: widget "timeline" cannot read
    "service.errorBreakdown"; it accepts [service.incidents]
```

Run `make validate` to check a configuration without starting the server.

## What it decides, and what it does not

Three commitments shape the whole design:

**Status is never reported, only derived.** A collector sends observations; the
dashboard evaluates them against the thresholds it publishes. A service at 97.2%
availability is a major outage whatever its operator would prefer to call it —
and the interface shows the reader which threshold it crossed:

> Why this status: Major outage = availability below 99.00% or error rate above 2.00%.

Those numbers come from `domain.yaml`, so the published rule cannot drift from
the applied one.

**Absence is not zero.** A service that has not reported reads as *unknown*, not
as *down*. "We cannot tell" and "it is broken" are different claims, and only one
of them is usually true.

**Status is now; rank is over a window.** The verdict at the top answers "right
now". The leaderboard's ranking and every figure in it follow the period you
select, and move together — a board ordered by ninety-day standing but showing
this minute's availability would invite you to conclude the sort is broken.

## Eight languages

English, Hindi, Arabic, Chinese, Spanish, French, Russian and Swahili ship, and
the whole interface is translated in each — not the shell with English leaking
through the drawer.

Numbers, dates and plurals come from CLDR via the locale code. Nothing about
them is configured, because CLDR is right about more cases than a config block
would be:

```
en  73,450,514    99.50%    27 seconds ago
hi  7,34,50,514   99.50%    27 सेकंड पहले        ← Indian grouping, [3,2]
ar  ٧٣٬٤٥٠٬٥١٤     ٩٩٫٥٠٪    قبل ٢٧ ثانية        ← Arabic-Indic digits, RTL
ru  73 450 514    99,50%    27 секунд назад     ← narrow no-break space
```

Arabic gets all six of its plural categories, including the dual — two results
reads **نتيجتان**, not "2 نتائج". Russian gets its four. Chinese gets its one,
so its plural terms have a single arm, which is correct rather than lazy.
`direction: rtl` on Arabic is the only styling switch; the stylesheet uses
logical properties throughout.

Adding a ninth language is dropping `config/locales/pt.yaml` into an override
directory. The directory listing is the registry — there is nothing to register
and no code to change. Terms fall back to English key by key, so a partial
translation is genuinely useful from the first line.

**What is deliberately not translated:** the ~124 names of Indian services,
providers and states. Those are proper nouns belonging to the *example* domain
config that a deployment replaces wholesale — inventing an Arabic rendering of
"Maharashtra" would be thrown away by the first real deployment. Hindi is the
exception, since the prototype already had them.

Four tests guard the parts that fail silently: a translation that drops a
`{placeholder}`, a plural missing an arm its language distinguishes, a widget
passing an argument name its message does not declare, and copy hardcoded into
a template. Each of those renders as a hole in a sentence rather than an error.

## Without JavaScript

Every control is a real form or link with a real GET target. HTMX upgrades a
page load into a partial swap; it is not what makes the control work. Filters,
sorting, the period selector and the service drawer all function with scripting
disabled, and there is a test asserting it.

## Building on it

```sh
make            # the full list of targets
make ci         # lint, static build, drift check, tests, coverage
make demo-pull  # prove the pull driver end to end
make demo-push  # prove the push endpoint with curl
make test-db    # the store contract suite against all four backends
```

Requires Go 1.26. `buf` and `protoc` are needed only to regenerate the wire
contracts, and their output is committed, so a clean clone builds and runs
without them.

### Dependencies

Seven, all pure Go — which is why `CGO_ENABLED=0` static builds and a `scratch`
image work, and why the binary has nothing to install alongside it.

| | For |
|---|---|
| `golang.org/x/text` | CLDR plurals, digit grouping, collation |
| `gopkg.in/yaml.v3` | Config, where comments are the documentation |
| `google.golang.org/protobuf` | The two wire contracts |
| `modernc.org/sqlite` | SQLite — pure Go, unlike `mattn/go-sqlite3` |
| `github.com/jackc/pgx/v5` | Postgres, through its `database/sql` adapter |
| `github.com/go-sql-driver/mysql` | MySQL **and** MariaDB — one driver covers both |
| htmx (vendored) | Fragment swaps |
| RapiDoc (vendored) | The API reference at `/api` |

Both vendored files are committed, minified, and served from the binary — no
`package.json` and no build step. RapiDoc is stored gzipped and served as it is
stored, because the four megabytes it unpacks to would otherwise be a fifth of
the artefact spent on a documentation page.

The three database drivers are optional at build time: `make build-lite` drops
them and the binary goes from 22 MB to 14 MB.

### Layout

```
api/          the wire contracts, as .proto
config/       the shipped defaults — this is what a deployment replaces
examples/     both wire formats, and the demo catalogue
internal/     pure core (model, rules, query, chart, mapping…) and the shell
  store/      the persistence contract, its four backends, and one shared suite
  persist/    what puts the store in the data path without any driver knowing
web/          templates, stylesheet, fonts, htmx
```

The pure core holds no I/O, no `time.Now()` and no globals: clocks and HTTP are
injected, which is what keeps every judgement the dashboard makes testable as a
plain function. See [`docs/coverage.md`](docs/coverage.md) for what the coverage
gate expects and the one documented way to opt out of it.

## Licence

Apache 2.0. The shipped configuration describes Indian public services from the
[API Setu](https://directory.apisetu.gov.in/) directory; it is an example, and
it is exactly what a deployment replaces.
