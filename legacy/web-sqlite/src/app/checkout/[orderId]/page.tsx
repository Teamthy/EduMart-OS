import Link from "next/link";
import { notFound } from "next/navigation";
import { Shell } from "@/components/Shell";
import { requirePageUser, naira } from "@/lib/server/guards";
import { getOrder } from "@/lib/server/commerce";
import { PayButton } from "./PayButton";

export const dynamic = "force-dynamic";

export default async function CheckoutPage({ params }: { params: Promise<{ orderId: string }> }) {
  const user = await requirePageUser();
  const { orderId } = await params;
  const order = getOrder(user.id, orderId);
  if (!order) notFound();

  return (
    <Shell title="Checkout" subtitle={`Order ${order.reference}`} activePath="/library" action={<Link className="btn secondary sm" href="/library">Back to library</Link>}>
      <div className="split" style={{ gridTemplateColumns: "1.2fr 0.8fr", alignItems: "start" }}>
        <section className="card pad-lg stack">
          <div className="card-title">Order summary</div>
          <div>
            {order.items.map((item: any) => (
              <div key={item.id} className="row-between" style={{ borderTop: "1px solid var(--line)", padding: "12px 0" }}>
                <span>
                  <span className="strong small" style={{ display: "block" }}>{item.title_snapshot}</span>
                  <span className="tiny muted">Digital access · quantity {item.quantity}</span>
                </span>
                <span className="strong small">{naira(item.unit_amount_minor * item.quantity)}</span>
              </div>
            ))}
          </div>
          <div className="stack-sm" style={{ borderTop: "1px solid var(--line)", paddingTop: 12 }}>
            <div className="row-between small"><span className="muted">Subtotal</span><span>{naira(order.subtotal_minor)}</span></div>
            <div className="row-between small"><span className="muted">Discount</span><span>{naira(order.discount_minor)}</span></div>
            <div className="row-between"><span className="strong">Total</span><span className="strong serif" style={{ fontSize: 20 }}>{naira(order.total_minor)}</span></div>
          </div>
          <div className="banner info small">
            <span className="ico">ℹ</span>
            Totals are calculated on the server from active price records. Returning from the payment page is not
            proof of payment — access is granted only after a verified provider webhook.
          </div>
        </section>

        <aside className="stack" style={{ position: "sticky", top: 84 }}>
          <section className="card pad-lg stack">
            <div className="card-title">Payment</div>
            <div className="row-between small">
              <span className="muted">Status</span>
              <span className={`badge ${order.status === "paid" || order.status === "fulfilled" ? "" : order.status === "payment_failed" ? "rose" : "amber"}`} style={{ textTransform: "capitalize" }}>
                {order.status.replace("_", " ")}
              </span>
            </div>
            {order.status === "pending_payment" ? (
              <PayButton orderId={order.id} totalMinor={order.total_minor} />
            ) : order.status === "paid" || order.status === "fulfilled" ? (
              <div className="stack-sm">
                <div className="banner info small"><span className="ico">✓</span>Payment verified. Your access is active.</div>
                <Link className="btn block" href="/library">Open your library</Link>
              </div>
            ) : (
              <div className="stack-sm">
                <div className="banner error small">{order.status === "payment_failed" ? "The payment did not complete. You have not been charged." : `This order is ${order.status}.`}</div>
                <Link className="btn secondary block" href="/library">Try another publication</Link>
              </div>
            )}
            <p className="tiny muted">
              Card details are never handled by Edumart. This demo uses a sandbox provider that signs its webhook
              payload so the verification path is exercised end to end.
            </p>
          </section>
        </aside>
      </div>
    </Shell>
  );
}
