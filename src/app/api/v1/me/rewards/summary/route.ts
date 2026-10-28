import { handle, ok } from "@/lib/server/http";
import { requireUser } from "@/lib/server/auth";
import { pointsSummary, commissionSummary, ensureReferralCode } from "@/lib/server/rewards";

export async function GET(req: Request) {
  return handle(req, async () => {
    const user = await requireUser();
    const referral = ensureReferralCode(user.id, user.displayName);
    return ok({
      points: pointsSummary(user.id),
      commissions: commissionSummary(user.id),
      referral: { code: referral.code, status: referral.status },
    });
  });
}
