import { cookies } from "next/headers";
import { q, nowIso, id } from "@/lib/server/db";
import { ApiError, handle, ok, readJson, rateLimit, clientIp, assertSameOrigin } from "@/lib/server/http";
import { hashPassword, normalizeEmail, createSession, sessionCookie } from "@/lib/server/auth";
import { ensureReferralCode, attributeReferral } from "@/lib/server/rewards";
import { audit, track } from "@/lib/server/audit";

type Body = { email?: string; password?: string; displayName?: string; examPathwayId?: string; targetDate?: string; referralCode?: string };

export async function POST(req: Request) {
  return handle(req, async () => {
    assertSameOrigin(req);
    const limit = rateLimit(`register:${clientIp(req)}`, 10, 60_000);
    if (!limit.allowed) throw new ApiError("RATE_LIMITED", "Too many sign-up attempts. Please wait a moment.");

    const body = await readJson<Body>(req);
    const email = normalizeEmail(body.email ?? "");
    const password = body.password ?? "";
    const displayName = (body.displayName ?? "").trim();

    const details: { field: string; message: string }[] = [];
    if (!/^[^@\s]+@[^@\s]+\.[^@\s]+$/.test(email)) details.push({ field: "email", message: "Enter a valid email address." });
    if (password.length < 8) details.push({ field: "password", message: "Use at least 8 characters." });
    if (displayName.length < 2) details.push({ field: "displayName", message: "Enter your name." });
    if (details.length) throw new ApiError("VALIDATION_ERROR", "Please fix the highlighted fields.", details);

    // Uniform response for duplicate email keeps the endpoint free of account enumeration.
    if (q.get("SELECT id FROM users WHERE email_normalized = ?", email)) {
      throw new ApiError("CONFLICT", "An account with that email already exists. Try signing in instead.");
    }

    const userId = id();
    const timestamp = nowIso();
    q.run(
      `INSERT INTO users (id, email_normalized, password_hash, display_name, status, email_verified_at, exam_pathway_id, target_date, timezone, created_at, updated_at)
       VALUES (?,?,?,?, 'active', NULL, ?, ?, 'Africa/Lagos', ?, ?)`,
      userId,
      email,
      hashPassword(password),
      displayName,
      body.examPathwayId ?? null,
      body.targetDate ?? null,
      timestamp,
      timestamp,
    );
    q.run("INSERT INTO role_assignments (id, user_id, role, scope) VALUES (?,?, 'student', 'global')", id(), userId);
    ensureReferralCode(userId, displayName);
    if (body.referralCode) attributeReferral(userId, body.referralCode);

    const { token, expiresAt } = createSession(userId, req.headers.get("user-agent"));
    (await cookies()).set(sessionCookie(token, Math.floor((new Date(expiresAt).getTime() - Date.now()) / 1000)));

    audit({ actorUserId: userId, action: "account.registered", resourceType: "user", resourceId: userId });
    track({ userId, eventType: "registration_completed", entityType: "user", entityId: userId });

    return ok(
      { user: { id: userId, email, displayName, roles: ["student"] }, next: "/onboarding" },
      { status: 201 },
    );
  });
}
