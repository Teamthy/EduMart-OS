import type { Metadata } from "next";
import Link from "next/link";
import { WifiOff } from "lucide-react";

import { Button } from "@/components/ui/button";

export const metadata: Metadata = { title: "Offline" };

export default function OfflinePage() {
  return (
    <main id="main" className="container-page flex min-h-dvh max-w-lg flex-col items-center justify-center gap-4 text-center">
      <WifiOff aria-hidden="true" className="size-10 text-muted-foreground" />
      <h1 className="text-2xl">You&apos;re offline</h1>
      <p className="text-sm text-muted-foreground">
        Edumart needs a connection to load questions, because the clock and scoring live on the
        server. Any answers already saved are safe — reopen this page once you&apos;re back online.
      </p>
      <Button asChild variant="outline">
        <Link href="/dashboard">Try again</Link>
      </Button>
    </main>
  );
}
