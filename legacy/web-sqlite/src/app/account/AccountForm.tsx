"use client";

import { useRouter } from "next/navigation";
import { useState } from "react";

export function AccountForm({
  user,
  pathways,
}: {
  user: { displayName: string; examPathwayId: string | null; targetDate: string | null; timezone: string };
  pathways: { id: string; name: string }[];
}) {
  const router = useRouter();
  const [displayName, setDisplayName] = useState(user.displayName);
  const [examPathwayId, setExamPathwayId] = useState(user.examPathwayId ?? "");
  const [targetDate, setTargetDate] = useState(user.targetDate ? user.targetDate.slice(0, 10) : "");
  const [state, setState] = useState<"idle" | "saving" | "saved" | "error">("idle");

  return (
    <form
      className="stack-sm"
      onSubmit={async (e) => {
        e.preventDefault();
        setState("saving");
        const res = await fetch("/api/v1/me", {
          method: "PATCH",
          headers: { "content-type": "application/json" },
          body: JSON.stringify({
            displayName,
            examPathwayId: examPathwayId || null,
            targetDate: targetDate ? new Date(targetDate).toISOString() : null,
          }),
        }).catch(() => null);
        setState(res && res.ok ? "saved" : "error");
        if (res && res.ok) router.refresh();
      }}
    >
      <div className="field">
        <label htmlFor="name">Display name</label>
        <input id="name" className="input" value={displayName} onChange={(e) => setDisplayName(e.target.value)} required />
      </div>
      <div className="split" style={{ gap: 10 }}>
        <div className="field">
          <label htmlFor="pathway">Exam pathway</label>
          <select id="pathway" className="select" value={examPathwayId} onChange={(e) => setExamPathwayId(e.target.value)}>
            <option value="">Not selected</option>
            {pathways.map((p) => <option key={p.id} value={p.id}>{p.name}</option>)}
          </select>
        </div>
        <div className="field">
          <label htmlFor="target">Target date</label>
          <input id="target" type="date" className="input" value={targetDate} onChange={(e) => setTargetDate(e.target.value)} />
        </div>
      </div>
      <div className="row" style={{ gap: 10 }}>
        <button className="btn sm" type="submit" disabled={state === "saving"}>{state === "saving" ? "Saving…" : "Save changes"}</button>
        {state === "saved" ? <span className="badge">Saved</span> : null}
        {state === "error" ? <span className="tiny" style={{ color: "var(--rose-600)" }}>Could not save.</span> : null}
      </div>
    </form>
  );
}
