import Link from "next/link";
import { Shell } from "@/components/Shell";
import { requirePageUser, naira } from "@/lib/server/guards";
import { listProducts, listEntitlements } from "@/lib/server/commerce";

export const dynamic = "force-dynamic";

export default async function LibraryPage({ searchParams }: { searchParams: Promise<{ q?: string; subject?: string }> }) {
  const user = await requirePageUser("/library");
  const { q, subject } = await searchParams;
  const products = listProducts({ q, subject });
  const owned = listEntitlements(user.id);
  const ownedIds = new Set(owned.map((o) => o.slug));

  const subjects = [...new Set(listProducts().map((p) => p.subject_key))].filter(Boolean);

  return (
    <Shell
      title="Library"
      subtitle="Reviewed learning publications. Access is granted by the backend after verified payment."
      activePath="/library"
      action={<Link className="btn secondary sm" href="/orders">My orders</Link>}
    >
      <section className="card tight">
        <form className="row wrap" method="get" style={{ gap: 8 }}>
          <input className="input" name="q" defaultValue={q ?? ""} placeholder="Search titles, subjects or descriptions" style={{ flex: 1, minWidth: 220 }} />
          <select className="select" name="subject" defaultValue={subject ?? ""} style={{ maxWidth: 200 }}>
            <option value="">All subjects</option>
            {subjects.map((s) => <option key={s} value={s}>{s}</option>)}
          </select>
          <button className="btn sm" type="submit">Search</button>
          {q || subject ? <Link className="btn ghost sm" href="/library">Clear</Link> : null}
        </form>
      </section>

      {owned.length > 0 ? (
        <section className="stack">
          <h2 style={{ fontSize: "1.2rem" }}>Your access</h2>
          <div className="grid-3">
            {owned.map((item) => (
              <Link key={item.id} href={`/library/${item.slug}`} className="card interactive row" style={{ gap: 14 }}>
                <div className={`product-cover tone-${item.cover_tone}`} style={{ width: 54, height: 68, flex: "0 0 54px", padding: 8 }}>
                  <span className="s" style={{ fontSize: 8 }}>{item.type}</span>
                </div>
                <div>
                  <div className="strong small">{item.title}</div>
                  <div className="tiny muted">{item.pages} pages · active</div>
                  <span className="badge" style={{ marginTop: 6 }}>Open</span>
                </div>
              </Link>
            ))}
          </div>
        </section>
      ) : null}

      <section className="stack">
        <div className="row-between">
          <h2 style={{ fontSize: "1.2rem" }}>{products.length} publication{products.length === 1 ? "" : "s"}</h2>
        </div>
        {products.length === 0 ? (
          <div className="empty">
            <span className="glyph">▤</span>
            <div className="strong">No publications match that search</div>
            <Link className="btn sm" href="/library">Clear filters</Link>
          </div>
        ) : (
          <div className="grid-3">
            {products.map((product) => (
              <article key={product.id} className="card interactive stack" style={{ gap: 12 }}>
                <div className="row" style={{ gap: 14, alignItems: "flex-start" }}>
                  <div className={`product-cover tone-${product.cover_tone}`} style={{ width: 76, height: 100, flex: "0 0 76px", padding: 10 }}>
                    <span className="s" style={{ fontSize: 8 }}>{product.exam_key || "study"}</span>
                    <span className="t" style={{ fontSize: 11 }}>{product.subject_key}</span>
                  </div>
                  <div style={{ minWidth: 0 }}>
                    <div className="strong small" style={{ lineHeight: 1.3 }}>{product.title}</div>
                    <div className="tiny muted" style={{ marginTop: 3 }}>{product.subtitle}</div>
                    <div className="tiny muted" style={{ marginTop: 6 }}>{product.publisher_name} · {product.pages} pages</div>
                  </div>
                </div>
                <div className="row-between">
                  <span className="serif" style={{ fontSize: 18, color: "var(--green-900)" }}>
                    {product.price_minor === 0 ? "Free" : naira(product.price_minor)}
                  </span>
                  {ownedIds.has(product.slug) ? (
                    <span className="badge">Owned</span>
                  ) : (
                    <Link className="btn sm" href={`/library/${product.slug}`}>View</Link>
                  )}
                </div>
              </article>
            ))}
          </div>
        )}
      </section>
    </Shell>
  );
}
