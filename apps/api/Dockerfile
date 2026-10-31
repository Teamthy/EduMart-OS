# The API as a container image.
#
#   docker build -f apps/api/Dockerfile --build-arg VERSION=$(git rev-parse --short HEAD) \
#     -t edumart-api:local apps/api
#   docker run --rm -p 8080:8080 -e EDUMART_DRIVER=postgres -e EDUMART_DSN='postgres://…' edumart-api:local
#
# Three decisions worth stating, because each of them is a bug the alternative would have shipped:
#
# 1. **Built and run on the same Debian release.** `EDUMART_DRIVER=sqlite3` uses a cgo SQLite, so the binary
#    links against glibc. A `scratch` or `alpine` runtime would build and then fail at the first request with
#    a missing loader, which is the kind of failure that only shows up once the image is in a registry.
# 2. **Not root, ever.** The image declares a numeric UID so a Kubernetes `runAsNonRoot` check can verify it
#    without resolving a name, and the process writes nothing to disk: the database is external and
#    `EDUMART_SEED` fixtures go into it, not into the filesystem.
# 3. **The healthcheck is liveness, not readiness.** `/healthz` is cheap and touches no dependency, so a
#    brief database outage does not make the container runtime restart a healthy process. Readiness is
#    `/readyz`, and it belongs to whatever is routing traffic — see docs/DEPLOYMENT.md.

# ── build ─────────────────────────────────────────────────────────────────────────────────────────────
FROM golang:1.27-bookworm AS build

WORKDIR /src

# Dependencies first: they change far less often than the source, so this layer stays cached across builds.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# The release identity is stamped in, not guessed at run time: a rollout that cannot tell you which build
# answered a request cannot tell you whether it finished.
ARG VERSION=dev
RUN CGO_ENABLED=1 go build -trimpath \
      -ldflags "-s -w -X main.version=${VERSION}" \
      -o /out/api ./cmd/api

# ── runtime ───────────────────────────────────────────────────────────────────────────────────────────
FROM debian:bookworm-slim

# ca-certificates: the API calls the payment provider over TLS, and a container without a trust store
# fails every refund with an opaque x509 error. tzdata: exam deadlines are computed in time zones.
# curl: only for the healthcheck below; it is not exposed to a client.
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl tzdata \
 && rm -rf /var/lib/apt/lists/* \
 && groupadd --gid 10001 edumart \
 && useradd --uid 10001 --gid 10001 --no-create-home --shell /usr/sbin/nologin edumart

COPY --from=build /out/api /usr/local/bin/api

USER 10001:10001
EXPOSE 8080

# Fail loudly instead of silently: a migration job that exits 0 when the schema is behind is a deploy that
# reports success and serves 500s.
ENTRYPOINT ["/usr/local/bin/api"]

HEALTHCHECK --interval=10s --timeout=3s --start-period=20s --retries=3 \
  CMD curl -fsS "http://127.0.0.1:${PORT:-8080}/healthz" || exit 1
