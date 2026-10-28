"use client";

import { useRouter } from "next/navigation";
import { useEffect, useState } from "react";
import { AuthFrame } from "@/components/AuthFrame";

type Pathway = { id: string; key: string; name: string; body: string; blurb: string };

/** Deliberately short: only the information needed to personalise learning. */
export default function OnboardingPage() {
  const router = useRouter();
  const [step, setStep] = useState(0);
  const [pathways, setPathways] = useState<Pathway[]>([]);
  const [pathwayId, setPathwayId] = useState("");
  const [targetDate, setTargetDate] = useState("");
  const [dailyMinutes, setDailyMinutes] = useState(30);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    fetch("/api/v1/exam-pathways")
      .then((r) => r.json())
      .then((d) => setPathways(d.pathways ?? []))
      .catch(() => setPathways([]));
  }, []);

  async function finish() {
    setBusy(true);
    setError(null);
    try {
      const res = await fetch("/api/v1/me", {
        method: "PATCH",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({
          examPathwayId: pathwayId || null,
          targetDate: targetDate ? new Date(targetDate).toISOString() : null,
        }),
      });
      if (!res.ok) {
        const data = await res.json().catch(() => null);
        setError(data?.error?.message ?? "Could not save your preferences.");
        return;
      }
      if (typeof window !== "undefined") localStorage.setItem("edumart.dailyMinutes", String(dailyMinutes));
      router.push("/dashboard");
      router.refresh();
    } catch {
      setError("Network problem. Please try again.");
    } finally {
      setBusy(false);
    }
  }

  const steps = [
    {
      title: "What are you preparing for?",
      body: "This shapes the subjects, syllabus and mock exams we recommend.",
      content: (
        <div className="choice-grid">
          {pathways.map((p) => (
            <button
              key={p.id}
              type="button"
              className={`choice ${pathwayId === p.id ? "selected" : ""}`}
              onClick={() => setPathwayId(p.id)}
            >
              <div className="choice-title">{p.name}</div>
              <div className="choice-sub">{p.body}</div>
            </button>
          ))}
        </div>
      ),
    },
    {
      title: "When is your exam?",
      body: "Optional — it lets us pace your revision and count down on your dashboard.",
      content: (
        <div className="field">
          <label htmlFor="target">Target date</label>
          <input id="target" type="date" className="input" value={targetDate} onChange={(e) => setTargetDate(e.target.value)} />
          <span className="hint">You can change this at any time without losing your history.</span>
        </div>
      ),
    },
    {
      title: "How much time can you study each day?",
      body: "We use this to size your daily practice goal.",
      content: (
        <div className="choice-grid">
          {[15, 30, 45, 60, 90].map((minutes) => (
            <button key={minutes} type="button" className={`choice ${dailyMinutes === minutes ? "selected" : ""}`} onClick={() => setDailyMinutes(minutes)}>
              <div className="choice-title">{minutes} minutes</div>
              <div className="choice-sub">{minutes <= 30 ? "Light and steady" : minutes <= 60 ? "Focused" : "Intensive"}</div>
            </button>
          ))}
        </div>
      ),
    },
  ];

  return (
    <AuthFrame title={`Step ${step + 1} of 3`} subtitle={steps[step].title}>
      <div className="progress" aria-hidden><span style={{ width: `${((step + 1) / 3) * 100}%` }} /></div>
      <div className="stack">
        {error ? <div className="banner error"><span className="ico">!</span>{error}</div> : null}
        <p className="small muted">{steps[step].body}</p>
        {steps[step].content}
        <div className="row" style={{ gap: 10 }}>
          {step > 0 ? (
            <button className="btn secondary" type="button" onClick={() => setStep((s) => s - 1)}>Back</button>
          ) : null}
          {step < 2 ? (
            <button className="btn" type="button" onClick={() => setStep((s) => s + 1)} disabled={step === 0 && !pathwayId}>
              Continue
            </button>
          ) : (
            <button className="btn" type="button" onClick={finish} disabled={busy}>
              {busy ? "Saving…" : "Go to my dashboard"}
            </button>
          )}
          <button className="btn ghost sm" type="button" onClick={finish} style={{ marginLeft: "auto" }}>Skip for now</button>
        </div>
      </div>
    </AuthFrame>
  );
}
