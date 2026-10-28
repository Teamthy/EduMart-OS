import Link from "next/link";

export default function NotFound() {
  return (
    <div className="auth-main" style={{ minHeight: "100dvh" }}>
      <div className="auth-card center stack">
        <span className="eyebrow">404</span>
        <h1 style={{ fontSize: "1.8rem" }}>We could not find that page</h1>
        <p className="small muted">
          The link may have expired, or the resource may belong to another account. We do not reveal whether a
          protected record exists.
        </p>
        <div className="btn-row" style={{ justifyContent: "center" }}>
          <Link className="btn" href="/dashboard">Go to dashboard</Link>
          <Link className="btn secondary" href="/">Home</Link>
        </div>
      </div>
    </div>
  );
}
