import Link from "next/link";

export default function OfflinePage() {
  return (
    <div className="auth-main" style={{ minHeight: "100dvh" }}>
      <div className="auth-card center stack">
        <span className="brand-mark" style={{ margin: "0 auto" }} aria-hidden>E</span>
        <h1 style={{ fontSize: "1.6rem" }}>You are offline</h1>
        <p className="small muted">
          Edumart needs a connection to load questions, save answers and submit assessments. Nothing is marked saved
          until the server confirms it — reconnect and any queued answers will sync.
        </p>
        <Link className="btn" href="/dashboard">Try again</Link>
      </div>
    </div>
  );
}
