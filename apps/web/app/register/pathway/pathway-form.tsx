"use client";

import { useActionState, useState } from "react";
import { Loader2 } from "lucide-react";

import type { Pathway, SignupDraft } from "@edumart/shared";

import { Alert } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { savePathway } from "@/lib/signup-actions";
import { EMPTY_STATE } from "@/lib/signup-state";
import { cn } from "@/lib/utils";

export function PathwayForm({ pathways, draft }: { pathways: Pathway[]; draft: SignupDraft }) {
  const [state, formAction, pending] = useActionState(savePathway, EMPTY_STATE);
  const [selected, setSelected] = useState(draft.examPathwayId);
  const selectedPathway = pathways.find((pathway) => pathway.id === selected);

  return (
    <form action={formAction} className="space-y-4">
      <input type="hidden" name="examPathwayId" value={selected} />
      <input type="hidden" name="examPathwayKey" value={selectedPathway?.key ?? ""} />

      {state.message ? <Alert tone="error" title="Could not save this step">{state.message}</Alert> : null}

      <fieldset className="space-y-2">
        <legend className="sr-only">Exam pathway</legend>
        {pathways.map((pathway) => {
          const active = selected === pathway.id;
          return (
            <label
              key={pathway.id}
              className={cn(
                "flex cursor-pointer items-start gap-3 rounded-xl border p-4 transition-colors",
                active ? "border-primary bg-secondary" : "border-border bg-card hover:border-input",
              )}
            >
              <input
                type="radio"
                name="pathwayChoice"
                value={pathway.id}
                checked={active}
                onChange={() => setSelected(pathway.id)}
                className="mt-1 size-4 accent-[#0d5c46]"
              />
              <span className="space-y-1">
                <span className="block font-semibold">{pathway.name}</span>
                <span className="block text-sm text-muted-foreground">{pathway.blurb || pathway.body}</span>
                <span className="block text-xs text-muted-foreground">{pathway.subjects} subjects available</span>
              </span>
            </label>
          );
        })}
      </fieldset>

      {state.errors.examPathwayId ? (
        <p role="alert" className="text-sm font-medium text-destructive">
          {state.errors.examPathwayId}
        </p>
      ) : null}

      <div className="flex items-center justify-between gap-3 pt-2">
        <p className="text-xs text-muted-foreground">You can change this later in Settings.</p>
        <Button type="submit" disabled={pending || !selected}>
          {pending ? <Loader2 aria-hidden="true" className="animate-spin" /> : null}
          {pending ? "Saving…" : "Continue"}
        </Button>
      </div>
    </form>
  );
}
