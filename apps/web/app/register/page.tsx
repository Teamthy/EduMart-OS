import type { Metadata } from "next";
import { redirect } from "next/navigation";

import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { readDraft } from "@/lib/signup-draft";
import { startSignupFromForm } from "@/lib/signup-actions";

export const metadata: Metadata = { title: "Create your account" };

export default async function RegisterEntryPage({
  searchParams,
}: {
  searchParams: Promise<{ ref?: string }>;
}) {
  const [draft, params] = await Promise.all([readDraft(), searchParams]);

  // Resuming is the default: an in-progress draft always wins over starting again.
  if (draft.draft && draft.step) redirect(`/register/${draft.step}`);

  const referralCode = (params.ref ?? "").toUpperCase();

  return (
    <div className="space-y-6">
      <div className="space-y-2">
        <h1 className="text-2xl">Five short steps to your study plan</h1>
        <p className="text-sm text-muted-foreground">
          About two minutes. We ask for your exam, subjects and schedule first, then create your
          account — so nothing you enter is lost if you get interrupted.
        </p>
      </div>

      <Card>
        <CardContent className="space-y-4 p-5">
          <ol className="space-y-2 text-sm">
            {[
              "Which exam you're preparing for",
              "The subjects you'll sit",
              "Your exam date and daily study time",
              "Your name, email and password",
              "A plan you can start immediately",
            ].map((item, index) => (
              <li key={item} className="flex gap-3">
                <span className="grid size-6 shrink-0 place-items-center rounded-full bg-secondary text-xs font-bold text-secondary-foreground">
                  {index + 1}
                </span>
                <span>{item}</span>
              </li>
            ))}
          </ol>

          {referralCode ? (
            <p className="rounded-lg bg-secondary p-3 text-sm text-secondary-foreground">
              You were invited with code <strong>{referralCode}</strong>. It&apos;ll be credited when you sign up.
            </p>
          ) : null}

          <form action={startSignupFromForm}>
            <input type="hidden" name="referralCode" value={referralCode} />
            <Button type="submit" className="w-full" size="lg">
              Start
            </Button>
          </form>

          <p className="text-xs text-muted-foreground">
            Already started on your phone? Open the app and continue — your progress is stored on our server.
          </p>
        </CardContent>
      </Card>
    </div>
  );
}
