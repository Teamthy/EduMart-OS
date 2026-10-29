import type { Metadata } from "next";
import Link from "next/link";

import { serverFetch } from "@/lib/server-api";
import { requireUser } from "@/lib/session";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { SettingsForm } from "./settings-form";
import { VerificationCard } from "./verification-card";
import type { Pathway, Subject } from "@edumart/shared";

export const metadata: Metadata = { title: "Settings" };
export const dynamic = "force-dynamic";

export default async function SettingsPage() {
  const user = await requireUser("/settings");
  const [{ subjects }, { pathways }] = await Promise.all([
    serverFetch<{ subjects: Subject[] }>("/v1/subjects"),
    serverFetch<{ pathways: Pathway[] }>("/v1/exam-pathways"),
  ]);

  return (
    <div className="space-y-6">
      <header className="space-y-1">
        <h1 className="text-2xl">Settings</h1>
        <p className="text-sm text-muted-foreground">Signed in as {user.email}</p>
      </header>

      <VerificationCard user={user} />

      <SettingsForm user={user} subjects={subjects} pathways={pathways} />

      <Card>
        <CardHeader>
          <CardTitle>Account</CardTitle>
          <CardDescription>Sessions, data and support.</CardDescription>
        </CardHeader>
        <CardContent className="space-y-3 text-sm">
          <p className="text-muted-foreground">
            Signing out revokes this session on the server, not just in this browser.
          </p>
          <form action="/api/session/logout" method="post">
            <button
              type="submit"
              className="rounded-lg border border-border px-4 py-2 text-sm font-semibold hover:bg-muted"
            >
              Sign out of this device
            </button>
          </form>
          <p>
            Need help?{" "}
            <Link href="/support" className="font-semibold text-primary underline-offset-4 hover:underline">
              Contact support
            </Link>
          </p>
        </CardContent>
      </Card>
    </div>
  );
}
