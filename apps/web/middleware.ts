import { NextResponse, type NextRequest } from "next/server";

import { REFRESH_COOKIE, SESSION_COOKIE, rotate, shouldRefresh } from "@/lib/session-refresh";

/**
 * Renews an expired session before the page renders, so a learner with a valid refresh cookie is never
 * bounced to the sign-in form.
 *
 * Two details make this work rather than nearly work:
 *
 *  1. The rotated cookies go on **the response**, so the browser stores them for next time; and the new
 *     access token also goes on **the forwarded request**, because the render happening now would otherwise
 *     still see the expired cookie and redirect to /login. Doing only one of the two produces a redirect
 *     loop that looks like a broken session.
 *  2. Failures are not fatal. If the refresh fails — an expired chain, a revoked account, reuse detected —
 *     the request continues without a session and the normal signed-out path takes over. A middleware that
 *     throws turns a sign-in page into a 500.
 */
export async function middleware(request: NextRequest) {
  const jar = { get: (name: string) => request.cookies.get(name)?.value };
  if (!shouldRefresh(request.nextUrl.pathname, jar)) {
    return NextResponse.next();
  }

  const apiBase = process.env.EDUMART_API_URL ?? "http://127.0.0.1:8080";
  // `rotate` puts the fresh cookie on the forwarded request *before* the response is constructed, because
  // `NextResponse.next({ request })` copies the headers when it is built.
  const result = await rotate(request, apiBase);

  const response = NextResponse.next({ request: { headers: result.requestHeaders } });

  // The API's own Set-Cookie values, attributes intact. When the refresh was rejected it clears them, which is
  // how a dead chain stops being presented on every request.
  for (const cookie of result.setCookies) {
    response.headers.append("set-cookie", cookie);
  }

  if (result.rotated) {
    // A rotated session must never be served from a cache keyed on the old one.
    response.headers.set("cache-control", "no-store");
  } else if (result.setCookies.length === 0) {
    // The chain is gone and the API said nothing: drop the cookie here so the browser stops presenting it.
    // `Path` must match the one the cookie was set with, or the deletion is a no-op.
    response.headers.append("set-cookie", `${REFRESH_COOKIE}=; Path=/v1/auth; Max-Age=0; HttpOnly; SameSite=Lax`);
    response.headers.append("set-cookie", `${SESSION_COOKIE}=; Path=/; Max-Age=0; HttpOnly; SameSite=Lax`);
  }

  return response;
}

export const config = {
  /**
   * Every page and the API proxy, but not static assets: the middleware can trigger a network call, and a
   * call for every image and chunk is a cost with no session to renew.
   */
  matcher: ["/((?!_next/static|_next/image|favicon.ico|icon.svg|icon-maskable.svg|sw.js|manifest.webmanifest).*)"],
};
