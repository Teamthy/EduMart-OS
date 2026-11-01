import { afterEach, describe, expect, it, vi } from "vitest";

vi.mock("next/headers", () => ({
  cookies: async () => ({ getAll: () => [], get: () => undefined }),
}));

const { getCurrentUser } = await import("@/lib/session");
const { ServerApiError } = await import("@/lib/server-api");

function mockFetch(handler: () => Promise<Response>) {
  vi.stubGlobal("fetch", vi.fn(handler));
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("getCurrentUser", () => {
  it("returns the principal for a valid session", async () => {
    mockFetch(async () =>
      new Response(JSON.stringify({ user: { id: "u_1", email: "a@b.test", roles: ["student"] } }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );

    await expect(getCurrentUser()).resolves.toMatchObject({ id: "u_1" });
  });

  it("returns null when the session is missing or revoked", async () => {
    mockFetch(async () =>
      new Response(JSON.stringify({ error: { code: "UNAUTHENTICATED", message: "Sign in to continue." } }), {
        status: 401,
        headers: { "Content-Type": "application/json" },
      }),
    );

    await expect(getCurrentUser()).resolves.toBeNull();
  });

  it("returns null rather than throwing when the API cannot be reached", async () => {
    // A host with no API — the deployment case — must still render the public pages. Every `/v1/*`
    // call answers 503 through the proxy, so the outage stays visible where it matters.
    mockFetch(async () => {
      throw new TypeError("fetch failed");
    });

    await expect(getCurrentUser()).resolves.toBeNull();
  });

  it("still throws on a real server error, which is not a signed-out visitor", async () => {
    mockFetch(async () =>
      new Response(JSON.stringify({ error: { code: "INTERNAL_ERROR", message: "Something went wrong." } }), {
        status: 500,
        headers: { "Content-Type": "application/json" },
      }),
    );

    await expect(getCurrentUser()).rejects.toBeInstanceOf(ServerApiError);
  });
});
