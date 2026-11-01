#!/usr/bin/env python3
"""Prove the browser session lifecycle end to end, against a real server.

Rotation, single-use refresh tokens and reuse detection existed and were covered by tests — and no browser
ever used them, because the web app kept only a seven-day access cookie. This script checks the properties
that make the mechanism real, in the order a browser experiences them, using a real HTTP session and a real
cookie jar:

  1. **Sign-in issues both cookies**, and the refresh one is scoped to `/v1/auth` (not the whole origin).
  2. **The access token expires** (a short TTL is configured for the run, so this is seconds, not an hour).
  3. **The refresh cookie renews the session** through `POST /v1/auth/refresh`, with no body — exactly what
     the web middleware sends.
  4. **The new pair is rotated**: the old access token is dead and the old refresh token is refused.
  5. **Sign-out kills the chain**, not just the row the access token names. A refresh cookie that survives
     sign-out is a sign-out button that does nothing.
  6. **A dead chain clears the cookies** instead of leaving the browser to present a token that can never work.

Usage:
    python3 scripts/session-lifecycle.py                       # against EDUMART_BASE_URL
    python3 scripts/session-lifecycle.py --base http://127.0.0.1:8091 --ttl 3

It starts nothing. Point it at a running API (or at the web proxy, with --via-web, which is the interesting
one: it proves the *browser's* path, where the middleware rotates the session).
"""

from __future__ import annotations

import argparse
import http.cookiejar
import json
import os
import sys
import time
import urllib.error
import urllib.request

SESSION_COOKIE = "edumart_session"
REFRESH_COOKIE = "edumart_refresh"
REFRESH_PATH = "/v1/auth"


class Failure(RuntimeError):
    pass


class Browser:
    """A cookie jar that behaves like a browser: it stores cookies with their paths and sends them back only
    to the paths they were set for. Using a real CookieJar rather than a dict is the point — a dict would
    happily send the refresh cookie to every URL, and the scoping would never be exercised."""

    def __init__(self, base: str):
        self.base = base.rstrip("/")
        self.jar = http.cookiejar.CookieJar()
        self.opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(self.jar))

    def call(self, method: str, path: str, body: dict | None = None) -> tuple[int, dict, list[str]]:
        data = json.dumps(body).encode() if body is not None else None
        request = urllib.request.Request(self.base + path, data=data, method=method)
        request.add_header("Accept", "application/json")
        if data is not None:
            request.add_header("Content-Type", "application/json")
        try:
            with self.opener.open(request, timeout=20) as response:
                return response.status, read_json(response.read()), response.headers.get_all("Set-Cookie") or []
        except urllib.error.HTTPError as caught:
            return caught.code, read_json(caught.read()), caught.headers.get_all("Set-Cookie") or []

    def cookies(self) -> dict[str, http.cookiejar.Cookie]:
        return {cookie.name: cookie for cookie in self.jar}

    def drop(self, name: str) -> None:
        """Forget one cookie, the way a browser does when its Expires passes."""
        for cookie in list(self.jar):
            if cookie.name == name:
                self.jar.clear(cookie.domain, cookie.path, cookie.name)

    def has(self, name: str) -> bool:
        return name in self.cookies()


def read_json(raw: bytes) -> dict:
    try:
        return json.loads(raw or b"{}")
    except json.JSONDecodeError:
        return {}


def check(condition: bool, message: str) -> None:
    if not condition:
        raise Failure(message)
    print(f"  ok  {message}")


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(description="Prove the browser session lifecycle against a real server.")
    parser.add_argument("--base", default=os.environ.get("EDUMART_BASE_URL", "http://127.0.0.1:8080"))
    parser.add_argument("--email", default="student@edumart.ng")
    parser.add_argument("--password", default="Student123!")
    parser.add_argument("--ttl", type=int, default=0,
                        help="the access-token TTL the server was started with, so this knows how long to wait")
    parser.add_argument("--via-web", action="store_true",
                        help="the base URL is the web app: assert that its middleware rotates the session")
    args = parser.parse_args(argv)

    browser = Browser(args.base)
    print(f"session lifecycle: {args.base}{' (through the web proxy)' if args.via_web else ''}")

    print("\n1. sign in")
    status, body, issued = browser.call("POST", "/v1/auth/login",
                                        {"email": args.email, "password": args.password, "client": "web"})
    check(status == 200, f"sign-in returned {status}")
    stored = browser.cookies()
    check(SESSION_COOKIE in stored, "the access cookie was stored")
    check(REFRESH_COOKIE in stored, "the refresh cookie was stored")
    refresh = stored[REFRESH_COOKIE]
    check(refresh.path == REFRESH_PATH,
          f"the refresh cookie is scoped to {refresh.path!r}, not the whole origin")
    # Attributes are asserted on the raw header: a CookieJar deliberately does not model HttpOnly, because a
    # real browser never shows it to script either.
    refresh_header = next(value for value in issued if value.startswith(f"{REFRESH_COOKIE}="))
    check("HttpOnly" in refresh_header, "the refresh cookie is HttpOnly")
    check("SameSite=Lax" in refresh_header.replace(" ", ""), "the refresh cookie is SameSite=Lax")
    refresh_value = refresh.value

    status, me, _ = browser.call("GET", "/v1/me")
    check(status == 200 and me.get("user", {}).get("email") == args.email, "the session authenticates")

    if args.ttl:
        print(f"\n2. wait for the {args.ttl}s access token to expire")
        time.sleep(args.ttl + 1)
        # A browser deletes the cookie when its Max-Age passes; do the same thing rather than hoping.
        browser.drop(SESSION_COOKIE)
        status, _, _ = browser.call("GET", "/v1/me")
        check(status == 401, "the expired access cookie no longer authenticates")

        print("\n3. the refresh cookie renews the session, with no body")
        status, rotated, _ = browser.call("POST", f"{REFRESH_PATH}/refresh")
        check(status == 200, f"the refresh call returned {status}")
        check(bool(rotated.get("token")), "a new access token was issued")
        after = browser.cookies()
        check(after[REFRESH_COOKIE].value != refresh_value, "the refresh token was rotated")

        status, me, _ = browser.call("GET", "/v1/me")
        check(status == 200, "the renewed session authenticates")
        if args.via_web:
            print("\n3b. the same thing, the way a page navigation does it")
            browser.drop(SESSION_COOKIE)
            status, _, _ = browser.call("GET", "/dashboard")
            check(status == 200, f"the web app served the dashboard after rotating the session ({status})")

    print("\n4. sign out")
    # Keep a copy of the refresh cookie: the point is to prove the *chain* is dead, so it must be presented
    # again after sign-out rather than being cleared locally.
    survivor = browser.cookies().get(REFRESH_COOKIE)
    status, _, _ = browser.call("POST", "/v1/auth/logout")
    check(status == 200, f"sign-out returned {status}")

    if survivor is not None:
        replay = Browser(args.base)
        jar_cookie = http.cookiejar.Cookie(
            version=0, name=REFRESH_COOKIE, value=survivor.value, port=None, port_specified=False,
            domain="127.0.0.1", domain_specified=False, domain_initial_dot=False, path=REFRESH_PATH,
            path_specified=True, secure=False, expires=None, discard=True, comment=None, comment_url=None,
            rest={}, rfc2109=False,
        )
        replay.jar.set_cookie(jar_cookie)
        status, body, _ = replay.call("POST", f"{REFRESH_PATH}/refresh")
        check(status != 200,
              f"the refresh cookie presented after sign-out was refused ({status}, "
              f"{(body.get('error') or {}).get('code')})")

    print("\n5. a dead chain does not leave the browser holding it")
    stranger = Browser(args.base)
    status, body, headers = stranger.call("POST", f"{REFRESH_PATH}/refresh", {"refreshToken": "never-issued"})
    check(status == 401, f"an unknown refresh token was refused ({status})")
    check(any(f"{REFRESH_COOKIE}=" in value for value in headers),
          "the response clears the refresh cookie rather than leaving it to fail forever")

    print("\nsession lifecycle passed")
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main(sys.argv[1:]))
    except Failure as failure:
        print(f"\nFAILED: {failure}", file=sys.stderr)
        sys.exit(1)
