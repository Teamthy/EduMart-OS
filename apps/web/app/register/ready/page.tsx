import Link from "next/link";
import { redirect } from "next/navigation";
import { ArrowRight, CalendarClock, CheckCircle2, Sparkles } from "lucide-react";

import { SIGNUP_STEPS, daysUntil, formatDate } from "@edumart/shared";

import { SignupStepper } from "@/components/signup/stepper";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { getCurrentUser } from "@/lib/session";

export default async function ReadyStep() {
  // The account exists by now; if it does not, the wizard sent the learner here by mistake.
  const user = await getCurrentUser();
  if (!user) redirect("/register");
  const step = SIGNUP_STEPS[4]!;
  const days = daysUntil(user.targetDate);

  return (
    <div>
      <SignupStepper current="confirm" />
      <div className="mb-6 flex items-start gap-3">
        <CheckCircle2 aria-hidden="true" className="mt-1 size-7 text-success" />
        <div>
          <h1 className="text-2xl">{step.title}, {user.displayName.split(" ")[0]}</h1>
          <p className="text-sm text-muted-foreground">{step.description}</p>
        </div>
      </div>

      <Card className="mb-6">
        <CardContent className="grid gap-4 p-5 sm:grid-cols-3">
          <div className="space-y-1">
            <p className="text-xs uppercase tracking-wide text-muted-foreground">Exam date</p>
            <p className="font-semibold">{formatDate(user.targetDate)}</p>
            <p className="text-xs text-muted-foreground">{days !== null ? `${days} days to prepare` : "Set a date in Settings"}</p>
          </div>
          <div className="space-y-1">
            <p className="text-xs uppercase tracking-wide text-muted-foreground">Daily commitment</p>
            <p className="font-semibold">{user.dailyMinutes} minutes</p>
            <p className="text-xs text-muted-foreground">Adjust any time</p>
          </div>
          <div className="space-y-1">
            <p className="text-xs uppercase tracking-wide text-muted-foreground">Subjects</p>
            <p className="font-semibold">{user.subjectIds.length} selected</p>
            <p className="text-xs text-muted-foreground">We&apos;ll start with the weakest topics</p>
          </div>
        </CardContent>
      </Card>

      <div className="space-y-3 rounded-xl border border-border bg-card p-5">
        <p className="flex items-center gap-2 text-sm font-semibold">
          <Sparkles aria-hidden="true" className="size-4 text-primary" />
          Your first session is ready
        </p>
        <p className="text-sm text-muted-foreground">
          A 10-question practice set calibrates your plan. It takes about eight minutes and every
          answer saves as you tap it.
        </p>
        <div className="flex flex-wrap gap-2">
          <Button asChild size="lg">
            <Link href="/practice?start=1">
              Start practice <ArrowRight aria-hidden="true" />
            </Link>
          </Button>
          <Button asChild variant="outline" size="lg">
            <Link href="/dashboard">
              <CalendarClock aria-hidden="true" />
              Go to dashboard
            </Link>
          </Button>
        </div>
      </div>
    </div>
  );
}
