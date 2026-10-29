import type { Metadata } from "next";
import Link from "next/link";

import type { ClassSummary, Organization, OrganizationMember } from "@edumart/shared";

import { Alert } from "@/components/ui/alert";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { serverFetch, ServerApiError } from "@/lib/server-api";
import { requireUser } from "@/lib/session";
import { InstitutionCard } from "./institution-card";

export const metadata: Metadata = { title: "Institutions" };
export const dynamic = "force-dynamic";

interface Roster {
  organization: Organization;
  members: OrganizationMember[];
  classes: ClassSummary[];
}

export default async function InstitutionsPage() {
  const user = await requireUser("/institutions");

  let organizations: Organization[] = [];
  let loadError: string | null = null;
  try {
    const body = await serverFetch<{ organizations: Organization[] }>("/v1/organizations");
    organizations = body.organizations;
  } catch (caught) {
    loadError =
      caught instanceof ServerApiError ? caught.message : "We could not load your institutions. Try again.";
  }

  // Rosters and classes are only fetched for the organisations this person administers: asking for the
  // rest would be a request the API is going to refuse, and refusals make a page look broken.
  const administerable = organizations.filter((org) => org.role === "owner" || org.role === "admin");
  const rosters = await Promise.all(
    administerable.map(async (organization) => {
      const [members, classes] = await Promise.all([
        serverFetch<{ members: OrganizationMember[] }>(`/v1/organizations/${organization.id}/members`)
          .then((body) => body.members)
          .catch(() => []),
        serverFetch<{ classes: ClassSummary[] }>(`/v1/organizations/${organization.id}/classes`)
          .then((body) => body.classes)
          .catch(() => []),
      ]);
      return { organization, members, classes } satisfies Roster;
    }),
  );

  return (
    <div className="space-y-6">
      <header className="space-y-1">
        <h1 className="text-2xl">Institutions</h1>
        <p className="text-sm text-muted-foreground">
          Organisations you belong to. Authority here comes from your membership, not from the role you
          picked when you signed up.
        </p>
      </header>

      {loadError ? (
        <Alert tone="error" title="Could not load institutions">
          {loadError}
        </Alert>
      ) : null}

      {organizations.length === 0 && !loadError ? (
        <Card>
          <CardHeader>
            <CardTitle>No organisation yet</CardTitle>
            <CardDescription>
              A school, tutorial centre or NGO you belong to will appear here. Ask an administrator for an
              invitation, or register one below.
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-3 text-sm text-muted-foreground">
            <p>
              {user.emailVerified
                ? "Registering an organisation makes you its owner and requires a platform administrator's approval."
                : "Confirm your email address first — an institution needs a contact address we can actually reach."}
            </p>
            {!user.emailVerified ? (
              <Link href="/settings" className="font-semibold text-primary underline-offset-4 hover:underline">
                Confirm your email in settings
              </Link>
            ) : null}
          </CardContent>
        </Card>
      ) : null}

      {organizations.map((organization) => (
        <InstitutionCard
          key={organization.id}
          organization={organization}
          members={rosters.find((roster) => roster.organization.id === organization.id)?.members ?? []}
          classes={rosters.find((roster) => roster.organization.id === organization.id)?.classes ?? []}
          canAdminister={organization.role === "owner" || organization.role === "admin"}
        />
      ))}
    </div>
  );
}
