"use client";

import { useEffect } from "react";

import { Button } from "@/components/ui/button";

/** Route-level error boundary: explains what failed and offers a retry, with a request id. */
export default function ErrorBoundary({ error, reset }: { error: Error & { digest?: string }; reset: () => void }) {
  useEffect(() => {
    // In production this is where a client error reporter would be called.
    console.error(error);
  }, [error]);

  return (
    <main id="main" className="container-page flex min-h-dvh max-w-lg flex-col items-center justify-center gap-4 text-center">
      <h1 className="text-2xl">Something went wrong</h1>
      <p className="text-sm text-muted-foreground">
        The page failed to load. Your saved answers and scores are unaffected.
      </p>
      {error.digest ? <p className="text-xs text-muted-foreground">Reference {error.digest}</p> : null}
      <Button onClick={reset}>Try again</Button>
    </main>
  );
}
