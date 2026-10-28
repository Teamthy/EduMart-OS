"use client";

import { useRouter } from "next/navigation";
import { useState } from "react";

export function AcceptInvite({ token, classId }: { token: string; classId: string }) {
  const router = useRouter();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [done, setDone] = useState(false);

  if (done) {
    return (
      <div className="stack-sm">
        <div className="banner info"><span className="ico">✓</span>You have joined the class.</div>
        <div className="btn-row">
          <button className="btn" type="button" onClick={() => router.push(`/classes/${classId}`)}>Open class</button>
          <button className="btn secondary" type="button" onClick={() => router.push("/classes")}>All classes</button>
        </div>
      </div>
    );
  }

  return (
    <div className="stack-sm">
      {error ? <div className="banner error small">{error}</div> : null}
      <button
        className="btn lg"
        type="button"
        disabled={busy}
        onClick={async () => {
          setBusy(true);
          setError(null);
          const res = await fetch(`/api/v1/invitations/${token}/accept`, {
            method: "POST",
            headers: { "content-type": "application/json" },
            body: "{}",
          }).catch(() => null);
          const data = res ? await res.json().catch(() => ({})) : {};
          setBusy(false);
          if (!res || !res.ok) {
            setError(data?.error?.message ?? "Could not accept this invitation.");
            return;
          }
          setDone(true);
          router.refresh();
        }}
      >
        {busy ? "Joining…" : "Accept invitation"}
      </button>
    </div>
  );
}
