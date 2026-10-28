import Link from "next/link";
import { Shell } from "@/components/Shell";
import { requirePageUser, relativeTime } from "@/lib/server/guards";
import { listPathways } from "@/lib/server/content";
import { ensureReferralCode } from "@/lib/server/rewards";
import { q } from "@/lib/server/db";
import { AccountForm } from "./AccountForm";
import { SupportForm } from "./SupportForm";

export const dynamic = "force-dynamic";

export default async function AccountPage() {
  const user = await requirePageUser("/account");
  const pathways = listPathways();
  const referral = ensureReferralCode(user.id, user.displayName);
  const cases = q.all<any>(
    "SELECT id, category, status, subject, created_at FROM support_cases WHERE opened_by = ? ORDER BY created_at DESC LIMIT 5",
    user.id,
  );
  const roleLabelText = user.roles.includes("platform_admin")
    ? "Platform administrator"
    : user.roles.includes("teacher")
      ? "Teacher"
      : "Student";

  return (
    <Shell title="Settings" subtitle="Profile, preferences, privacy and support." activePath="/account">
      <div className="split" style={{ gridTemplateColumns: "1.1fr 0.9fr", alignItems: "start" }}>
        <div className="stack">
          <section className="card">
            <div className="card-head">
              <div>
                <div className="card-title">Profile & learning preferences</div>
                <div className="card-sub">Changing your pathway never deletes your history</div>
              </div>
              <span className="badge neutral">{roleLabelText}</span>
            </div>
            <AccountForm
              user={{ displayName: user.displayName, examPathwayId: user.examPathwayId, targetDate: user.targetDate, timezone: user.timezone }}
              pathways={pathways.map((p) => ({ id: p.id, name: p.name }))}
            />
          </section>

          <section className="card">
            <div className="card-title">Account & session security</div>
            <dl className="kv" style={{ marginTop: 10 }}>
              <dt>Email</dt><dd>{user.email}</dd>
              <dt>Account status</dt><dd style={{ textTransform: "capitalize" }}>{user.status}</dd>
              <dt>Member since</dt><dd>{new Date(user.createdAt).toLocaleDateString("en-NG", { day: "numeric", month: "long", year: "numeric" })}</dd>
              <dt>Roles</dt><dd style={{ textTransform: "capitalize" }}>{user.roles.join(", ")}</dd>
              <dt>Session</dt><dd>HttpOnly cookie · revoked on sign-out and password reset</dd>
            </dl>
            <div className="banner info small" style={{ marginTop: 12 }}>
              <span className="ico">🔒</span>
              Passwords use salted scrypt hashing. Reset links are single-use and expire in 30 minutes; a reset revokes
              every existing session.
            </div>
          </section>

          <section className="card">
            <div className="card-title">Your data</div>
            <p className="small muted" style={{ marginTop: 4 }}>
              You can request a copy of your learning records or account deletion. Deletion is processed under the
              retention policy: assessment and payment records required for audit are retained, and personal fields
              are removed.
            </p>
            <div className="btn-row" style={{ marginTop: 10 }}>
              <Link className="btn secondary sm" href="/support">Request data export</Link>
              <Link className="btn ghost sm" href="/support">Request deletion</Link>
            </div>
          </section>
        </div>

        <aside className="stack">
          <section className="card">
            <div className="card-title">Rewards</div>
            <p className="tiny muted" style={{ marginTop: 4 }}>Your referral code</p>
            <div className="input mono strong" style={{ marginTop: 6, letterSpacing: "0.06em" }}>{referral.code}</div>
            <Link className="btn secondary sm block" href="/rewards" style={{ marginTop: 10 }}>Open rewards</Link>
          </section>

          <section className="card">
            <div className="card-title">Support</div>
            {cases.length > 0 ? (
              <div className="stack-sm" style={{ margin: "10px 0" }}>
                {cases.map((c) => (
                  <div key={c.id} className="row-between" style={{ borderTop: "1px solid var(--line)", paddingTop: 8 }}>
                    <span>
                      <span className="small strong" style={{ display: "block" }}>{c.subject}</span>
                      <span className="tiny muted">{c.category} · {relativeTime(c.created_at)}</span>
                    </span>
                    <span className="badge neutral" style={{ textTransform: "capitalize" }}>{c.status}</span>
                  </div>
                ))}
              </div>
            ) : (
              <p className="tiny muted" style={{ margin: "6px 0 10px" }}>No open cases. Payment, access and exam disputes are handled here.</p>
            )}
            <SupportForm />
          </section>
        </aside>
      </div>
    </Shell>
  );
}
