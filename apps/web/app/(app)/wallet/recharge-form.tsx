"use client";

import { useRouter } from "next/navigation";
import { useState } from "react";
import { Loader2, Plus } from "lucide-react";

import { ApiError } from "@edumart/shared";

import { Alert } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { api } from "@/lib/browser-api";

interface WalletView {
  minTopupMinor: number;
  nairaPerPoint: number;
  currency: string;
}

/**
 * Recharge.
 *
 * The presets are derived from the floor the server sent, so a deployment that raises the minimum raises the
 * buttons too — a form that offers an amount the API refuses is worse than no form. Amounts are sent as kobo;
 * naira never crosses the wire, which removes the common 100x mistake at its source.
 *
 * The confirm button only appears in sandbox mode. Real deployments settle from the provider's webhook, and a
 * browser able to mark its own top-up paid would make the minimum, the rate and the ledger all decorative.
 */
export function RechargeForm({ wallet, sandboxEnabled }: { wallet: WalletView; sandboxEnabled: boolean }) {
  const router = useRouter();
  const floor = Math.max(wallet.minTopupMinor, 20000);
  const presets = [floor, floor * 2, floor * 5, floor * 10];
  const [amountMinor, setAmountMinor] = useState(floor);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [pendingId, setPendingId] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);

  const points = Math.floor(amountMinor / 100 / wallet.nairaPerPoint);

  async function recharge() {
    setBusy(true);
    setError(null);
    setNotice(null);
    try {
      const { topup } = await api().startTopup(amountMinor);
      setPendingId(topup.id);
      setNotice(
        sandboxEnabled
          ? `Recharge ${topup.reference} is pending. Use the sandbox button below to confirm it.`
          : `Recharge ${topup.reference} is pending. Complete the payment with your provider; credits appear once it settles.`,
      );
      router.refresh();
    } catch (caught) {
      setError(caught instanceof ApiError ? caught.message : "That recharge could not be started.");
    } finally {
      setBusy(false);
    }
  }

  async function settle() {
    if (!pendingId) return;
    setBusy(true);
    setError(null);
    try {
      await api().confirmTopup(pendingId);
      setPendingId(null);
      setNotice("Credits added. The balance above will not move until this page refreshes.");
      router.refresh();
    } catch (caught) {
      setError(caught instanceof ApiError ? caught.message : "That recharge could not be confirmed.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">Recharge</CardTitle>
      </CardHeader>
      <CardContent className="space-y-4">
        {error ? <Alert tone="error">{error}</Alert> : null}
        {notice ? <Alert tone="success">{notice}</Alert> : null}

        <div className="flex flex-wrap gap-2">
          {presets.map((minor) => (
            <button
              key={minor}
              type="button"
              onClick={() => setAmountMinor(minor)}
              aria-pressed={amountMinor === minor}
              className={
                amountMinor === minor
                  ? "min-h-11 rounded-lg border border-primary bg-secondary px-4 text-sm font-semibold"
                  : "min-h-11 rounded-lg border border-border bg-card px-4 text-sm font-semibold text-muted-foreground"
              }
            >
              ₦{(minor / 100).toLocaleString("en-NG")}
            </button>
          ))}
        </div>

        <div className="space-y-2">
          <label htmlFor="amount-naira" className="text-sm font-medium">
            Or enter an amount in naira
          </label>
          <Input
            id="amount-naira"
            type="number"
            min={floor / 100}
            step={Math.max(wallet.nairaPerPoint, 1)}
            value={amountMinor / 100}
            onChange={(event) => {
              // Naira in the field, kobo on the wire. A blank or nonsense entry keeps the last good amount.
              const naira = Number(event.target.value);
              if (!Number.isFinite(naira) || naira < 0) return;
              setAmountMinor(Math.round(naira * 100));
            }}
            className="max-w-48"
          />
          <p className="text-xs text-muted-foreground">
            {amountMinor < floor
              ? `The smallest recharge is ₦${(floor / 100).toLocaleString("en-NG")}.`
              : `₦${(amountMinor / 100).toLocaleString("en-NG")} buys ${points} credit${points === 1 ? "" : "s"}.`}
          </p>
        </div>

        <div className="flex flex-wrap gap-2">
          <Button type="button" onClick={() => void recharge()} disabled={busy || amountMinor < floor}>
            {busy ? <Loader2 aria-hidden="true" className="animate-spin" /> : <Plus aria-hidden="true" />}
            Start recharge
          </Button>
          {sandboxEnabled && pendingId ? (
            <Button type="button" variant="outline" onClick={() => void settle()} disabled={busy}>
              Confirm sandbox payment
            </Button>
          ) : null}
        </div>

        <p className="text-xs text-muted-foreground">
          A recharge is not an order: nothing is credited until the payment is confirmed, and a confirmed payment
          credits once even if the provider calls twice.
        </p>
      </CardContent>
    </Card>
  );
}
