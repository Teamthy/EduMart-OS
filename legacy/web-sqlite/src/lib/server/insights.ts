import { q } from "./db";

/**
 * Learning insights are derived from authoritative completed assessment sessions.
 * They can be rebuilt at any time; they are never the record of a score.
 */

export type AttemptSummary = {
  id: string;
  mode: string;
  status: string;
  started_at: string;
  submitted_at: string | null;
  score_raw: number | null;
  score_max: number | null;
  score_percent: number | null;
  title: string;
};

export function recentAttempts(userId: string, limit = 8): AttemptSummary[] {
  return q.all<AttemptSummary>(
    `SELECT s.id, s.mode, s.status, s.started_at, s.submitted_at, s.score_raw, s.score_max, s.score_percent,
            COALESCE(d.title, CASE s.mode WHEN 'practice' THEN 'Practice session' ELSE 'Practice' END) AS title
       FROM assessment_sessions s
       LEFT JOIN assessment_definitions d ON d.id = s.definition_id
      WHERE s.user_id = ? AND s.status != 'in_progress'
      ORDER BY COALESCE(s.submitted_at, s.started_at) DESC LIMIT ?`,
    userId,
    limit,
  );
}

export function activeAttempt(userId: string) {
  return q.get<any>(
    `SELECT s.id, s.mode, s.deadline_at, d.title FROM assessment_sessions s
       LEFT JOIN assessment_definitions d ON d.id = s.definition_id
      WHERE s.user_id = ? AND s.status = 'in_progress' ORDER BY s.started_at DESC LIMIT 1`,
    userId,
  );
}

export function topicStrength(userId: string, limit = 8) {
  return q.all<any>(
    `SELECT t.id AS topic_id, t.name AS topic_name, s.name AS subject_name,
            tp.attempts, tp.correct,
            CAST(ROUND(tp.correct * 100.0 / MAX(tp.attempts, 1)) AS INTEGER) AS accuracy,
            tp.last_practiced_at
       FROM topic_performance tp
       JOIN topics t ON t.id = tp.topic_id
       JOIN subjects s ON s.id = t.subject_id
      WHERE tp.user_id = ?
      ORDER BY accuracy ASC, tp.attempts DESC LIMIT ?`,
    userId,
    limit,
  );
}

function dayKey(iso: string): string {
  return iso.slice(0, 10);
}

/** Streak is computed from distinct activity days in the learner's own timezone (UTC dates here). */
export function activityStreak(userId: string): { current: number; longest: number; activeDays: number } {
  const rows = q.all<{ d: string }>(
    `SELECT DISTINCT substr(COALESCE(submitted_at, started_at), 1, 10) AS d
       FROM assessment_sessions WHERE user_id = ? AND status != 'in_progress'
      ORDER BY d DESC`,
    userId,
  );
  const days = rows.map((r) => r.d);
  if (days.length === 0) return { current: 0, longest: 0, activeDays: 0 };

  let current = 0;
  const today = dayKey(new Date().toISOString());
  const yesterday = dayKey(new Date(Date.now() - 86400000).toISOString());
  if (days[0] === today || days[0] === yesterday) {
    current = 1;
    for (let i = 1; i < days.length; i++) {
      const diff = (new Date(days[i - 1]).getTime() - new Date(days[i]).getTime()) / 86400000;
      if (Math.round(diff) === 1) current += 1;
      else break;
    }
  }
  let longest = 1;
  let run = 1;
  for (let i = 1; i < days.length; i++) {
    const diff = (new Date(days[i - 1]).getTime() - new Date(days[i]).getTime()) / 86400000;
    if (Math.round(diff) === 1) {
      run += 1;
      longest = Math.max(longest, run);
    } else {
      run = 1;
    }
  }
  return { current, longest: Math.max(longest, current), activeDays: days.length };
}

export function learningSummary(userId: string) {
  const totals = q.get<any>(
    `SELECT COUNT(*) AS attempts,
            COALESCE(AVG(score_percent), 0) AS avg_percent,
            COALESCE(MAX(score_percent), 0) AS best_percent,
            COALESCE(SUM(score_max), 0) AS questions_seen
       FROM assessment_sessions WHERE user_id = ? AND status != 'in_progress'`,
    userId,
  );
  const answers = q.get<any>(
    `SELECT COUNT(*) AS answered, COALESCE(SUM(is_correct), 0) AS correct
       FROM assessment_responses r JOIN assessment_sessions s ON s.id = r.session_id
      WHERE s.user_id = ? AND r.selected_option_id IS NOT NULL AND s.status != 'in_progress'`,
    userId,
  );
  const points = q.get<any>(
    "SELECT COALESCE(SUM(amount), 0) AS balance FROM points_ledger_entries WHERE user_id = ?",
    userId,
  );

  return {
    attempts: Number(totals?.attempts ?? 0),
    averagePercent: Math.round(Number(totals?.avg_percent ?? 0)),
    bestPercent: Math.round(Number(totals?.best_percent ?? 0)),
    questionsAnswered: Number(answers?.answered ?? 0),
    accuracy: Number(answers?.answered ?? 0) === 0 ? 0 : Math.round((Number(answers.correct) / Number(answers.answered)) * 100),
    points: Number(points?.balance ?? 0),
    streak: activityStreak(userId),
    topics: topicStrength(userId, 6),
    recent: recentAttempts(userId, 5),
    active: activeAttempt(userId),
  };
}

/** Class-level report for an authorized teacher. Scoped at the query level by class id. */
export function classReport(classId: string) {
  const members = q.all<any>(
    `SELECT u.id, u.display_name, u.email_normalized, cm.joined_at,
            (SELECT COUNT(*) FROM assessment_sessions s
              WHERE s.user_id = u.id AND s.class_id = ? AND s.status != 'in_progress') AS attempts,
            (SELECT COALESCE(ROUND(AVG(s.score_percent)), 0) FROM assessment_sessions s
              WHERE s.user_id = u.id AND s.class_id = ? AND s.status != 'in_progress') AS average
       FROM class_memberships cm JOIN users u ON u.id = cm.user_id
      WHERE cm.class_id = ? AND cm.role = 'student' AND cm.status = 'active'
      ORDER BY u.display_name`,
    classId,
    classId,
    classId,
  );
  const assessments = q.all<any>(
    `SELECT d.id, d.title, d.duration_seconds, d.availability_start, d.availability_end, d.attempt_limit, d.status,
            (SELECT COUNT(*) FROM assessment_items ai WHERE ai.definition_id = d.id) AS question_count,
            (SELECT COUNT(*) FROM assessment_sessions s WHERE s.definition_id = d.id AND s.status != 'in_progress') AS submissions,
            (SELECT COALESCE(ROUND(AVG(s.score_percent)), 0) FROM assessment_sessions s
              WHERE s.definition_id = d.id AND s.status != 'in_progress') AS average
       FROM assessment_definitions d WHERE d.class_id = ? ORDER BY d.created_at DESC`,
    classId,
  );
  return { members, assessments };
}
