import type { Metadata } from "next";
import Link from "next/link";
import { ShieldAlert } from "lucide-react";

import { Button } from "@/components/ui/button";

export const metadata: Metadata = { title: "No access" };

export default function PermissionDeniedPage() {
  return (
    <main id="main" className="container-page flex min-h-dvh max-w-lg flex-col items-center justify-center gap-4 text-center">
      <ShieldAlert aria-hidden="true" className="size-10 text-muted-foreground" />
      <h1 className="text-2xl">You don&apos;t have access to that page</h1>
      <p className="text-sm text-muted-foreground">
        Your account doesn&apos;t include this permission. If you think this is a mistake, ask your
        teacher or an administrator to check your role — roles are managed on the server, not here.
      </p>
      <div className="flex gap-2">
        <Button asChild>
          <Link href="/dashboard">Back to dashboard</Link>
        </Button>
        <Button asChild variant="outline">
          <Link href="/support">Contact support</Link>
        </Button>
      </div>
    </main>
  );
}
