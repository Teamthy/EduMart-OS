/**
 * Silent session rotation for the browser.
 *
 * The API has always issued a single-use refresh token with reuse detection. No browser ever used it: the
 * web app kept only the access cookie and signed the learner in again when it expired, so the refresh path
 * was dead code in production and "rotating tokens" described the test suite rather than the product.
 *
 * The mechanism relies on one property of cookies rather than on client-side bookkeeping: **the browser
 * deletes a cookie when its `Expires` passes.** So "access cookie missing, refresh cookie present" is exactly
 * "the access token expired and the session can still be renewed" — no expiry timestamp to keep in a second
 * place, and no clock comparison that can drift from the server's.
 *
 * This module is pure: it decides whether to refresh and performs the exchange. `middleware.ts` wires it to
 * Next.js.
 */

export const SESSION_COOKIE = "edumart_session";
export const REFRESH_COOKIE = "edumart_refresh";
/**
 * Must match `auth.RefreshCookiePath` in the Go service. The browser only sends the refresh cookie to this
 * path, which is why the refresh call is made with that path rather than a bare `/`.
 */
export const REFRESH_PATH = "/v1/auth";
export const REFRESH_ENDPOINT = `${REFRESH_PATH}/refresh`;

/** Endpoints that must never trigger a refresh: the refresh itself, sign-in and sign-out. */
const AUTH_PATHS = [`${REFRESH_PATH}/refresh`, `${REFRESH_PATH}/login`, `${REFRESH_PATH}/logout`, `${REFRESH_PATH}/register`];

export interface CookieJar {
  get(name: string): string | undefined;
}

/**
 * shouldRefresh reports whether this request needs a silent rotation.
 *
 * Three refusals matter as much as the acceptance:
 *  - no refresh cookie: nothing to rotate with, so the visitor is simply signed out;
 *  - an access cookie that still exists: the browser has not expired it, and refreshing on every request
 *    would rotate a working token continuously and turn a long session into a stream of writes;
 *  - a request for an auth endpoint: refreshing while signing in would race the sign-in's own cookies.
 */
export function shouldRefresh(pathname: string, jar: CookieJar): boolean {
  if (AUTH_PATHS.some((path) => pathname === path || pathname.startsWith(`${path}/`))) return false;
  if (!jar.get(REFRESH_COOKIE)) return false;
  if (jar.get(SESSION_COOKIE)) return false;
  return true;
}

/**
 * rotate prepares everything a middleware needs to renew a session, and nothing else, so the ordering that
 * makes it correct can be tested without a Next.js runtime.
 *
 * The ordering is the whole point. A `NextResponse.next({ request: { headers } })` reads the headers **when it
 * is constructed**, so the fresh cookie has to be placed on the forwarded request *before* the response
 * exists. Getting that backwards was a real defect here: the rotation succeeded, the browser stored the new
 * cookies, and the page still redirected to /login because the render had already been handed the expired
 * one. It presents as a sign-in loop and looks like a session bug rather than an ordering bug.
 */
export async function rotate(
  request: Request,
  apiBase: string,
): Promise<{ requestHeaders: Headers; setCookies: string[]; rotated: boolean; failure?: string }> {
  const requestHeaders = new Headers(request.headers);
  const result = await refreshSession(request, apiBase);

  if (result.ok && result.sessionToken) {
    requestHeaders.set("cookie", withSessionCookie(request.headers.get("cookie") ?? "", result.sessionToken));
  }

  return {
    requestHeaders,
    setCookies: result.setCookies,
    rotated: result.ok,
    failure: result.ok ? undefined : result.reason,
  };
}

export interface RefreshResult {
  ok: boolean;
  /** Cookies to set on the response, exactly as the API sent them (attributes preserved). */
  setCookies: string[];
  /** The new session token, for the forwarded request that renders the page. */
  sessionToken?: string;
  /** Present when the exchange failed; the caller decides whether to surface it. */
  reason?: string;
}

/**
 * refreshSession exchanges the refresh cookie for a new pair.
 *
 * `setCookies` is passed through untouched rather than re-serialised here: the API owns the attributes
 * (`Path`, `HttpOnly`, `Secure`, `SameSite`, `Max-Age`), and a proxy that rebuilds them is a second place for
 * a security attribute to be dropped. That is exactly the kind of bug this file's tests exist for.
 */
export async function refreshSession(request: Request, apiBase: string): Promise<RefreshResult> {
  const cookie = request.headers.get("cookie") ?? "";
  if (!cookie.includes(`${REFRESH_COOKIE}=`)) {
    return { ok: false, setCookies: [], reason: "no refresh cookie" };
  }

  let response: Response;
  try {
    response = await fetch(`${apiBase.replace(/\/+$/, "")}${REFRESH_ENDPOINT}`, {
      method: "POST",
      // The cookie is the credential. Sending it explicitly (rather than relying on a cookie jar this runtime
      // may not have) keeps the exchange identical in the edge runtime and in Node.
      headers: {
        cookie: cookie,
        accept: "application/json",
        "x-edumart-client": "web",
      },
      // A refresh that hangs must not hold the page request open.
      signal: AbortSignal.timeout(5000),
    });
  } catch (error) {
    return { ok: false, setCookies: [], reason: error instanceof Error ? error.message : "network error" };
  }

  const setCookies = response.headers.getSetCookie?.() ?? [];
  if (!response.ok) {
    // A failed refresh means the chain is gone (expired, revoked, or reuse detected). The API clears the
    // cookies itself; if it did not, the browser would keep presenting a token that can never work.
    return { ok: false, setCookies, reason: `refresh rejected (${response.status})` };
  }

  const sessionToken = readSessionToken(setCookies);
  return { ok: true, setCookies, sessionToken };
}

/** readSessionToken extracts the access token from the Set-Cookie headers the API just sent. */
export function readSessionToken(setCookies: string[]): string | undefined {
  for (const raw of setCookies) {
    const pair = raw.split(";")[0] ?? "";
    const separator = pair.indexOf("=");
    if (separator === -1) continue;
    const name = pair.slice(0, separator).trim();
    const value = pair.slice(separator + 1).trim();
    if (name === SESSION_COOKIE && value) return value;
  }
  return undefined;
}

/**
 * withSessionCookie returns the request cookie header as the page render should see it.
 *
 * The middleware's response cookies are for the *browser's next* request. The request being rendered now
 * still carries the expired access cookie, so without this the page renders as signed out immediately after
 * a successful refresh — a redirect loop that only stops when the browser retries.
 */
export function withSessionCookie(cookieHeader: string, sessionToken: string): string {
  const withoutSession = cookieHeader
    .split(";")
    .map((part) => part.trim())
    .filter((part) => part && !part.startsWith(`${SESSION_COOKIE}=`));
  return [...withoutSession, `${SESSION_COOKIE}=${sessionToken}`].join("; ");
}
