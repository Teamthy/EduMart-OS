"use client";

import { useRouter } from "next/navigation";
import { useState } from "react";

export function SupportForm() {
  const router = useRouter();
  const [open, setOpen] = useState(false);
  const [category, setCategory] = useState("access");
  const [subject, setSubject] = useState("");
  const [body, setBody] = useState("");
  const [state, setState] = useState<"idle" | "sending" | "sent" | "error">("idle");

  if (!open) {
    return <button className="btn sm block" type="button" onClick={() => setOpen(true)}>Open a support case</button>;
  }

  if (state === "sent") {
    return <div className="banner info small"><span className="ico">✓</span>Your case has been opened. Support will respond within the published window.</div>;
  }

  return (
    <form
      className="stack-sm"
      onSubmit={async (e) => {
        e.preventDefault();
        setState("sending");
        const res = await fetch("/api/v1/support/cases", {
          method: "POST",
          headers: { "content-type": "application/json" },
          body: JSON.stringify({ category, subject, body }),
        }).catch(() => null);
        setState(res && res.ok ? "sent" : "error");
        if (res && res.ok) router.refresh();
      }}
    >
      <div className="field">
        <label htmlFor="category">Category</label>
        <select id="category" className="select" value={category} onChange={(e) => setCategory(e.target.value)}>
          <option value="access">Access problem</option>
          <option value="payment">Payment or refund</option>
          <option value="content">Content issue</option>
          <option value="exam_dispute">Assessment dispute</option>
          <option value="technical">Technical problem</option>
          <option value="privacy">Privacy or data request</option>
        </select>
      </div>
      <div className="field">
        <label htmlFor="subject">Subject</label>
        <input id="subject" className="input" value={subject} onChange={(e) => setSubject(e.target.value)} required maxLength={140} />
      </div>
      <div className="field">
        <label htmlFor="body">Details</label>
        <textarea id="body" className="textarea" rows={3} value={body} onChange={(e) => setBody(e.target.value)} />
      </div>
      {state === "error" ? <div className="banner error tiny">Could not open the case. Try again.</div> : null}
      <div className="btn-row">
        <button className="btn sm" type="submit" disabled={state === "sending"}>{state === "sending" ? "Sending…" : "Submit case"}</button>
        <button className="btn ghost sm" type="button" onClick={() => setOpen(false)}>Cancel</button>
      </div>
    </form>
  );
}
