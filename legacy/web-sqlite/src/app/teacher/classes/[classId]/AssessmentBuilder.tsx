"use client";

import { useRouter } from "next/navigation";
import { useState } from "react";

type Subject = { id: string; name: string };
type Topic = { id: string; name: string; question_count: number };

export function AssessmentBuilder({
  classId,
  subjects,
  initialSubjectId,
  initialTopics,
}: {
  classId: string;
  subjects: Subject[];
  initialSubjectId?: string;
  initialTopics: Topic[];
}) {
  const router = useRouter();
  const [title, setTitle] = useState("");
  const [subjectId, setSubjectId] = useState(initialSubjectId ?? subjects[0]?.id ?? "");
  const [topics, setTopics] = useState<Topic[]>(initialTopics);
  const [selectedTopics, setSelectedTopics] = useState<string[]>([]);
  const [count, setCount] = useState(10);
  const [duration, setDuration] = useState(20);
  const [attempts, setAttempts] = useState(1);
  const [opensAt, setOpensAt] = useState("");
  const [closesAt, setClosesAt] = useState("");
  const [release, setRelease] = useState<"immediate" | "on_close">("immediate");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [okMessage, setOkMessage] = useState<string | null>(null);

  async function changeSubject(id: string) {
    setSubjectId(id);
    setSelectedTopics([]);
    const res = await fetch(`/api/v1/subjects/${id}/topics`).catch(() => null);
    const data = res ? await res.json().catch(() => ({})) : {};
    setTopics(data.topics ?? []);
  }

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    setOkMessage(null);
    const res = await fetch("/api/v1/assessment-definitions", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({
        title,
        classId,
        subjectId,
        topicIds: selectedTopics,
        questionCount: count,
        durationSeconds: duration * 60,
        attemptLimit: attempts,
        availabilityStart: opensAt ? new Date(opensAt).toISOString() : undefined,
        availabilityEnd: closesAt ? new Date(closesAt).toISOString() : undefined,
        resultRelease: release,
      }),
    }).catch(() => null);
    const data = res ? await res.json().catch(() => ({})) : {};
    setBusy(false);
    if (!res || !res.ok) {
      setError(data?.error?.message ?? "Could not publish this assessment.");
      return;
    }
    setOkMessage(`Published with ${data.assessment.questionCount} questions.`);
    setTitle("");
    router.refresh();
  }

  return (
    <form className="stack-sm" onSubmit={submit}>
      {error ? <div className="banner error small">{error}</div> : null}
      {okMessage ? <div className="banner info small">{okMessage}</div> : null}

      <div className="field">
        <label htmlFor="title">Title</label>
        <input id="title" className="input" value={title} onChange={(e) => setTitle(e.target.value)} placeholder="Week 5 — Trigonometry check" required />
      </div>

      <div className="split" style={{ gap: 10 }}>
        <div className="field">
          <label htmlFor="subject">Subject</label>
          <select id="subject" className="select" value={subjectId} onChange={(e) => changeSubject(e.target.value)}>
            {subjects.map((s) => <option key={s.id} value={s.id}>{s.name}</option>)}
          </select>
        </div>
        <div className="field">
          <label htmlFor="count">Questions</label>
          <input id="count" type="number" min={1} max={60} className="input" value={count} onChange={(e) => setCount(Number(e.target.value) || 1)} />
        </div>
      </div>

      <div className="field">
        <label>Topic scope</label>
        <div className="pill-list">
          {topics.map((topic) => {
            const active = selectedTopics.includes(topic.id);
            return (
              <button
                key={topic.id}
                type="button"
                className={`chip ${active ? "active" : ""}`}
                onClick={() => setSelectedTopics((cur) => (active ? cur.filter((id) => id !== topic.id) : [...cur, topic.id]))}
              >
                {topic.name} <span className="tiny" style={{ opacity: 0.6 }}>{topic.question_count}</span>
              </button>
            );
          })}
        </div>
      </div>

      <div className="split" style={{ gap: 10 }}>
        <div className="field">
          <label htmlFor="duration">Duration (minutes)</label>
          <input id="duration" type="number" min={1} max={180} className="input" value={duration} onChange={(e) => setDuration(Number(e.target.value) || 1)} />
        </div>
        <div className="field">
          <label htmlFor="attempts">Attempts allowed</label>
          <input id="attempts" type="number" min={1} max={10} className="input" value={attempts} onChange={(e) => setAttempts(Number(e.target.value) || 1)} />
        </div>
      </div>

      <div className="split" style={{ gap: 10 }}>
        <div className="field">
          <label htmlFor="opens">Opens</label>
          <input id="opens" type="datetime-local" className="input" value={opensAt} onChange={(e) => setOpensAt(e.target.value)} />
        </div>
        <div className="field">
          <label htmlFor="closes">Closes</label>
          <input id="closes" type="datetime-local" className="input" value={closesAt} onChange={(e) => setClosesAt(e.target.value)} />
        </div>
      </div>

      <div className="field">
        <label htmlFor="release">When can learners see results?</label>
        <select id="release" className="select" value={release} onChange={(e) => setRelease(e.target.value as "immediate" | "on_close")}>
          <option value="immediate">Immediately after submitting</option>
          <option value="on_close">When the assessment window closes</option>
        </select>
      </div>

      <button className="btn sm" type="submit" disabled={busy}>{busy ? "Publishing…" : "Publish assessment"}</button>
    </form>
  );
}
