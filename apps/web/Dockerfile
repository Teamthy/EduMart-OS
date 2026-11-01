# The web app as a container image.
#
# The build context is the **repository root**, not apps/web: the web app depends on the `@edumart/shared`
# workspace package and on the root lockfile, so building from apps/web alone produces a trace that is
# missing a dependency and fails at the first request that touches it.
#
#   docker build -f apps/web/Dockerfile -t edumart-web:local .
#   docker run --rm -p 3000:3000 -e EDUMART_API_URL=http://api:8080 edumart-web:local
#
# EDUMART_API_URL is read **when a request arrives**, by the `/v1` proxy route handler and by server-rendered
# pages alike, so one image is promoted from staging to production without a rebuild. That is a change: the
# proxy used to be a `next build` rewrite, whose destination was frozen into the routes manifest, which made
# the image half-configurable and produced containers whose pages worked while their API calls went to the
# wrong host. `docs/DEPLOYMENT.md` records the measurement.

# ── dependencies ─────────────────────────────────────────────────────────────────────────────────────
FROM node:22-bookworm-slim AS deps

WORKDIR /repo
COPY package.json package-lock.json ./
COPY packages/shared/package.json packages/shared/
COPY apps/web/package.json apps/web/
# `npm ci` from the root lockfile: the workspace links are part of what is being built, and installing only
# apps/web would resolve a different tree from the one CI tested.
RUN npm ci --workspace @edumart/web --workspace @edumart/shared --include-workspace-root

# ── build ────────────────────────────────────────────────────────────────────────────────────────────
FROM deps AS build

# Deliberately **not** set here. A build that needs the API's address cannot be promoted between
# environments, and a build-time default is exactly how the wrong host gets shipped.
ENV NEXT_TELEMETRY_DISABLED=1

COPY packages/shared packages/shared
COPY apps/web apps/web
# The repository's own build command, not a second one that could drift from it. `@edumart/shared` is
# TypeScript source compiled by Next (`transpilePackages`), so there is no separate build step to forget.
RUN npm run build --workspace @edumart/web

# ── runtime ──────────────────────────────────────────────────────────────────────────────────────────
# `output: "standalone"` produces a server that needs only the traced files, so the runtime image carries no
# package manager, no dev dependencies and no source-to-build step at start-up.
FROM node:22-bookworm-slim AS runtime

WORKDIR /app

ENV NODE_ENV=production \
    NEXT_TELEMETRY_DISABLED=1 \
    PORT=3000 \
    HOSTNAME=0.0.0.0

RUN groupadd --gid 10002 edumart \
 && useradd --uid 10002 --gid 10002 --no-create-home --shell /usr/sbin/nologin edumart

# The monorepo trace keeps the workspace layout, so the server lives at apps/web/server.js rather than at the
# root. Static assets and public/ are not traced automatically and must be copied beside it.
COPY --from=build --chown=10002:10002 /repo/apps/web/.next/standalone ./
COPY --from=build --chown=10002:10002 /repo/apps/web/.next/static ./apps/web/.next/static
COPY --from=build --chown=10002:10002 /repo/apps/web/public ./apps/web/public

WORKDIR /app/apps/web

USER 10002:10002
EXPOSE 3000

# Liveness/readiness are a **static file**, not a page and not the proxied /healthz. A page renders against the
# API, so probing one would take every web pod out of rotation the moment the API hiccups — turning one
# dependency's bad minute into a total outage. This answers exactly "is this server serving?".
HEALTHCHECK --interval=10s --timeout=3s --start-period=15s --retries=3 \
  CMD node -e "fetch('http://127.0.0.1:'+(process.env.PORT||3000)+'/manifest.webmanifest').then(r=>process.exit(r.ok?0:1)).catch(()=>process.exit(1))"

CMD ["node", "server.js"]
