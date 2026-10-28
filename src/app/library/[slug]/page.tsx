import Link from "next/link";
import { notFound } from "next/navigation";
import { Shell } from "@/components/Shell";
import { requirePageUser, naira } from "@/lib/server/guards";
import { productBySlug, entitlementFor } from "@/lib/server/commerce";
import { PurchasePanel } from "./PurchasePanel";

export const dynamic = "force-dynamic";

export default async function ProductPage({ params }: { params: Promise<{ slug: string }> }) {
  const user = await requirePageUser("/library");
  const { slug } = await params;
  const product = productBySlug(slug);
  if (!product || product.status !== "published") notFound();
  const entitlement = entitlementFor(user.id, product.id);

  return (
    <Shell
      title={product.title}
      subtitle={product.publisher_name ?? "Edumart publication"}
      activePath="/library"
      action={<Link className="btn secondary sm" href="/library">Back to library</Link>}
    >
      <div className="split" style={{ gridTemplateColumns: "0.85fr 1.15fr", alignItems: "start" }}>
        <div className={`product-cover tone-${product.cover_tone}`} style={{ aspectRatio: "4/5", maxWidth: 320, padding: 22 }}>
          <div>
            <div className="s">{product.exam_key || "study"} · {product.type.replace("_", " ")}</div>
            <div className="t" style={{ fontSize: 26, marginTop: 10 }}>{product.title}</div>
          </div>
          <div>
            <div className="s" style={{ opacity: 0.9 }}>{product.publisher_name}</div>
            <div className="s">{product.pages} pages</div>
          </div>
        </div>

        <div className="stack">
          <section className="card pad-lg stack">
            <div className="row wrap" style={{ gap: 8 }}>
              <span className="badge neutral" style={{ textTransform: "capitalize" }}>{product.type.replace("_", " ")}</span>
              <span className="badge neutral">{product.subject_key}</span>
              {product.publisher_status === "approved" ? <span className="badge">Verified publisher</span> : <span className="badge amber">Publisher review pending</span>}
            </div>
            <p className="lead">{product.description}</p>
            <div className="row-between wrap" style={{ borderTop: "1px solid var(--line)", paddingTop: 14 }}>
              <div>
                <div className="eyebrow">Price</div>
                <div className="serif" style={{ fontSize: 28, color: "var(--green-900)" }}>
                  {product.price_minor === 0 ? "Free" : naira(product.price_minor)}
                </div>
                <div className="tiny muted">One-time purchase · access does not expire</div>
              </div>
              <div style={{ minWidth: 220 }}>
                <PurchasePanel
                  slug={product.slug}
                  priceMinor={product.price_minor}
                  owned={Boolean(entitlement)}
                  entitlementId={entitlement?.id ?? null}
                />
              </div>
            </div>
          </section>

          <section className="card">
            <div className="card-title">What you get</div>
            <ul className="small muted" style={{ margin: "8px 0 0", paddingLeft: 18, lineHeight: 1.9 }}>
              <li>Protected digital access granted after verified payment</li>
              <li>Short-lived signed links — no public file URLs</li>
              <li>Refund and support path if something is wrong</li>
              <li>Access tied to your account, not your device</li>
            </ul>
          </section>

          <section className="card">
            <div className="card-title">Rights & provenance</div>
            <p className="tiny muted" style={{ marginTop: 4 }}>
              Publisher: {product.publisher_name}. Listing status: approved. In production the rights register records
              licence basis, permission evidence and takedown history for each publication.
            </p>
          </section>
        </div>
      </div>
    </Shell>
  );
}
