import { q, id, nowIso, transaction } from "./db";
import { ApiError } from "./http";
import {
  DEFAULT_POLICY,
  parsePolicy,
  scoreAttempt,
  pointsForCompletion,
  type SnapQuestion,
  type ScoringPolicy,
} from "../domain/scoring";
import { canAcceptAnswer, deadlineFrom, isResultReleased, seededShuffle } from "../domain/exam";
import { audit, track } from "./audit";

/**
 * Assessment engine.
 *
 * Invariants enforced here (not in the UI):
 *  1. The server owns start time, deadline, attempt eligibility and final state.
 *  2. Every attempt is served from an immutable snapshot taken at start.
 *  3. Answer writes are versioned and retry-safe; a duplicate write cannot create a duplicate row.
 *  4. Submission and expiry run the same finalization function, so scoring is deterministic.
 *  5. Answer keys never leave this module before the result release policy allows it.
 */

export type SessionRow = {
  id: string;
  user_id: string;
  definition_id: string | null;
  class_id: string | null;
  mode: "practice" | "mock" | "class";
  attempt_no: number;
  status: "in_progress" | "submitted" | "expired" | "cancelled";
  started_at: string;
  deadline_at: string;
  submitted_at: string | null;
  score_raw: number | null;
  score_max: number | null;
  score_percent: number | null;
  snapshot_json: string;
  idempotency_key: string | null;
  release_at: string | null;
  created_at: string;
};

type Snapshot = { policy: ScoringPolicy; questions: SnapQuestion[] };

type VersionRow = {
  qv_id: string;
  question_id: string;
  stem: string;
  options_json: string;
  answer_option_id: string;
  explanation: string;
  difficulty: string;
  subject_id: string;
  subject_name: string;
  topic_id: string | null;
  topic_name: string | null;
  points?: number;
};

const VERSION_SELECT = `
  SELECT qv.id AS qv_id, qv.question_id, qv.stem, qv.options_json, qv.answer_option_id, qv.explanation,
         qv.review_state, qv.published_at, q.difficulty, q.subject_id, s.name AS subject_name,
         q.topic_id, t.name AS topic_name
    FROM question_versions qv
    JOIN questions q ON q.id = qv.question_id
    JOIN subjects s ON s.id = q.subject_id
    LEFT JOIN topics t ON t.id = q.topic_id
`;

function toSnapQuestion(row: VersionRow, position: number, points = 1): SnapQuestion {
  return {
    qvId: row.qv_id,
    questionId: row.question_id,
    subjectId: row.subject_id,
    subjectName: row.subject_name,
    topicId: row.topic_id ?? undefined,
    topicName: row.topic_name ?? undefined,
    stem: row.stem,
    options: JSON.parse(row.options_json),
    answerOptionId: row.answer_option_id,
    explanation: row.explanation,
    difficulty: row.difficulty,
    points,
    position,
  };
}

export function readSnapshot(session: Pick<SessionRow, "snapshot_json">): Snapshot {
  return JSON.parse(session.snapshot_json) as Snapshot;
}

// ─────────────────────────── question selection ──────────────────────────

export function publishedQuestions(opts: {
  subjectId?: string;
  topicIds?: string[];
  difficulty?: string;
  limit: number;
  seed: string;
}): SnapQuestion[] {
  const where: string[] = ["qv.review_state = 'approved'", "qv.published_at IS NOT NULL", "q.status = 'published'"];
  const params: unknown[] = [];
  if (opts.subjectId) {
    where.push("q.subject_id = ?");
    params.push(opts.subjectId);
  }
  if (opts.topicIds && opts.topicIds.length > 0) {
    where.push(`q.topic_id IN (${opts.topicIds.map(() => "?").join(",")})`);
    params.push(...opts.topicIds);
  }
  if (opts.difficulty && opts.difficulty !== "all") {
    where.push("q.difficulty = ?");
    params.push(opts.difficulty);
  }
  const rows = q.all<VersionRow>(`${VERSION_SELECT} WHERE ${where.join(" AND ")}`, ...params);
  const shuffled = seededShuffle(rows, opts.seed);
  return shuffled.slice(0, opts.limit).map((row, i) => toSnapQuestion(row, i + 1));
}

// ─────────────────────────────── start ───────────────────────────────────

function attemptNumber(userId: string, definitionId: string): number {
  const row = q.get<{ n: number }>(
    "SELECT COUNT(*) AS n FROM assessment_sessions WHERE user_id = ? AND definition_id = ?",
    userId,
    definitionId,
  );
  return (row ? Number(row.n) : 0) + 1;
}

export function startPracticeSession(
  userId: string,
  input: { subjectId: string; topicIds?: string[]; count?: number; difficulty?: string; idempotencyKey?: string | null },
): SessionRow {
  const count = Math.min(Math.max(input.count ?? 10, 1), 40);
  const sessionSeed = `${userId}:${Date.now()}:${input.subjectId}`;
  const questions = publishedQuestions({
    subjectId: input.subjectId,
    topicIds: input.topicIds,
    difficulty: input.difficulty,
    limit: count,
    seed: sessionSeed,
  });

  if (questions.length === 0) {
    throw new ApiError("NOT_FOUND", "No approved questions are available for that selection yet.");
  }

  const startedAt = new Date();
  // Practice sessions are generous but still server-bounded, so an abandoned tab cannot stay open forever.
  const deadline = deadlineFrom(startedAt, 4 * 60 * 60);
  const sessionId = id();
  const snapshot: Snapshot = { policy: DEFAULT_POLICY, questions };

  transaction(() => {
    q.run(
      `INSERT INTO assessment_sessions (id, user_id, definition_id, class_id, mode, attempt_no, status,
         started_at, deadline_at, submitted_at, score_raw, score_max, score_percent, snapshot_json, idempotency_key, release_at, created_at)
       VALUES (?,?,NULL,NULL,'practice',1,'in_progress',?,?,NULL,NULL,NULL,NULL,?,?,NULL,?)`,
      sessionId,
      userId,
      startedAt.toISOString(),
      deadline.toISOString(),
      JSON.stringify(snapshot),
      input.idempotencyKey ?? null,
      startedAt.toISOString(),
    );
    audit({
      actorUserId: userId,
      action: "practice.started",
      resourceType: "assessment_session",
      resourceId: sessionId,
      metadata: { subjectId: input.subjectId, questions: questions.length },
    });
  });

  track({ userId, eventType: "practice_started", entityType: "assessment_session", entityId: sessionId });
  return getSessionRow(sessionId)!;
}

export function startDefinitionSession(
  user: { id: string; roles: string[] },
  definitionId: string,
  idempotencyKey: string | null,
): SessionRow {
  const definition = q.get<any>(
    `SELECT * FROM assessment_definitions WHERE id = ?`,
    definitionId,
  );
  if (!definition) throw new ApiError("NOT_FOUND", "That assessment could not be found.");
  if (definition.status !== "published") {
    throw new ApiError("ASSESSMENT_NOT_OPEN", "This assessment has not been published yet.");
  }

  const now = Date.now();
  if (definition.availability_start && now < new Date(definition.availability_start).getTime()) {
    throw new ApiError("ASSESSMENT_NOT_OPEN", "This assessment is not open yet.");
  }
  if (definition.availability_end && now > new Date(definition.availability_end).getTime()) {
    throw new ApiError("ASSESSMENT_NOT_OPEN", "This assessment has closed.");
  }

  // Class assessments: membership is checked server-side, never inferred from the request.
  if (definition.class_id) {
    const member = q.get<{ role: string }>(
      "SELECT role FROM class_memberships WHERE class_id = ? AND user_id = ? AND status = 'active'",
      definition.class_id,
      user.id,
    );
    if (!member) throw new ApiError("FORBIDDEN", "You are not a member of the class for this assessment.");
  }

  const attemptNo = attemptNumber(user.id, definitionId);
  if (attemptNo > definition.attempt_limit) {
    throw new ApiError("CONFLICT", `You have used all ${definition.attempt_limit} attempt(s) for this assessment.`);
  }

  const items = q.all<VersionRow & { points: number; position: number }>(
    `${VERSION_SELECT.replace("FROM question_versions qv", ", ai.points, ai.position FROM question_versions qv")}
     JOIN assessment_items ai ON ai.question_version_id = qv.id
     WHERE ai.definition_id = ?
     ORDER BY ai.position ASC`,
    definitionId,
  );
  if (items.length === 0) {
    throw new ApiError("CONFLICT", "This assessment has no approved questions yet.");
  }

  const policy = parsePolicy(definition.scoring_policy_json);
  const questions = items.map((row, i) => toSnapQuestion(row, row.position ?? i + 1, row.points ?? 1));
  const startedAt = new Date();
  const deadline = deadlineFrom(startedAt, definition.duration_seconds);
  const sessionId = id();
  const snapshot: Snapshot = { policy, questions };

  transaction(() => {
    q.run(
      `INSERT INTO assessment_sessions (id, user_id, definition_id, class_id, mode, attempt_no, status,
         started_at, deadline_at, submitted_at, score_raw, score_max, score_percent, snapshot_json, idempotency_key, release_at, created_at)
       VALUES (?,?,?,?,?,?,'in_progress',?,?,NULL,NULL,NULL,NULL,?,?,NULL,?)`,
      sessionId,
      user.id,
      definitionId,
      definition.class_id,
      definition.type,
      attemptNo,
      startedAt.toISOString(),
      deadline.toISOString(),
      JSON.stringify(snapshot),
      idempotencyKey,
      startedAt.toISOString(),
    );
    audit({
      actorUserId: user.id,
      action: "assessment.started",
      resourceType: "assessment_session",
      resourceId: sessionId,
      metadata: { definitionId, attemptNo, questions: questions.length },
    });
  });

  track({ userId: user.id, eventType: "assessment_started", entityType: "assessment_session", entityId: sessionId });
  return getSessionRow(sessionId)!;
}

export function getSessionRow(sessionId: string): SessionRow | null {
  return q.get<SessionRow>("SELECT * FROM assessment_sessions WHERE id = ?", sessionId);
}

// ──────────────────────────── answer writes ──────────────────────────────

export type SaveResult = {
  saved: true;
  server_saved_at: string;
  version: number;
  session_status: string;
  remaining_seconds: number;
};

export function saveResponse(
  userId: string,
  sessionId: string,
  questionVersionId: string,
  input: { selectedOptionId: string | null; expectedVersion?: number; clientSequence?: number },
): SaveResult {
  const session = getSessionRow(sessionId);
  if (!session) throw new ApiError("NOT_FOUND", "That attempt could not be found.");
  if (session.user_id !== userId) throw new ApiError("FORBIDDEN", "This attempt belongs to another learner.");

  const decision = canAcceptAnswer({ status: session.status, deadlineAt: session.deadline_at });
  if (!decision.accepted) {
    // Close it now so the learner immediately sees the finalized state rather than a pending write.
    if (decision.code === "SESSION_EXPIRED") finalizeSession(session, "expired");
    throw new ApiError(decision.code, decision.message);
  }

  const snapshot = readSnapshot(session);
  const question = snapshot.questions.find((qq) => qq.qvId === questionVersionId);
  if (!question) throw new ApiError("FORBIDDEN", "That question is not part of this attempt.");

  if (input.selectedOptionId !== null && !question.options.some((o) => o.id === input.selectedOptionId)) {
    throw new ApiError("VALIDATION_ERROR", "That answer option does not exist for this question.");
  }

  const existing = q.get<{
    answer_version: number;
    selected_option_id: string | null;
    client_sequence: number | null;
  }>(
    "SELECT answer_version, selected_option_id, client_sequence FROM assessment_responses WHERE session_id = ? AND question_version_id = ?",
    sessionId,
    questionVersionId,
  );

  /**
   * Retry safety. If a write was committed but the response was lost, the client retries with the
   * same mutation (same client_sequence, or the same expected_version + same value). Replay the
   * acknowledgement instead of reporting a conflict — no duplicate row is created, and the client
   * can safely move on to "Saved".
   */
  if (existing) {
    const sameValue = (existing.selected_option_id ?? null) === (input.selectedOptionId ?? null);
    const sameSequence =
      input.clientSequence !== undefined &&
      existing.client_sequence !== null &&
      input.clientSequence === existing.client_sequence;
    const replayOfLastWrite =
      typeof input.expectedVersion === "number" && input.expectedVersion === existing.answer_version - 1 && sameValue;
    if (sameValue && (sameSequence || replayOfLastWrite)) {
      return {
        saved: true,
        server_saved_at: nowIso(),
        version: existing.answer_version,
        session_status: session.status,
        remaining_seconds: Math.max(0, Math.round((new Date(session.deadline_at).getTime() - Date.now()) / 1000)),
      } as SaveResult;
    }
  }

  // Optimistic concurrency: a stale client cannot silently overwrite a newer save.
  if (existing && typeof input.expectedVersion === "number" && input.expectedVersion !== existing.answer_version) {
    throw new ApiError(
      "CONFLICT",
      "This answer was updated on another device. Refresh to see the latest saved value.",
      { current_version: existing.answer_version },
    );
  }

  const nextVersion = (existing?.answer_version ?? 0) + 1;
  const savedAt = nowIso();
  const isCorrect = input.selectedOptionId ? input.selectedOptionId === question.answerOptionId : null;

  transaction(() => {
    if (existing) {
      q.run(
        `UPDATE assessment_responses
            SET selected_option_id = ?, is_correct = ?, answer_version = ?, client_sequence = ?, saved_at = ?, updated_at = ?
          WHERE session_id = ? AND question_version_id = ?`,
        input.selectedOptionId,
        isCorrect === null ? null : isCorrect ? 1 : 0,
        nextVersion,
        input.clientSequence ?? null,
        savedAt,
        savedAt,
        sessionId,
        questionVersionId,
      );
    } else {
      q.run(
        `INSERT INTO assessment_responses (id, session_id, question_version_id, selected_option_id, is_correct,
           awarded_points, answer_version, client_sequence, saved_at, updated_at)
         VALUES (?,?,?,?,?,0,?,?,?,?)`,
        id(),
        sessionId,
        questionVersionId,
        input.selectedOptionId,
        isCorrect === null ? null : isCorrect ? 1 : 0,
        nextVersion,
        input.clientSequence ?? null,
        savedAt,
        savedAt,
      );
    }
  });

  return {
    saved: true,
    server_saved_at: savedAt,
    version: nextVersion,
    session_status: session.status,
    remaining_seconds: Math.max(0, Math.round((new Date(session.deadline_at).getTime() - Date.now()) / 1000)),
  };
}

// ────────────────────────────── finalize ─────────────────────────────────

/**
 * The single finalization path. Both explicit submission and server-side expiry call this,
 * inside one transaction, with an idempotency guard so a duplicate submission cannot
 * produce a second score or a second points award.
 */
export function finalizeSession(session: SessionRow, reason: "submitted" | "expired"): SessionRow {
  if (session.status !== "in_progress") return session;

  const snapshot = readSnapshot(session);
  const responses = q.all<{ question_version_id: string; selected_option_id: string | null }>(
    "SELECT question_version_id, selected_option_id FROM assessment_responses WHERE session_id = ?",
    session.id,
  );

  const breakdown = scoreAttempt(
    snapshot.questions,
    responses.map((r) => ({ qvId: r.question_version_id, selectedOptionId: r.selected_option_id })),
    snapshot.policy,
  );

  const finishedAt = nowIso();

  transaction(() => {
    const current = getSessionRow(session.id)!;
    if (current.status !== "in_progress") return; // another request won the race

    q.run(
      `UPDATE assessment_sessions
          SET status = ?, submitted_at = ?, score_raw = ?, score_max = ?, score_percent = ?
        WHERE id = ? AND status = 'in_progress'`,
      reason === "submitted" ? "submitted" : "expired",
      finishedAt,
      breakdown.raw,
      breakdown.max,
      breakdown.percent,
      session.id,
    );

    for (const item of breakdown.perQuestion) {
      q.run(
        `UPDATE assessment_responses SET is_correct = ?, awarded_points = ?, updated_at = ?
          WHERE session_id = ? AND question_version_id = ?`,
        item.isCorrect === null ? null : item.isCorrect ? 1 : 0,
        item.awarded,
        finishedAt,
        session.id,
        item.qvId,
      );
    }

    // Topic performance is derived data: rebuildable, never the source of truth for a score.
    for (const question of snapshot.questions) {
      const response = breakdown.perQuestion.find((p) => p.qvId === question.qvId);
      if (!question.topicId || !response || response.isCorrect === null) continue;
      const existing = q.get<{ id: string }>(
        "SELECT id FROM topic_performance WHERE user_id = ? AND topic_id = ?",
        session.user_id,
        question.topicId,
      );
      if (existing) {
        q.run(
          "UPDATE topic_performance SET attempts = attempts + 1, correct = correct + ?, last_practiced_at = ? WHERE id = ?",
          response.isCorrect ? 1 : 0,
          finishedAt,
          existing.id,
        );
      } else {
        q.run(
          "INSERT INTO topic_performance (id, user_id, topic_id, attempts, correct, last_practiced_at) VALUES (?,?,?,1,?,?)",
          id(),
          session.user_id,
          question.topicId,
          response.isCorrect ? 1 : 0,
          finishedAt,
        );
      }
    }

    // Points: separate asset from money, idempotent per attempt.
    const points = pointsForCompletion(session.mode, breakdown.percent);
    q.run(
      `INSERT OR IGNORE INTO points_ledger_entries
        (id, user_id, amount, entry_type, source_type, source_id, rule_version, idempotency_key, note, created_at)
       VALUES (?,?,?,'earn',?,?,'v1',?,?,?)`,
      id(),
      session.user_id,
      points,
      `${session.mode}_completed`,
      session.id,
      `points:attempt:${session.id}`,
      `${session.mode} attempt completed`,
      finishedAt,
    );

    audit({
      actorUserId: session.user_id,
      action: reason === "submitted" ? "assessment.submitted" : "assessment.expired",
      resourceType: "assessment_session",
      resourceId: session.id,
      metadata: { score: breakdown.raw, max: breakdown.max, policy: breakdown.policyVersion },
    });
  });

  track({
    userId: session.user_id,
    eventType: reason === "submitted" ? "assessment_submitted" : "assessment_expired",
    entityType: "assessment_session",
    entityId: session.id,
    metadata: { percent: breakdown.percent },
  });
  return getSessionRow(session.id)!;
}

/** Job sweep: closes attempts whose deadline has passed, using the same finalization path. */
export function expireDueSessions(limit = 50): number {
  const rows = q.all<SessionRow>(
    "SELECT * FROM assessment_sessions WHERE status = 'in_progress' AND deadline_at < ? ORDER BY deadline_at ASC LIMIT ?",
    nowIso(),
    limit,
  );
  for (const row of rows) finalizeSession(row, "expired");
  return rows.length;
}

// ─────────────────────────── client projections ──────────────────────────

export type QuestionView = {
  qvId: string;
  stem: string;
  options: { id: string; label: string }[];
  position: number;
  topicName?: string;
  subjectName?: string;
  difficulty?: string;
  selectedOptionId: string | null;
  answerVersion: number;
};

/** Projects the session for the learner — answer keys and explanations are withheld until release. */
export function sessionView(session: SessionRow, opts: { includeAnswers?: boolean } = {}) {
  const snapshot = readSnapshot(session);
  const responses = q.all<{ question_version_id: string; selected_option_id: string | null; answer_version: number }>(
    "SELECT question_version_id, selected_option_id, answer_version FROM assessment_responses WHERE session_id = ?",
    session.id,
  );
  const byQv = new Map(responses.map((r) => [r.question_version_id, r]));

  const questions: QuestionView[] = snapshot.questions.map((question) => ({
    qvId: question.qvId,
    stem: question.stem,
    options: question.options,
    position: question.position,
    topicName: question.topicName,
    subjectName: question.subjectName,
    difficulty: question.difficulty,
    selectedOptionId: byQv.get(question.qvId)?.selected_option_id ?? null,
    answerVersion: byQv.get(question.qvId)?.answer_version ?? 0,
  }));

  const released = isResultReleased(
    { status: session.status, releaseAt: session.release_at },
    "immediate",
    null,
  );

  const base = {
    session_id: session.id,
    mode: session.mode,
    status: session.status,
    attempt_no: session.attempt_no,
    started_at: session.started_at,
    deadline_at: session.deadline_at,
    submitted_at: session.submitted_at,
    server_time: nowIso(),
    question_count: questions.length,
    questions,
    score:
      session.score_raw === null
        ? null
        : { raw: session.score_raw, max: session.score_max, percent: session.score_percent },
    result_released: released,
  };

  if (!released || !opts.includeAnswers || session.status === "in_progress") return base;

  return {
    ...base,
    review: snapshot.questions.map((question) => ({
      qvId: question.qvId,
      answerOptionId: question.answerOptionId,
      explanation: question.explanation,
      topicName: question.topicName,
      topicId: question.topicId,
      difficulty: question.difficulty,
    })),
  };
}

// ─────────────────────── authoring (teacher / admin) ─────────────────────

export function createAssessmentDefinition(
  user: { id: string; roles: string[] },
  input: {
    title: string;
    classId?: string | null;
    subjectId: string;
    topicIds?: string[];
    questionCount?: number;
    durationSeconds?: number;
    attemptLimit?: number;
    availabilityStart?: string | null;
    availabilityEnd?: string | null;
    type?: "mock" | "class";
    resultRelease?: "immediate" | "on_close";
  },
): { id: string; questionCount: number } {
  const title = (input.title ?? "").trim();
  if (title.length < 4) throw new ApiError("VALIDATION_ERROR", "Give the assessment a title of at least 4 characters.");
  const duration = Math.min(Math.max(input.durationSeconds ?? 1200, 60), 3 * 60 * 60);
  const limit = Math.min(Math.max(input.attemptLimit ?? 1, 1), 10);
  const count = Math.min(Math.max(input.questionCount ?? 10, 1), 60);

  let organizationId: string | null = null;
  if (input.classId) {
    const cls = q.get<any>("SELECT * FROM classes WHERE id = ?", input.classId);
    if (!cls) throw new ApiError("NOT_FOUND", "That class could not be found.");
    const membership = q.get<any>(
      "SELECT role FROM class_memberships WHERE class_id = ? AND user_id = ? AND status = 'active'",
      input.classId,
      user.id,
    );
    if (!membership || (membership.role !== "teacher" && cls.owner_user_id !== user.id)) {
      throw new ApiError("FORBIDDEN", "Only a teacher of that class can create assessments for it.");
    }
    organizationId = cls.organization_id;
  } else if (!user.roles.includes("content_editor") && !user.roles.includes("platform_admin")) {
    // Public mocks are a platform-curated resource; teachers create class assessments.
    throw new ApiError("FORBIDDEN", "Public assessments can only be created by platform staff.");
  }

  const questions = publishedQuestions({
    subjectId: input.subjectId,
    topicIds: input.topicIds,
    limit: count,
    seed: `${title}:${Date.now()}`,
  });
  if (questions.length < 1) {
    throw new ApiError("CONFLICT", "No approved questions match that selection. Choose another subject or topic.");
  }

  const definitionId = id();
  const createdAt = nowIso();
  transaction(() => {
    q.run(
      `INSERT INTO assessment_definitions (id, owner_user_id, organization_id, class_id, title, type, duration_seconds,
         availability_start, availability_end, attempt_limit, scoring_policy_json, config_json, result_release, status, published_at, created_at)
       VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?, 'published', ?, ?)`,
      definitionId,
      user.id,
      organizationId,
      input.classId ?? null,
      title,
      input.type ?? (input.classId ? "class" : "mock"),
      duration,
      input.availabilityStart ?? createdAt,
      input.availabilityEnd ?? null,
      limit,
      JSON.stringify(DEFAULT_POLICY),
      JSON.stringify({ subjectId: input.subjectId, topicIds: input.topicIds ?? [], questionCount: questions.length }),
      input.resultRelease ?? "immediate",
      createdAt,
      createdAt,
    );
    questions.forEach((question, i) => {
      q.run(
        "INSERT INTO assessment_items (id, definition_id, question_version_id, points, position) VALUES (?,?,?,?,?)",
        id(),
        definitionId,
        question.qvId,
        question.points,
        i + 1,
      );
    });
  });

  audit({
    actorUserId: user.id,
    action: "assessment.created",
    resourceType: "assessment_definition",
    resourceId: definitionId,
    metadata: { title, classId: input.classId ?? null, questions: questions.length },
  });

  return { id: definitionId, questionCount: questions.length };
}
