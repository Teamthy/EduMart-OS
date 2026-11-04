import { afterEach, describe, expect, it, vi } from "vitest";

import {
  LiveApiError,
  acknowledgeWarning,
  proctorDesk,
  reopenAttempt,
  sendProctorWarning,
  sessionWarnings,
  voidAttempt,
} from "@/lib/live-api";

interface Call {
  url: string;
  method: string;
  body: unknown;
}

/**
 * Stub the browser's fetch and record what the client actually sent.
 *
 * The proctoring client is thin on purpose — the interesting behaviour is on the server — so what is worth
 * testing here is the part that would break silently: the envelope. A warning that posts `sessionId` instead of
 * `sessionIds` is a warning to nobody, and nothing on screen would say so.
 */
function stubFetch(response: { ok?: boolean; status?: number; json?: unknown } = {}) {
  const calls: Call[] = [];
  const fetchImpl = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    calls.push({
      url: String(input),
      method: init?.method ?? "GET",
      body: init?.body ? JSON.parse(String(init.body)) : undefined,
    });
    return {
      ok: response.ok ?? true,
      status: response.status ?? 200,
      text: async () => JSON.stringify(response.json ?? {}),
    } as unknown as Response;
  });
  vi.stubGlobal("fetch", fetchImpl);
  return calls;
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("the proctor's desk", () => {
  it("reads the sitting from the exam's own proctor route", async () => {
    const calls = stubFetch({
      json: {
        sitting: { examId: "exam_1", inProgress: 2, flagged: 1, unreadWarnings: 1 },
        candidates: [{ sessionId: "sesn_1", status: "in_progress", answered: 3, questions: 20 }],
        actions: [],
      },
    });

    const desk = await proctorDesk("exam_1");

    expect(calls[0].url).toBe("/v1/exams/exam_1/proctor");
    expect(calls[0].method).toBe("GET");
    expect(desk.sitting.inProgress).toBe(2);
    expect(desk.candidates[0].answered).toBe(3);
  });

  it("addresses a warning to explicit session ids, never to the sitting", async () => {
    const calls = stubFetch({ json: { issued: 1, warnings: [] } });

    await sendProctorWarning("exam_1", { sessionIds: ["sesn_1", "sesn_2"], kind: "attention" });

    expect(calls[0].url).toBe("/v1/exams/exam_1/proctor/warnings");
    expect(calls[0].method).toBe("POST");
    // The plural is the contract: `sessionId` would be silently ignored by the server and warn nobody.
    expect(calls[0].body).toMatchObject({ sessionIds: ["sesn_1", "sesn_2"], kind: "attention" });
  });

  it("sends the host's own words only for a notice", async () => {
    const calls = stubFetch({ json: { issued: 1, warnings: [] } });

    await sendProctorWarning("exam_1", { sessionIds: ["sesn_1"], kind: "time" });
    await sendProctorWarning("exam_1", { sessionIds: ["sesn_1"], kind: "notice", note: "Stay on this tab." });

    // A note attached to a server-composed kind would be dropped by the server anyway; not sending it keeps the
    // client honest about where the words came from.
    expect(calls[0].body).not.toHaveProperty("note");
    expect(calls[1].body).toMatchObject({ kind: "notice", note: "Stay on this tab." });
  });

  it("carries a void's reason, and an error's message back to the console", async () => {
    const calls = stubFetch({
      ok: false,
      status: 409,
      json: { error: { code: "CONFLICT", message: "Only an attempt that is still in progress can be annulled." } },
    });

    await expect(voidAttempt("exam_1", "sesn_1", "Photographing the paper")).rejects.toMatchObject({
      code: "CONFLICT",
      status: 409,
      message: "Only an attempt that is still in progress can be annulled.",
    });
    expect(calls[0].body).toMatchObject({ sessionId: "sesn_1", reason: "Photographing the paper" });
  });

  it("reopens through its own route, so the log records an intervention rather than a delete", async () => {
    const calls = stubFetch({ json: { candidate: { sessionId: "sesn_1", status: "in_progress" } } });

    const { candidate } = await reopenAttempt("exam_1", "sesn_1", "annulled the wrong attempt");

    expect(calls[0].url).toBe("/v1/exams/exam_1/proctor/reopen");
    expect(calls[0].body).toMatchObject({ sessionId: "sesn_1", reason: "annulled the wrong attempt" });
    expect(candidate.status).toBe("in_progress");
  });
});

describe("a warning reaching the candidate", () => {
  it("polls the candidate's own attempt for warnings", async () => {
    const calls = stubFetch({
      json: {
        unread: 1,
        warnings: [
          {
            id: "warn_1",
            sessionId: "sesn_1",
            kind: "attention",
            note: "Your attempt has recorded 2 signal(s)",
            issuedAt: "2026-11-04T10:00:00Z",
            acknowledgedAt: null,
          },
        ],
      },
    });

    const { unread, warnings } = await sessionWarnings("sesn_1");

    expect(calls[0].url).toBe("/v1/assessment-sessions/sesn_1/warnings");
    expect(unread).toBe(1);
    expect(warnings[0].acknowledgedAt).toBeNull();
  });

  it("acknowledges one warning by id, and reports a refusal as the server worded it", async () => {
    const calls = stubFetch({
      json: { warning: { id: "warn_1", acknowledgedAt: "2026-11-04T10:00:09Z" } },
    });

    const { warning } = await acknowledgeWarning("sesn_1", "warn_1");

    expect(calls[0].url).toBe("/v1/assessment-sessions/sesn_1/warnings/warn_1/acknowledge");
    expect(calls[0].method).toBe("POST");
    expect(warning.acknowledgedAt).toBe("2026-11-04T10:00:09Z");

    stubFetch({ ok: false, status: 403, json: { error: { code: "FORBIDDEN", message: "This attempt belongs to another learner." } } });
    const refusal = await acknowledgeWarning("sesn_1", "warn_1").catch((error: unknown) => error);
    expect(refusal).toBeInstanceOf(LiveApiError);
    expect((refusal as LiveApiError).message).toBe("This attempt belongs to another learner.");
  });

  it("says the network dropped rather than inventing a server error", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => {
      throw new Error("offline");
    }));

    await expect(sessionWarnings("sesn_1")).rejects.toMatchObject({ code: "NETWORK_ERROR", status: 0 });
  });
});
