/**
 * `/v1/*` — the only path from the browser to the Go API.
 *
 * The proxying itself lives in `lib/api-proxy.ts`, because a Next.js route file may only export HTTP
 * methods: `next build` rejects anything else ("`proxy` is not a valid Route export field"), and that
 * rejection is what turned this from a rewrite into a testable module in the first place.
 */

import { proxy } from "@/lib/api-proxy";

export async function GET(request: Request) {
  return proxy(request);
}

export const POST = GET;
export const PUT = GET;
export const PATCH = GET;
export const DELETE = GET;
export const HEAD = GET;
export const OPTIONS = GET;
