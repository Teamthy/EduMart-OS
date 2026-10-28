"use client";

import { useRouter } from "next/navigation";
import { useState } from "react";

export function StartAssessment({
  definitionId,
  disabled,
  remaining,
}: {
  definitionId: string;
  disabled: boolean;
  remaining: number;
}) {
  const router = useRouter();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function start() {
    setBusy(true);
    setError(null);
    try {
      const res = await fetch(`/api/v1/assessment-definitions/${definitionId}/sessions`, {
        method: "POST",
        headers: {
          "content-type": "application/json",
          "idempotency-key": `start-${definitionId}-${Date.now()}`,
        },
        body: JSON.stringify({
          client_context: { app_version: "web-1.0.0", timezone: Intl.DateTimeFormat().resolvedOptions().timeZone },
        }),
      });
      const data = await res.json();
      if (!res.ok) {
        setError(data?.error?.message ?? "Could not start this assessment.");
        return;
      }
      router.push(`/attempt/${data.session.session_id}`);
    } catch {
      setError("Network problem. Check your connection and try again.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="stack-sm">
      {error ? <div className="banner error small"><span className="ico">!</span>{error}</div> : null}
      <button className="btn lg block" type="button" onClick={start} disabled={disabled || busy}>
        {busy ? "Starting…" : "Start assessment"}
      </button>
      {remaining > 1 ? <p className="tiny muted center">You have {remaining} attempts available.</p> : null}
    </div>
  );
}
