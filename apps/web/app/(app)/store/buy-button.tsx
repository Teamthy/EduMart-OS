"use client";

import { useRouter } from "next/navigation";
import { useState } from "react";
import { Loader2 } from "lucide-react";

import { ApiError } from "@edumart/shared";

import { Button } from "@/components/ui/button";
import { api } from "@/lib/browser-api";

export function BuyButton({ productId }: { productId: string }) {
  const router = useRouter();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function buy() {
    setBusy(true);
    setError(null);
    try {
      const { order } = await api().createOrder(productId);
      router.push(`/checkout/${order.id}`);
    } catch (caught) {
      setError(caught instanceof ApiError ? caught.message : "We couldn't start that order.");
      setBusy(false);
    }
  }

  return (
    <div className="text-right">
      {error ? <p className="mb-1 text-xs text-destructive">{error}</p> : null}
      <Button size="sm" onClick={() => void buy()} disabled={busy}>
        {busy ? <Loader2 aria-hidden="true" className="animate-spin" /> : null}
        {busy ? "Starting…" : "Buy"}
      </Button>
    </div>
  );
}
