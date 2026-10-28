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
  createdAt: string;
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
  subjectId: string | null;
  joinCode: string;
  role: string;
  members: number;
  assignments: number;
  createdAt: string;
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

export interface Order {
  id: string;
  status: "pending_payment" | "paid" | "refunded" | "cancelled";
  currency: string;
  subtotalMinor: number;
  discountMinor: number;
  totalMinor: number;
  reference: string;
  createdAt: string;
  paidAt: string | null;
  items: OrderItem[];
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
