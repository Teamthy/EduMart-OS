import { cookies } from "next/headers";

import type { ErrorCode } from "@edumart/shared";

/**
 * Server-side API access.
 *
 * Server components and server actions run on the Next.js server, so they call the Go API
 * directly (never through the public origin) and forward the inbound cookies explicitly. That is
 * what keeps the session httpOnly while still allowing server rendering.
 */
const API_URL = process.env.EDUMART_API_URL ?? "http://127.0.0.1:8080";

export class ServerApiError extends Error {
  readonly code: ErrorCode | "NETWORK_ERROR";
  readonly status: number;
  readonly requestId: string;
  readonly details: unknown;

  constructor(options: { code: ErrorCode | "NETWORK_ERROR"; message: string; status: number; requestId?: string; details?: unknown }) {
    super(options.message);
    this.name = "ServerApiError";
    this.code = options.code;
    this.status = options.status;
    this.requestId = options.requestId ?? "";
    this.details = options.details;
  }

  get isUnauthenticated() {
    return this.status === 401;
  }
  get isForbidden() {
    return this.status === 403;
  }
  get isNotFound() {
    return this.status === 404;
  }
}

interface FetchOptions {
  method?: "GET" | "POST" | "PATCH" | "PUT" | "DELETE";
  body?: unknown;
  /** Forward the browser's cookies (default true for authenticated reads). */
  withCookies?: boolean;
  cache?: RequestCache;
}

export async function serverFetch<T>(path: string, options: FetchOptions = {}): Promise<T> {
  const { method = "GET", body, withCookies = true, cache = "no-store" } = options;

  const headers: Record<string, string> = { Accept: "application/json" };
  if (body !== undefined) headers["Content-Type"] = "application/json";

  if (withCookies) {
    const cookieStore = await cookies();
    const cookieHeader = cookieStore
      .getAll()
      .map((cookie) => `${cookie.name}=${cookie.value}`)
      .join("; ");
    if (cookieHeader) headers.Cookie = cookieHeader;
  }

  let response: Response;
  try {
    response = await fetch(`${API_URL}${path}`, {
      method,
      headers,
      body: body === undefined ? undefined : JSON.stringify(body),
      cache,
    });
  } catch {
    throw new ServerApiError({
      code: "NETWORK_ERROR",
      message: "The API is unreachable. Start it with `npm run dev:api`.",
      status: 0,
    });
  }

  const text = await response.text();
  const payload: unknown = text ? safeJson(text) : {};

  if (!response.ok) {
    const errorBody = (payload as { error?: { code?: ErrorCode; message?: string; request_id?: string; details?: unknown } }).error;
    throw new ServerApiError({
      code: errorBody?.code ?? "INTERNAL_ERROR",
      message: errorBody?.message ?? "Something went wrong.",
      status: response.status,
      requestId: errorBody?.request_id,
      details: errorBody?.details,
    });
  }

  return payload as T;
}

function safeJson(text: string): unknown {
  try {
    return JSON.parse(text);
  } catch {
    return {};
  }
}
