/**
 * One typed API client for web and React Native.
 *
 * - The base URL is injected by the shell (browser: same-origin proxy; native: absolute URL).
 * - Failures always surface as `ApiError` with the server's code, message, request id and details,
 *   so every screen can render a truthful error state instead of a generic message.
 */
import type {
  ApiErrorBody,
  AssessmentSessionView,
  Assignment,
  ClassMember,
  ClassSummary,
  Dashboard,
  ErrorCode,
  FieldError,
  LearningSummary,
  Order,
  Pathway,
  PlatformMetrics,
  Product,
  ReviewQueueItem,
  RewardsSummary,
  SaveAnswerResult,
  SessionResponse,
  Organization,
  OrganizationMember,
  OrganizationReport,
  Invitation,
  StudyPlan,
  Subject,
  SupportCase,
  Topic,
  User,
  VerificationIssued,
} from "./types";

export class ApiError extends Error {
  readonly code: ErrorCode | "NETWORK_ERROR";
  readonly status: number;
  readonly requestId: string;
  readonly details: FieldError[] | Record<string, unknown>;

  constructor(options: {
    code: ErrorCode | "NETWORK_ERROR";
    message: string;
    status: number;
    requestId?: string;
    details?: FieldError[] | Record<string, unknown>;
  }) {
    super(options.message);
    this.name = "ApiError";
    this.code = options.code;
    this.status = options.status;
    this.requestId = options.requestId ?? "";
    this.details = options.details ?? [];
  }

  /** Field errors are rendered next to inputs; anything else is rendered as a banner. */
  get fieldErrors(): FieldError[] {
    return Array.isArray(this.details) ? this.details : [];
  }

  get isOffline(): boolean {
    return this.code === "NETWORK_ERROR";
  }

  get isPermissionDenied(): boolean {
    return this.code === "FORBIDDEN" || this.code === "UNAUTHENTICATED";
  }
}

export interface ApiClientOptions {
  baseUrl: string;
  /** Called when the server reports an unauthenticated request, so the shell can redirect. */
  onUnauthenticated?: () => void;
  /** Token store: web uses httpOnly cookies, native passes a bearer token. */
  getToken?: () => string | null | Promise<string | null>;
  fetchImpl?: typeof fetch;
}

export class ApiClient {
  private readonly baseUrl: string;
  private readonly options: ApiClientOptions;

  constructor(options: ApiClientOptions) {
    this.options = options;
    this.baseUrl = options.baseUrl.replace(/\/$/, "");
  }

  private async request<T>(method: string, path: string, body?: unknown): Promise<T> {
    const doFetch = this.options.fetchImpl ?? fetch;
    const headers: Record<string, string> = { Accept: "application/json" };
    if (body !== undefined) headers["Content-Type"] = "application/json";

    const token = await this.options.getToken?.();
    if (token) headers.Authorization = `Bearer ${token}`;

    let response: Response;
    try {
      response = await doFetch(`${this.baseUrl}${path}`, {
        method,
        headers,
        body: body === undefined ? undefined : JSON.stringify(body),
        credentials: "include",
      });
    } catch {
      throw new ApiError({
        code: "NETWORK_ERROR",
        message: "You're offline or the connection dropped. Your work is saved on this device.",
        status: 0,
      });
    }

    if (response.status === 204) return undefined as T;

    const text = await response.text();
    const payload: unknown = text ? safeJson(text) : {};

    if (!response.ok) {
      const errorBody = (payload as ApiErrorBody).error;
      if (response.status === 401) this.options.onUnauthenticated?.();
      throw new ApiError({
        code: errorBody?.code ?? "INTERNAL_ERROR",
        message: errorBody?.message ?? "Something went wrong. Please try again.",
        status: response.status,
        requestId: errorBody?.request_id,
        details: errorBody?.details,
      });
    }
    return payload as T;
  }

  private get<T>(path: string) {
    return this.request<T>("GET", path);
  }
  private post<T>(path: string, body?: unknown) {
    return this.request<T>("POST", path, body);
  }
  private put<T>(path: string, body?: unknown) {
    return this.request<T>("PUT", path, body);
  }
  private patch<T>(path: string, body?: unknown) {
    return this.request<T>("PATCH", path, body);
  }
  private delete<T>(path: string) {
    return this.request<T>("DELETE", path);
  }

  // ── identity ──────────────────────────────────────────────────────────
  register = (input: unknown) => this.post<SessionResponse>("/v1/auth/register", input);
  login = (email: string, password: string, client: "web" | "mobile" = "web") =>
    this.post<SessionResponse>("/v1/auth/login", { email, password, client });
  logout = () => this.post<{ signedOut: boolean }>("/v1/auth/logout");
  me = () => this.get<{ user: User }>("/v1/me");
  updateMe = (input: unknown) => this.patch<{ user: User }>("/v1/me", input);
  requestVerification = (channel: "email" | "phone", phone?: string) =>
    this.post<VerificationIssued>("/v1/auth/verification/request", { channel, phone });
  confirmVerification = (channel: "email" | "phone", code: string) =>
    this.post<{ verified: boolean; channel: string; user: User }>("/v1/auth/verification/confirm", {
      channel,
      code,
    });
  forgotPassword = (email: string) => this.post<{ sent: boolean; message: string }>("/v1/auth/password/forgot", { email });
  resetPassword = (token: string, password: string) =>
    this.post<{ reset: boolean }>("/v1/auth/password/reset", { token, password });

  // ── institutions ──────────────────────────────────────────────────────
  organizations = () => this.get<{ organizations: Organization[] }>("/v1/organizations");
  organization = (id: string) => this.get<{ organization: Organization }>(`/v1/organizations/${id}`);
  createOrganization = (input: { name: string; kind?: Organization["kind"] }) =>
    this.post<{ organization: Organization }>("/v1/organizations", input);
  organizationMembers = (id: string) =>
    this.get<{ members: OrganizationMember[] }>(`/v1/organizations/${id}/members`);
  inviteToOrganization = (id: string, input: { email: string; role: Invitation["role"] }) =>
    this.post<{ invitation: Invitation; token: string }>(`/v1/organizations/${id}/invitations`, input);
  organizationInvitations = (id: string) =>
    this.get<{ invitations: Invitation[] }>(`/v1/organizations/${id}/invitations`);
  revokeInvitation = (id: string, invitationId: string) =>
    this.delete<{ revoked: boolean }>(`/v1/organizations/${id}/invitations/${invitationId}`);
  removeMember = (id: string, userId: string) =>
    this.delete<{ removed: boolean }>(`/v1/organizations/${id}/members/${userId}`);
  acceptInvitation = (token: string) =>
    this.post<{ joined: boolean; organization: Organization }>("/v1/organizations/invitations/accept", { token });

  // ── taxonomy ──────────────────────────────────────────────────────────
  pathways = () => this.get<{ pathways: Pathway[] }>("/v1/exam-pathways");
  subjects = (pathwayKey?: string) =>
    this.get<{ subjects: Subject[] }>(`/v1/subjects${pathwayKey ? `?pathwayKey=${encodeURIComponent(pathwayKey)}` : ""}`);
  topics = (subjectId: string) =>
    this.get<{ subject: Subject; topics: Topic[] }>(`/v1/subjects/${subjectId}/topics`);
  validateReferral = (code: string) =>
    this.get<{ valid: boolean; referrerFirstName: string }>(`/v1/referrals/validate/${encodeURIComponent(code)}`);

  // ── home and analytics ───────────────────────────────────────────────
  dashboard = () => this.get<{ dashboard: Dashboard }>("/v1/dashboard");
  learningSummary = () => this.get<{ summary: LearningSummary }>("/v1/learning-summary");
  studyPlan = () => this.get<{ plan: StudyPlan }>("/v1/study-plan");
  saveStudyPlan = (input: { targetDate: string | null; dailyMinutes: number; focusTopics: string[] }) =>
    this.put<{ plan: StudyPlan }>("/v1/study-plan", input);

  // ── assessment ────────────────────────────────────────────────────────
  startPractice = (input: { subjectId: string; topicIds?: string[]; count?: number; difficulty?: string }) =>
    this.post<{ session: AssessmentSessionView }>("/v1/practice-sessions", input);
  startDefinition = (definitionId: string) =>
    this.post<{ session: AssessmentSessionView }>(`/v1/assessment-definitions/${definitionId}/sessions`);
  session = (id: string) => this.get<{ session: AssessmentSessionView }>(`/v1/assessment-sessions/${id}`);
  history = (limit = 20) => this.get<{ sessions: unknown[] }>(`/v1/assessment-sessions?limit=${limit}`);
  saveAnswer = (
    sessionId: string,
    qvId: string,
    input: { selectedOptionId: string | null; expectedVersion: number; clientSequence: number },
  ) => this.put<SaveAnswerResult>(`/v1/assessment-sessions/${sessionId}/responses/${qvId}`, input);
  submitSession = (id: string) => this.post<{ session: AssessmentSessionView }>(`/v1/assessment-sessions/${id}/submit`);
  cancelSession = (id: string) => this.post<{ cancelled: boolean }>(`/v1/assessment-sessions/${id}/cancel`);

  // ── classes ───────────────────────────────────────────────────────────
  classes = () => this.get<{ classes: ClassSummary[] }>("/v1/classes");
  createClass = (input: { name: string; subjectId?: string; organizationId?: string }) =>
    this.post<{ class: ClassSummary }>("/v1/classes", input);

  organizationClasses = (organizationId: string) =>
    this.get<{ classes: ClassSummary[] }>(`/v1/organizations/${organizationId}/classes`);
  organizationReport = (organizationId: string) =>
    this.get<{ report: OrganizationReport }>(`/v1/organizations/${organizationId}/report`);
  rotateClassJoinCode = (classId: string) =>
    this.post<{ class: ClassSummary }>(`/v1/classes/${classId}/join-code`, {});
  joinClass = (joinCode: string) => this.post<{ class: ClassSummary }>("/v1/classes/join", { joinCode });
  roster = (classId: string) => this.get<{ members: ClassMember[] }>(`/v1/classes/${classId}/roster`);
  assignments = (classId: string) => this.get<{ assignments: Assignment[] }>(`/v1/classes/${classId}/assignments`);

  // ── commerce ──────────────────────────────────────────────────────────
  products = (subjectKey?: string) =>
    this.get<{ products: Product[] }>(`/v1/products${subjectKey ? `?subject=${encodeURIComponent(subjectKey)}` : ""}`);
  createOrder = (productId: string) => this.post<{ order: Order }>("/v1/orders", { productId });
  order = (id: string) => this.get<{ order: Order }>(`/v1/orders/${id}`);
  orders = () => this.get<{ orders: Order[] }>("/v1/orders");
  library = () => this.get<{ products: Product[] }>("/v1/library");
  simulatePayment = (orderId: string) => this.post<{ order: Order }>(`/v1/orders/${orderId}/simulate-payment`);

  // ── rewards ───────────────────────────────────────────────────────────
  rewards = () => this.get<{ rewards: RewardsSummary }>("/v1/rewards");
  redeem = (perk: string) => this.post<{ rewards: RewardsSummary }>("/v1/rewards/redeem", { perk });

  // ── staff ─────────────────────────────────────────────────────────────
  reviewQueue = () => this.get<{ items: ReviewQueueItem[] }>("/v1/admin/review-queue");
  reviewQuestion = (qvId: string, decision: "approve" | "reject", reason: string) =>
    this.post<{ reviewed: boolean }>(`/v1/admin/question-versions/${qvId}/review`, { decision, reason });
  metrics = () => this.get<{ metrics: PlatformMetrics }>("/v1/admin/metrics");
  cases = (status = "open") => this.get<{ cases: SupportCase[] }>(`/v1/admin/cases?status=${status}`);
  resolveCase = (id: string) => this.post<{ resolved: boolean }>(`/v1/admin/cases/${id}/resolve`);
  openCase = (input: { subject: string; category?: string; body?: string }) =>
    this.post<{ case: SupportCase }>("/v1/support-cases", input);
}

function safeJson(text: string): unknown {
  try {
    return JSON.parse(text);
  } catch {
    return {};
  }
}

/** Human copy for an error code, used when the server message is too terse for a screen. */
export function errorTitle(error: unknown): string {
  if (error instanceof ApiError) {
    switch (error.code) {
      case "NETWORK_ERROR":
        return "You're offline";
      case "UNAUTHENTICATED":
        return "Please sign in again";
      case "FORBIDDEN":
        return "You don't have access";
      case "NOT_FOUND":
        return "Not found";
      case "SESSION_EXPIRED":
        return "Time's up";
      case "SESSION_CLOSED":
        return "This attempt is closed";
      case "RATE_LIMITED":
        return "Too many requests";
      case "VALIDATION_ERROR":
        return "Check your details";
      default:
        return "Something went wrong";
    }
  }
  return "Something went wrong";
}
