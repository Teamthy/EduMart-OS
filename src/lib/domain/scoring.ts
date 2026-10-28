/**
 * Scoring is deterministic and versioned. The same function finalizes both an explicit
 * submission and a server-side expiry, so a learner can never get two different scores for
 * one attempt. Historical scores are reproducible because each session stores an immutable
 * question snapshot plus the scoring policy version used.
 */

export type ScoringPolicy = {
  version: number;
  /** points awarded for a correct answer */
  correct: number;
  /** points deducted for a wrong answer (negative marking), only applied when negativeMarking */
  wrong: number;
  /** points for an unanswered question (normally 0) */
  unanswered: number;
  negativeMarking: boolean;
};

export const DEFAULT_POLICY: ScoringPolicy = {
  version: 1,
  correct: 1,
  wrong: 0,
  unanswered: 0,
  negativeMarking: false,
};

export const POLICY_REGISTRY: Record<number, ScoringPolicy> = {
  1: DEFAULT_POLICY,
  2: { version: 2, correct: 2, wrong: 1, unanswered: 0, negativeMarking: true },
};

export type SnapQuestion = {
  qvId: string;
  questionId: string;
  subjectId?: string;
  subjectName?: string;
  topicId?: string;
  topicName?: string;
  stem: string;
  options: { id: string; label: string }[];
  answerOptionId: string;
  explanation: string;
  difficulty?: string;
  points: number;
  position: number;
};

export type SnapResponse = {
  qvId: string;
  selectedOptionId: string | null;
};

export type ScoreBreakdown = {
  raw: number;
  max: number;
  percent: number;
  correct: number;
  incorrect: number;
  unanswered: number;
  policyVersion: number;
  perQuestion: {
    qvId: string;
    selectedOptionId: string | null;
    answerOptionId: string;
    isCorrect: boolean | null;
    awarded: number;
  }[];
};

export function parsePolicy(json: string | null | undefined): ScoringPolicy {
  if (!json) return DEFAULT_POLICY;
  try {
    const parsed = JSON.parse(json) as Partial<ScoringPolicy>;
    if (typeof parsed.version === "number" && POLICY_REGISTRY[parsed.version]) {
      return { ...POLICY_REGISTRY[parsed.version], ...parsed };
    }
    return { ...DEFAULT_POLICY, ...parsed, version: parsed.version ?? DEFAULT_POLICY.version };
  } catch {
    return DEFAULT_POLICY;
  }
}

export function scoreAttempt(
  questions: SnapQuestion[],
  responses: SnapResponse[],
  policy: ScoringPolicy = DEFAULT_POLICY,
): ScoreBreakdown {
  const byQv = new Map<string, SnapResponse>();
  for (const r of responses) byQv.set(r.qvId, r);

  let raw = 0;
  let max = 0;
  let correct = 0;
  let incorrect = 0;
  let unanswered = 0;

  const perQuestion = questions.map((question) => {
    const points = question.points ?? 1;
    max += points * Math.max(policy.correct, 1);
    const response = byQv.get(question.qvId);
    const selected = response?.selectedOptionId ?? null;

    if (!selected) {
      unanswered += 1;
      raw += policy.unanswered * points;
      return {
        qvId: question.qvId,
        selectedOptionId: null,
        answerOptionId: question.answerOptionId,
        isCorrect: null,
        awarded: policy.unanswered * points,
      };
    }

    const isCorrect = selected === question.answerOptionId;
    let awarded: number;
    if (isCorrect) {
      correct += 1;
      awarded = policy.correct * points;
    } else {
      incorrect += 1;
      awarded = policy.negativeMarking ? -policy.wrong * points : 0;
    }
    raw += awarded;
    return {
      qvId: question.qvId,
      selectedOptionId: selected,
      answerOptionId: question.answerOptionId,
      isCorrect,
      awarded,
    };
  });

  const percent = max === 0 ? 0 : Math.max(0, Math.round((raw / max) * 100));
  return {
    raw: Math.max(0, raw),
    max,
    percent,
    correct,
    incorrect,
    unanswered,
    policyVersion: policy.version,
    perQuestion,
  };
}

/** Points awarded for completing work — separate from exam score, not money. */
export function pointsForCompletion(kind: "practice" | "mock" | "class", percent: number): number {
  const base = kind === "practice" ? 10 : kind === "mock" ? 40 : 25;
  const bonus = percent >= 70 ? 10 : 0;
  return base + bonus;
}
