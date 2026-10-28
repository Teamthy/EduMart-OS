import Link from "next/link";
import { notFound } from "next/navigation";
import { Shell } from "@/components/Shell";
import { requirePageUser, naira } from "@/lib/server/guards";
import { q } from "@/lib/server/db";
import { SandboxPay } from "./SandboxPay";

export const dynamic = "force-dynamic";

export default async function SandboxCheckoutPage({ params }: { params: Promise<{ reference: string }> }) {
  const user = await requirePageUser();
  const { reference } = await params;
  const attempt = q.get<any>(
    `SELECT pa.*, o.user_id, o.reference AS order_reference, o.id AS order_id
       FROM payment_attempts pa JOIN orders o ON o.id = pa.order_id
      WHERE pa.provider_reference = ?`,
    reference,
  );
  if (!attempt || attempt.user_id !== user.id) notFound();

  return (
    <Shell title="Sandbox provider" subtitle="This page stands in for the payment provider's hosted checkout" activePath="/library">
      <div className="split" style={{ gridTemplateColumns: "1fr 1fr", alignItems: "start" }}>
        <section className="card pad-lg stack">
          <div className="row-between">
            <span className="badge amber">Sandbox</span>
            <span className="tiny muted mono">{attempt.provider_reference}</span>
          </div>
          <h2 style={{ fontSize: "1.6rem" }}>{naira(attempt.amount_minor)}</h2>
          <div className="kv">
            <dt>Order</dt><dd className="mono">{attempt.order_reference}</dd>
            <dt>Status</dt><dd style={{ textTransform: "capitalize" }}>{attempt.status}</dd>
            <dt>Currency</dt><dd>{attempt.currency}</dd>
          </div>
          <p className="small muted">
            No real money moves. Confirming sends a signature-verified webhook to Edumart, which is the only
            thing that can mark the order paid and grant access.
          </p>
          <SandboxPay reference={attempt.provider_reference} orderId={attempt.order_id} disabled={attempt.status !== "pending"} />
        </section>

        <aside className="card pad-lg stack">
          <div className="card-title">What happens on confirm</div>
          <ol className="small muted" style={{ paddingLeft: 18, lineHeight: 1.9, margin: 0 }}>
            <li>Provider posts an event with an HMAC signature.</li>
            <li>Edumart verifies the signature over the raw body.</li>
            <li>The event is deduplicated on provider + event id.</li>
            <li>Amount and currency are compared with our stored attempt.</li>
            <li>Order and entitlement transition in one transaction.</li>
            <li>A reconciliation job is queued.</li>
          </ol>
          <div className="banner info small">
            <span className="ico">🔒</span>
            Re-sending the same event is harmless: it is recorded as a duplicate and no second entitlement is created.
          </div>
          <Link href={`/checkout/${attempt.order_id}`} className="btn secondary sm">Back to order</Link>
        </aside>
      </div>
    </Shell>
  );
}
