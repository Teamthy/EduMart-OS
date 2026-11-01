"use client";

import { useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import { Loader2 } from "lucide-react";

import { Alert } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Field } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select } from "@/components/ui/select";

interface Subject {
  id: string;
  name: string;
}

/**
 * Schedules a sitting.
 *
 * The paper is either drawn from a subject (a mock, built in one step) or named question by question (a class
 * test, reproducible). Both are the same thing to the server: a definition with a window, a duration and an
 * immutable snapshot per candidate.
 */
export function ExamForm() {
  const router = useRouter();
  const [title, setTitle] = useState("");
  const [subjectId, setSubjectId] = useState("");
  const [count, setCount] = useState(20);
  const [durationMinutes, setDurationMinutes] = useState(45);
  const [startsAt, setStartsAt] = useState(() => new Date(Date.now() + 3_600_000).toISOString().slice(0, 16));
  const [windowMinutes, setWindowMinutes] = useState(60);
  const [shuffle, setShuffle] = useState(true);
  const [subjects, setSubjects] = useState<Subject[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [created, setCreated] = useState<{ url: string } | null>(null);

  useEffect(() => {
    void (async () => {
      try {
        const response = await fetch("/v1/subjects", { headers: { Accept: "application/json" } });
        const payload = await response.json();
        if (response.ok) setSubjects((payload.subjects ?? []) as Subject[]);
      } catch {
        // A missing subject list is not fatal: the form still explains itself, the request will say why.
      }
    })();
  }, []);

  async function submit(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setBusy(true);
    setError(null);
    setCreated(null);
    try {
      const start = new Date(startsAt);
      const response = await fetch("/v1/exams", {
        method: "POST",
        headers: { "Content-Type": "application/json", Accept: "application/json" },
        body: JSON.stringify({
          title,
          subjectId: subjectId || undefined,
          questionCount: count,
          durationMinutes,
          startsAt: start.toISOString(),
          endsAt: new Date(start.getTime() + windowMinutes * 60_000).toISOString(),
          shuffle,
        }),
      });
      const payload = await response.json();
      if (!response.ok) {
        setError(payload?.error?.message ?? "Could not schedule the sitting.");
        return;
      }
      setCreated({ url: payload.exam.url as string });
      setTitle("");
      router.refresh();
    } catch {
      setError("The API is unreachable. Please try again.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <form onSubmit={submit} className="space-y-4">
      {error ? <Alert tone="error" title="Could not schedule">{error}</Alert> : null}
      {created ? (
        <Alert tone="success" title="Sitting published">
          Share this link: <span className="font-mono break-all">{created.url}</span>
        </Alert>
      ) : null}

      <Field id="exam-title" label="Title">
        <Input
          id="exam-title"
          value={title}
          onChange={(event) => setTitle(event.target.value)}
          placeholder="JSS3 Algebra mock"
          required
          minLength={3}
        />
      </Field>

      <div className="grid gap-3 sm:grid-cols-2">
        <Field id="exam-subject" label="Draw questions from" hint="Approved, published questions only.">
          <Select id="exam-subject" value={subjectId} onChange={(event) => setSubjectId(event.target.value)}>
            <option value="">Choose a subject</option>
            {subjects.map((subject) => (
              <option key={subject.id} value={subject.id}>
                {subject.name}
              </option>
            ))}
          </Select>
        </Field>
        <Field id="exam-count" label="Questions">
          <Input
            id="exam-count"
            type="number"
            min={1}
            max={200}
            value={count}
            onChange={(event) => setCount(Number(event.target.value))}
          />
        </Field>
      </div>

      <div className="grid gap-3 sm:grid-cols-3">
        <Field id="exam-start" label="The sitting opens">
          <Input
            id="exam-start"
            type="datetime-local"
            value={startsAt}
            onChange={(event) => setStartsAt(event.target.value)}
            required
          />
        </Field>
        <Field id="exam-window" label="Window (minutes)" hint="How long candidates may start.">
          <Input
            id="exam-window"
            type="number"
            min={5}
            value={windowMinutes}
            onChange={(event) => setWindowMinutes(Number(event.target.value))}
          />
        </Field>
        <Field id="exam-duration" label="Each attempt (minutes)">
          <Input
            id="exam-duration"
            type="number"
            min={5}
            max={300}
            value={durationMinutes}
            onChange={(event) => setDurationMinutes(Number(event.target.value))}
          />
        </Field>
      </div>

      <div className="flex items-center gap-2">
        <Checkbox id="exam-shuffle" checked={shuffle} onCheckedChange={(value) => setShuffle(value === true)} />
        <Label htmlFor="exam-shuffle" className="text-sm">
          Give every candidate the same questions in a different order
        </Label>
      </div>

      <p className="text-xs text-muted-foreground">
        The window wins over the duration: an attempt that starts late ends when the window closes, and the
        deadline the server stores says so. An attempt that is not submitted is scored automatically at the
        deadline.
      </p>

      <Button type="submit" disabled={busy || title.trim().length < 3}>
        {busy ? <Loader2 aria-hidden="true" className="animate-spin" /> : null}
        {busy ? "Publishing…" : "Publish sitting"}
      </Button>
    </form>
  );
}
