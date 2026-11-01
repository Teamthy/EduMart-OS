import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { proxy } from "@/lib/api-proxy";

/**
 * The `/v1` proxy, tested as a function rather than through a browser.
 *
 * The bug this file exists for is a **configuration lifetime** bug, and no amount of clicking would have
 * found it: it was one variable read at build time by the rewrite and at run time by the server components,
 * which produced a container whose pages worked and whose API calls went to the wrong host (or the
 * reverse). The first test below is that exact case, stated as an assertion.
 */

const originalFetch = globalThis.fetch;

function proxyRequest(path: string, init: RequestInit = {}): Request {
  return new Request(`http://localhost:3000${path}`, init);
}

/** Captures what the proxy sent upstream and returns a canned response. */
/** `calls[0]` with the undefined that `noUncheckedIndexedAccess` (correctly) adds to it. */
function call(calls: { url: string; init: RequestInit }[], index = 0) {
  const found = calls[index];
  if (!found) {
    throw new Error(`expected an upstream call at index ${index}, saw ${calls.length}`);
  }
  return found;
}

function stubUpstream(response: Response = new Response('{"ok":true}', { status: 200 })) {
  const calls: { url: string; init: RequestInit }[] = [];
  globalThis.fetch = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
    calls.push({ url: String(input), init: init ?? {} });
    return response;
  }) as unknown as typeof fetch;
  return calls;
}

beforeEach(() => {
  vi.stubEnv("EDUMART_API_URL", "http://api.internal:8080");
});

afterEach(() => {
  globalThis.fetch = originalFetch;
  vi.unstubAllEnvs();
});

describe("the /v1 proxy", () => {
  it("reads EDUMART_API_URL when the request arrives, not when the bundle was built", async () => {
    // This is the regression. With the old rewrite the destination was frozen by `next build`, so this
    // test could not have been written at all — the value was in a manifest, not in code that runs.
    vi.stubEnv("EDUMART_API_URL", "http://a-different-environment:8080");
    const calls = stubUpstream();

    await proxy(proxyRequest("/v1/subjects"));

    expect(calls).toHaveLength(1);
    expect(call(calls).url).toBe("http://a-different-environment:8080/v1/subjects");
  });

  it("keeps the path and the query string intact", async () => {
    // A proxy that drops a segment turns a working API into a 404 that nobody attributes to the proxy.
    const calls = stubUpstream();

    await proxy(proxyRequest("/v1/search?q=algebra&limit=10"));

    expect(call(calls).url).toBe("http://api.internal:8080/v1/search?q=algebra&limit=10");
  });

  it("tolerates a base URL with a trailing slash", async () => {
    vi.stubEnv("EDUMART_API_URL", "http://api.internal:8080/");
    const calls = stubUpstream();

    await proxy(proxyRequest("/v1/me"));

    expect(call(calls).url).toBe("http://api.internal:8080/v1/me");
  });

  it("carries the method and the body of a write", async () => {
    const calls = stubUpstream(new Response('{"order":{"id":"o1"}}', { status: 201 }));

    const response = await proxy(
      proxyRequest("/v1/orders", {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ productId: "p1" }),
      }),
    );

    expect(call(calls).init.method).toBe("POST");
    expect(new TextDecoder().decode(call(calls).init.body as ArrayBuffer)).toBe('{"productId":"p1"}');
    expect(response.status).toBe(201);
  });

  it("passes the status through untouched, including an error the UI renders", async () => {
    stubUpstream(
      Response.json(
        { error: { code: "UNAUTHENTICATED", message: "Please sign in to continue.", request_id: "abc" } },
        { status: 401 },
      ),
    );

    const response = await proxy(proxyRequest("/v1/me"));

    expect(response.status).toBe(401);
    await expect(response.json()).resolves.toMatchObject({ error: { code: "UNAUTHENTICATED" } });
  });

  it("forwards the session cookie upstream and the Set-Cookie back down", async () => {
    const calls = stubUpstream(
      new Response("{}", {
        status: 200,
        headers: [
          ["set-cookie", "edumart_session=abc; Path=/; HttpOnly; SameSite=Lax"],
          ["set-cookie", "edumart_refresh=def; Path=/; HttpOnly; SameSite=Lax"],
        ],
      }),
    );

    const response = await proxy(
      proxyRequest("/v1/me", { headers: { cookie: "edumart_session=abc" } }),
    );

    const sent = new Headers(call(calls).init.headers as Headers);
    expect(sent.get("cookie")).toBe("edumart_session=abc");

    // Two cookies must stay two headers. Collapsing them into one comma-joined value is the classic way a
    // proxy logs everybody out, and it is invisible until a session expires unexpectedly.
    expect(response.headers.getSetCookie()).toEqual([
      "edumart_session=abc; Path=/; HttpOnly; SameSite=Lax",
      "edumart_refresh=def; Path=/; HttpOnly; SameSite=Lax",
    ]);
  });

  it("does not forward headers that describe the hop instead of the request", async () => {
    const calls = stubUpstream();

    await proxy(
      proxyRequest("/v1/subjects", {
        headers: { connection: "keep-alive", "x-request-id": "keep-me" },
      }),
    );

    const sent = new Headers(call(calls).init.headers as Headers);
    expect(sent.get("connection")).toBeNull();
    expect(sent.get("x-request-id")).toBe("keep-me");
  });

  it("answers a dead API with the API's own error envelope, and no internal host", async () => {
    globalThis.fetch = vi.fn(async () => {
      throw new TypeError("fetch failed");
    }) as unknown as typeof fetch;

    const response = await proxy(proxyRequest("/v1/subjects"));

    expect(response.status).toBe(502);
    const body = await response.json();
    expect(body.error.code).toBe("SERVICE_UNAVAILABLE");
    expect(body.error.request_id).toBe("");
    // The upstream address must never reach the browser: it is internal topology.
    expect(JSON.stringify(body)).not.toContain("api.internal");
  });

  it("reports a timeout differently from a refusal", async () => {
    globalThis.fetch = vi.fn(async () => {
      const error = new Error("The operation was aborted due to timeout");
      error.name = "TimeoutError";
      throw error;
    }) as unknown as typeof fetch;

    const response = await proxy(proxyRequest("/v1/subjects"));

    expect(response.status).toBe(502);
    await expect(response.json()).resolves.toMatchObject({
      error: { message: "The API did not respond in time." },
    });
  });
});
