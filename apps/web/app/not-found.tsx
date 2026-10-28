import Link from "next/link";

import { Button } from "@/components/ui/button";

export default function NotFound() {
  return (
    <main id="main" className="container-page flex min-h-dvh max-w-lg flex-col items-center justify-center gap-4 text-center">
      <p className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">404</p>
      <h1 className="text-2xl">We couldn&apos;t find that page</h1>
      <p className="text-sm text-muted-foreground">
        The link may be old, or the item may belong to another account.
      </p>
      <Button asChild>
        <Link href="/dashboard">Go to dashboard</Link>
      </Button>
    </main>
  );
}
