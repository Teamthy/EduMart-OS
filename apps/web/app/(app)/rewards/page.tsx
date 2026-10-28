import type { Metadata } from "next";
import { Gift, Sparkles, Users } from "lucide-react";

import { formatMoney, formatRelativeDate, type RewardsSummary } from "@edumart/shared";

import { Alert } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { EmptyState } from "@/components/ui/empty-state";
import { RedeemPerk } from "./redeem-perk";
import { ServerApiError, serverFetch } from "@/lib/server-api";
import { requireUser } from "@/lib/session";

export const metadata: Metadata = { title: "Rewards" };
export const dynamic = "force-dynamic";

export default async function RewardsPage() {
  await requireUser("/rewards");

  let rewards: RewardsSummary;
  try {
    ({ rewards } = await serverFetch<{ rewards: RewardsSummary }>("/v1/rewards"));
  } catch (error) {
    return (
      <Alert tone="error" title="We couldn't load your rewards">
        {error instanceof ServerApiError ? error.message : "Please try again."}
      </Alert>
    );
  }

  return (
    <div className="space-y-6">
      <header className="space-y-1">
        <h1 className="text-2xl">Rewards</h1>
        <p className="text-sm text-muted-foreground">
          Learning points are earned from completed work. They are separate from money — commission
          is tracked in its own ledger and never mixed with points.
        </p>
      </header>

      <section className="grid gap-3 sm:grid-cols-3">
        <Card>
          <CardContent className="p-4">
            <Sparkles aria-hidden="true" className="mb-1 size-4 text-primary" />
            <p className="text-xs uppercase tracking-wide text-muted-foreground">Points balance</p>
            <p className="font-serif text-2xl">{rewards.pointsBalance}</p>
            <p className="text-xs text-muted-foreground">{rewards.pointsEarned} earned to date</p>
          </CardContent>
        </Card>
        <Card>
          <CardContent className="p-4">
            <Users aria-hidden="true" className="mb-1 size-4 text-primary" />
            <p className="text-xs uppercase tracking-wide text-muted-foreground">Referrals</p>
            <p className="font-serif text-2xl">{rewards.referrals}</p>
            <p className="text-xs text-muted-foreground">{rewards.qualifiedReferrals} qualified</p>
          </CardContent>
        </Card>
        <Card>
          <CardContent className="p-4">
            <Gift aria-hidden="true" className="mb-1 size-4 text-primary" />
            <p className="text-xs uppercase tracking-wide text-muted-foreground">Commission earned</p>
            <p className="font-serif text-2xl">{formatMoney(rewards.commissionMinor, rewards.currency)}</p>
            <p className="text-xs text-muted-foreground">Paid out after each qualifying purchase</p>
          </CardContent>
        </Card>
      </section>

      <Card>
        <CardHeader>
          <CardTitle>Your referral code</CardTitle>
          <CardDescription>Share it — you earn 10% of a friend&apos;s first purchase.</CardDescription>
        </CardHeader>
        <CardContent className="space-y-3">
          <p className="rounded-lg border border-dashed border-border bg-muted px-4 py-3 text-center font-mono text-lg tracking-widest">
            {rewards.referralCode || "—"}
          </p>
          <p className="text-xs text-muted-foreground">
            Friends can enter the code during signup, or open your link:{" "}
            <span className="font-mono">/register?ref={rewards.referralCode || "YOUR-CODE"}</span>
          </p>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Spend points</CardTitle>
          <CardDescription>Perks are cosmetic or convenience — never paid content.</CardDescription>
        </CardHeader>
        <CardContent>
          <RedeemPerk balance={rewards.pointsBalance} />
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Points activity</CardTitle>
        </CardHeader>
        <CardContent>
          {rewards.entries.length === 0 ? (
            <EmptyState
              title="No points yet"
              description="Finish a practice set or mock and points land here immediately."
              icon={<Sparkles aria-hidden="true" className="size-6" />}
            />
          ) : (
            <ul className="divide-y divide-border">
              {rewards.entries.map((entry) => (
                <li key={entry.id} className="flex items-center justify-between gap-3 py-2.5 first:pt-0 last:pb-0">
                  <div>
                    <p className="text-sm font-medium">{entry.note || entry.sourceType}</p>
                    <p className="text-xs text-muted-foreground">{formatRelativeDate(entry.createdAt)}</p>
                  </div>
                  <Badge variant={entry.amount >= 0 ? "success" : "outline"}>
                    {entry.amount >= 0 ? "+" : ""}
                    {entry.amount}
                  </Badge>
                </li>
              ))}
            </ul>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
