import { NextResponse } from "next/server";

import { DRAFT_COOKIE } from "@/lib/signup-cookie";
import { createDraft, readDraft } from "@/lib/signup-draft";

/**
 * Starts (or resumes) the signup wizard.
 *
 * A route handler rather than a server action because it must work as a plain form POST — the first
 * step of signup cannot depend on client JavaScript — and because it sets the draft cookie.
 *
 * The answers themselves live in the Go API; only the opaque draft token travels in this cookie, so
 * nothing the learner typed is readable from the browser.
 */
export async function POST(request: Request) {
  const origin = publicOrigin(request);
  const form = await request.formData().catch(() => null);
  const referral = form?.get("referralCode");

  const existing = await readDraft().catch(() => null);
  if (existing?.draft && existing.step) {
    return NextResponse.redirect(new URL(`/register/${existing.step}`, origin), { status: 303 });
  }

  let created;
  try {
    created = await createDraft(typeof referral === "string" ? referral : "");
  } catch {
    return NextResponse.redirect(new URL("/register?error=unavailable", origin), { status: 303 });
  }

  const response = NextResponse.redirect(new URL("/register/pathway", origin), { status: 303 });
  if (created.token) {
    response.cookies.set(DRAFT_COOKIE, created.token, {
      httpOnly: true,
      sameSite: "lax",
      path: "/",
      maxAge: 60 * 60 * 24 * 30,
      secure: process.env.NODE_ENV === "production",
    });
  }
  return response;
}

/**
 * Redirects must be absolute, and `request.url` on a self-hosted Next server reflects the bind
 * address (0.0.0.0), which a browser cannot follow. Prefer the forwarded host so the redirect works
 * behind a proxy or preview domain.
 */
function publicOrigin(request: Request): string {
  const headers = request.headers;
  const host = headers.get("x-forwarded-host") ?? headers.get("host");
  if (!host) return new URL(request.url).origin;
  const proto = headers.get("x-forwarded-proto") ?? new URL(request.url).protocol.replace(":", "");
  return `${proto}://${host}`;
}
