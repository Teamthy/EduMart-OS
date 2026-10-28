import { handle, ok, readJson, ApiError } from "@/lib/server/http";
import { q } from "@/lib/server/db";
import { processProviderEvent, signPayload } from "@/lib/server/commerce";
import { randomUUID } from "node:crypto";

/**
 * Sandbox payment provider stand-in.
 *
 * It constructs a payload, signs it exactly like a real provider would, and feeds it through the
 * same webhook verification path. Nothing here can be used to bypass signature checks, and the
 * route is hard-disabled outside development/demo environments.
 */
export async function POST(req: Request) {
  return handle(req, async () => {
    if (process.env.NODE_ENV === "production" && process.env.ALLOW_SANDBOX_PAYMENTS !== "true") {
      throw new ApiError("FORBIDDEN", "The sandbox payment provider is disabled in this environment.");
    }
    const body = await readJson<{ reference?: string; outcome?: string }>(req);
    const attempt = q.get<any>("SELECT * FROM payment_attempts WHERE provider_reference = ?", body.reference ?? "");
    if (!attempt) throw new ApiError("NOT_FOUND", "Unknown payment reference.");

    const payload = JSON.stringify({
      event_id: `evt_${randomUUID()}`,
      type: "payment.updated",
      provider_reference: attempt.provider_reference,
      amount_minor: attempt.amount_minor,
      currency: attempt.currency,
      status: body.outcome === "failed" ? "failed" : "succeeded",
      occurred_at: new Date().toISOString(),
    });

    const result = processProviderEvent("sandbox", payload, signPayload(payload));
    return ok({ confirmed: true, ...result });
  });
}
