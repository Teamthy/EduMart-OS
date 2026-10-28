import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";

/**
 * Integration tests against a real database file.
 *
 * The assessment engine is exercised end to end: start → autosave → retry → submit → score,
 * including the invariants the PRD calls out (idempotent writes, single finalization,
 * ownership checks, hidden answer keys before release).
 */

let dir: string;
let engine: typeof import("../src/lib/server/assessment");
let auth: typeof import("../src/lib/server/auth");
let db: typeof import("../src/lib/server/db");
let userIds: { student: string; other: string; subjectId: string; definitionId: string };

beforeAll(async () => {
  dir = mkdtempSync(path.join(tmpdir(), "edumart-"));
  process.env.DATABASE_URL = `file:${path.join(dir, "test.db")}`;
  db = await import("../src/lib/server/db");
  engine = await import("../src/lib/server/assessment");
  auth = await import("../src/lib/server/auth");

  const primary = db.q.get<any>("SELECT * FROM exam_pathways WHERE key = 'jamb'");
  const subject = db.q.get<any>("SELECT * FROM subjects WHERE exam_pathway_id = ? AND key = 'jamb-mathematics'", primary.id);
  userIds = { student: "", other: "", subjectId: subject.id, definitionId: "" };

  for (const email of ["engine-a@test.local", "engine-b@test.local"]) {
    const id = db.id();
    db.q.run(
      `INSERT INTO users (id, email_normalized, password_hash, display_name, status, created_at, updated_at)
       VALUES (?,?,?,?, 'active', ?, ?)`,
      id,
      email,
      auth.hashPassword("Password123!"),
      email,
      db.nowIso(),
      db.nowIso(),
    );
    db.q.run("INSERT INTO role_assignments (id, user_id, role, scope) VALUES (?,?, 'student','global')", db.id(), id);
    if (!userIds.student) userIds.student = id;
    else userIds.other = id;
  }

  userIds.definitionId = db.q.get<any>(
    "SELECT id FROM assessment_definitions WHERE class_id IS NULL AND type = 'mock' LIMIT 1",
  ).id;
}, 60_000);

afterAll(() => {
  if (dir) rmSync(dir, { recursive: true, force: true });
});

describe("practice engine", () => {
  it("starts a session with an immutable snapshot and no answer keys", () => {
    const session = engine.startPracticeSession(userIds.student, { subjectId: userIds.subjectId, count: 5 });
    expect(session.status).toBe("in_progress");
    const view = engine.sessionView(session, { includeAnswers: false });
    expect(view.questions).toHaveLength(5);
    expect(JSON.stringify(view)).not.toContain("answerOptionId");
    expect(JSON.stringify(view)).not.toContain("explanation");
  });

  it("acknowledges an answer with a canonical version", () => {
    const session = engine.startPracticeSession(userIds.student, { subjectId: userIds.subjectId, count: 2 });
    const view = engine.sessionView(session);
    const question = view.questions[0];
    const first = engine.saveResponse(userIds.student, session.id, question.qvId, {
      selectedOptionId: question.options[0].id,
      expectedVersion: 0,
      clientSequence: 10,
    });
    expect(first.saved).toBe(true);
    expect(first.version).toBe(1);

    const second = engine.saveResponse(userIds.student, session.id, question.qvId, {
      selectedOptionId: question.options[1].id,
      expectedVersion: 1,
      clientSequence: 11,
    });
    expect(second.version).toBe(2);

    // One row per (session, question) regardless of how many saves happened.
    const rows = db.q.all<any>(
      "SELECT * FROM assessment_responses WHERE session_id = ? AND question_version_id = ?",
      session.id,
      question.qvId,
    );
    expect(rows).toHaveLength(1);
  });

  it("replays a retried write instead of creating a duplicate or reporting conflict", () => {
    const session = engine.startPracticeSession(userIds.student, { subjectId: userIds.subjectId, count: 2 });
    const question = engine.sessionView(session).questions[0];
    const payload = { selectedOptionId: question.options[0].id, expectedVersion: 0, clientSequence: 42 };

    const first = engine.saveResponse(userIds.student, session.id, question.qvId, payload);
    const retry = engine.saveResponse(userIds.student, session.id, question.qvId, payload); // response "lost", client retried
    expect(first.version).toBe(retry.version);

    const count = db.q.get<any>(
      "SELECT COUNT(*) AS n FROM assessment_responses WHERE session_id = ?",
      session.id,
    );
    expect(Number(count.n)).toBe(1);
  });

  it("rejects a stale write that would overwrite a newer answer", () => {
    const session = engine.startPracticeSession(userIds.student, { subjectId: userIds.subjectId, count: 2 });
    const question = engine.sessionView(session).questions[0];
    engine.saveResponse(userIds.student, session.id, question.qvId, {
      selectedOptionId: question.options[0].id,
      expectedVersion: 0,
    });
    expect(() =>
      engine.saveResponse(userIds.student, session.id, question.qvId, {
        selectedOptionId: question.options[1].id,
        expectedVersion: 0,
      }),
    ).toThrowError(/another device/i);
  });

  it("refuses writes from a learner who does not own the attempt", () => {
    const session = engine.startPracticeSession(userIds.student, { subjectId: userIds.subjectId, count: 1 });
    const question = engine.sessionView(session).questions[0];
    expect(() =>
      engine.saveResponse(userIds.other, session.id, question.qvId, { selectedOptionId: question.options[0].id }),
    ).toThrowError(/another learner/i);
  });

  it("finalizes exactly once and awards points once, even on duplicate submits", () => {
    const session = engine.startPracticeSession(userIds.student, { subjectId: userIds.subjectId, count: 3 });
    const view = engine.sessionView(session);
    for (const question of view.questions) {
      engine.saveResponse(userIds.student, session.id, question.qvId, {
        selectedOptionId: question.options[0].id,
        expectedVersion: 0,
      });
    }

    const first = engine.finalizeSession(engine.getSessionRow(session.id)!, "submitted");
    const second = engine.finalizeSession(engine.getSessionRow(session.id)!, "submitted");

    expect(first.status).toBe("submitted");
    expect(second.score_raw).toBe(first.score_raw);
    expect(second.submitted_at).toBe(first.submitted_at);

    const ledger = db.q.all<any>(
      "SELECT * FROM points_ledger_entries WHERE source_id = ?",
      session.id,
    );
    expect(ledger).toHaveLength(1);
  });

  it("scores deterministically against the snapshot and reveals keys only after release", () => {
    const session = engine.startPracticeSession(userIds.student, { subjectId: userIds.subjectId, count: 4 });
    const view = engine.sessionView(session);
    const snapshot = engine.readSnapshot(session);

    let expected = 0;
    view.questions.forEach((question, i) => {
      const key = snapshot.questions[i].answerOptionId;
      if (i % 2 === 0) {
        expected += 1;
        engine.saveResponse(userIds.student, session.id, question.qvId, { selectedOptionId: key, expectedVersion: 0 });
      } else {
        const wrong = question.options.find((option) => option.id !== key)!.id;
        engine.saveResponse(userIds.student, session.id, question.qvId, { selectedOptionId: wrong, expectedVersion: 0 });
      }
    });

    const finalized = engine.finalizeSession(engine.getSessionRow(session.id)!, "submitted");
    expect(finalized.score_raw).toBe(expected);

    const released = engine.sessionView(finalized, { includeAnswers: true });
    expect("review" in released).toBe(true);
  });

  it("expires an overdue attempt through the same finalization path", () => {
    const session = engine.startPracticeSession(userIds.student, { subjectId: userIds.subjectId, count: 2 });
    db.q.run("UPDATE assessment_sessions SET deadline_at = ? WHERE id = ?", "2000-01-01T00:00:00.000Z", session.id);
    const swept = engine.expireDueSessions(10);
    expect(swept).toBeGreaterThan(0);
    const row = engine.getSessionRow(session.id)!;
    expect(row.status).toBe("expired");
    expect(row.score_percent).not.toBeNull();
    expect(() =>
      engine.saveResponse(userIds.student, session.id, engine.sessionView(session).questions[0].qvId, {
        selectedOptionId: "option-a",
      }),
    ).toThrowError();
  });

  it("enforces the attempt limit for a published assessment", () => {
    const definition = db.q.get<any>("SELECT * FROM assessment_definitions WHERE id = ?", userIds.definitionId);
    const created: string[] = [];
    for (let i = 0; i < Number(definition.attempt_limit); i++) {
      created.push(engine.startDefinitionSession({ id: userIds.student, roles: ["student"] }, definition.id, null).id);
    }
    expect(() => engine.startDefinitionSession({ id: userIds.student, roles: ["student"] }, definition.id, null)).toThrowError(
      /attempt/i,
    );
  });

  it("scopes attempts to their owner", () => {
    const session = engine.startPracticeSession(userIds.student, { subjectId: userIds.subjectId, count: 1 });
    expect(() => engine.saveResponse(userIds.other, session.id, "not-a-question", { selectedOptionId: null })).toThrowError();
  });
});
