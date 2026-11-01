import type { Metadata } from "next";
import Link from "next/link";
import { Coins, Wallet as WalletIcon } from "lucide-react";

import { Alert } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { ServerApiError, serverFetch } from "@/lib/server-api";
import { requireUser } from "@/lib/session";
import { RechargeForm } from "./recharge-form";

interface Topup {
  id: string;
  reference: string;
  amountMinor: number;
  currency: string;
  points: number;
  nairaPerPoint: number;
  status: "pending" | "paid" | "failed";
  createdAt: string;
  paidAt: string | null;
}

interface LedgerRow {
  id: string;
  amount: number;
  entryType: string;
  source: string;
  note: string;
  createdAt: string;
}

interface Wallet {
  balance: number;
  earned: number;
  spent: number;
  minTopupMinor: number;
  nairaPerPoint: number;
  currency: string;
  topups: Topup[];
  recent: LedgerRow[];
}

export const metadata: Metadata = { title: "Wallet" };
const SANDBOX_ENABLED = process.env.EDUMART_ALLOW_SANDBOX_PAYMENTS !== "false";
export const dynamic = "force-dynamic";

function naira(minor: number) {
  return `₦${(minor / 100).toLocaleString("en-NG")}`;
}

function when(value: string) {
  const date = new Date(value);
  return Number.isNaN(date.getTime())
    ? value
    : date.toLocaleString("en-NG", { dateStyle: "medium", timeStyle: "short", timeZone: "Africa/Lagos" });
}

export default async function WalletPage() {
  await requireUser("/wallet");

  let wallet: Wallet;
  try {
    const response = await serverFetch<{ wallet: Wallet }>("/v1/wallet");
    wallet = response.wallet;
  } catch (error) {
    return (
      <Alert tone="error" title="Your wallet is unavailable right now">
        {error instanceof ServerApiError ? error.message : "Please try again shortly."}
      </Alert>
    );
  }

  return (
    <div className="space-y-6">
      <header className="space-y-1">
        <h1 className="text-2xl">Wallet</h1>
        <p className="text-sm text-muted-foreground">
          Credits pay for live classes and anything else in the store. Recharge from ₦200; the floor and the rate
          below come from the server, so the numbers here are the numbers charged.
        </p>
      </header>

      <div className="grid gap-4 sm:grid-cols-3">
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-base">
              <WalletIcon aria-hidden="true" className="size-4" />
              Balance
            </CardTitle>
          </CardHeader>
          <CardContent>
            <p className="text-3xl font-semibold tabular-nums">{wallet.balance}</p>
            <p className="text-xs text-muted-foreground">
              credits · earned {wallet.earned} · spent {wallet.spent}
            </p>
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle className="text-base">Recharge rate</CardTitle>
          </CardHeader>
          <CardContent>
            <p className="text-3xl font-semibold tabular-nums">₦{wallet.nairaPerPoint}</p>
            <p className="text-xs text-muted-foreground">
              per credit · minimum {naira(wallet.minTopupMinor)}
            </p>
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle className="text-base">Need more?</CardTitle>
          </CardHeader>
          <CardContent className="space-y-2 text-xs text-muted-foreground">
            <p>Referrals and streaks also earn credits — see your rewards page.</p>
            <Link href="/rewards" className="text-primary underline-offset-2 hover:underline">
              Open rewards
            </Link>
          </CardContent>
        </Card>
      </div>

      <RechargeForm wallet={wallet} sandboxEnabled={SANDBOX_ENABLED} />

      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2 text-base">
            <Coins aria-hidden="true" className="size-4" />
            Recent activity
          </CardTitle>
        </CardHeader>
        <CardContent>
          {wallet.recent.length === 0 ? (
            <p className="text-sm text-muted-foreground">Nothing yet. Your first recharge starts the history.</p>
          ) : (
            <ul className="divide-y divide-border text-sm">
              {wallet.recent.map((row) => (
                <li key={row.id} className="flex items-center justify-between gap-3 py-2">
                  <span className="min-w-0">
                    <span className="block truncate">{row.note || row.source}</span>
                    <span className="text-xs text-muted-foreground">{when(row.createdAt)}</span>
                  </span>
                  <span
                    className={
                      row.amount < 0 ? "shrink-0 tabular-nums text-muted-foreground" : "shrink-0 tabular-nums text-emerald-600"
                    }
                  >
                    {row.amount > 0 ? "+" : ""}
                    {row.amount}
                  </span>
                </li>
              ))}
            </ul>
          )}
        </CardContent>
      </Card>

      {wallet.topups.length > 0 ? (
        <Card>
          <CardHeader>
            <CardTitle className="text-base">Recharges</CardTitle>
          </CardHeader>
          <CardContent>
            <ul className="divide-y divide-border text-sm">
              {wallet.topups.map((topup) => (
                <li key={topup.id} className="flex items-center justify-between gap-3 py-2">
                  <span className="min-w-0">
                    <span className="block truncate">
                      {naira(topup.amountMinor)} → {topup.points} credits
                    </span>
                    <span className="font-mono text-xs text-muted-foreground">{topup.reference}</span>
                  </span>
                  <Badge variant={topup.status === "paid" ? "default" : "outline"}>{topup.status}</Badge>
                </li>
              ))}
            </ul>
          </CardContent>
        </Card>
      ) : null}
    </div>
  );
}
