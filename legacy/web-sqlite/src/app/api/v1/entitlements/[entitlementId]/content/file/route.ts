import { handle, ApiError } from "@/lib/server/http";
import { requireUser } from "@/lib/server/auth";
import { q } from "@/lib/server/db";
import { verifySignature } from "@/lib/server/commerce";

export async function GET(req: Request, ctx: { params: Promise<{ entitlementId: string }> }) {
  return handle(req, async () => {
    const user = await requireUser();
    const { entitlementId } = await ctx.params;
    const url = new URL(req.url);
    const token = url.searchParams.get("token") ?? "";
    const expires = url.searchParams.get("expires") ?? "";

    if (!expires || new Date(expires).getTime() < Date.now()) throw new ApiError("FORBIDDEN", "This access link has expired.");
    const row = q.get<any>(
      `SELECT e.*, p.title, p.file_key FROM entitlements e JOIN products p ON p.id = e.product_id WHERE e.id = ?`,
      entitlementId,
    );
    if (!row || row.user_id !== user.id) throw new ApiError("NOT_FOUND", "Publication not found.");
    if (!verifySignature(`${row.file_key}:${expires}:${user.id}`, token)) {
      throw new ApiError("FORBIDDEN", "This access link is not valid for your account.");
    }

    return new Response(
      JSON.stringify({
        title: row.title,
        file_key: row.file_key,
        message: "In production this streams the protected PDF from private object storage using this signed token.",
        chapters: [
          { title: "Chapter 1 — Foundations", pages: "1–24" },
          { title: "Chapter 2 — Worked examples", pages: "25–68" },
          { title: "Chapter 3 — Timed drills", pages: "69–110" },
        ],
      }),
      { headers: { "content-type": "application/json", "cache-control": "private, no-store" } },
    );
  });
}
