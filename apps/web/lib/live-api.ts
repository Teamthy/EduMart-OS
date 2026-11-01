"use client";

/**
 * Browser-side access to the live-class and exam endpoints.
 *
 * Requests go to the same origin (`/v1/...`) where the Next.js proxy forwards them to the Go API, so the
 * session stays in an httpOnly cookie and no token touches JavaScript. Everything here throws `LiveApiError`
 * with the server's message, because "Something went wrong" in a room where the error is "this class is full"
 * is a worse product than the truth.
 */

export class LiveApiError extends Error {
  readonly code: string;
  readonly status: number;

  constructor(options: { code: string; message: string; status: number }) {
    super(options.message);
    this.name = "LiveApiError";
    this.code = options.code;
    this.status = options.status;
  }
}

async function call<T>(path: string, init?: RequestInit): Promise<T> {
  let response: Response;
  try {
    response = await fetch(`/v1${path}`, {
      ...init,
      headers: {
        Accept: "application/json",
        ...(init?.body ? { "Content-Type": "application/json" } : {}),
        ...init?.headers,
      },
      credentials: "include",
      cache: "no-store",
    });
  } catch {
    throw new LiveApiError({
      code: "NETWORK_ERROR",
      message: "You're offline or the connection dropped.",
      status: 0,
    });
  }
  const text = await response.text();
  const payload = text ? (JSON.parse(text) as Record<string, unknown>) : {};
  if (!response.ok) {
    const error = (payload.error ?? {}) as { code?: string; message?: string };
    throw new LiveApiError({
      code: error.code ?? "ERROR",
      message: error.message ?? "Something went wrong.",
      status: response.status,
    });
  }
  return payload as T;
}

// ── live classes ─────────────────────────────────────────────────────────────────────────────────

export interface LiveClass {
  id: string;
  slug: string;
  title: string;
  description: string;
  creditsCost: number;
  /** How many seats the room carries. Capped at 50 by the server. */
  capacity: number;
  status: string;
  startsAt: string;
  durationMinutes: number;
  hostUserId: string;
  hostName: string;
  attendees: number;
  joinUrl: string;
  role: string;
  /** `open` seats whoever pays or knocks; `approval` queues learners until the host decides. */
  admission: "open" | "approval";
  /** Where the caller stands: admitted, pending, rejected, or empty if they have never knocked. */
  admissionStatus: "admitted" | "pending" | "rejected" | "";
}

export interface StageSeat {
  userId: string;
  displayName: string;
  avatar?: { initials: string; color: string };
  role: string;
  paymentMethod: string;
  creditsPaid: number;
  joinedAt: string;
  leftAt: string | null;
  present: boolean;
  stageRole: "host" | "speaker" | "audience";
  stageSlot: number;
  status: "admitted" | "pending" | "rejected";
}

export interface BoardEventRow {
  seq: number;
  authorId: string;
  nonce: string;
  ciphertext: string;
  createdAt: string;
}

export interface SignalRow {
  seq: number;
  fromUserId: string;
  toUserId: string | null;
  kind: string;
  payload: string;
  createdAt: string;
}

export interface AttendanceRow {
  userId: string;
  displayName: string;
  sessions: number;
  minutes: number;
  firstJoinedAt: string;
  lastSeenAt: string;
  creditsPaid: number;
}

export async function listLiveClasses(scope: "all" | "mine" | "past" = "all") {
  return call<{ classes: LiveClass[] }>(`/virtual-classes?scope=${scope}`);
}

export async function createLiveClass(input: {
  title: string;
  description?: string;
  creditsCost: number;
  capacity?: number;
  startsAt: string;
  durationMinutes: number;
  admission?: "open" | "approval";
}) {
  return call<{ class: LiveClass }>("/virtual-classes", { method: "POST", body: JSON.stringify(input) });
}

export interface JoinOutcome {
  class: LiveClass;
  creditsSpent: number;
  alreadyJoined: boolean;
  /** True when the class requires approval and this call queued a request instead of taking a seat. */
  pending: boolean;
  admissionStatus: "admitted" | "pending" | "";
}

export async function joinLiveClass(id: string, discountCode?: string) {
  return call<JoinOutcome>(`/virtual-classes/${id}/join`, {
    method: "POST",
    body: JSON.stringify(discountCode ? { discountCode } : {}),
  });
}

/** The host's waiting room: everybody whose request is still undecided. Host-only, refused for anyone else. */
export async function liveRequests(id: string) {
  return call<{ requests: StageSeat[] }>(`/virtual-classes/${id}/requests`);
}

export async function decideRequest(id: string, userId: string, decision: "accept" | "reject") {
  return call<{ attendees: StageSeat[] }>(`/virtual-classes/${id}/requests/${userId}`, {
    method: "POST",
    body: JSON.stringify({ decision }),
  });
}

export async function leaveLiveClass(id: string) {
  return call<{ left: boolean }>(`/virtual-classes/${id}/leave`, { method: "POST", body: JSON.stringify({}) });
}

export async function startLiveClass(id: string) {
  return call<{ class: LiveClass }>(`/virtual-classes/${id}/start`, { method: "POST", body: JSON.stringify({}) });
}

export async function endLiveClass(id: string) {
  return call<{ class: LiveClass }>(`/virtual-classes/${id}/end`, { method: "POST", body: JSON.stringify({}) });
}

export async function liveRoster(id: string) {
  return call<{ attendees: StageSeat[] }>(`/virtual-classes/${id}/roster`);
}

export async function liveIce(id: string) {
  return call<{ iceServers: RTCIceServer[] }>(`/virtual-classes/${id}/ice`);
}

export async function setStage(id: string, userId: string, role: "speaker" | "audience") {
  return call<{ attendees: StageSeat[] }>(`/virtual-classes/${id}/stage`, {
    method: "POST",
    body: JSON.stringify({ userId, role }),
  });
}

/**
 * One media negotiation: the browser's offer in, the forwarder's answer out, plus the slot the server assigned.
 *
 * The slot is read, not requested — the server uses the stage role recorded on the caller's attendance row.
 */
export async function connectMedia(id: string, sdp: string) {
  return call<{ sdp: string; stageRole: string; stageSlot: number }>(`/virtual-classes/${id}/media`, {
    method: "POST",
    body: JSON.stringify({ sdp }),
  });
}

export async function liveBoard(id: string, since = 0) {
  return call<{ events: BoardEventRow[] }>(`/virtual-classes/${id}/board?since=${since}`);
}

export async function appendBoard(id: string, event: { nonce: string; ciphertext: string }) {
  return call<{ seq: number }>(`/virtual-classes/${id}/board`, {
    method: "POST",
    body: JSON.stringify(event),
  });
}

export async function pollSignals(id: string, since: number, wait = 20) {
  return call<{ signals: SignalRow[] }>(`/virtual-classes/${id}/signal?since=${since}&wait=${wait}`);
}

export async function postSignal(
  id: string,
  input: { kind: "offer" | "answer" | "ice" | "presence" | "leave" | "chat"; toUserId?: string; payload: string },
) {
  return call<{ seq: number }>(`/virtual-classes/${id}/signal`, {
    method: "POST",
    body: JSON.stringify({ toUserId: "", ...input }),
  });
}

export async function liveAttendance(id: string) {
  return call<{ attendance: AttendanceRow[] }>(`/virtual-classes/${id}/attendance`);
}

export async function liveDiscountCodes(id: string) {
  return call<{ discountCodes: { code: string; percentOff: number; uses: number; maxUses: number }[] }>(
    `/virtual-classes/${id}/discount-codes`,
  );
}

export async function createDiscountCode(id: string, percentOff: number, maxUses: number) {
  return call<{ discountCode: { code: string } }>(`/virtual-classes/${id}/discount-codes`, {
    method: "POST",
    body: JSON.stringify({ percentOff, maxUses }),
  });
}

// ── exams ────────────────────────────────────────────────────────────────────────────────────────

export interface Exam {
  id: string;
  slug: string;
  title: string;
  hostName: string;
  durationMinutes: number;
  startsAt: string;
  endsAt: string;
  shuffled: boolean;
  attemptLimit: number;
  status: string;
  questionCount: number;
  candidates: number;
  url: string;
  role: "host" | "candidate";
  open: boolean;
  secondsUntilStart: number;
  secondsUntilClose: number;
  myAttempt: {
    sessionId: string;
    status: string;
    startedAt: string;
    deadlineAt: string;
    submittedAt: string | null;
    scorePercent: number | null;
    remainingSeconds: number;
  } | null;
}

export interface ExamCandidate {
  userId: string;
  displayName: string;
  status: string;
  startedAt: string;
  submittedAt: string | null;
  deadlineAt: string;
  scoreRaw: number | null;
  scoreMax: number | null;
  remainingSeconds: number;
  /**
   * What the candidate's own browser reported about attention. It is evidence of distraction, not of identity:
   * nothing here proves who was at the keyboard.
   */
  integrity?: {
    signals: number;
    focusLost: number;
    pasted: number;
    copied: number;
    lastAt: string | null;
  };
}

export interface AttemptView {
  session_id: string;
  status: string;
  started_at: string;
  deadline_at: string;
  server_time: string;
  submitted_at: string | null;
  remaining_seconds: number;
  question_count: number;
  questions: {
    qvId: string;
    stem: string;
    options: { id: string; label: string }[];
    position: number;
    selectedOptionId: string | null;
  }[];
  score: { raw: number; max: number; percent: number } | null;
  review?: { qvId: string; answerOptionId: string; explanation: string }[];
}

export async function listExams() {
  return call<{ exams: Exam[] }>("/exams");
}

export async function createExam(input: {
  title: string;
  subjectId?: string;
  durationMinutes: number;
  startsAt: string;
  endsAt?: string;
  shuffle: boolean;
  questionCount?: number;
  questionVersionIds?: string[];
}) {
  return call<{ exam: Exam }>("/exams", { method: "POST", body: JSON.stringify(input) });
}

export async function getExam(idOrSlug: string) {
  return call<{ exam: Exam }>(`/exams/${encodeURIComponent(idOrSlug)}`);
}

export async function examInvite(slug: string) {
  return call<{ exam: Exam }>(`/exams/link/${encodeURIComponent(slug)}`);
}

export async function startExam(idOrSlug: string) {
  return call<AttemptView>(`/exams/${encodeURIComponent(idOrSlug)}/start`, {
    method: "POST",
    body: JSON.stringify({}),
  });
}

export async function examResults(idOrSlug: string) {
  return call<{ candidates: ExamCandidate[] }>(`/exams/${encodeURIComponent(idOrSlug)}/results`);
}

export async function saveAnswer(sessionId: string, qvId: string, selectedOptionId: string) {
  return call<Record<string, unknown>>(
    `/assessment-sessions/${encodeURIComponent(sessionId)}/responses/${encodeURIComponent(qvId)}`,
    { method: "PUT", body: JSON.stringify({ selectedOptionId }) },
  );
}

export async function submitAttempt(sessionId: string) {
  return call<Record<string, unknown>>(`/assessment-sessions/${encodeURIComponent(sessionId)}/submit`, {
    method: "POST",
    body: JSON.stringify({}),
  });
}

// ── consistency ─────────────────────────────────────────────────────────────────────────────────

export interface ConsistencyDay {
  date: string;
  active: boolean;
  kinds: string[];
  isToday: boolean;
  inStreak: boolean;
}

export interface Consistency {
  currentStreak: number;
  longestStreak: number;
  activeDays: number;
  windowDays: number;
  totalActiveDays: number;
  todayActive: boolean;
  streakAtRisk: boolean;
  timeZone: string;
  days: ConsistencyDay[];
  kindLabels: Record<string, string>;
}

export async function getConsistency(days = 30) {
  return call<{ consistency: Consistency }>(`/consistency?days=${days}`);
}

// ── wallet ──────────────────────────────────────────────────────────────────────────────────────

export interface Topup {
  id: string;
  reference: string;
  amountMinor: number;
  currency: string;
  points: number;
  nairaPerPoint: number;
  status: "pending" | "paid" | "failed";
  provider: string;
  createdAt: string;
  paidAt: string | null;
}

export interface LedgerRow {
  id: string;
  amount: number;
  entryType: string;
  source: string;
  note: string;
  createdAt: string;
}

export interface Wallet {
  balance: number;
  earned: number;
  spent: number;
  /** The recharge floor in kobo — 20000 is ₦200. Rendered from the server so a price change needs no release. */
  minTopupMinor: number;
  nairaPerPoint: number;
  currency: string;
  topups: Topup[];
  recent: LedgerRow[];
}

export async function getWallet() {
  return call<{ wallet: Wallet }>("/wallet");
}

export async function startTopup(amountMinor: number) {
  return call<{ topup: Topup }>("/wallet/topups", {
    method: "POST",
    body: JSON.stringify({ amountMinor }),
  });
}

/**
 * Settle a top-up without a payment provider. Registered only where the deployment allows sandbox payments, so
 * this can 403 in production — the wallet screen offers it only when the environment says so.
 */
export async function confirmTopup(id: string) {
  return call<{ topup: Topup; wallet: Wallet }>(`/wallet/topups/${id}/confirm`, {
    method: "POST",
    body: JSON.stringify({}),
  });
}

// ── practice selection ──────────────────────────────────────────────────────────────────────────

export interface PracticeSelection {
  /** `practice` is untimed revision; `simulation` is a clocked paper with no corrections afterwards. */
  mode?: "practice" | "simulation";
  subjectId?: string;
  topicIds?: string[];
  classId?: string;
  /** WAEC, NECO, JAMB, Post-UTME — the pathway key or body, matched case-insensitively. */
  examBody?: string;
  mixed?: boolean;
  difficulty?: string;
  count?: number;
  durationMinutes?: number;
}

export async function startPracticeSession(input: PracticeSelection) {
  return call<{ session: { session_id: string; mode: string; remaining_seconds: number } }>("/practice-sessions", {
    method: "POST",
    body: JSON.stringify(input),
  });
}
