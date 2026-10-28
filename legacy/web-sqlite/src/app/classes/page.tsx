import Link from "next/link";
import { Shell } from "@/components/Shell";
import { requirePageUser } from "@/lib/server/guards";
import { listClasses } from "@/lib/server/classes";
import { JoinClassForm } from "./JoinClassForm";

export const dynamic = "force-dynamic";

export default async function ClassesPage() {
  const user = await requirePageUser("/classes");
  const classes = listClasses(user.id);

  return (
    <Shell
      title="Classes"
      subtitle="Teacher-led classes you belong to."
      activePath="/classes"
      action={<Link className="btn sm" href="/teacher">Teacher overview</Link>}
    >
      <div className="split" style={{ gridTemplateColumns: "1.6fr 1fr", alignItems: "start" }}>
        <section className="stack">
          {classes.length === 0 ? (
            <div className="empty">
              <span className="glyph">👥</span>
              <div className="strong">You have not joined a class yet</div>
              <div className="small">Ask your teacher for a class code, or open an invitation link they shared.</div>
            </div>
          ) : (
            <div className="grid-3">
              {classes.map((cls) => (
                <Link key={cls.id} href={`/classes/${cls.id}`} className="card interactive stack">
                  <div className="row-between">
                    <span className={`badge ${cls.role === "teacher" ? "indigo" : "neutral"}`} style={{ textTransform: "capitalize" }}>{cls.role}</span>
                    <span className="tiny muted">{cls.student_count} learners</span>
                  </div>
                  <div>
                    <div className="strong" style={{ fontSize: 15 }}>{cls.name}</div>
                    <div className="tiny muted">{cls.organization_name ?? "Personal class"}</div>
                  </div>
                  <div className="row-between tiny muted">
                    <span>{cls.assessment_count} assessments</span>
                    <span className="mono">{cls.join_code}</span>
                  </div>
                </Link>
              ))}
            </div>
          )}
        </section>

        <aside className="stack aside-sticky" style={{ position: "sticky", top: 84 }}>
          <section className="card">
            <div className="card-title">Join with a class code</div>
            <p className="tiny muted" style={{ margin: "4px 0 12px" }}>
              Codes are issued by your teacher. Joining gives you access only to that class.
            </p>
            <JoinClassForm />
          </section>
          <section className="card">
            <div className="card-title">Invitation link</div>
            <p className="tiny muted" style={{ marginTop: 4 }}>
              Invitation links are single-use or limited-use, expire after 7 days and can be revoked by the teacher.
              Open the link you were sent and accept it while signed in.
            </p>
          </section>
        </aside>
      </div>
    </Shell>
  );
}
