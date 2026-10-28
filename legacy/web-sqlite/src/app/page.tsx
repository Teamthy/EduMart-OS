import Link from "next/link";
import { listPathways, listMockDefinitions } from "@/lib/server/content";
import { listProducts } from "@/lib/server/commerce";
import { getCurrentUser } from "@/lib/server/auth";

export const dynamic = "force-dynamic";

export default async function LandingPage() {
  const pathways = listPathways();
  const mocks = listMockDefinitions();
  const products = listProducts().slice(0, 3);
  const user = await getCurrentUser();

  const naira = (minor: number) => `₦${(minor / 100).toLocaleString("en-NG")}`;

  return (
    <div>
      <header className="topbar" style={{ background: "rgba(248,247,244,0.9)" }}>
        <Link href="/" className="brand" style={{ padding: 0, color: "var(--green-900)" }}>
          <span className="brand-mark" aria-hidden>E</span>
          <span style={{ color: "var(--green-900)" }}>
            <span className="brand-name">Edumart</span>
            <span className="brand-sub" style={{ color: "var(--ink-3)" }}>Learn · Assess · Advance</span>
          </span>
        </Link>
        <span className="spacer" />
        <div className="btn-row">
          <Link className="btn ghost sm" href="/library">Library</Link>
          {user ? (
            <Link className="btn sm" href="/dashboard">Open dashboard</Link>
          ) : (
            <>
              <Link className="btn secondary sm" href="/login">Sign in</Link>
              <Link className="btn sm" href="/register">Get started</Link>
            </>
          )}
        </div>
      </header>

      <main className="page" style={{ gap: 34, paddingBottom: 60 }}>
        <section className="hero">
          <span className="glow" aria-hidden />
          <div className="hero-grid">
            <div className="stack">
              <span className="badge gold" style={{ background: "rgba(231,178,61,0.18)", color: "#f2cf87", width: "fit-content" }}>
                Built for Nigerian learners
              </span>
              <h1>Prepare properly. See exactly where you stand.</h1>
              <p className="lead" style={{ color: "rgba(234,244,238,0.84)" }}>
                Practice thousands of approved questions, sit server-timed mock examinations that survive a
                bad connection, and get topic-level feedback your teacher can act on.
              </p>
              <div className="btn-row" style={{ marginTop: 6 }}>
                <Link className="btn gold lg" href="/register">Start free practice</Link>
                <Link className="btn ghost lg" href="/login" style={{ color: "#eaf4ee" }}>I have an account</Link>
              </div>
              <div className="stat-strip">
                <div>
                  <div className="n">4</div>
                  <div className="l">Exam pathways</div>
                </div>
                <div>
                  <div className="n">{products.length > 0 ? "6+" : "0"}</div>
                  <div className="l">Publications</div>
                </div>
                <div>
                  <div className="n">100%</div>
                  <div className="l">Reviewed items</div>
                </div>
              </div>
            </div>

            <div className="card pad-lg" style={{ background: "rgba(255,255,255,0.97)" }}>
              <div className="card-head">
                <div>
                  <div className="card-title">Next mock exam</div>
                  <div className="card-sub">Server-timed · autosaved · scored on submit</div>
                </div>
                <span className="badge dot">Live</span>
              </div>
              {mocks.slice(0, 2).map((mock) => (
                <div key={mock.id} className="stack-sm" style={{ padding: "12px 0", borderTop: "1px solid var(--line)" }}>
                  <div className="row-between">
                    <span className="strong" style={{ fontSize: 14 }}>{mock.title}</span>
                    <span className="badge neutral">{Math.round(mock.duration_seconds / 60)} min</span>
                  </div>
                  <div className="tiny muted">{mock.question_count} questions · {mock.attempt_limit} attempts</div>
                </div>
              ))}
              <Link className="btn block sm" href="/exam" style={{ marginTop: 12 }}>See all mock exams</Link>
            </div>
          </div>
        </section>

        <section className="stack">
          <div className="row-between wrap">
            <div>
              <span className="eyebrow">Exam pathways</span>
              <h2 style={{ marginTop: 4 }}>One platform, every route to your next level</h2>
            </div>
            <Link href="/register" className="btn secondary sm">Choose a pathway</Link>
          </div>
          <div className="grid-4">
            {pathways.map((pathway) => (
              <article key={pathway.id} className="card interactive subject-card">
                <span className="badge neutral">{pathway.body.split(" ").slice(0, 3).join(" ")}</span>
                <h3 style={{ fontSize: "1.05rem" }}>{pathway.name}</h3>
                <p className="small muted">{pathway.blurb}</p>
              </article>
            ))}
          </div>
        </section>

        <section className="split">
          <article className="card pad-lg stack">
            <span className="eyebrow">Assessment integrity</span>
            <h2 style={{ fontSize: "1.35rem" }}>Your answers are never lost to a bad connection</h2>
            <p className="small muted">
              Every answer is written to the server and acknowledged before the interface says
              &ldquo;Saved&rdquo;. If the network drops, you will see exactly which answers are pending,
              which failed, and what the server has confirmed.
            </p>
            <div className="stack-sm">
              {[
                ["Server-authoritative timer", "Changing your device clock cannot extend an exam."],
                ["Idempotent autosave", "Retries never duplicate or overwrite a newer answer."],
                ["Deterministic scoring", "Submission and expiry use the same versioned scoring path."],
              ].map(([title, sub]) => (
                <div key={title} className="row" style={{ alignItems: "flex-start", gap: 10 }}>
                  <span className="badge dot" style={{ marginTop: 3 }}>✓</span>
                  <span>
                    <span className="strong small">{title}</span>
                    <div className="tiny muted">{sub}</div>
                  </span>
                </div>
              ))}
            </div>
          </article>

          <article className="card pad-lg stack">
            <span className="eyebrow">Publications</span>
            <h2 style={{ fontSize: "1.35rem" }}>Trusted materials, protected access</h2>
            <div className="stack-sm">
              {products.map((product) => (
                <Link key={product.id} href={`/library/${product.slug}`} className="row-between" style={{ padding: "9px 0", borderBottom: "1px solid var(--line)" }}>
                  <span>
                    <span className="strong small" style={{ display: "block" }}>{product.title}</span>
                    <span className="tiny muted">{product.publisher_name} · {product.pages} pages</span>
                  </span>
                  <span className="badge">{product.price_minor === 0 ? "Free" : naira(product.price_minor)}</span>
                </Link>
              ))}
            </div>
            <Link href="/library" className="btn secondary sm" style={{ width: "fit-content" }}>Browse the library</Link>
          </article>
        </section>

        <section className="card pad-lg" style={{ background: "var(--green-50)", borderColor: "#cfe6da" }}>
          <div className="row-between wrap" style={{ gap: 18 }}>
            <div>
              <h2 style={{ fontSize: "1.3rem" }}>For schools and teachers</h2>
              <p className="small muted" style={{ maxWidth: "58ch", marginTop: 4 }}>
                Create a class, invite learners with an expiring link, schedule a timed assessment, and
                review class and individual performance — with tenant isolation enforced on the server.
              </p>
            </div>
            <div className="btn-row">
              <Link className="btn" href="/register">Create a teacher account</Link>
              <Link className="btn secondary" href="/login">Sign in</Link>
            </div>
          </div>
        </section>

        <footer className="row-between wrap tiny muted" style={{ borderTop: "1px solid var(--line)", paddingTop: 18 }}>
          <span>© {new Date().getFullYear()} Edumart. Original practice content, reviewed before publication.</span>
          <span className="row wrap" style={{ gap: 14 }}>
            <span>Privacy</span>
            <span>Terms</span>
            <span>Refund policy</span>
            <span>Support</span>
          </span>
        </footer>
      </main>
    </div>
  );
}
