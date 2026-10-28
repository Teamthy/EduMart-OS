import Link from "next/link";
import { Shell } from "@/components/Shell";
import { requirePageUser, relativeTime } from "@/lib/server/guards";
import { pointsSummary, commissionSummary, ensureReferralCode } from "@/lib/server/rewards";
import { q } from "@/lib/server/db";
import { ShareCode } from "./ShareCode";

export const dynamic = "force-dynamic";

export default async function RewardsPage() {
  const user = await requirePageUser("/rewards");
  const points = pointsSummary(user.id);
  const commissions = commissionSummary(user.id);
  const referral = ensureReferralCode(user.id, user.displayName);
  const attributions = q.all<any>(
    `SELECT ra.status, ra.attributed_at, u.display_name
       FROM referral_attributions ra JOIN users u ON u.id = ra.referred_user_id
      WHERE ra.referral_code_id = ? ORDER BY ra.attributed_at DESC`,
    referral.id,
  );

  return (
    <Shell title="Rewards" subtitle="Learning points and monetary commissions are separate assets with separate rules." activePath="/rewards">
      <div className="banner warn">
        <span className="ico">⚖</span>
        <span>
          Points are non-cash and only reflect learning activity. Commissions are recorded in a separate ledger and
          <strong> payouts are disabled</strong> until legal, accounting, provider and fraud controls are approved.
        </span>
      </div>

      <div className="split" style={{ gridTemplateColumns: "1fr 1fr" }}>
        <section className="card pad-lg stack">
          <div className="row-between">
            <div>
              <div className="eyebrow">Learning points</div>
              <div className="serif" style={{ fontSize: 40, color: "var(--green-900)", lineHeight: 1.1 }}>{points.balance}</div>
              <div className="tiny muted">Earned from completed practice and assessments</div>
            </div>
            <span className="badge">Non-cash</span>
          </div>
          <div>
            <div className="card-title" style={{ fontSize: 13 }}>Recent entries</div>
            {points.ledger.length === 0 ? (
              <div className="tiny muted" style={{ marginTop: 6 }}>No entries yet — complete an attempt to earn points.</div>
            ) : (
              points.ledger.slice(0, 6).map((entry) => (
                <div key={entry.id} className="row-between" style={{ borderTop: "1px solid var(--line)", padding: "8px 0" }}>
                  <span>
                    <span className="small strong" style={{ display: "block", textTransform: "capitalize" }}>{entry.source_type.replace(/_/g, " ")}</span>
                    <span className="tiny muted">{relativeTime(entry.created_at)}</span>
                  </span>
                  <span className={`badge ${entry.amount > 0 ? "" : "neutral"}`}>{entry.amount > 0 ? "+" : ""}{entry.amount}</span>
                </div>
              ))
            )}
          </div>
          <p className="tiny muted">Ledger is append-only. Corrections use compensating entries, never edits.</p>
        </section>

        <section className="card pad-lg stack">
          <div className="row-between">
            <div>
              <div className="eyebrow">Commission balance</div>
              <div className="serif" style={{ fontSize: 40, color: "var(--green-900)", lineHeight: 1.1 }}>
                ₦{((commissions.eligibleMinor + commissions.pendingMinor) / 100).toLocaleString("en-NG")}
              </div>
              <div className="tiny muted">Accrued from qualifying referred purchases</div>
            </div>
            <span className="badge amber">Payouts off</span>
          </div>
          <div className="grid-3" style={{ gap: 8 }}>
            <div className="metric tight" style={{ padding: 10 }}>
              <span className="label">Pending</span>
              <span className="small strong">₦{(commissions.pendingMinor / 100).toLocaleString("en-NG")}</span>
            </div>
            <div className="metric tight" style={{ padding: 10 }}>
              <span className="label">Eligible</span>
              <span className="small strong">₦{(commissions.eligibleMinor / 100).toLocaleString("en-NG")}</span>
            </div>
            <div className="metric tight" style={{ padding: 10 }}>
              <span className="label">Reversed</span>
              <span className="small strong">₦{(commissions.reversedMinor / 100).toLocaleString("en-NG")}</span>
            </div>
          </div>
          <div className="stack-sm">
            {commissions.ledger.length === 0 ? (
              <div className="tiny muted">No commission entries. Referrals only pay on a qualifying purchase.</div>
            ) : (
              commissions.ledger.map((entry) => (
                <div key={entry.id} className="row-between" style={{ borderTop: "1px solid var(--line)", padding: "8px 0" }}>
                  <span>
                    <span className="small strong" style={{ display: "block", textTransform: "capitalize" }}>{entry.entry_type} · {entry.source_type.replace(/_/g, " ")}</span>
                    <span className="tiny muted">{relativeTime(entry.created_at)}</span>
                  </span>
                  <span className="row" style={{ gap: 6 }}>
                    <span className="small">{entry.amount_minor < 0 ? "−" : ""}₦{(Math.abs(entry.amount_minor) / 100).toLocaleString("en-NG")}</span>
                    <span className={`badge ${entry.status === "eligible" ? "" : entry.status === "reversed" ? "rose" : "amber"}`} style={{ textTransform: "capitalize" }}>{entry.status}</span>
                  </span>
                </div>
              ))
            )}
          </div>
          <Link className="btn secondary sm" href="/account">Read the reward rules</Link>
        </section>
      </div>

      <section className="card pad-lg">
        <div className="card-head">
          <div>
            <div className="card-title">Refer a study partner</div>
            <div className="card-sub">One attribution per learner · self-referrals are rejected · caps apply</div>
          </div>
        </div>
        <div className="split" style={{ alignItems: "center" }}>
          <div className="stack-sm">
            <ShareCode code={referral.code} />
            <p className="tiny muted">
              Commission is 10% of a referred learner&apos;s first qualifying purchase, capped at ₦2,000, and only
              after the refund window. Recruitment alone never pays.
            </p>
          </div>
          <div>
            <div className="card-title" style={{ fontSize: 13 }}>Attributions</div>
            {attributions.length === 0 ? (
              <div className="tiny muted" style={{ marginTop: 6 }}>Nobody has used your code yet.</div>
            ) : (
              attributions.map((a, i) => (
                <div key={i} className="row-between" style={{ borderTop: "1px solid var(--line)", padding: "8px 0" }}>
                  <span className="small">{a.display_name}</span>
                  <span className={`badge ${a.status === "qualified" ? "" : "amber"}`} style={{ textTransform: "capitalize" }}>{a.status}</span>
                </div>
              ))
            )}
          </div>
        </div>
      </section>
    </Shell>
  );
}
