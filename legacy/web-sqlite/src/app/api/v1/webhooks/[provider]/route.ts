import { handle, ok } from "@/lib/server/http";
import { processProviderEvent } from "@/lib/server/commerce";

/**
 * Provider webhook. Signature is verified over the raw body before any state change;
 * events are deduplicated on (provider, event_id) so retries and out-of-order delivery are safe.
 */
export async function POST(req: Request, ctx: { params: Promise<{ provider: string }> }) {
  return handle(req, async () => {
    const { provider } = await ctx.params;
    const raw = await req.text();
    const signature = req.headers.get("x-edumart-signature");
    const result = processProviderEvent(provider, raw, signature);
    return ok(result, { status: 200 });
  });
}
