import { createHash, randomBytes, randomUUID } from "node:crypto";
import { cookies } from "next/headers";
import { q, nowIso, isoPlus } from "./db";

export const SESSION_COOKIE = "edumart_session";
const SESSION_TTL_MS = Number(process.env.SESSION_TTL_HOURS ?? 168) * 60 * 60 * 1000;

// ─────────────────────────────── passwords ───────────────────────────────
export { hashPassword, verifyPassword } from "./password";

export function sha256(value: string): string {
  return createHash("sha256").update(value).digest("hex");
}

export function normalizeEmail(email: string): string {
  return email.trim().toLowerCase();
}

// ─────────────────────────────── sessions ────────────────────────────────

export type AuthUser = {
  id: string;
  email: string;
  displayName: string;
  status: string;
  examPathwayId: string | null;
  targetDate: string | null;
  timezone: string;
  roles: string[];
  createdAt: string;
};

export function rolesFor(userId: string): string[] {
  const rows = q.all<{ role: string }>("SELECT role FROM role_assignments WHERE user_id = ?", userId);
  return rows.map((r) => r.role);
}

export function createSession(userId: string, userAgent?: string | null): { token: string; expiresAt: string } {
  const token = randomBytes(32).toString("base64url");
  const expiresAt = isoPlus(SESSION_TTL_MS);
  q.run(
    "INSERT INTO sessions (id, user_id, user_agent, created_at, expires_at) VALUES (?, ?, ?, ?, ?)",
    sha256(token),
    userId,
    userAgent ?? null,
    nowIso(),
    expiresAt,
  );
  return { token, expiresAt };
}

export function revokeSessionByToken(token: string) {
  q.run("UPDATE sessions SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL", nowIso(), sha256(token));
}

/** Resolves the current user from the HttpOnly session cookie. Server-side only. */
export async function getCurrentUser(): Promise<AuthUser | null> {
  const store = await cookies();
  const token = store.get(SESSION_COOKIE)?.value;
  if (!token) return null;

  const row = q.get<any>(
    `SELECT s.id AS session_id, s.expires_at, s.revoked_at,
            u.id, u.email_normalized, u.display_name, u.status, u.exam_pathway_id, u.target_date, u.timezone, u.created_at
       FROM sessions s JOIN users u ON u.id = s.user_id
      WHERE s.id = ?`,
    sha256(token),
  );
  if (!row) return null;
  if (row.revoked_at) return null;
  if (new Date(row.expires_at).getTime() < Date.now()) return null;
  if (row.status !== "active") return null;

  return {
    id: row.id,
    email: row.email_normalized,
    displayName: row.display_name,
    status: row.status,
    examPathwayId: row.exam_pathway_id,
    targetDate: row.target_date,
    timezone: row.timezone ?? "Africa/Lagos",
    roles: rolesFor(row.id),
    createdAt: row.created_at,
  };
}

export async function requireUser(): Promise<AuthUser> {
  const user = await getCurrentUser();
  if (!user) throw new AuthError("UNAUTHENTICATED", "Please sign in to continue.", 401);
  return user;
}

export class AuthError extends Error {
  code: string;
  status: number;
  constructor(code: string, message: string, status = 403) {
    super(message);
    this.code = code;
    this.status = status;
  }
}

export function requireRole(user: AuthUser, ...roles: string[]): void {
  if (user.roles.includes("platform_admin")) return;
  if (!roles.some((r) => user.roles.includes(r))) {
    throw new AuthError("FORBIDDEN", "You do not have permission to perform this action.", 403);
  }
}

// ───────────────────────── cookie write helpers ──────────────────────────

export function sessionCookie(token: string, maxAgeSeconds: number) {
  return {
    name: SESSION_COOKIE,
    value: token,
    httpOnly: true,
    sameSite: "lax" as const,
    secure: process.env.NODE_ENV === "production",
    path: "/",
    maxAge: maxAgeSeconds,
  };
}

export function newId(): string {
  return randomUUID();
}
