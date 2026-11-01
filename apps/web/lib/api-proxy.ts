/**
 * The `/v1/*` proxy, as a route handler instead of a Next.js rewrite.
 *
 * ## Why this is not a rewrite any more
 *
 * It used to be one line in `next.config.ts`:
 *
 *     { source: "/v1/:path*", destination: `${apiUrl}/v1/:path*` }
 *
 * with `apiUrl` read from `EDUMART_API_URL`. That looks equivalent to this file and is not, because
 * `next build` evaluates the rewrite once and writes the destination into the routes manifest. The
 * consequences were measured on a real standalone build of this app:
 *
 *   - built with the default (127.0.0.1:8080) and run with `EDUMART_API_URL` pointing at a live API on
 *     8091, the pages rendered and `GET /v1/subjects` still went to 8080 → `ECONNREFUSED`;
 *   - built with the right URL and run without the variable, the opposite happened: `GET /v1/subjects`
 *     worked and the server-rendered pages failed with "The API is unreachable".
 *
 * That is the worst shape a configuration bug can take: **a container that half works**, where the same
 * variable is read at two different times by two different mechanisms, and which half is broken depends
 * on how the image was built rather than on how it was run. An image that has to be rebuilt to be pointed
 * at another environment is also an image you cannot promote from staging to production.
 *
 * Here `EDUMART_API_URL` is read when a request arrives — the same lifetime as `lib/server-api.ts`, which
 * was always runtime. One variable, one meaning, one place to get it wrong.
 */

import { NextResponse } from "next/server";

/** Where server-side code reaches the API. Resolved per request, never at build time. */
function apiBase(): string {
  return (process.env.EDUMART_API_URL ?? "http://127.0.0.1:8080").replace(/\/+$/, "");
}

/**
 * Headers that describe *this* hop, not the request. Forwarding them corrupts the next hop: a stale
 * `content-length` truncates a body, and `connection`/`transfer-encoding` describe a socket that does not
 * exist on the other side.
 */
const HOP_BY_HOP = new Set([
  "connection",
  "keep-alive",
  "proxy-authenticate",
  "proxy-authorization",
  "te",
  "trailer",
  "transfer-encoding",
  "upgrade",
  "host",
  "content-length",
]);

/** The API's own timeout for a request is 30s; the proxy must not outlive it and hold a socket open. */
const UPSTREAM_TIMEOUT_MS = 30_000;

export async function proxy(request: Request): Promise<Response> {
  const incoming = new URL(request.url);
  // The path is taken from the request itself rather than rebuilt from the route params: rebuilding it is
  // one more place to drop a segment, and `/v1/subjects` silently becoming `/subjects` is a 404 nobody
  // attributes to the proxy.
  const target = `${apiBase()}${incoming.pathname}${incoming.search}`;

  const headers = new Headers();
  request.headers.forEach((value, key) => {
    if (!HOP_BY_HOP.has(key.toLowerCase())) {
      headers.set(key, value);
    }
  });

  // GET/HEAD must not carry a body; everything else is forwarded verbatim so a JSON payload, a form and a
  // binary upload all behave the same way they did through the rewrite.
  const method = request.method.toUpperCase();
  const body = method === "GET" || method === "HEAD" ? undefined : await request.arrayBuffer();

  let upstream: Response;
  try {
    upstream = await fetch(target, {
      method,
      headers,
      body,
      redirect: "manual",
      signal: AbortSignal.timeout(UPSTREAM_TIMEOUT_MS),
    });
  } catch (error) {
    // A 502 with a shape the client already understands, instead of a Next.js error page: the web UI
    // renders its "service unavailable" state from this, and a stack trace would leak the internal host.
    const timedOut = error instanceof Error && error.name === "TimeoutError";
    return NextResponse.json(
      {
        error: {
          code: "SERVICE_UNAVAILABLE",
          // Same envelope and the same field names as the Go API (`request_id`, `details`), so a client
          // parses one shape whether the API answered or the proxy could not reach it. `request_id` is
          // empty because no API request happened — inventing one would point support at a request the
          // logs have never seen.
          message: timedOut ? "The API did not respond in time." : "The API is unreachable.",
          request_id: "",
          details: [],
        },
      },
      { status: 502 },
    );
  }

  const responseHeaders = new Headers();
  upstream.headers.forEach((value, key) => {
    // `set-cookie` is handled separately: it is the one header that legitimately appears many times, and a
    // plain copy would collapse two cookies into one corrupt value.
    if (!HOP_BY_HOP.has(key.toLowerCase()) && key.toLowerCase() !== "set-cookie") {
      responseHeaders.set(key, value);
    }
  });
  const cookies = upstream.headers.getSetCookie?.() ?? [];
  for (const cookie of cookies) {
    responseHeaders.append("set-cookie", cookie);
  }

  return new Response(upstream.body, { status: upstream.status, headers: responseHeaders });
}
