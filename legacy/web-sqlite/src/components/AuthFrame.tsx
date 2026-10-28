import Link from "next/link";

export function AuthFrame({
  title,
  subtitle,
  children,
  footer,
}: {
  title: string;
  subtitle: string;
  children: React.ReactNode;
  footer?: React.ReactNode;
}) {
  return (
    <div className="auth-wrap">
      <aside className="auth-aside">
        <Link href="/" className="brand" style={{ padding: 0 }}>
          <span className="brand-mark" aria-hidden>E</span>
          <span>
            <span className="brand-name">Edumart</span>
            <span className="brand-sub">Learn · Assess · Advance</span>
          </span>
        </Link>
        <div className="stack">
          <h1 style={{ color: "#fff", fontSize: "clamp(1.7rem, 2.4vw, 2.4rem)", maxWidth: "18ch" }}>
            Practice with feedback that actually tells you something.
          </h1>
          <p style={{ color: "rgba(234,244,238,0.8)", maxWidth: "44ch" }}>
            JAMB, WAEC, NECO and Post-UTME practice, server-timed mocks, classroom assessments and
            a reviewed library of Nigerian learning material.
          </p>
          <ul style={{ listStyle: "none", padding: 0, margin: "10px 0 0", display: "grid", gap: 10 }}>
            {[
              "Answers acknowledged by the server, not just the browser",
              "Topic-level strengths and weaknesses after every attempt",
              "Class invites with expiring, revocable links",
            ].map((item) => (
              <li key={item} className="row" style={{ gap: 9, fontSize: 13.5, color: "rgba(234,244,238,0.86)" }}>
                <span aria-hidden style={{ color: "#e7b23d" }}>◆</span> {item}
              </li>
            ))}
          </ul>
        </div>
        <p className="tiny" style={{ color: "rgba(234,244,238,0.5)" }}>
          Original practice content · reviewed before publication · WCAG 2.2 AA target
        </p>
      </aside>

      <main className="auth-main">
        <div className="auth-card">
          <div>
            <h2 style={{ fontSize: "1.6rem" }}>{title}</h2>
            <p className="small muted" style={{ marginTop: 4 }}>{subtitle}</p>
          </div>
          {children}
          {footer}
        </div>
      </main>
    </div>
  );
}
