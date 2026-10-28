import Link from "next/link";
import { Shell } from "@/components/Shell";
import { requirePageUser } from "@/lib/server/guards";
import { q } from "@/lib/server/db";
import { sha256 } from "@/lib/server/auth";
import { AcceptInvite } from "./AcceptInvite";

export const dynamic = "force-dynamic";

export default async function JoinPage({ params }: { params: Promise<{ token: string }> }) {
  const user = await requirePageUser();
  const { token } = await params;
  const invitation = q.get<any>(
    `SELECT i.*, c.name AS class_name, o.name AS org_name
       FROM invitations i
       LEFT JOIN classes c ON c.id = i.class_id
       LEFT JOIN organizations o ON o.id = i.organization_id
      WHERE i.token_hash = ?`,
    sha256(token),
  );

  const usable =
    invitation &&
    invitation.use_count < invitation.max_uses &&
    new Date(invitation.expires_at).getTime() > Date.now() &&
    (!invitation.email || invitation.email.toLowerCase() === user.email);

  return (
    <Shell title="Class invitation" subtitle={invitation?.org_name ?? "Edumart"} activePath="/classes">
      <div className="card pad-lg stack" style={{ maxWidth: 560 }}>
        {!invitation ? (
          <>
            <div className="banner error"><span className="ico">!</span>This invitation link is not valid.</div>
            <Link className="btn secondary" href="/classes">Go to my classes</Link>
          </>
        ) : !usable ? (
          <>
            <div className="banner error">
              <span className="ico">!</span>
              {invitation.use_count >= invitation.max_uses
                ? "This invitation has already been used."
                : new Date(invitation.expires_at).getTime() <= Date.now()
                  ? "This invitation has expired."
                  : "This invitation was issued for a different email address."}
            </div>
            <p className="small muted">Ask your teacher for a new link or use the class code instead.</p>
            <Link className="btn secondary" href="/classes">Go to my classes</Link>
          </>
        ) : (
          <>
            <span className="badge indigo">Invitation</span>
            <h2 style={{ fontSize: "1.5rem" }}>Join {invitation.class_name}</h2>
            <p className="small muted">
              You are signed in as <strong>{user.email}</strong>. Accepting adds you to this class only — it does not
              grant access to other classes in the organization.
            </p>
            <dl className="kv tiny">
              <dt>Organization</dt><dd>{invitation.org_name ?? "—"}</dd>
              <dt>Role</dt><dd style={{ textTransform: "capitalize" }}>{invitation.intended_role}</dd>
              <dt>Expires</dt><dd>{new Date(invitation.expires_at).toLocaleDateString("en-NG")}</dd>
              <dt>Uses</dt><dd>{invitation.use_count} of {invitation.max_uses}</dd>
            </dl>
            <AcceptInvite token={token} classId={invitation.class_id} />
          </>
        )}
      </div>
    </Shell>
  );
}
