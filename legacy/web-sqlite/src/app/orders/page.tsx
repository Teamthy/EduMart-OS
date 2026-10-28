import Link from "next/link";
import { Shell } from "@/components/Shell";
import { requirePageUser, naira, relativeTime } from "@/lib/server/guards";
import { listOrders } from "@/lib/server/commerce";

export const dynamic = "force-dynamic";

export default async function OrdersPage() {
  const user = await requirePageUser("/library");
  const orders = listOrders(user.id);

  return (
    <Shell title="Orders" subtitle="Receipts and payment state for your purchases." activePath="/library" action={<Link className="btn secondary sm" href="/library">Library</Link>}>
      {orders.length === 0 ? (
        <div className="empty">
          <span className="glyph">₦</span>
          <div className="strong">No orders yet</div>
          <div className="small">Purchases appear here with their payment status and receipt reference.</div>
          <Link className="btn sm" href="/library">Browse the library</Link>
        </div>
      ) : (
        <div className="table-wrap">
          <table className="data">
            <thead><tr><th>Reference</th><th>Created</th><th>Items</th><th>Total</th><th>Status</th><th /></tr></thead>
            <tbody>
              {orders.map((order) => (
                <tr key={order.id}>
                  <td className="mono small">{order.reference}</td>
                  <td className="small muted">{relativeTime(order.created_at)}</td>
                  <td className="small">{order.item_count}</td>
                  <td className="small strong">{naira(order.total_minor)}</td>
                  <td>
                    <span className={`badge ${order.status === "paid" || order.status === "fulfilled" ? "" : order.status === "payment_failed" ? "rose" : "amber"}`} style={{ textTransform: "capitalize" }}>
                      {order.status.replace("_", " ")}
                    </span>
                  </td>
                  <td style={{ textAlign: "right" }}>
                    <Link className="btn ghost sm" href={`/checkout/${order.id}`}>Open</Link>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </Shell>
  );
}
