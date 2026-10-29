/**
 * Wire types for the Edumart API. These mirror `apps/api/internal/**` JSON payloads exactly:
 * field names are the Go struct tags, so a change on the server must be reflected here.
 */

// ── error envelope ────────────────────────────────────────────────────────
export type ErrorCode =
  | "VALIDATION_ERROR"
  | "UNAUTHENTICATED"
  | "FORBIDDEN"
  | "NOT_FOUND"
  | "CONFLICT"
  | "ASSESSMENT_NOT_OPEN"
  | "SESSION_EXPIRED"
  | "SESSION_CLOSED"
  | "RATE_LIMITED"
  | "PAYMENT_PENDING"
  | "EMAIL_NOT_VERIFIED"
  | "INTERNAL_ERROR";

export interface FieldError {
  field: string;
  message: string;
}

export interface ApiErrorBody {
  error: {
    code: ErrorCode;
    message: string;
    request_id: string;
    details: FieldError[] | Record<string, unknown>;
  };
}

// ── identity ──────────────────────────────────────────────────────────────
export type Role =
  | "student"
  | "teacher"
  | "org_admin"
  | "content_editor"
  | "finance"
  | "support"
  | "platform_admin";

export interface User {
  id: string;
  email: string;
  displayName: string;
  status: string;
  examPathwayId: string | null;
  targetDate: string | null;
  timezone: string;
  dailyMinutes: number;
  onboardingStep: number;
  onboardingComplete: boolean;
  roles: Role[];
  subjectIds: string[];
  /** E.164 number on file, whether or not it has been verified. */
  phone: string | null;
  /** True only after a code sent to the address was confirmed. */
  emailVerified: boolean;
  phoneVerified: boolean;
  createdAt: string;
}

// ── institutions ──────────────────────────────────────────────────────────
export type OrganizationRole = "owner" | "admin" | "teacher" | "student" | "platform_admin";
export type OrganizationStatus = "pending_verification" | "active" | "suspended";

export interface Organization {
  id: string;
  name: string;
  slug: string;
  kind: "school" | "centre" | "ngo" | "company";
  status: OrganizationStatus;
  verified: boolean;
  /** The signed-in user's own role in this organisation. */
  role: OrganizationRole;
  members: number;
  classes: number;
  createdAt: string;
  verifiedAt?: string;
}

export interface OrganizationMember {
  userId: string;
  displayName: string;
  email: string;
  role: Exclude<OrganizationRole, "platform_admin">;
  status: string;
  joinedAt: string;
}

export interface Invitation {
  id: string;
  email: string;
  role: "admin" | "teacher" | "student";
  status: "pending" | "accepted" | "revoked" | "expired";
  expiresAt: string;
  createdAt: string;
  maxUses: number;
  useCount: number;
}

/** Where a verification code was sent, and for how long it is good. */
export interface VerificationIssued {
  sent: boolean;
  channel: "email" | "phone";
  /** Masked: enough to recognise the address or number, not enough to harvest it. */
  destination: string;
  expires_at: string;
  resend_after: number;
  attempts_allowed: number;
}

export interface SessionResponse {
  token: string;
  expires_at: string;
  user: User;
  next: string;
}

// ── taxonomy ──────────────────────────────────────────────────────────────
export interface Pathway {
  id: string;
  key: string;
  name: string;
  body: string;
  blurb: string;
  subjects: number;
}

export interface Subject {
  id: string;
  key: string;
  name: string;
  icon: string;
  pathwayKey: string;
  questionCount: number;
}

export interface Topic {
  id: string;
  key: string;
  name: string;
  questionCount: number;
  sortOrder: number;
}

// ── assessment ────────────────────────────────────────────────────────────
export type SessionStatus = "in_progress" | "submitted" | "expired" | "cancelled";

export interface AnswerOption {
  id: string;
  label: string;
}

/** A learner-facing question. Never carries the answer key or explanation. */
export interface QuestionView {
  qvId: string;
  stem: string;
  options: AnswerOption[];
  position: number;
  topicName?: string;
  subjectName?: string;
  difficulty?: string;
  selectedOptionId: string | null;
  answerVersion: number;
}

export interface ReviewItem {
  qvId: string;
  answerOptionId: string;
  explanation: string;
  topicName?: string;
  topicId?: string;
  difficulty?: string;
}

export interface ScoreView {
  raw: number;
  max: number;
  percent: number;
}

export interface AssessmentSessionView {
  session_id: string;
  mode: "practice" | "mock" | "class";
  status: SessionStatus;
  attempt_no: number;
  started_at: string;
  deadline_at: string;
  submitted_at: string | null;
  server_time: string;
  question_count: number;
  questions: QuestionView[];
  score: ScoreView | null;
  result_released: boolean;
  remaining_seconds: number;
  review?: ReviewItem[];
  definitionId?: string;
}

export interface SaveAnswerResult {
  saved: boolean;
  server_saved_at: string;
  version: number;
  session_status: SessionStatus;
  remaining_seconds: number;
}

export interface ConflictDetails {
  current_version?: number;
  [key: string]: unknown;
}

// ── dashboard and analytics ───────────────────────────────────────────────
export interface TopicStat {
  topicId: string;
  topicName: string;
  subjectName: string;
  attempts: number;
  correct: number;
  accuracy: number;
  trend: "new" | "strong" | "steady" | "needs-work";
}

export interface RecentAttempt {
  id: string;
  mode: string;
  title: string;
  status: SessionStatus;
  scorePercent: number | null;
  startedAt: string;
  submittedAt: string | null;
}

export interface Dashboard {
  displayName: string;
  daysToExam: number | null;
  targetDate: string | null;
  examPathway: string;
  pointsBalance: number;
  streakDays: number;
  attemptsCompleted: number;
  averagePercent: number | null;
  studyMinutesThisWeek: number;
  recentAttempts: RecentAttempt[];
  weakTopics: TopicStat[];
  strongTopics: TopicStat[];
  activeSessionId: string | null;
  activeSessionDeadline: string | null;
  recommendation: string;
}

export interface SubjectSummary {
  subjectId: string;
  subjectName: string;
  attempts: number;
  accuracy: number;
}

export interface LearningSummary {
  topics: TopicStat[];
  attempts: RecentAttempt[];
  totalAttempts: number;
  questionsAnswered: number;
  averagePercent: number | null;
  bestPercent: number | null;
  bySubject: SubjectSummary[];
}

export interface StudyPlan {
  targetDate: string | null;
  dailyMinutes: number;
  focusTopics: string[];
  updatedAt: string;
}

// ── classes ───────────────────────────────────────────────────────────────
export interface ClassSummary {
  id: string;
  name: string;
  /** The institution that owns the class; null for a teacher's personal class. */
  organizationId: string | null;
  subjectId: string | null;
  joinCode: string;
  /**
   * When the current code stops working. Null only for rows that predate the column; codes are issued
   * with a 30-day window and rotation replaces the old code outright.
   */
  joinCodeExpiresAt: string | null;
  role: string;
  members: number;
  assignments: number;
  createdAt: string;
}

/**
 * The pair issued at sign-in. The access token is short-lived; the refresh token is single-use and
 * rotated on every exchange, so a client must replace what it stores with each response.
 */
export interface SessionIssued {
  token: string;
  expiresAt: string;
  refreshToken: string;
  refreshExpiresAt: string;
  user: User;
  next?: string;
}

/** One message the system promised to send later. */
export interface Reminder {
  id: string;
  userId: string;
  kind: "study_nudge" | "deadline";
  templateKey: string;
  subject: string;
  /** Safe to display: generated from public facts, never a credential. */
  body: string;
  resourceId: string;
  dueAt: string;
  dedupeKey: string;
  status: "pending" | "claimed" | "sent" | "skipped" | "cancelled";
  createdAt: string;
  claimedAt: string;
  settledAt: string;
  deliveryId: string;
  /** Why a reminder was skipped, e.g. "already submitted". */
  note: string;
}

export interface ReminderStats {
  pending: number;
  claimed: number;
  sent: number;
  skipped: number;
  cancelled: number;
}

/** An institution's own dashboard: its people, its teaching, and what its learners actually did. */
export interface OrganizationReport {
  organizationId: string;
  generatedAt: string;
  windowDays: number;
  members: { total: number; owners: number; admins: number; teachers: number; students: number };
  teaching: { classes: number; assignments: number };
  results: {
    activeLearners: number;
    submissions: number;
    /** Null rather than 0 when nothing has been submitted: zero would be a claim, null is the truth. */
    averageScorePercent: number | null;
  };
  classes: Array<{
    id: string;
    name: string;
    members: number;
    assignments: number;
    submissions: number;
    averageScorePercent: number | null;
  }>;
}

export interface ClassMember {
  userId: string;
  displayName: string;
  email: string;
  role: string;
  joinedAt: string;
  attempts: number;
  averagePercent: number | null;
}

export interface Assignment {
  definitionId: string;
  title: string;
  dueAt: string | null;
  status: string;
  durationSeconds: number;
  submissions: number;
  roster: number;
  myStatus: string;
}

// ── commerce ──────────────────────────────────────────────────────────────
export interface Product {
  id: string;
  slug: string;
  title: string;
  subtitle: string;
  description: string;
  type: string;
  subjectKey: string;
  examKey: string;
  priceMinor: number;
  currency: string;
  pages: number;
  coverTone: string;
  publisher: string;
  owned: boolean;
}

export interface OrderItem {
  productId: string;
  productSlug: string;
  title: string;
  unitAmountMinor: number;
  quantity: number;
}

/** One recorded return of money against an order. Refunds are appended, never applied as edits. */
export interface Refund {
  id: string;
  orderId: string;
  amountMinor: number;
  currency: string;
  /** True when this refund returned everything that was left on the order. */
  fullRefund: boolean;
  reason: string;
  actorUserId: string | null;
  provider: string;
  providerRefundId: string | null;
  idempotencyKey: string;
  createdAt: string;
}

export interface Order {
  id: string;
  /** `paid` and `partially_refunded` both mean the platform still holds money for this order. */
  status: "pending_payment" | "paid" | "partially_refunded" | "refunded" | "cancelled";
  currency: string;
  subtotalMinor: number;
  discountMinor: number;
  totalMinor: number;
  reference: string;
  createdAt: string;
  paidAt: string | null;
  items: OrderItem[];
  /** Derived from the refund rows on every read; the total is never rewritten. */
  refundedMinor: number;
  refunds: Refund[];
}

/** Where two records of the same money disagree. */
export type DiscrepancyKind =
  | "paid_without_entitlement"
  | "entitlement_without_payment"
  | "refund_exceeds_total"
  | "succeeded_attempt_unpaid"
  | "paid_without_attempt";

export interface Discrepancy {
  kind: DiscrepancyKind;
  orderId: string;
  detail: string;
}

export interface Reconciliation {
  from: string;
  to: string;
  /** How many distinct checks ran. A report that checked nothing is not a clean report. */
  checked: number;
  paidOrders: number;
  refundedMinor: number;
  webhookFailures: number;
  discrepancies: Discrepancy[];
}

// ── rewards ───────────────────────────────────────────────────────────────
export interface PointsEntry {
  id: string;
  amount: number;
  entryType: "earn" | "spend" | "adjust";
  sourceType: string;
  note: string;
  createdAt: string;
}

export interface RewardsSummary {
  pointsBalance: number;
  pointsEarned: number;
  referralCode: string;
  referrals: number;
  qualifiedReferrals: number;
  commissionMinor: number;
  currency: string;
  entries: PointsEntry[];
}

// ── staff ─────────────────────────────────────────────────────────────────
export interface ReviewQueueItem {
  qvId: string;
  questionId: string;
  version: number;
  stem: string;
  subjectName: string;
  topicName: string;
  difficulty: string;
  reviewState: string;
  sourceName: string;
  licenseBasis: string;
  attribution: string;
  createdAt: string;
}

export interface PlatformMetrics {
  users: number;
  activeLearners: number;
  attempts: number;
  attemptsToday: number;
  paidOrders: number;
  revenueMinor: number;
  currency: string;
  pendingReview: number;
  openCases: number;
  publishedQuestions: number;
}

export interface SupportCase {
  id: string;
  subject: string;
  category: string;
  priority: string;
  status: string;
  openedBy: string;
  createdAt: string;
}
