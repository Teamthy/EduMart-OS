"use client";

import { useState } from "react";

export function InvitePanel({ classId, joinCode }: { classId: string; joinCode: string }) {
  const [link, setLink] = useState<string | null>(null);
  const [email, setEmail] = useState("");
  const [uses, setUses] = useState(30);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  return (
    <div className="stack-sm">
      <div className="banner info small">
        <span className="ico">#</span>
        Class code <span className="mono strong">{joinCode}</span>
      </div>
      <div className="field">
        <label htmlFor="inviteEmail">Email (optional — restricts the link to one address)</label>
        <input id="inviteEmail" className="input" type="email" value={email} onChange={(e) => setEmail(e.target.value)} placeholder="learner@example.com" />
      </div>
      <div className="field">
        <label htmlFor="uses">Maximum uses</label>
        <input id="uses" type="number" min={1} max={200} className="input" value={uses} onChange={(e) => setUses(Number(e.target.value) || 1)} />
      </div>
      {error ? <div className="banner error tiny">{error}</div> : null}
      <button
        className="btn sm block"
        type="button"
        disabled={busy}
        onClick={async () => {
          setBusy(true);
          setError(null);
          const res = await fetch(`/api/v1/classes/${classId}/invitations`, {
            method: "POST",
            headers: { "content-type": "application/json" },
            body: JSON.stringify({ email: email || undefined, maxUses: uses, role: "student" }),
          }).catch(() => null);
          const data = res ? await res.json().catch(() => ({})) : {};
          setBusy(false);
          if (!res || !res.ok) {
            setError(data?.error?.message ?? "Could not create an invitation.");
            return;
          }
          const url = `${window.location.origin}${data.invitation.joinPath}`;
          setLink(url);
          navigator.clipboard?.writeText(url).catch(() => {});
        }}
      >
        {busy ? "Creating…" : "Create invitation link"}
      </button>
      {link ? (
        <div className="stack-sm">
          <div className="banner info tiny" style={{ wordBreak: "break-all" }}>{link}</div>
          <div className="tiny muted">Copied to clipboard if your browser allowed it. Expires in 7 days.</div>
        </div>
      ) : null}
    </div>
  );
}
