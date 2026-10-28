import { NextResponse } from "next/server";
import { randomUUID } from "node:crypto";
import { AuthError } from "./auth";
import { q, nowIso, id } from "./db";

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

const STATUS: Record<ErrorCode, number> = {
  VALIDATION_ERROR: 400,
  UNAUTHENTICATED: 401,
  FORBIDDEN: 403,
  NOT_FOUND: 404,
  CONFLICT: 409,
  ASSESSMENT_NOT_OPEN: 409,
  SESSION_EXPIRED: 409,
  SESSION_CLOSED: 409,
  RATE_LIMITED: 429,
  PAYMENT_PENDING: 409,
  INTERNAL_ERROR: 500,
};

export class ApiError extends Error {
  code: ErrorCode;
  status: number;
  details?: unknown;
  constructor(code: ErrorCode, message: string, details?: unknown, status?: number) {
    super(message);
    this.code = code;
    this.status = status ?? STATUS[code] ?? 500;
    this.details = details;
  }
}

export function requestId(req?: Request): string {
  return req?.headers.get("x-request-id") || randomUUID();
}

export function ok<T>(data: T, init?: { status?: number; requestId?: string; headers?: Record<string, string> }) {
  return NextResponse.json(data as any, {
    status: init?.status ?? 200,
    headers: {
      "x-request-id": init?.requestId ?? randomUUID(),
      "cache-control": "no-store",
      ...(init?.headers ?? {}),
    },
  });
}

export function fail(code: ErrorCode, message: string, details?: unknown, reqId?: string) {
  return NextResponse.json(
    { error: { code, message, request_id: reqId ?? randomUUID(), details: details ?? [] } },
    { status: STATUS[code] ?? 500, headers: { "x-request-id": reqId ?? randomUUID(), "cache-control": "no-store" } },
  );
}

/** Uniform handler: never leaks stack traces or secrets to clients. */
export async function handle(req: Request, fn: (reqId: string) => Promise<Response>): Promise<Response> {
  const reqId = requestId(req);
  try {
    const res = await fn(reqId);
    res.headers.set("x-request-id", reqId);
    return res;
  } catch (err) {
    if (err instanceof ApiError) return fail(err.code, err.message, err.details, reqId);
    if (err instanceof AuthError) {
      return fail(err.code as ErrorCode, err.message, [], reqId);
    }
    console.error(`[api] unhandled error request_id=${reqId}`, err);
    return fail("INTERNAL_ERROR", "Something went wrong. Please try again with the request ID shown.", [], reqId);
  }
}

export async function readJson<T>(req: Request): Promise<T> {
  const type = req.headers.get("content-type") ?? "";
  if (!type.includes("application/json")) {
    throw new ApiError("VALIDATION_ERROR", "Expected a JSON request body.");
  }
  let body: unknown;
  try {
    body = await req.json();
  } catch {
    throw new ApiError("VALIDATION_ERROR", "The request body is not valid JSON.");
  }
  if (!body || typeof body !== "object") {
    throw new ApiError("VALIDATION_ERROR", "The request body must be a JSON object.");
  }
  return body as T;
}

// ─────────────────────────── rate limiting ───────────────────────────
// In-process sliding window. Production: Redis-backed with the same interface.

type Bucket = { hits: number[] };
const buckets = new Map<string, Bucket>();

export function rateLimit(key: string, limit: number, windowMs: number): { allowed: boolean; retryAfter: number } {
  const now = Date.now();
  const bucket = buckets.get(key) ?? { hits: [] };
  bucket.hits = bucket.hits.filter((t) => now - t < windowMs);
  if (bucket.hits.length >= limit) {
    buckets.set(key, bucket);
    const retryAfter = Math.ceil((windowMs - (now - bucket.hits[0])) / 1000);
    return { allowed: false, retryAfter: Math.max(1, retryAfter) };
  }
  bucket.hits.push(now);
  buckets.set(key, bucket);
  if (buckets.size > 5000) buckets.delete(buckets.keys().next().value as string);
  return { allowed: true, retryAfter: 0 };
}

export function clientIp(req: Request): string {
  return (
    req.headers.get("x-forwarded-for")?.split(",")[0]?.trim() ||
    req.headers.get("x-real-ip") ||
    "local"
  );
}

// ───────────────────────────── idempotency ─────────────────────────────

/**
 * Retry-safe writes: if the same Idempotency-Key has been seen for this scope, replay the
 * stored response instead of repeating the effect (start attempt, submit, order, webhook).
 */
export function withIdempotency<T extends unknown>(
  opts: { key: string | null; scope: string; userId?: string; requestHash?: string },
  fn: () => { status: number; body: T },
): { status: number; body: T; replayed: boolean } {
  if (!opts.key) {
    const out = fn();
    return { ...out, replayed: false };
  }
  const existing = q.get<any>("SELECT * FROM idempotency_records WHERE key = ?", `${opts.scope}:${opts.key}`);
  if (existing) {
    return { status: existing.status_code, body: JSON.parse(existing.response_json), replayed: true };
  }
  const out = fn();
  q.run(
    "INSERT INTO idempotency_records (id, key, scope, user_id, request_hash, response_json, status_code, created_at) VALUES (?,?,?,?,?,?,?,?)",
    id(),
    `${opts.scope}:${opts.key}`,
    opts.scope,
    opts.userId ?? null,
    opts.requestHash ?? "",
    JSON.stringify(out.body),
    out.status,
    nowIso(),
  );
  return { ...out, replayed: false };
}

export function idempotencyKeyFrom(req: Request): string | null {
  const key = req.headers.get("idempotency-key");
  return key && key.length >= 8 && key.length <= 200 ? key : null;
}

/**
 * CSRF defence in depth for cookie-authenticated state changes.
 *
 * The session cookie is SameSite=Lax, which already blocks cross-site form posts; this check
 * adds an explicit Origin comparison. Host discovery accounts for reverse proxies that forward
 * the public host separately (preview hosts, load balancers) and falls back to the request URL.
 */
export function assertSameOrigin(req: Request): void {
  const method = req.method.toUpperCase();
  if (method === "GET" || method === "HEAD") return;

  const origin = req.headers.get("origin");
  if (!origin) return; // non-browser clients (curl, tests, server-to-server) send no Origin

  let originHost: string;
  try {
    originHost = new URL(origin).host;
  } catch {
    throw new ApiError("FORBIDDEN", "Invalid request origin.");
  }

  let requestHost: string | null = null;
  try {
    requestHost = new URL(req.url).host;
  } catch {
    requestHost = null;
  }

  const allowed = new Set(
    [
      req.headers.get("host"),
      req.headers.get("x-forwarded-host")?.split(",")[0]?.trim(),
      requestHost,
    ].filter(Boolean) as string[],
  );

  // If the hosting environment gives us no way to know our own host, rely on SameSite cookies.
  if (allowed.size === 0) return;
  if (!allowed.has(originHost)) {
    throw new ApiError("FORBIDDEN", "Cross-site request blocked.");
  }
}
