# The API

Everything the dashboard does over HTTP, and the choice that matters most: how
data gets in.

The reference is served by the deployment itself at **`/api`**, with every
request editable and sendable against the running instance. The document behind
it is at **`/api/openapi.json`**, and is committed at `api/openapi.json` so a
clean clone can hand it to a code generator without running anything.

That document is generated from the wire contracts rather than written alongside
them — `make openapi`, checked by `make drift` — so a proto field that changes
shape cannot leave the published description behind.

## Push or pull

Two ways in. They are alternatives rather than layers: exactly one is active,
chosen by a single line in `config/sources.yaml`.

| | Push | Pull |
|---|---|---|
| Who initiates | your collector | the dashboard, on a timer |
| You build | something that posts | nothing |
| Configured by | `driver: push` and a token env var | `driver: pull` and a mapping |
| Wire format | `dpi.v1.IngestRequest`, JSON or protobuf | whatever JSON you already serve |
| Can carry incidents | **yes** | no |
| Can carry error buckets | **yes** | no |
| Taxonomy validated | **yes**, per record | no |
| Reaches services behind a firewall | yes | only if the dashboard can reach them |
| History | send it, or let the dashboard roll its own up | roll-up, or map a nested series |

**Push is the richer of the two.** Incidents and error buckets have no mapping
expression, so a deployment that wants the drawer's incident timeline or its
error-code breakdown has to push. Every record is also checked against the
deployment's own `domain.yaml`: a `categoryId` nothing declares is rejected with
a message saying so, rather than becoming a service no filter can reach.

**Pull is the one you can turn on this afternoon.** If you already serve JSON
describing your services, a mapping in `sources.yaml` is the whole integration —
no scheduler, no credentials to distribute, nothing to deploy. What it costs you
is the two record types above, and the validation: a mapping that reads the wrong
field produces a board of unknowns rather than an error.

## Trying each one

Both are exercisable from `/api`, and both from a terminal.

### Push

```sh
DPI_INGEST_TOKEN=demo-token make demo-push
```

Or by hand, against a deployment running `driver: push`:

```sh
curl -X POST "$DASHBOARD/api/v1/ingest" \
  -H "authorization: Bearer $DPI_INGEST_TOKEN" \
  -H "content-type: application/json" \
  --data @examples/push/payload.json
```

The response is `200` whenever the body decoded, and says what happened:

```json
{ "accepted": 5, "rejected": 1,
  "errors": [ { "serviceId": "pension", "field": "categoryId",
                "message": "\"cat.pensions\" is not declared in domain.yaml" } ],
  "receivedAt": "2026-07-27T12:00:04.113Z" }
```

**A collector checks `rejected`, not the status code.** Partial acceptance is
deliberate: one malformed record in a batch of two hundred should not reject the
other hundred and ninety-nine.

Then read it back:

```sh
curl "$DASHBOARD/api/v1/services"
```

### Pull

The dashboard polls you, so there is nothing to send it. What there is instead is
a dry run: post the document your API would serve, plus the mapping you are
considering, and get back exactly what the dashboard would make of it.

```sh
curl -X POST "$DASHBOARD/api/v1/pull/preview" \
  -H "content-type: application/json" \
  --data '{
    "document": [ { "svc": "aadhaar", "up": 0.9992 } ],
    "mapping": {
      "map": { "id": "$.svc", "metrics.availability": "$.up" },
      "transform": { "metrics.availability": [ { "fn": "ratioToPercent" } ] }
    }
  }'
```

The mapping is the same structure `sources.yaml` takes, key for key, so a block
that works here can be pasted into the file and a block from the file pasted here
to find out what it does.

**The response carries the derived status, and that is the point.** Drop the
`ratioToPercent` from the example above and the mapping still reads every field
it was asked to — and every service comes back as a major outage, because 0.9992%
availability is what you told it. That is the failure the endpoint exists to
catch, and the one that is otherwise invisible until the board is live.

Records the mapping could not read are reported rather than dropped in silence:

```json
{ "services": [ … ], "skipped": [ { "index": 3, "reason": "no id" } ] }
```

Nothing is fetched. The document travels in the body, so this endpoint cannot be
used to make the dashboard issue requests from inside its own network.

## What the dashboard decides for itself

**Status is never reported, only derived.** `status`, `trends` and `rankMovement`
are absent from the ingest message rather than ignored, so the contract states it
rather than relying on you to read a comment. A service at 97.2% availability is
a major outage whatever its operator would prefer to call it, and the drawer
shows the reader which published threshold it crossed.

The same is true through pull: whatever an upstream calls its own status, the
dashboard evaluates the numbers.

## The rest of the surface

`/healthz` is liveness and never inspects the data. `/readyz` is readiness and
answers `503` until there is something to serve — which is what keeps a rolling
deploy from cutting traffic over to an empty page.

`/__example/upstream/services` serves the dashboard's own data in a deliberately
foreign shape — ratios rather than percentages, `IDENTITY` rather than
`cat.identity` — so the shipped pull mapping has something real to poll.
Switching to your own service is then one URL change against a path already
proven to work. It is absent under `driver: pull`, so a deployment cannot
re-publish a copy of what it polls.

The reader-facing pages and the fragments they swap in are in the document too.
They are HTML rather than JSON, and they are there because a surface only partly
written down is one nobody can reason about.

**Two operations are conditional**, and the document served by a deployment says
so in the operation's own description, naming the line in `sources.yaml` that
would enable it. The committed file always describes the whole surface.

## Deliberate limits

- **No CORS headers.** The console is same-origin with the API it documents, and
  `curl` does not need them. A browser client on another origin needs a proxy —
  which is a deployment decision, not one this binary should make for you.
- **No rate limiting and no idempotency key.** `id` is the upsert key, so
  replaying a payload converges; a collector that needs throttling has a
  reverse proxy in front of it.
- **The reference is English.** The chrome around it is translated like every
  other page, but the document itself — field names, enum values, error messages,
  descriptions — is not. It documents a wire contract that is English, and a
  translated shell around an English body would imply a translation that does not
  exist.
- **The renderer is third-party.** See `docs/accessibility.md` for why `/api` is
  the one page here outside the conformance claim, and what was done to keep the
  rest of it true.
