/**
 * Timed-assessment state machine. Server-owned: the client countdown is a display aid only.
 *
 *   created → in_progress → submitted
 *                         ↘ expired
 *                         ↘ cancelled (policy-gated, never by the learner)
 *
 * Submitted/expired/cancelled are terminal. There is no path back to in_progress.
 */

export const GRACE_MS = 5_000; // small network tolerance; documented and deterministic

export type SessionStatus = "in_progress" | "submitted" | "expired" | "cancelled";

export const TERMINAL: SessionStatus[] = ["submitted", "expired", "cancelled"];

export function isTerminal(status: string): boolean {
  return TERMINAL.includes(status as SessionStatus);
}

export function deadlineFrom(startedAt: Date, durationSeconds: number): Date {
  return new Date(startedAt.getTime() + durationSeconds * 1000);
}

/** Server-authoritative: recomputed from stored started_at + duration, never from client input. */
export function effectiveDeadline(startedAt: string, durationSeconds: number): string {
  return new Date(new Date(startedAt).getTime() + durationSeconds * 1000).toISOString();
}

export type WriteDecision =
  | { accepted: true }
  | { accepted: false; code: "SESSION_CLOSED" | "SESSION_EXPIRED"; message: string };

/**
 * Whether an answer write may be committed. A write exactly at the deadline is accepted
 * inside the grace window; writes after it are rejected with SESSION_EXPIRED so the client
 * can fetch the finalized result instead of silently dropping the answer.
 */
export function canAcceptAnswer(
  session: { status: string; deadlineAt: string },
  now: Date = new Date(),
): WriteDecision {
  if (session.status === "cancelled") {
    return { accepted: false, code: "SESSION_CLOSED", message: "This attempt was cancelled." };
  }
  if (isTerminal(session.status)) {
    return { accepted: false, code: "SESSION_CLOSED", message: "This attempt is already closed." };
  }
  const limit = new Date(session.deadlineAt).getTime() + GRACE_MS;
  if (now.getTime() > limit) {
    return {
      accepted: false,
      code: "SESSION_EXPIRED",
      message: "The deadline has passed. Your saved answers are being finalized.",
    };
  }
  return { accepted: true };
}

export function remainingSeconds(deadlineAt: string, now: Date = new Date()): number {
  return Math.max(0, Math.round((new Date(deadlineAt).getTime() - now.getTime()) / 1000));
}

/** Result visibility policy: teachers may withhold feedback until the window closes. */
export function isResultReleased(
  session: { status: string; releaseAt?: string | null },
  releaseMode: "immediate" | "on_close" = "immediate",
  availabilityEnd?: string | null,
  now: Date = new Date(),
): boolean {
  if (session.status === "in_progress") return false;
  if (releaseMode === "on_close" && availabilityEnd) {
    return now.getTime() >= new Date(availabilityEnd).getTime();
  }
  if (session.releaseAt) {
    return now.getTime() >= new Date(session.releaseAt).getTime();
  }
  return true;
}

export function formatDuration(seconds: number): string {
  if (seconds < 60) return `${seconds}s`;
  const m = Math.floor(seconds / 60);
  if (m < 60) return `${m} min`;
  const h = Math.floor(m / 60);
  return `${h}h ${m % 60}m`;
}

/** Fair, deterministic question draw: seeded shuffle so a retry of the same attempt is stable. */
export function seededShuffle<T>(items: T[], seed: string): T[] {
  let h = 2166136261;
  for (let i = 0; i < seed.length; i++) {
    h ^= seed.charCodeAt(i);
    h = Math.imul(h, 16777619);
  }
  const arr = [...items];
  for (let i = arr.length - 1; i > 0; i--) {
    h ^= h << 13;
    h ^= h >>> 17;
    h ^= h << 5;
    const j = Math.abs(h) % (i + 1);
    [arr[i], arr[j]] = [arr[j], arr[i]];
  }
  return arr;
}
