"use client";

import { useRouter } from "next/navigation";
import { useState } from "react";

export function RefundAction({ orderId }: { orderId: string }) {
  const router = useRouter();
  const [open, setOpen] = useState(false);
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState<string | null>(null);

  if (!open) {
    return <button className="btn ghost sm" type="button" onClick={() => setOpen(true)}>Refund</button>;
  }

  return (
    <div className="stack-sm" style={{ minWidth: 220 }}>
      {message ? <div className="banner info tiny">{message}</div> : null}
      <input className="input" placeholder="Refund reason" value={reason} onChange={(e) => setReason(e.target.value)} aria-label="Refund reason" />
      <div className="btn-row" style={{ justifyContent: "flex-end" }}>
        <button className="btn ghost sm" type="button" onClick={() => setOpen(false)}>Cancel</button>
        <button
          className="btn danger sm"
          type="button"
          disabled={busy || reason.trim().length < 5}
          onClick={async () => {
            setBusy(true);
            const res = await fetch(`/api/v1/admin/orders/${orderId}/refund`, {
              method: "POST",
              headers: { "content-type": "application/json" },
              body: JSON.stringify({ reason }),
            }).catch(() => null);
            const data = res ? await res.json().catch(() => ({})) : {};
            setBusy(false);
            if (!res || !res.ok) {
              setMessage(data?.error?.message ?? "Refund failed.");
              return;
            }
            setMessage("Refunded. Entitlement revoked and commission reversed.");
            setTimeout(() => router.refresh(), 800);
          }}
        >
          {busy ? "Refunding…" : "Confirm refund"}
        </button>
      </div>
    </div>
  );
}
