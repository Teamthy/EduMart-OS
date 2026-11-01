/**
 * `/healthz` proxies the API's liveness probe, exactly as the old rewrite did.
 *
 * It is deliberately **not** what the web deployment probes: this route fails whenever the API is down, and
 * a readiness probe that depends on another service takes every web pod out of rotation during that
 * service's bad minute. The web containers probe a static asset instead — see `apps/web/Dockerfile`.
 */

import { proxy } from "@/lib/api-proxy";

export async function GET(request: Request) {
  return proxy(request);
}
