"use client";

import { useRouter } from "next/navigation";
import { useState } from "react";

export function JoinClassForm() {
  const router = useRouter();
  const [code, setCode] = useState("");
  const [message, setMessage] = useState<{ type: "error" | "info"; text: string } | null>(null);
  const [busy, setBusy] = useState(false);

  return (
    <form
      className="stack-sm"
      onSubmit={async (e) => {
        e.preventDefault();
        setBusy(true);
        setMessage(null);
        const res = await fetch("/api/v1/classes/join", {
          method: "POST",
          headers: { "content-type": "application/json" },
          body: JSON.stringify({ code }),
        }).catch(() => null);
        const data = res ? await res.json().catch(() => ({})) : {};
        setBusy(false);
        if (!res || !res.ok) {
          setMessage({ type: "error", text: data?.error?.message ?? "Could not join that class." });
          return;
        }
        setMessage({ type: "info", text: `Joined ${data.joined.name}.` });
        setCode("");
        router.refresh();
      }}
    >
      <div className="field">
        <label htmlFor="code">Class code</label>
        <input
          id="code"
          className="input mono"
          placeholder="EDU-1A2B3C"
          value={code}
          onChange={(e) => setCode(e.target.value.toUpperCase())}
          required
        />
      </div>
      {message ? <div className={`banner ${message.type} tiny`}>{message.text}</div> : null}
      <button className="btn block sm" type="submit" disabled={busy}>{busy ? "Joining…" : "Join class"}</button>
    </form>
  );
}
