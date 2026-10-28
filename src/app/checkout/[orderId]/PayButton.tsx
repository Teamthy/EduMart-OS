"use client";

import { useRouter } from "next/navigation";
import { useState } from "react";

export function PayButton({ orderId, totalMinor }: { orderId: string; totalMinor: number }) {
  const router = useRouter();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  return (
    <div className="stack-sm">
      {error ? <div className="banner error tiny">{error}</div> : null}
      <button
        className="btn lg block"
        type="button"
        disabled={busy}
        onClick={async () => {
          setBusy(true);
          setError(null);
          const res = await fetch(`/api/v1/orders/${orderId}/payment-attempts`, {
            method: "POST",
            headers: { "content-type": "application/json", "idempotency-key": `pay-${orderId}` },
            body: JSON.stringify({ provider: "sandbox" }),
          }).catch(() => null);
          const data = res ? await res.json().catch(() => ({})) : {};
          setBusy(false);
          if (!res || !res.ok) {
            setError(data?.error?.message ?? "Could not start the payment.");
            return;
          }
          if (data.attempt?.checkout_url) router.push(data.attempt.checkout_url);
          else router.refresh();
        }}
      >
        {busy ? "Opening provider…" : `Pay ₦${(totalMinor / 100).toLocaleString("en-NG")}`}
      </button>
    </div>
  );
}
