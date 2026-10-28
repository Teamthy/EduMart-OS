"use client";

import { useRouter } from "next/navigation";
import { useEffect, useState } from "react";
import { Loader2, RefreshCw } from "lucide-react";

import { ApiError } from "@edumart/shared";

import { Alert } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { api } from "@/lib/browser-api";

/**
 * Polls the order until the webhook has been applied, then re-renders the server component.
 * The "simulate" button exists only in sandbox mode and drives the *same* signed-webhook path the
 * real provider uses, so a demo can never diverge from production behaviour.
 */
export function PendingPayment({ orderId, sandboxEnabled }: { orderId: string; sandboxEnabled: boolean }) {
  const router = useRouter();
  const [polling, setPolling] = useState(true);
  const [simulating, setSimulating] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [ticks, setTicks] = useState(0);

  useEffect(() => {
    if (!polling) return;
    const interval = setInterval(async () => {
      try {
        const { order } = await api().order(orderId);
        setTicks((value) => value + 1);
        if (order.status !== "pending_payment") {
          setPolling(false);
          router.refresh();
        }
      } catch {
        // Keep polling: a transient failure should not stop the check.
      }
    }, 4000);
    return () => clearInterval(interval);
  }, [orderId, polling, router]);

  async function simulate() {
    setSimulating(true);
    setError(null);
    try {
      await api().simulatePayment(orderId);
      router.refresh();
    } catch (caught) {
      setError(caught instanceof ApiError ? caught.message : "The sandbox payment could not be simulated.");
    } finally {
      setSimulating(false);
    }
  }

  return (
    <div className="space-y-3">
      {error ? <Alert tone="error">{error}</Alert> : null}

      <p className="flex items-center gap-2 text-xs text-muted-foreground" aria-live="polite">
        <Loader2 aria-hidden="true" className="size-3.5 animate-spin" />
        Checking automatically{sandboxEnabled ? "" : ""} — {ticks} check{ticks === 1 ? "" : "s"} so far.
      </p>

      <div className="flex flex-wrap gap-2">
        <Button variant="outline" onClick={() => router.refresh()}>
          <RefreshCw aria-hidden="true" /> Check again
        </Button>
        {sandboxEnabled ? (
          <Button onClick={() => void simulate()} disabled={simulating}>
            {simulating ? <Loader2 aria-hidden="true" className="animate-spin" /> : null}
            Simulate provider webhook (sandbox)
          </Button>
        ) : null}
      </div>
    </div>
  );
}
