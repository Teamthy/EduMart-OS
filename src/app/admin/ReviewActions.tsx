"use client";

import { useRouter } from "next/navigation";
import { useState } from "react";

export function ReviewActions({
  questionVersionId,
  reportId,
  mode = "question",
}: {
  questionVersionId: string;
  reportId?: string;
  mode?: "question" | "report";
}) {
  const router = useRouter();
  const [busy, setBusy] = useState<string | null>(null);
  const [reason, setReason] = useState("");
  const [message, setMessage] = useState<string | null>(null);

  async function decide(decision: "approve" | "reject" | "withdraw") {
    setBusy(decision);
    setMessage(null);
    const res = await fetch(`/api/v1/admin/questions/${questionVersionId}/review-decision`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ decision, reason: reason || undefined }),
    }).catch(() => null);
    const data = res ? await res.json().catch(() => ({})) : {};
    setBusy(null);
    if (!res || !res.ok) {
      setMessage(data?.error?.message ?? "Decision failed.");
      return;
    }
    setMessage(`Recorded: ${decision}.`);
    router.refresh();
  }

  return (
    <div className="stack-sm" style={{ marginTop: 10 }}>
      {message ? <div className="banner info tiny">{message}</div> : null}
      <input
        className="input"
        placeholder="Reason (required to reject or withdraw)"
        value={reason}
        onChange={(e) => setReason(e.target.value)}
        aria-label="Decision reason"
      />
      <div className="btn-row">
        <button className="btn sm" type="button" onClick={() => decide("approve")} disabled={busy !== null}>
          {busy === "approve" ? "Approving…" : "Approve & publish"}
        </button>
        <button className="btn secondary sm" type="button" onClick={() => decide("reject")} disabled={busy !== null}>Reject</button>
        <button className="btn ghost sm" type="button" onClick={() => decide("withdraw")} disabled={busy !== null}>Withdraw</button>
        {mode === "report" ? <span className="tiny muted" style={{ alignSelf: "center" }}>Report #{reportId?.slice(0, 6)}</span> : null}
      </div>
    </div>
  );
}
