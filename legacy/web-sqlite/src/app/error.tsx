"use client";

export default function GlobalError({ reset }: { error: Error; reset: () => void }) {
  return (
    <div className="auth-main" style={{ minHeight: "100dvh" }}>
      <div className="auth-card center stack">
        <span className="eyebrow">Something went wrong</span>
        <h1 style={{ fontSize: "1.7rem" }}>We could not finish that request</h1>
        <p className="small muted">
          The error has been logged with a request ID. If you were in an assessment, your saved answers are safe —
          return to the attempt to continue.
        </p>
        <div className="btn-row" style={{ justifyContent: "center" }}>
          <button className="btn" type="button" onClick={reset}>Try again</button>
          <a className="btn secondary" href="/dashboard">Dashboard</a>
        </div>
      </div>
    </div>
  );
}
