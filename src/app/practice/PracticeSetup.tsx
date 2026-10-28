"use client";

import { useMemo, useState } from "react";
import { useRouter } from "next/navigation";

type Subject = { id: string; name: string; icon: string; question_count: number };
type Topic = { id: string; name: string; question_count: number };

export function PracticeSetup({
  subjects,
  pathwayName,
  initialSubjectId,
  initialTopics,
}: {
  subjects: Subject[];
  pathwayName: string;
  initialSubjectId: string;
  initialTopics: Topic[];
}) {
  const router = useRouter();
  const [subjectId, setSubjectId] = useState(initialSubjectId);
  const [topics, setTopics] = useState<Topic[]>(initialTopics);
  const [selectedTopics, setSelectedTopics] = useState<string[]>([]);
  const [count, setCount] = useState(10);
  const [difficulty, setDifficulty] = useState("all");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const subject = subjects.find((s) => s.id === subjectId);
  const available = useMemo(() => {
    if (selectedTopics.length === 0) return subject?.question_count ?? 0;
    return topics.filter((t) => selectedTopics.includes(t.id)).reduce((sum, t) => sum + t.question_count, 0);
  }, [selectedTopics, topics, subject]);

  async function changeSubject(id: string) {
    setSubjectId(id);
    setSelectedTopics([]);
    setError(null);
    try {
      const res = await fetch(`/api/v1/subjects/${id}/topics`);
      const data = await res.json();
      setTopics(data.topics ?? []);
    } catch {
      setTopics([]);
    }
  }

  async function start() {
    setBusy(true);
    setError(null);
    try {
      const res = await fetch("/api/v1/practice-sessions", {
        method: "POST",
        headers: {
          "content-type": "application/json",
          "idempotency-key": `practice-${Date.now()}-${Math.random().toString(36).slice(2)}`,
        },
        body: JSON.stringify({
          subjectId,
          topicIds: selectedTopics,
          count,
          difficulty,
        }),
      });
      const data = await res.json();
      if (!res.ok) {
        setError(data?.error?.message ?? "Could not start this session.");
        return;
      }
      router.push(`/attempt/${data.session.session_id}`);
    } catch {
      setError("Network problem. Check your connection and try again.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="split" style={{ gridTemplateColumns: "1.5fr 1fr", alignItems: "start" }}>
      <div className="stack">
        <section className="card">
          <div className="card-head">
            <div>
              <div className="card-title">Subject</div>
              <div className="card-sub">{pathwayName}</div>
            </div>
          </div>
          <div className="choice-grid">
            {subjects.map((s) => (
              <button
                key={s.id}
                type="button"
                className={`choice ${subjectId === s.id ? "selected" : ""}`}
                onClick={() => changeSubject(s.id)}
              >
                <div className="choice-title">{s.icon} {s.name}</div>
                <div className="choice-sub">{s.question_count} approved questions</div>
              </button>
            ))}
          </div>
        </section>

        <section className="card">
          <div className="card-head">
            <div>
              <div className="card-title">Focus topics</div>
              <div className="card-sub">Leave empty to mix the whole subject</div>
            </div>
            {selectedTopics.length > 0 ? (
              <button className="btn ghost sm" type="button" onClick={() => setSelectedTopics([])}>Clear</button>
            ) : null}
          </div>
          {topics.length === 0 ? (
            <div className="empty" style={{ padding: 22 }}>
              <span className="small">Loading topics…</span>
            </div>
          ) : (
            <div className="pill-list">
              {topics.map((topic) => {
                const active = selectedTopics.includes(topic.id);
                return (
                  <button
                    key={topic.id}
                    type="button"
                    className={`chip ${active ? "active" : ""}`}
                    aria-pressed={active}
                    onClick={() =>
                      setSelectedTopics((current) =>
                        current.includes(topic.id) ? current.filter((id) => id !== topic.id) : [...current, topic.id],
                      )
                    }
                  >
                    {topic.name} <span className="tiny" style={{ opacity: 0.65 }}>{topic.question_count}</span>
                  </button>
                );
              })}
            </div>
          )}
        </section>

        <section className="card">
          <div className="card-head">
            <div className="card-title">Difficulty</div>
          </div>
          <div className="choice-grid">
            {[
              ["all", "Mixed", "Recommended — mirrors real papers"],
              ["easy", "Easy", "Build confidence first"],
              ["medium", "Medium", "Exam-standard items"],
              ["hard", "Hard", "Stretch topics"],
            ].map(([value, label, hint]) => (
              <button
                key={value}
                type="button"
                className={`choice ${difficulty === value ? "selected" : ""}`}
                onClick={() => setDifficulty(value)}
              >
                <div className="choice-title">{label}</div>
                <div className="choice-sub">{hint}</div>
              </button>
            ))}
          </div>
        </section>
      </div>

      <aside className="stack aside-sticky" style={{ position: "sticky", top: 84 }}>
        <section className="card pad-lg">
          <div className="card-title">Session summary</div>
          <div className="stack-sm" style={{ marginTop: 12 }}>
            <div className="row-between small">
              <span className="muted">Subject</span>
              <span className="strong">{subject?.name}</span>
            </div>
            <div className="row-between small">
              <span className="muted">Topics</span>
              <span className="strong">{selectedTopics.length === 0 ? "All topics" : `${selectedTopics.length} selected`}</span>
            </div>
            <div className="row-between small">
              <span className="muted">Questions available</span>
              <span className="strong">{available}</span>
            </div>
            <div className="field" style={{ marginTop: 6 }}>
              <label htmlFor="count">Questions in this session</label>
              <input
                id="count"
                className="input"
                type="number"
                min={1}
                max={40}
                value={count}
                onChange={(e) => setCount(Math.max(1, Math.min(40, Number(e.target.value) || 1)))}
              />
            </div>
            <div className="banner info tiny" style={{ marginTop: 8 }}>
              <span className="ico">ℹ</span>
              Practice is not timed by default, but your attempt is saved on the server. Explanations appear after you submit.
            </div>
            {error ? <div className="banner error small"><span className="ico">!</span>{error}</div> : null}
            <button className="btn lg block" type="button" onClick={start} disabled={busy || available === 0}>
              {busy ? "Starting…" : "Start practice"}
            </button>
            {available === 0 ? <div className="tiny muted center">No approved questions match this selection yet.</div> : null}
          </div>
        </section>
      </aside>
    </div>
  );
}
