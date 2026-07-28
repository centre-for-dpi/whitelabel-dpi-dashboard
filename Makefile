# Development entry points. Everything here works on a clean clone with only Go
# installed — buf and protoc are needed solely to regenerate the wire contracts,
# and their output is committed so `make run` never requires them.
#
# Three exceptions, each of which says so in its own comment: the image and
# compose targets need Docker, the store contract suite needs the databases from
# docker-compose.test.yml, and `a11y-browser` needs a Chrome. None of them is on
# the path from a clone to a running dashboard.

GO       ?= go
BINARY   ?= whitelabel-dpi-dashboard
COVERDIR ?= .coverage
IMAGE    ?= ghcr.io/centre-for-dpi/whitelabel-dpi-dashboard
PORT     ?= 8080

.DEFAULT_GOAL := help

.PHONY: help
help: ## Show this help
	@grep -hE '^[a-zA-Z0-9_-]+:.*?## ' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

# --- run -------------------------------------------------------------------

.PHONY: run
run: ## Run the dashboard on :8080 with the demonstration data
	$(GO) run . -config config -addr :$(PORT)

.PHONY: validate
validate: ## Check the configuration without starting the server
	$(GO) run . -config config -validate

.PHONY: build
build: ## Build a static binary with everything embedded
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags='-s -w' -o $(BINARY) .

# The three database drivers are about a third of the binary. A deployment that
# has settled on one can drop the others; the tags are additive, so combine
# whichever you do not need.
#
#   all four backends   21 MB
#   sqlite only         17 MB
#   memory only         14 MB
.PHONY: build-lite
build-lite: ## Build without the database drivers (memory storage only)
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags='-s -w' \
	  -tags nosqlite,nopostgres,nomysql -o $(BINARY)-lite .

.PHONY: build-sqlite
build-sqlite: ## Build with SQLite only, dropping the Postgres and MySQL drivers
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags='-s -w' \
	  -tags nopostgres,nomysql -o $(BINARY)-sqlite .

# --- integration demos ------------------------------------------------------

.PHONY: demo-pull
demo-pull: ## Prove the pull driver end to end against a second instance
	@echo "Starting a seed instance on :8090 to act as the upstream…"
	@$(GO) run . -addr :8090 & echo $$! > .demo.pid; sleep 3
	@echo "Starting a pull instance on :8091 that fetches from it…"
	@mkdir -p .demo && sed 's/^driver: seed/driver: pull/' config/sources.yaml > .demo/sources.yaml
	@UPSTREAM_URL=http://127.0.0.1:8090/__example/upstream/services \
		$(GO) run . -config .demo -addr :8091 & echo $$! >> .demo.pid; sleep 4
	@echo; echo "seed  :8090 →" $$(curl -s localhost:8090/ | grep -oE 'w-summary[^"]*">[^<]+' | sed 's/.*">//')
	@echo "pull  :8091 →" $$(curl -s localhost:8091/ | grep -oE 'w-summary[^"]*">[^<]+' | sed 's/.*">//')
	@echo; echo "Identical verdicts through different drivers is the point."
	@kill $$(cat .demo.pid) 2>/dev/null; rm -rf .demo .demo.pid

.PHONY: demo-push
demo-push: ## Prove the push endpoint with curl and the committed fixture
	@mkdir -p .demo && sed 's/^driver: seed/driver: push/' config/sources.yaml > .demo/sources.yaml
	@DPI_INGEST_TOKEN=demo-token $(GO) run . -config .demo -addr :8092 & echo $$! > .demo.pid; sleep 3
	@echo "Before any push:" $$(curl -s -o /dev/null -w '%{http_code}' localhost:8092/readyz) "from /readyz"
	@curl -s -X POST localhost:8092/api/v1/ingest \
		-H "authorization: Bearer demo-token" \
		-H "content-type: application/json" \
		--data @examples/push/payload.json
	@echo "After the push: " $$(curl -s -o /dev/null -w '%{http_code}' localhost:8092/readyz) "from /readyz"
	@kill $$(cat .demo.pid) 2>/dev/null; rm -rf .demo .demo.pid

# --- generated artefacts ---------------------------------------------------

.PHONY: seed
seed: ## Regenerate the example fixtures in both wire formats
	$(GO) run ./cmd/seedgen

.PHONY: schema
schema: ## Regenerate schema/*.schema.json from the config structs
	$(GO) run ./cmd/schemagen -out schema

.PHONY: proto
proto: ## Regenerate the protobuf Go from api/**.proto (needs buf)
	buf lint
	buf generate

.PHONY: generate
generate: proto schema seed ## Regenerate everything that is committed but derived

# --- containers -------------------------------------------------------------

.PHONY: image
image: ## Build the container image
	docker build -t $(IMAGE):latest .

.PHONY: image-run
image-run: image ## Build and run the container, with nothing mounted
	docker run --rm -p $(PORT):8080 $(IMAGE):latest

TAG ?= latest

.PHONY: image-smoke
image-smoke: image smoke-image ## Build the image, then prove it serves

# Separate from the build so CI can smoke an image it has already built — a
# target that rebuilt would either waste the build or, worse, test a different
# image from the one about to be published.
#
# This is what answers "does the image work", which a successful build does not:
# a container that starts and then fails to serve is exactly the failure a
# build-only check misses.
.PHONY: smoke-image
smoke-image: ## Prove an already-built $(IMAGE):$(TAG) serves
	@set -e; \
	  name=dpi-smoke-$$$$; \
	  docker run -d --name $$name -p 18080:8080 $(IMAGE):$(TAG) >/dev/null; \
	  trap 'docker rm -f $$name >/dev/null 2>&1 || true' EXIT; \
	  for i in $$(seq 1 60); do \
	    curl -fsS -o /dev/null http://127.0.0.1:18080/healthz 2>/dev/null && break || sleep 0.5; \
	  done; \
	  for path in / /healthz /readyz /assets/app.css /assets/app.js /assets/theme.css; do \
	    code=$$(curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:18080$$path); \
	    test "$$code" = "200" || { echo "GET $$path -> $$code"; docker logs $$name; exit 1; }; \
	    echo "GET $$path -> $$code"; \
	  done; \
	  health=$$(docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}' $$name); \
	  echo "healthcheck: $$health"; \
	  test "$$health" != "unhealthy" || { docker logs $$name; exit 1; }; \
	  echo "image serves correctly"

.PHONY: compose
compose: ## Bring the whole thing up with docker compose
	docker compose up --build

# --- checks ----------------------------------------------------------------

.PHONY: test
test: ## Run the test suite
	$(GO) test ./...

# --- accessibility ----------------------------------------------------------
#
# Three layers, in increasing cost and decreasing frequency:
#
#   1. Contrast, from theme.yaml, inside `make validate`. Pure computation over
#      the palette, so a brand that breaks WCAG 1.4.3 or 1.4.11 cannot start.
#   2. Structure, inside `make test`. Every rule decidable from the rendered
#      HTML — names, aria references, heading order, unresolved term ids — over
#      every route, fragment and locale. No browser, so it gates every commit.
#   3. The rendered result, in `make a11y`. axe-core in headless Chrome, plus
#      the keyboard, focus-containment and forced-colours assertions axe cannot
#      make. Needs a Chrome binary, so it is a separate target.

A11YPORT ?= 8231

.PHONY: a11y
a11y: ## Audit accessibility without a browser (layers 1 and 2)
	@$(GO) run . -config config -validate >/dev/null
	@echo "contrast: the shipped palette satisfies every obligation"
	$(GO) test -run 'A11y|Accessible|TermID|Announcer|Auditor|Glyph|WCAG|Contrast|Script|Keyboard' ./... ./internal/a11y/

# Starts a server, runs the browser suite against it, and stops it again — so the
# target is one command with no setup, and a failure cannot leave a process
# behind. The suite skips itself, rather than failing, when there is no Chrome.
.PHONY: a11y-browser
a11y-browser: ## Audit accessibility in headless Chrome across the full matrix (layer 3)
	@$(GO) build -o /tmp/dpi-a11y-server . || exit 1
	@/tmp/dpi-a11y-server -config config -addr :$(A11YPORT) & echo $$! > /tmp/dpi-a11y.pid; \
	  trap 'kill $$(cat /tmp/dpi-a11y.pid) 2>/dev/null; rm -f /tmp/dpi-a11y.pid /tmp/dpi-a11y-server' EXIT; \
	  for i in $$(seq 1 40); do \
	    curl -fsS -o /dev/null http://127.0.0.1:$(A11YPORT)/healthz && break || sleep 0.25; \
	  done; \
	  cd test/a11y && $(GO) test -tags a11y -v -timeout 20m \
	    -base http://127.0.0.1:$(A11YPORT) $(if $(SHOTS),-shots $(SHOTS),) ./...

.PHONY: a11y-report
a11y-report: ## Run the browser suite and write a screenshot per matrix cell
	@$(MAKE) a11y-browser SHOTS=$(CURDIR)/.a11y-shots
	@echo "screenshots: $(CURDIR)/.a11y-shots"

# --- storage backends -------------------------------------------------------

# The DSNs match docker-compose.test.yml. They are exported rather than passed
# per-invocation so that `make test-db` and a bare `go test` behave the same
# once the servers are up.
PG_DSN      ?= postgres://dpi:dpi@127.0.0.1:55432/dpi_test?sslmode=disable
MYSQL_DSN   ?= dpi:dpi@tcp(127.0.0.1:53306)/dpi_test?parseTime=true
MARIADB_DSN ?= dpi:dpi@tcp(127.0.0.1:53307)/dpi_test?parseTime=true

.PHONY: test-db-up
test-db-up: ## Start Postgres, MySQL and MariaDB for the store contract suite
	docker compose -f docker-compose.test.yml up -d --wait

.PHONY: test-db-down
test-db-down: ## Stop and remove them
	docker compose -f docker-compose.test.yml down -v

# The identical contract suite against all four backends. SQLite needs nothing
# and runs on every `make test`; this is what proves the other three behave the
# same, and is the only evidence that "swap one config key" is true rather than
# merely intended.
.PHONY: test-db
test-db: test-db-up ## Run the store contract suite against all four backends
	DPI_TEST_POSTGRES_DSN='$(PG_DSN)' \
	DPI_TEST_MYSQL_DSN='$(MYSQL_DSN)' \
	DPI_TEST_MARIADB_DSN='$(MARIADB_DSN)' \
	$(GO) test ./internal/store/... -count=1 -v

.PHONY: cover
cover: ## Run tests and report coverage across the whole module
	@mkdir -p $(COVERDIR)
	@# -coverpkg is what attributes the end-to-end tests. Without it, packages
	@# exercised only through the server (render, server itself) report zero
	@# despite every line of them running on each request.
	@$(GO) test -coverprofile=$(COVERDIR)/cover.out -covermode=set \
		-coverpkg=$$($(GO) list ./... | grep -v /internal/gen | paste -sd,) ./... \
		| sed 's/ of statements in .*/ of the whole module/'
	@$(GO) tool cover -func=$(COVERDIR)/cover.out | tail -1

.PHONY: cover-report
cover-report: cover ## Show every line the tests do not reach
	@$(GO) tool cover -func=$(COVERDIR)/cover.out | grep -v "100.0%"

.PHONY: cover-html
cover-html: cover ## Open the coverage report in a browser
	$(GO) tool cover -html=$(COVERDIR)/cover.out

.PHONY: lint
lint: ## gofmt and vet
	@test -z "$$(gofmt -l . | tee /dev/stderr)" || (echo "run: gofmt -w ." && false)
	$(GO) vet ./...

.PHONY: drift
drift: ## Fail if a committed generated file is out of date
	@$(GO) run ./cmd/schemagen -check
	@git diff --quiet -- examples/ || (echo "examples/ is out of date; run make seed" && false)

.PHONY: static
static: ## Prove the binary still builds without cgo, as the scratch image needs
	CGO_ENABLED=0 $(GO) build -o /dev/null .

.PHONY: ci
# The single source of truth for what CI runs, so the workflow is one line and
# cannot drift from what a contributor can run locally. `validate` is here for the
# contrast gate: it is the only target that proves the shipped palette starts,
# which is a different claim from any test passing.
#
# The browser layer is deliberately not here. It needs a Chrome, and this target
# has to stay runnable on a clean clone; CI runs `a11y-browser` as its own job.
ci: lint static drift validate test cover ## Everything CI runs, except the browser suite

.PHONY: clean
clean: ## Remove build and coverage artefacts
	rm -rf $(BINARY) $(BINARY)-lite $(BINARY)-sqlite $(COVERDIR) dist .demo .demo.pid .a11y-shots
