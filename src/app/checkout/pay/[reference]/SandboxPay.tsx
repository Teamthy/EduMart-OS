"use client";

import { useRouter } from "next/navigation";
import { useState } from "react";

export function SandboxPay({ reference, orderId, disabled }: { reference: string; orderId: string; disabled: boolean }) {
  const router = useRouter();
  const [state, setState] = useState<"idle" | "paying" | "done" | "error">("idle");
  const [message, setMessage] = useState<string | null>(null);

  return (
    <div className="stack-sm">
      {message ? <div className={`banner ${state === "error" ? "error" : "info"} small`}>{message}</div> : null}
      <button
        className="btn lg block"
        type="button"
        disabled={disabled || state === "paying" || state === "done"}
        onClick={async () => {
          setState("paying");
          setMessage(null);
          const res = await fetch("/api/v1/sandbox/pay", {
            method: "POST",
            headers: { "content-type": "application/json" },
            body: JSON.stringify({ reference }),
          }).catch(() => null);
          const data = res ? await res.json().catch(() => ({})) : {};
          if (!res || !res.ok) {
            setState("error");
            setMessage(data?.error?.message ?? "The sandbox provider could not confirm this payment.");
            return;
          }
          setState("done");
          setMessage("Payment confirmed and verified. Granting access…");
          router.push(`/checkout/${orderId}`);
          router.refresh();
        }}
      >
        {state === "paying" ? "Confirming…" : state === "done" ? "Confirmed" : "Confirm payment"}
      </button>
      <button
        className="btn secondary block sm"
        type="button"
        disabled={state === "paying"}
        onClick={async () => {
          setState("paying");
          await fetch("/api/v1/sandbox/pay", {
            method: "POST",
            headers: { "content-type": "application/json" },
            body: JSON.stringify({ reference, outcome: "failed" }),
          }).catch(() => null);
          setState("error");
          setMessage("The sandbox provider reported a failed payment. No access was granted.");
        }}
      >
        Simulate a failed payment
      </button>
      <p className="tiny muted center">Sandbox only — this route is disabled in production.</p>
    </div>
  );
}
