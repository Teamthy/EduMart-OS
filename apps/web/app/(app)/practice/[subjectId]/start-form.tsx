"use client";

import { useRouter } from "next/navigation";
import { useState, useTransition } from "react";
import { Loader2 } from "lucide-react";

import { ApiError, type Subject, type Topic } from "@edumart/shared";

import { Alert } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { Select } from "@/components/ui/select";
import { api } from "@/lib/browser-api";
import { cn } from "@/lib/utils";

const COUNTS = [5, 10, 20, 30];

export function StartPracticeForm({
  subject,
  topics,
  initialTopicId,
}: {
  subject: Subject;
  topics: Topic[];
  /** Set when the learner arrived from a search result for a specific topic. */
  initialTopicId?: string;
}) {
  const router = useRouter();
  const [pending, startTransition] = useTransition();
  const [count, setCount] = useState(10);
  const [difficulty, setDifficulty] = useState("all");
  const [selectedTopics, setSelectedTopics] = useState<string[]>(() =>
    // A search result for a topic opened this form; ticking it here is the whole point of the deep link.
    initialTopicId && topics.some((topic) => topic.id === initialTopicId) ? [initialTopicId] : [],
  );
  const [error, setError] = useState<ApiError | null>(null);
  const [starting, setStarting] = useState(false);

  const toggleTopic = (id: string) =>
    setSelectedTopics((current) => (current.includes(id) ? current.filter((item) => item !== id) : [...current, id]));

  async function start() {
    setError(null);
    setStarting(true);
    try {
      const { session } = await api().startPractice({
        subjectId: subject.id,
        topicIds: selectedTopics,
        count,
        difficulty,
      });
      startTransition(() => router.push(`/attempt/${session.session_id}`));
    } catch (caught) {
      setError(caught instanceof ApiError ? caught : null);
      setStarting(false);
    }
  }

  return (
    <div className="space-y-5">
      {error ? (
        <Alert tone="error" title="We couldn't start that set">
          {error.message}
        </Alert>
      ) : null}

      <fieldset className="space-y-2">
        <legend className="text-sm font-medium">How many questions?</legend>
        <div className="flex flex-wrap gap-2">
          {COUNTS.map((option) => (
            <button
              key={option}
              type="button"
              onClick={() => setCount(option)}
              aria-pressed={count === option}
              className={cn(
                "min-h-11 rounded-lg border px-4 text-sm font-semibold",
                count === option ? "border-primary bg-secondary text-secondary-foreground" : "border-border bg-card text-muted-foreground",
              )}
            >
              {option}
            </button>
          ))}
        </div>
      </fieldset>

      <div className="space-y-2">
        <Label htmlFor="difficulty">Difficulty</Label>
        <Select id="difficulty" value={difficulty} onChange={(event) => setDifficulty(event.target.value)}>
          <option value="all">Mixed (recommended)</option>
          <option value="easy">Easy — build confidence</option>
          <option value="medium">Medium</option>
          <option value="hard">Hard — exam-level</option>
        </Select>
        <p className="text-xs text-muted-foreground">
          If your choice has too few reviewed questions, the set will be smaller rather than padded.
        </p>
      </div>

      <fieldset className="space-y-2">
        <legend className="text-sm font-medium">Focus topics (optional)</legend>
        <div className="grid gap-2 sm:grid-cols-2">
          {topics.map((topic) => {
            const active = selectedTopics.includes(topic.id);
            return (
              <label
                key={topic.id}
                className={cn(
                  "flex min-h-11 cursor-pointer items-center gap-3 rounded-lg border px-3 text-sm",
                  active ? "border-primary bg-secondary" : "border-border bg-card",
                )}
              >
                <input
                  type="checkbox"
                  checked={active}
                  onChange={() => toggleTopic(topic.id)}
                  className="size-4 accent-[#0d5c46]"
                />
                <span className="min-w-0 truncate">{topic.name}</span>
                <span className="ml-auto shrink-0 text-xs text-muted-foreground">{topic.questionCount}</span>
              </label>
            );
          })}
        </div>
      </fieldset>

      <Button onClick={start} disabled={starting || pending} size="lg" className="w-full">
        {starting || pending ? <Loader2 aria-hidden="true" className="animate-spin" /> : null}
        {starting ? "Building your set…" : `Start ${count} questions`}
      </Button>
      <p className="text-center text-xs text-muted-foreground">
        The server starts the clock when the set opens. Practice sets are not timed for scoring,
        but close automatically after four hours so an abandoned tab can&apos;t linger.
      </p>
    </div>
  );
}
