import Link from "next/link";
import { Shell } from "@/components/Shell";
import { requirePageUser } from "@/lib/server/guards";
import { listPathways, listSubjects, listTopics } from "@/lib/server/content";
import { PracticeSetup } from "./PracticeSetup";

export const dynamic = "force-dynamic";

export default async function PracticePage({
  searchParams,
}: {
  searchParams: Promise<{ subject?: string }>;
}) {
  const user = await requirePageUser("/practice");
  const { subject } = await searchParams;
  const pathways = listPathways();
  const activePathway = pathways.find((p) => p.id === user.examPathwayId) ?? pathways[0];
  const subjects = activePathway ? listSubjects(activePathway.id) : [];
  const initialSubject = subject && subjects.some((s) => s.id === subject) ? subject : subjects[0]?.id;
  const topics = initialSubject ? listTopics(initialSubject) : [];

  return (
    <Shell
      title="Practice"
      subtitle="Choose a subject, focus your topics, and start when you are ready."
      activePath="/practice"
      action={<Link className="btn secondary sm" href="/exam">Timed mock instead</Link>}
    >
      {subjects.length === 0 ? (
        <div className="empty">
          <span className="glyph">✎</span>
          <div className="strong">No subjects available yet</div>
          <div className="small">Pick an exam pathway in settings to load subjects.</div>
          <Link className="btn sm" href="/account">Open settings</Link>
        </div>
      ) : (
        <PracticeSetup
          subjects={subjects}
          pathwayName={activePathway?.name ?? ""}
          initialSubjectId={initialSubject!}
          initialTopics={topics}
        />
      )}
    </Shell>
  );
}
