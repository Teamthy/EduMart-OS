"use client";

import { useRouter } from "next/navigation";
import { useState } from "react";

export function PurchasePanel({
  slug,
  priceMinor,
  owned,
  entitlementId,
}: {
  slug: string;
  priceMinor: number;
  owned: boolean;
  entitlementId: string | null;
}) {
  const router = useRouter();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [manifest, setManifest] = useState<any>(null);

  if (owned && entitlementId) {
    return (
      <div className="stack-sm">
        <span className="badge">You have access</span>
        {manifest ? (
          <div className="stack-sm">
            {manifest.chapters?.map((chapter: any) => (
              <div key={chapter.title} className="row-between small" style={{ borderTop: "1px solid var(--line)", paddingTop: 6 }}>
                <span>{chapter.title}</span>
                <span className="tiny muted">pp. {chapter.pages}</span>
              </div>
            ))}
            <div className="banner info tiny">
              Access verified with a signed link that expires in 5 minutes. In production this streams the
              protected file from private object storage.
            </div>
          </div>
        ) : (
          <button
            className="btn lg block"
            type="button"
            disabled={busy}
            onClick={async () => {
              setBusy(true);
              setError(null);
              const res = await fetch(`/api/v1/entitlements/${entitlementId}/content`).catch(() => null);
              const data = res ? await res.json().catch(() => ({})) : {};
              setBusy(false);
              if (!res || !res.ok) {
                setError(data?.error?.message ?? "Could not open this publication.");
                return;
              }
              const file = await fetch(data.signed_url).then((r) => r.json()).catch(() => null);
              setManifest({ chapters: file?.chapters });
            }}
          >
            {busy ? "Opening…" : "Open reader"}
          </button>
        )}
        {error ? <div className="banner error tiny">{error}</div> : null}
      </div>
    );
  }

  return (
    <div className="stack-sm">
      <button
        className="btn lg block"
        type="button"
        disabled={busy}
        onClick={async () => {
          setBusy(true);
          setError(null);
          const res = await fetch("/api/v1/orders", {
            method: "POST",
            headers: { "content-type": "application/json", "idempotency-key": `order-${slug}-${Date.now()}` },
            body: JSON.stringify({ items: [slug] }),
          }).catch(() => null);
          const data = res ? await res.json().catch(() => ({})) : {};
          setBusy(false);
          if (!res || !res.ok) {
            setError(data?.error?.message ?? "Could not create the order.");
            return;
          }
          router.push(`/checkout/${data.order.id}`);
        }}
      >
        {busy ? "Preparing order…" : priceMinor === 0 ? "Get free access" : "Buy now"}
      </button>
      {error ? <div className="banner error tiny">{error}</div> : null}
      <p className="tiny muted center">Secure checkout · payment confirmed server-side</p>
    </div>
  );
}
