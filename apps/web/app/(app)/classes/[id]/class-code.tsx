"use client";

import { useRouter } from "next/navigation";
import { useState } from "react";
import { Loader2, RefreshCw } from "lucide-react";

import { ApiError, formatRelativeDate, type ClassSummary } from "@edumart/shared";

import { Alert } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { api } from "@/lib/browser-api";

/**
 * The class code, its expiry, and the button that revokes it.
 *
 * A code is a shared secret someone read aloud once. Rotating it is the only way to take that back, so
 * the control sits next to the code rather than buried in settings — and it says plainly that the old
 * code dies immediately, because a teacher who assumes both keep working will lock out the class.
 */
export function ClassCode({ classroom }: { classroom: ClassSummary }) {
  const router = useRouter();
  const [current, setCurrent] = useState(classroom);
  const [working, setWorking] = useState(false);
  const [error, setError] = useState<ApiError | null>(null);

  async function rotate() {
    setWorking(true);
    setError(null);
    try {
      const result = await api().rotateClassJoinCode(current.id);
      setCurrent({ ...current, ...result.class, members: current.members, assignments: current.assignments });
      router.refresh();
    } catch (caught) {
      setError(caught instanceof ApiError ? caught : null);
    } finally {
      setWorking(false);
    }
  }

  return (
    <section aria-labelledby={`code-${current.id}`} className="space-y-2">
      <h2 id={`code-${current.id}`} className="text-sm font-semibold">
        Class code
      </h2>
      <div className="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-border px-3 py-2">
        <div>
          <p className="font-mono text-lg tracking-[0.3em]">{current.joinCode}</p>
          <p className="text-xs text-muted-foreground">
            {current.joinCodeExpiresAt
              ? `Valid until ${formatRelativeDate(current.joinCodeExpiresAt)}`
              : "No expiry on this code — issue a new one to set a window."}
          </p>
        </div>
        <Button type="button" variant="secondary" size="sm" onClick={rotate} disabled={working}>
          {working ? (
            <Loader2 className="mr-2 size-4 animate-spin" aria-hidden />
          ) : (
            <RefreshCw className="mr-2 size-4" aria-hidden />
          )}
          Issue a new code
        </Button>
      </div>
      <p className="text-xs text-muted-foreground">
        A new code replaces this one immediately: the old code stops working for everyone, including
        learners who have not joined yet. It is valid for 30 days.
      </p>
      {error ? (
        <Alert tone="error" title="That didn't work">
          {error.message}
        </Alert>
      ) : null}
    </section>
  );
}
