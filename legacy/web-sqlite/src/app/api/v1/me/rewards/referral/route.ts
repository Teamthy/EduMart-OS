import { handle, ok } from "@/lib/server/http";
import { requireUser } from "@/lib/server/auth";
import { q } from "@/lib/server/db";
import { ensureReferralCode } from "@/lib/server/rewards";

export async function GET(req: Request) {
  return handle(req, async () => {
    const user = await requireUser();
    const code = ensureReferralCode(user.id, user.displayName);
    const attributions = q.all<any>(
      `SELECT ra.status, ra.attributed_at, u.display_name
         FROM referral_attributions ra JOIN users u ON u.id = ra.referred_user_id
        WHERE ra.referral_code_id = ? ORDER BY ra.attributed_at DESC`,
      code.id,
    );
    return ok({ code: code.code, status: code.status, attributions });
  });
}
