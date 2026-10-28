import { describe, expect, it } from "vitest";
import { DEFAULT_POLICY, POLICY_REGISTRY, parsePolicy, scoreAttempt, pointsForCompletion, type SnapQuestion } from "../src/lib/domain/scoring";
import { canAcceptAnswer, effectiveDeadline, isResultReleased, isTerminal, seededShuffle, remainingSeconds } from "../src/lib/domain/exam";

function q(id: string, answer: string, points = 1): SnapQuestion {
  return {
    qvId: id,
    questionId: `q-${id}`,
    stem: "stem",
    options: [
      { id: "a", label: "A" },
      { id: "b", label: "B" },
    ],
    answerOptionId: answer,
    explanation: "",
    points,
    position: 1,
  };
}

describe("scoring", () => {
  const questions = [q("1", "a"), q("2", "b"), q("3", "a"), q("4", "b")];

  it("scores correct, incorrect and unanswered deterministically", () => {
    const result = scoreAttempt(
      questions,
      [
        { qvId: "1", selectedOptionId: "a" }, // correct
        { qvId: "2", selectedOptionId: "a" }, // incorrect
        { qvId: "3", selectedOptionId: "a" }, // correct
        { qvId: "4", selectedOptionId: null }, // unanswered
      ],
      DEFAULT_POLICY,
    );
    expect(result.raw).toBe(2);
    expect(result.max).toBe(4);
    expect(result.percent).toBe(50);
    expect(result.correct).toBe(2);
    expect(result.incorrect).toBe(1);
    expect(result.unanswered).toBe(1);
    expect(result.policyVersion).toBe(1);
  });

  it("is a pure function — the same inputs always produce the same score", () => {
    const responses = [{ qvId: "1", selectedOptionId: "a" }];
    const a = scoreAttempt(questions, responses, DEFAULT_POLICY);
    const b = scoreAttempt(questions, responses, DEFAULT_POLICY);
    expect(a).toEqual(b);
  });

  it("applies negative marking only when the policy enables it", () => {
    const negative = POLICY_REGISTRY[2];
    const result = scoreAttempt(questions, [{ qvId: "1", selectedOptionId: "b" }], negative);
    expect(result.perQuestion[0].awarded).toBe(-negative.wrong);
    // raw never goes below zero for display purposes
    expect(result.raw).toBe(0);
  });

  it("respects per-question point weights", () => {
    const weighted = [q("1", "a", 2), q("2", "b", 3)];
    const result = scoreAttempt(weighted, [{ qvId: "1", selectedOptionId: "a" }], DEFAULT_POLICY);
    expect(result.raw).toBe(2);
    expect(result.max).toBe(5);
    expect(result.percent).toBe(40);
  });

  it("parses a stored policy by version and falls back safely", () => {
    expect(parsePolicy(JSON.stringify({ version: 2 }).toString()).version).toBe(2);
    expect(parsePolicy("not json").version).toBe(1);
    expect(parsePolicy(null).version).toBe(1);
  });

  it("awards points independently of marks", () => {
    expect(pointsForCompletion("practice", 0)).toBe(10);
    expect(pointsForCompletion("practice", 90)).toBe(20);
    expect(pointsForCompletion("mock", 90)).toBe(50);
  });
});

describe("timed assessment state machine", () => {
  const start = new Date("2026-05-01T09:00:00.000Z");

  it("derives the deadline on the server from start + duration", () => {
    expect(effectiveDeadline(start.toISOString(), 1800)).toBe("2026-05-01T09:30:00.000Z");
  });

  it("accepts writes before the deadline and inside the grace window", () => {
    const session = { status: "in_progress", deadlineAt: "2026-05-01T09:30:00.000Z" };
    expect(canAcceptAnswer(session, new Date("2026-05-01T09:29:59.000Z")).accepted).toBe(true);
    expect(canAcceptAnswer(session, new Date("2026-05-01T09:30:03.000Z")).accepted).toBe(true);
  });

  it("rejects writes after the deadline with SESSION_EXPIRED", () => {
    const session = { status: "in_progress", deadlineAt: "2026-05-01T09:30:00.000Z" };
    const decision = canAcceptAnswer(session, new Date("2026-05-01T09:30:30.000Z"));
    expect(decision.accepted).toBe(false);
    if (!decision.accepted) expect(decision.code).toBe("SESSION_EXPIRED");
  });

  it("never revives a terminal attempt", () => {
    for (const status of ["submitted", "expired", "cancelled"]) {
      expect(isTerminal(status)).toBe(true);
      expect(canAcceptAnswer({ status, deadlineAt: "2099-01-01T00:00:00.000Z" }).accepted).toBe(false);
    }
    expect(isTerminal("in_progress")).toBe(false);
  });

  it("withholds results until release policy allows", () => {
    expect(isResultReleased({ status: "in_progress" })).toBe(false);
    expect(isResultReleased({ status: "submitted" }, "immediate")).toBe(true);
    expect(
      isResultReleased(
        { status: "submitted" },
        "on_close",
        "2026-05-02T09:00:00.000Z",
        new Date("2026-05-01T12:00:00.000Z"),
      ),
    ).toBe(false);
    expect(
      isResultReleased(
        { status: "submitted" },
        "on_close",
        "2026-05-02T09:00:00.000Z",
        new Date("2026-05-02T10:00:00.000Z"),
      ),
    ).toBe(true);
  });

  it("clamps remaining seconds at zero", () => {
    expect(remainingSeconds("2026-05-01T09:30:00.000Z", new Date("2026-05-01T09:31:00.000Z"))).toBe(0);
    expect(remainingSeconds("2026-05-01T09:30:00.000Z", new Date("2026-05-01T09:29:00.000Z"))).toBe(60);
  });

  it("produces a stable shuffle for one attempt seed", () => {
    const items = Array.from({ length: 20 }, (_, i) => i);
    expect(seededShuffle(items, "abc")).toEqual(seededShuffle(items, "abc"));
    expect(seededShuffle(items, "abc")).not.toEqual(seededShuffle(items, "xyz"));
    expect(seededShuffle(items, "abc").sort((a, b) => a - b)).toEqual(items);
  });
});
