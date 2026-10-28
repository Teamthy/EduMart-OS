import type { Metadata } from "next";

import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { requireUser } from "@/lib/session";
import { SupportForm } from "./support-form";

export const metadata: Metadata = { title: "Support" };

export default async function SupportPage() {
  const user = await requireUser("/support");

  return (
    <div className="space-y-6">
      <header className="space-y-1">
        <h1 className="text-2xl">Support</h1>
        <p className="text-sm text-muted-foreground">
          Tell us what happened. Include the request ID shown in any error message — it lets us find
          the exact server log line.
        </p>
      </header>

      <Card>
        <CardHeader>
          <CardTitle>Open a request</CardTitle>
          <CardDescription>We reply by email to {user.email}.</CardDescription>
        </CardHeader>
        <CardContent>
          <SupportForm />
        </CardContent>
      </Card>
    </div>
  );
}
