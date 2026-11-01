import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import {
  REFRESH_COOKIE,
  SESSION_COOKIE,
  readSessionToken,
  refreshSession,
  rotate,
  shouldRefresh,
  withSessionCookie,
} from "@/lib/session-refresh";

/**
 * Silent rotation, tested as a function.
 *
 * The interesting cases are the refusals: refreshing on every request would rotate a working token dozens of
 * times a minute, and refreshing during sign-in would race the cookies the sign-in is setting. Both are easy
 * to get wrong in a way that "the happy path works" never notices.
 */

const originalFetch = globalThis.fetch;

function jar(cookies: Record<string, string>) {
  return { get: (name: string) => cookies[name] };
}

function requestWithCookies(cookieHeader: string): Request {
  return new Request("http://localhost:3000/dashboard", { headers: { cookie: cookieHeader } });
}

beforeEach(() => {
  vi.unstubAllEnvs();
});

afterEach(() => {
  globalThis.fetch = originalFetch;
});

describe("shouldRefresh", () => {
  it("refreshes when the access cookie is gone and a refresh cookie remains", () => {
    // The browser deletes a cookie whose Expires has passed, so this is exactly "the access token expired".
    expect(shouldRefresh("/dashboard", jar({ [REFRESH_COOKIE]: "r1" }))).toBe(true);
  });

  it("does not refresh while the access cookie is still valid", () => {
    // Rotating a working token on every request would turn a read into a write and could race itself.
    expect(shouldRefresh("/dashboard", jar({ [REFRESH_COOKIE]: "r1", [SESSION_COOKIE]: "s1" }))).toBe(false);
  });

  it("does not refresh when there is nothing to refresh with", () => {
    expect(shouldRefresh("/dashboard", jar({}))).toBe(false);
    expect(shouldRefresh("/dashboard", jar({ [SESSION_COOKIE]: "s1" }))).toBe(false);
  });

  it.each(["/v1/auth/refresh", "/v1/auth/login", "/v1/auth/logout", "/v1/auth/register"])(
    "never refreshes on %s",
    (path) => {
      // Refreshing here would fight the endpoint's own cookies: sign-in with an expiring refresh cookie would
      // mint a session before the new one is issued.
      expect(shouldRefresh(path, jar({ [REFRESH_COOKIE]: "r1" }))).toBe(false);
    },
  );

  it("still refreshes on a normal API call", () => {
    expect(shouldRefresh("/v1/subjects", jar({ [REFRESH_COOKIE]: "r1" }))).toBe(true);
  });
});

describe("refreshSession", () => {
  it("posts to the auth path with the cookie, and returns the API's cookies untouched", async () => {
    const calls: { url: string; init: RequestInit }[] = [];
    const setCookies = [
      `${SESSION_COOKIE}=new-access; Path=/; HttpOnly; SameSite=Lax`,
      `${REFRESH_COOKIE}=new-refresh; Path=/v1/auth; HttpOnly; SameSite=Lax`,
    ];
    globalThis.fetch = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
      calls.push({ url: String(input), init: init ?? {} });
      return new Response("{}", { status: 200, headers: setCookies.map((value) => ["set-cookie", value]) });
    }) as unknown as typeof fetch;

    const result = await refreshSession(requestWithCookies(`${REFRESH_COOKIE}=old-refresh`), "http://api:8080");

    expect(result.ok).toBe(true);
    expect(calls[0]?.url).toBe("http://api:8080/v1/auth/refresh");
    expect(new Headers(calls[0]?.init.headers as Headers).get("cookie")).toBe(`${REFRESH_COOKIE}=old-refresh`);
    // Attributes stay exactly as the API wrote them: re-serialising here is how a Path or HttpOnly disappears.
    expect(result.setCookies).toEqual(setCookies);
    expect(result.sessionToken).toBe("new-access");
  });

  it("does not call the API at all when there is no refresh cookie", async () => {
    const fetchSpy = vi.fn();
    globalThis.fetch = fetchSpy as unknown as typeof fetch;

    const result = await refreshSession(requestWithCookies("other=1"), "http://api:8080");

    expect(result.ok).toBe(false);
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  it("reports a rejected refresh without throwing, and keeps the clearing cookies", async () => {
    globalThis.fetch = vi.fn(async () =>
      new Response('{"error":{"code":"SESSION_REUSED"}}', {
        status: 401,
        headers: [
          ["set-cookie", `${SESSION_COOKIE}=; Path=/; Max-Age=0`],
          ["set-cookie", `${REFRESH_COOKIE}=; Path=/v1/auth; Max-Age=0`],
        ],
      }),
    ) as unknown as typeof fetch;

    const result = await refreshSession(requestWithCookies(`${REFRESH_COOKIE}=replayed`), "http://api:8080");

    // A middleware that throws on this turns every page into a 500 instead of a sign-in screen.
    expect(result.ok).toBe(false);
    expect(result.reason).toContain("401");
    expect(result.setCookies).toHaveLength(2);
  });

  it("survives the API being unreachable", async () => {
    globalThis.fetch = vi.fn(async () => {
      throw new TypeError("fetch failed");
    }) as unknown as typeof fetch;

    const result = await refreshSession(requestWithCookies(`${REFRESH_COOKIE}=r1`), "http://api:8080");

    expect(result.ok).toBe(false);
    expect(result.reason).toBe("fetch failed");
    expect(result.setCookies).toEqual([]);
  });
});

describe("session cookie plumbing", () => {
  it("reads the access token out of the Set-Cookie headers", () => {
    expect(
      readSessionToken([`${REFRESH_COOKIE}=x; Path=/v1/auth`, `${SESSION_COOKIE}=abc; Path=/; HttpOnly`]),
    ).toBe("abc");
  });

  it("returns nothing when no access cookie was set", () => {
    expect(readSessionToken([`${REFRESH_COOKIE}=x; Path=/v1/auth`])).toBeUndefined();
  });

  it("replaces the expired access cookie on the forwarded request, leaving the rest alone", () => {
    // Without this the page being rendered right now still sees the old, expired cookie and redirects to
    // /login immediately after a successful refresh — a loop that only ends when the browser retries.
    const forwarded = withSessionCookie(`${SESSION_COOKIE}=expired; theme=dark; ${REFRESH_COOKIE}=r2`, "fresh");

    expect(forwarded).toContain(`${SESSION_COOKIE}=fresh`);
    expect(forwarded).not.toContain("expired");
    expect(forwarded).toContain("theme=dark");
    expect(forwarded).toContain(`${REFRESH_COOKIE}=r2`);
  });
});

describe("rotate (the ordering that makes the middleware work)", () => {
  it("puts the fresh session on the forwarded request, and the rotated cookies on the response", async () => {
    const setCookies = [
      `${SESSION_COOKIE}=fresh; Path=/; HttpOnly; SameSite=Lax`,
      `${REFRESH_COOKIE}=fresh-refresh; Path=/v1/auth; HttpOnly; SameSite=Lax`,
    ];
    globalThis.fetch = vi.fn(async () =>
      new Response("{}", { status: 200, headers: setCookies.map((value) => ["set-cookie", value]) }),
    ) as unknown as typeof fetch;

    const result = await rotate(requestWithCookies(`${SESSION_COOKIE}=expired; ${REFRESH_COOKIE}=old`), "http://api:8080");

    expect(result.rotated).toBe(true);
    // The render that happens next sees a valid cookie...
    expect(result.requestHeaders.get("cookie")).toBe(`${REFRESH_COOKIE}=old; ${SESSION_COOKIE}=fresh`);
    // ...and the browser is told to replace both. Setting only the second is a redirect loop; only the first
    // is a rotation the browser never stores and therefore repeats on every request forever.
    expect(result.setCookies).toEqual(setCookies);
  });

  it("leaves the forwarded request untouched when the refresh is rejected", async () => {
    globalThis.fetch = vi.fn(async () => new Response("{}", { status: 401 })) as unknown as typeof fetch;

    const result = await rotate(requestWithCookies(`${SESSION_COOKIE}=expired; ${REFRESH_COOKIE}=dead`), "http://api:8080");

    expect(result.rotated).toBe(false);
    expect(result.requestHeaders.get("cookie")).toBe(`${SESSION_COOKIE}=expired; ${REFRESH_COOKIE}=dead`);
    expect(result.failure).toContain("401");
  });
});
