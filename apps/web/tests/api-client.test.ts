import { describe, expect, it, vi } from "vitest";

import { ApiClient, ApiError, formatCountdown, formatMoney, daysUntil, initials } from "@edumart/shared";

interface StubResponse {
  ok?: boolean;
  status?: number;
  json?: unknown;
  text?: string;
}

function clientWith(response: StubResponse) {
  const fetchImpl = vi.fn(
    async () =>
      ({
        ok: response.ok ?? true,
        status: response.status ?? 200,
        text: async () => response.text ?? JSON.stringify(response.json ?? {}),
      }) as unknown as Response,
  );

  return { fetchImpl, client: new ApiClient({ baseUrl: "http://api.test", fetchImpl: fetchImpl as unknown as typeof fetch }) };
}

describe("ApiClient error contract", () => {
  it("surfaces the server's code, message, request id and field errors", async () => {
    const { client } = clientWith({
      ok: false,
      status: 400,
      json: {
        error: {
          code: "VALIDATION_ERROR",
          message: "Please fix the highlighted fields.",
          request_id: "req_123",
          details: [{ field: "email", message: "Enter a valid email address." }],
        },
      },
    });

    await expect(client.login("bad", "bad")).rejects.toMatchObject({
      code: "VALIDATION_ERROR",
      status: 400,
      requestId: "req_123",
      message: "Please fix the highlighted fields.",
    });

    try {
      await client.login("bad", "bad");
    } catch (error) {
      expect(error).toBeInstanceOf(ApiError);
      expect((error as ApiError).fieldErrors).toEqual([{ field: "email", message: "Enter a valid email address." }]);
    }
  });

  it("reports a conflict's current version to the caller", async () => {
    const { client } = clientWith({
      ok: false,
      status: 409,
      json: {
        error: {
          code: "CONFLICT",
          message: "This answer was updated on another device.",
          request_id: "req_9",
          details: { current_version: 4 },
        },
      },
    });

    await expect(
      client.saveAnswer("s1", "qv1", { selectedOptionId: "opt_1", expectedVersion: 3, clientSequence: 2 }),
    ).rejects.toMatchObject({ code: "CONFLICT", details: { current_version: 4 } });
  });

  it("turns a transport failure into an offline error rather than a generic crash", async () => {
    const fetchImpl = vi.fn(async () => {
      throw new TypeError("Failed to fetch");
    });
    const client = new ApiClient({ baseUrl: "", fetchImpl: fetchImpl as unknown as typeof fetch });

    await expect(client.dashboard()).rejects.toMatchObject({ code: "NETWORK_ERROR", status: 0 });
  });

  it("calls onUnauthenticated exactly once for a 401", async () => {
    const onUnauthenticated = vi.fn();
    const fetchImpl = vi.fn(
      async () =>
        ({
          ok: false,
          status: 401,
          text: async () => JSON.stringify({ error: { code: "UNAUTHENTICATED", message: "Sign in", request_id: "r" } }),
        }) as unknown as Response,
    );
    const client = new ApiClient({ baseUrl: "", onUnauthenticated, fetchImpl: fetchImpl as unknown as typeof fetch });

    await expect(client.me()).rejects.toBeInstanceOf(ApiError);
    expect(onUnauthenticated).toHaveBeenCalledTimes(1);
  });
});

describe("formatters", () => {
  it("formats naira from minor units without inventing decimals", () => {
    expect(formatMoney(450000)).toBe("₦4,500");
    expect(formatMoney(0)).toBe("₦0");
  });

  it("never renders a negative countdown", () => {
    expect(formatCountdown(-5)).toBe("00:00");
    expect(formatCountdown(65)).toBe("01:05");
    expect(formatCountdown(3661)).toBe("1:01:01");
  });

  it("clamps exam countdown at zero", () => {
    expect(daysUntil(null)).toBeNull();
    expect(daysUntil("2020-01-01")).toBe(0);
  });

  it("derives initials from one or two names", () => {
    expect(initials("Amara Okafor")).toBe("AO");
    expect(initials("Amara")).toBe("AM");
    expect(initials("  ")).toBe("?");
  });
});

describe("Verification requests", () => {
  it("sends the channel and, for phone, the number — and returns where the code went", async () => {
    const { client, fetchImpl } = clientWith({
      status: 202,
      json: {
        sent: true,
        channel: "phone",
        destination: "+23••••••••567",
        expires_at: "2026-10-29T09:30:00Z",
        resend_after: 60,
        attempts_allowed: 5,
      },
    });

    const issued = await client.requestVerification("phone", "0803 123 4567");

    expect(issued.destination).toBe("+23••••••••567");
    const [, init] = fetchImpl.mock.calls[0];
    expect(JSON.parse(String(init?.body))).toEqual({ channel: "phone", phone: "0803 123 4567" });
  });

  it("confirms with exactly the channel and code the user typed", async () => {
    const { client, fetchImpl } = clientWith({
      json: { verified: true, channel: "email", user: { emailVerified: true } },
    });

    const result = await client.confirmVerification("email", "123456");

    expect(result.user.emailVerified).toBe(true);
    const [url, init] = fetchImpl.mock.calls[0];
    expect(String(url)).toContain("/v1/auth/verification/confirm");
    expect(JSON.parse(String(init?.body))).toEqual({ channel: "email", code: "123456" });
  });
});
