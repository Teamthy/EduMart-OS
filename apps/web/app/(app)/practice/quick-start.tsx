"use client";

import { useRouter } from "next/navigation";
import { useState, useTransition } from "react";
import { Loader2, Shuffle } from "lucide-react";

import { ApiError, type Pathway } from "@edumart/shared";

import { Alert } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { api } from "@/lib/browser-api";
import { cn } from "@/lib/utils";

type Mode = "practice" | "simulation";

interface ClassOption {
  id: string;
  name: string;
}

/**
 * Start a set without browsing a subject first.
 *
 * The three sources offered here are the three the API supports beyond "one subject": a whole exam body, one of
 * the learner's classes, or everything mixed together. Each is a distinct choice rather than a filter flag,
 * because "mixed" and "I forgot to pick a subject" must not look the same to the person tapping.
 */
export function QuickStart({ pathways, classes }: { pathways: Pathway[]; classes: ClassOption[] }) {
  const router = useRouter();
  const [pending, startTransition] = useTransition();
  const [mode, setMode] = useState<Mode>("practice");
  const [duration, setDuration] = useState(45);
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  async function begin(source: { mixed?: boolean; examBody?: string; classId?: string }, label: string) {
    setBusy(label);
    setError(null);
    try {
      const { session } = await api().startPractice({
        mode,
        count: mode === "simulation" ? 40 : 10,
        ...(mode === "simulation" ? { durationMinutes: duration } : {}),
        ...source,
      });
      startTransition(() => router.push(`/attempt/${session.session_id}`));
    } catch (caught) {
      setError(caught instanceof ApiError ? caught.message : "That set could not be started.");
      setBusy(null);
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2 text-base">
          <Shuffle aria-hidden="true" className="size-4" />
          Build a paper
        </CardTitle>
      </CardHeader>
      <CardContent className="space-y-4">
        {error ? <Alert tone="error">{error}</Alert> : null}

        <div className="flex flex-wrap gap-2">
          {(["practice", "simulation"] as const).map((option) => (
            <button
              key={option}
              type="button"
              aria-pressed={mode === option}
              onClick={() => setMode(option)}
              className={cn(
                "min-h-11 rounded-lg border px-4 text-sm font-semibold",
                mode === option
                  ? "border-primary bg-secondary text-secondary-foreground"
                  : "border-border bg-card text-muted-foreground",
              )}
            >
              {option === "practice" ? "Real practice" : "Exam simulation"}
            </button>
          ))}
          {mode === "simulation" ? (
            <label className="flex items-center gap-2 text-sm">
              <span className="text-muted-foreground">for</span>
              <select
                value={duration}
                onChange={(event) => setDuration(Number(event.target.value))}
                className="min-h-11 rounded-lg border border-border bg-card px-2 text-sm"
                aria-label="Simulation length"
              >
                {[30, 45, 60, 90, 120].map((minutes) => (
                  <option key={minutes} value={minutes}>
                    {minutes} min
                  </option>
                ))}
              </select>
            </label>
          ) : null}
        </div>

        <div className="space-y-2">
          <p className="text-sm font-medium">By exam body</p>
          <div className="flex flex-wrap gap-2">
            {pathways.map((pathway) => (
              <Button
                key={pathway.id}
                type="button"
                variant="outline"
                size="sm"
                disabled={Boolean(busy) || pending}
                onClick={() => void begin({ examBody: pathway.key }, pathway.key)}
              >
                {busy === pathway.key ? <Loader2 aria-hidden="true" className="animate-spin" /> : null}
                {pathway.name}
              </Button>
            ))}
          </div>
          <p className="text-xs text-muted-foreground">
            A WAEC set can only contain questions from WAEC subjects, so the paper matches the exam it is preparing
            you for.
          </p>
        </div>

        {classes.length > 0 ? (
          <div className="space-y-2">
            <p className="text-sm font-medium">From a class</p>
            <div className="flex flex-wrap gap-2">
              {classes.map((room) => (
                <Button
                  key={room.id}
                  type="button"
                  variant="outline"
                  size="sm"
                  disabled={Boolean(busy) || pending}
                  onClick={() => void begin({ classId: room.id }, room.id)}
                >
                  {busy === room.id ? <Loader2 aria-hidden="true" className="animate-spin" /> : null}
                  {room.name}
                </Button>
              ))}
            </div>
            <p className="text-xs text-muted-foreground">
              A class set is drawn from the questions your class&apos;s published assessments already use.
            </p>
          </div>
        ) : null}

        <div className="space-y-2">
          <p className="text-sm font-medium">Mixed</p>
          <Button
            type="button"
            disabled={Boolean(busy) || pending}
            onClick={() => void begin({ mixed: true }, "mixed")}
          >
            {busy === "mixed" ? <Loader2 aria-hidden="true" className="animate-spin" /> : null}
            Surprise me · {mode === "simulation" ? "40 questions" : "10 questions"}
          </Button>
          <p className="text-xs text-muted-foreground">
            Every approved question is in scope. Good for finding a weak spot you had not noticed.
          </p>
        </div>
      </CardContent>
    </Card>
  );
}
