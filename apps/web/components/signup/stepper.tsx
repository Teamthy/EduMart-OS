import { Check } from "lucide-react";

import { SIGNUP_STEPS, type SignupStepId } from "@edumart/shared";
import { cn } from "@/lib/utils";

/**
 * Wizard progress. The current step is conveyed with text as well as colour, so it is legible in
 * greyscale and to screen readers (`aria-current="step"`).
 */
export function SignupStepper({ current }: { current: SignupStepId }) {
  const currentIndex = SIGNUP_STEPS.findIndex((step) => step.id === current);

  return (
    <nav aria-label={`Signup progress: step ${currentIndex + 1} of ${SIGNUP_STEPS.length}`} className="mb-6">
      <ol className="flex items-center gap-1.5">
        {SIGNUP_STEPS.map((step, index) => {
          const complete = index < currentIndex;
          const active = index === currentIndex;
          return (
            <li key={step.id} className="flex-1">
              <div className={cn("h-1.5 rounded-full", complete ? "bg-primary" : active ? "bg-primary/60" : "bg-muted")} />
              <span className="sr-only">
                {step.label}: {complete ? "completed" : active ? "current step" : "not started"}
              </span>
            </li>
          );
        })}
      </ol>
      <div className="mt-2 flex items-center justify-between text-xs">
        <p className="font-semibold text-primary" aria-current="step">
          Step {currentIndex + 1} of {SIGNUP_STEPS.length} · {SIGNUP_STEPS[currentIndex]?.label}
        </p>
        {currentIndex > 0 ? (
          <p className="inline-flex items-center gap-1 text-muted-foreground">
            <Check aria-hidden="true" className="size-3" />
            Progress saved
          </p>
        ) : null}
      </div>
    </nav>
  );
}
