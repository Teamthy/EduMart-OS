"use client";

import { useRouter } from "next/navigation";
import { useState } from "react";
import { Loader2 } from "lucide-react";

import { ApiError } from "@edumart/shared";

import { Alert } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";

const PERKS = [
  { key: "streak-freeze", label: "Streak freeze", cost: 50, copy: "Protect your streak for one missed day." },
  { key: "mock-retake", label: "Extra mock attempt", cost: 120, copy: "One additional attempt on a timed mock." },
  { key: "revision-pack", label: "Revision pack", cost: 200, copy: "A curated set of 20 weak-topic questions." },
];

export function RedeemPerk({ balance }: { balance: number }) {
  const router = useRouter();
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [done, setDone] = useState<string | null>(null);

  async function redeem(perk: string, label: string) {
    setBusy(perk);
    setError(null);
    setDone(null);
    try {
      const { api } = await import("@/lib/browser-api");
      await api().redeem(perk);
      setDone(`${label} redeemed.`);
      router.refresh();
    } catch (caught) {
      setError(caught instanceof ApiError ? caught.message : "That reward could not be redeemed.");
    } finally {
      setBusy(null);
    }
  }

  return (
    <div className="space-y-3">
      {error ? <Alert tone="error">{error}</Alert> : null}
      {done ? <Alert tone="success">{done}</Alert> : null}

      <ul className="space-y-2">
        {PERKS.map((perk) => {
          const affordable = balance >= perk.cost;
          return (
            <li key={perk.key} className="flex items-center justify-between gap-3 rounded-lg border border-border p-3">
              <div>
                <p className="text-sm font-semibold">{perk.label}</p>
                <p className="text-xs text-muted-foreground">{perk.copy}</p>
              </div>
              <Button
                size="sm"
                variant={affordable ? "secondary" : "outline"}
                disabled={!affordable || busy !== null}
                onClick={() => void redeem(perk.key, perk.label)}
              >
                {busy === perk.key ? <Loader2 aria-hidden="true" className="animate-spin" /> : null}
                {perk.cost} pts
              </Button>
            </li>
          );
        })}
      </ul>
      <p className="text-xs text-muted-foreground">
        You have {balance} points. Redemptions are recorded in the same append-only ledger as earnings.
      </p>
    </div>
  );
}
