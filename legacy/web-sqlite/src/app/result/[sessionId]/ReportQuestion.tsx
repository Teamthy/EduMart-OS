"use client";

import { useState } from "react";

export function ReportQuestion({ questionVersionId }: { questionVersionId: string }) {
  const [open, setOpen] = useState(false);
  const [reason, setReason] = useState("incorrect_answer");
  const [details, setDetails] = useState("");
  const [state, setState] = useState<"idle" | "sending" | "sent" | "error">("idle");

  if (state === "sent") return <span className="tiny muted">Reported — thank you. Our content team will review it.</span>;

  return (
    <div>
      {!open ? (
        <button type="button" className="btn ghost sm" onClick={() => setOpen(true)}>Report this question</button>
      ) : (
        <div className="stack-sm" style={{ border: "1px solid var(--line)", borderRadius: 12, padding: 12, background: "#fff" }}>
          <div className="field">
            <label htmlFor={`reason-${questionVersionId}`}>What is wrong?</label>
            <select id={`reason-${questionVersionId}`} className="select" value={reason} onChange={(e) => setReason(e.target.value)}>
              <option value="incorrect_answer">The answer key is wrong</option>
              <option value="unclear_explanation">The explanation is unclear</option>
              <option value="typo">Typo or formatting issue</option>
              <option value="duplicate">Duplicate question</option>
              <option value="rights_issue">Rights or licensing concern</option>
              <option value="other">Something else</option>
            </select>
          </div>
          <div className="field">
            <label htmlFor={`details-${questionVersionId}`}>Details (optional)</label>
            <textarea className="textarea" id={`details-${questionVersionId}`} value={details} onChange={(e) => setDetails(e.target.value)} rows={2} />
          </div>
          <div className="btn-row">
            <button
              className="btn sm"
              type="button"
              disabled={state === "sending"}
              onClick={async () => {
                setState("sending");
                const res = await fetch("/api/v1/reports", {
                  method: "POST",
                  headers: { "content-type": "application/json" },
                  body: JSON.stringify({ questionVersionId, reason, details }),
                }).catch(() => null);
                setState(res && res.ok ? "sent" : "error");
              }}
            >
              {state === "sending" ? "Sending…" : "Send report"}
            </button>
            <button className="btn ghost sm" type="button" onClick={() => setOpen(false)}>Cancel</button>
            {state === "error" ? <span className="tiny" style={{ color: "var(--rose-600)" }}>Could not send. Try again.</span> : null}
          </div>
        </div>
      )}
    </div>
  );
}
