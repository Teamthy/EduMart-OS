"use client";

import Link from "next/link";
import { useActionState, useState } from "react";
import { Loader2 } from "lucide-react";

import type { SignupDraft, Subject } from "@edumart/shared";

import { Alert } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { saveSubjects } from "@/lib/signup-actions";
import { EMPTY_STATE } from "@/lib/signup-state";
import { cn } from "@/lib/utils";

export function SubjectsForm({ subjects, draft }: { subjects: Subject[]; draft: SignupDraft }) {
  const [state, formAction, pending] = useActionState(saveSubjects, EMPTY_STATE);
  const [selected, setSelected] = useState<string[]>(draft.subjectIds);

  const toggle = (id: string) =>
    setSelected((current) => (current.includes(id) ? current.filter((item) => item !== id) : [...current, id]));

  return (
    <form action={formAction} className="space-y-4">
      {selected.map((id) => (
        <input key={id} type="hidden" name="subjectIds" value={id} />
      ))}
      {/* Tells the server that an empty list is a deliberate choice, not a missing field. */}
      <input type="hidden" name="subjectsTouched" value="1" />

      {state.message ? <Alert tone="error" title="Could not save this step">{state.message}</Alert> : null}

      <fieldset>
        <legend className="sr-only">Subjects</legend>
        <div className="grid gap-2 sm:grid-cols-2">
          {subjects.map((subject) => {
            const active = selected.includes(subject.id);
            return (
              <label
                key={subject.id}
                className={cn(
                  "flex min-h-16 cursor-pointer items-center gap-3 rounded-xl border p-3 transition-colors",
                  active ? "border-primary bg-secondary" : "border-border bg-card hover:border-input",
                )}
              >
                <input
                  type="checkbox"
                  checked={active}
                  onChange={() => toggle(subject.id)}
                  className="size-5 accent-[#0d5c46]"
                />
                <span className="min-w-0">
                  <span className="block truncate font-semibold">
                    <span aria-hidden="true" className="mr-1.5 text-muted-foreground">
                      {subject.icon}
                    </span>
                    {subject.name}
                  </span>
                  <span className="block text-xs text-muted-foreground">{subject.questionCount} questions</span>
                </span>
              </label>
            );
          })}
        </div>
      </fieldset>

      {state.errors.subjectIds ? (
        <p role="alert" className="text-sm font-medium text-destructive">
          {state.errors.subjectIds}
        </p>
      ) : null}

      <div className="flex items-center justify-between gap-3 pt-2">
        <Button asChild variant="ghost">
          <Link href="/register/pathway">Back</Link>
        </Button>
        <div className="flex items-center gap-3">
          <p className="text-xs text-muted-foreground">{selected.length} selected</p>
          <Button type="submit" disabled={pending || selected.length === 0}>
            {pending ? <Loader2 aria-hidden="true" className="animate-spin" /> : null}
            {pending ? "Saving…" : "Continue"}
          </Button>
        </div>
      </div>
    </form>
  );
}
