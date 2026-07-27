# A two-stage build producing a scratch image of about 21 MB.
#
# Scratch is possible because every dependency is pure Go — that is why
# modernc.org/sqlite was chosen over mattn/go-sqlite3, and why CGO_ENABLED=0
# below is a constraint the whole dependency budget was designed around rather
# than an optimisation applied at the end. The three database drivers are about
# a third of that size; a deployment that has settled on one can drop the others
# by adding their build tags to the go build line below (see `make build-lite`).
#
# Nothing needs to be mounted. Configuration, templates, stylesheet, fonts and
# htmx are all compiled into the binary, so `docker run` with no volumes and no
# environment produces a working dashboard — on memory storage, which is why it
# works without a writable filesystem. Set DPI_STORAGE_DRIVER and DATABASE_URL
# to keep history across restarts.

FROM golang:1.26-alpine AS build

WORKDIR /src

# Dependencies first, so a source-only change reuses the cached module layer.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# -trimpath keeps build-machine paths out of panics.
# -s -w drop the symbol table and DWARF, which is most of the binary's size.
RUN CGO_ENABLED=0 go build \
      -trimpath \
      -ldflags='-s -w' \
      -o /out/dashboard .


FROM scratch

# The certificate bundle, so the pull driver can reach an HTTPS upstream. It is
# the one thing a scratch image genuinely cannot do without — the zone database
# is embedded in the binary instead, via a time/tzdata import.
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

COPY --from=build /out/dashboard /dashboard

# Unprivileged. There is no user database in a scratch image, so the id is
# numeric — which is also what a Kubernetes runAsNonRoot check looks for.
USER 65534:65534

EXPOSE 8080

ENV DPI_ADDR=:8080 \
    DPI_LOG_FORMAT=json

# Distinct from /healthz: readiness means there is data to serve, so a rolling
# deploy does not cut traffic over to a dashboard that has not polled yet.
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
  CMD ["/dashboard", "-healthcheck"]

ENTRYPOINT ["/dashboard"]
