"use client";

import { useRouter } from "next/navigation";
import { useState } from "react";
import { BadgeCheck, Loader2, ShieldQuestion, UserPlus } from "lucide-react";

import {
  ApiError,
  type ClassSummary,
  type Invitation,
  type Organization,
  type OrganizationMember,
} from "@edumart/shared";

import { Alert } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Field } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { api } from "@/lib/browser-api";

const ROLE_LABEL: Record<string, string> = {
  owner: "Owner",
  admin: "Administrator",
  teacher: "Teacher",
  student: "Student",
};

/**
 * One institution: its verification state, its roster, and the invitation form.
 *
 * The invitation is emailed to the invitee by the API's delivery queue, and the token is also shown
 * once, on creation, for the case where mail is broken or the person is standing next to you.
 */
export function InstitutionCard({
  organization,
  members,
  classes,
  canAdminister,
}: {
  organization: Organization;
  members: OrganizationMember[];
  classes: ClassSummary[];
  canAdminister: boolean;
}) {
  const router = useRouter();
  const [email, setEmail] = useState("");
  const [role, setRole] = useState<Invitation["role"]>("teacher");
  const [inviting, setInviting] = useState(false);
  const [issued, setIssued] = useState<{ email: string; token: string } | null>(null);
  const [removing, setRemoving] = useState<string | null>(null);
  const [error, setError] = useState<ApiError | null>(null);

  async function invite(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setInviting(true);
    setError(null);
    setIssued(null);
    try {
      const result = await api().inviteToOrganization(organization.id, { email: email.trim(), role });
      setIssued({ email: result.invitation.email, token: result.token });
      setEmail("");
      router.refresh();
    } catch (caught) {
      setError(caught instanceof ApiError ? caught : null);
    } finally {
      setInviting(false);
    }
  }

  async function remove(member: OrganizationMember) {
    setRemoving(member.userId);
    setError(null);
    try {
      await api().removeMember(organization.id, member.userId);
      router.refresh();
    } catch (caught) {
      setError(caught instanceof ApiError ? caught : null);
    } finally {
      setRemoving(null);
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex flex-wrap items-center gap-2">
          {organization.verified ? (
            <BadgeCheck className="h-4 w-4 text-primary" aria-hidden />
          ) : (
            <ShieldQuestion className="h-4 w-4 text-muted-foreground" aria-hidden />
          )}
          {organization.name}
          <span className="rounded-full border border-border px-2 py-0.5 text-xs font-medium text-muted-foreground">
            {ROLE_LABEL[organization.role] ?? organization.role}
          </span>
        </CardTitle>
        <CardDescription>
          {organization.verified
            ? `Verified institution · ${organization.members} members · ${organization.classes} classes`
            : "Awaiting approval by a platform administrator. You can still invite your team."}
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-5 text-sm">
        {error ? (
          <Alert tone="error" title="That didn't work">
            {error.message}
          </Alert>
        ) : null}

        {canAdminister ? (
          <section aria-labelledby={`classes-${organization.id}`} className="space-y-2">
            <h2 id={`classes-${organization.id}`} className="text-sm font-semibold">
              Classes
            </h2>
            {classes.length === 0 ? (
              <p className="text-sm text-muted-foreground">
                No classes yet. A class created for this institution by an owner or administrator appears
                here; a teacher&apos;s personal class stays theirs.
              </p>
            ) : (
              <ul className="divide-y divide-border rounded-lg border border-border">
                {classes.map((classroom) => (
                  <li key={classroom.id} className="flex flex-wrap items-center justify-between gap-2 px-3 py-2">
                    <span className="font-medium">{classroom.name}</span>
                    <span className="text-xs text-muted-foreground">
                      {classroom.members} {classroom.members === 1 ? "member" : "members"} ·{" "}
                      {classroom.assignments} {classroom.assignments === 1 ? "assignment" : "assignments"}
                    </span>
                  </li>
                ))}
              </ul>
            )}
          </section>
        ) : null}

        {canAdminister ? (
          <section aria-labelledby={`roster-${organization.id}`} className="space-y-2">
            <h2 id={`roster-${organization.id}`} className="text-sm font-semibold">
              Members
            </h2>
            <ul className="divide-y divide-border rounded-lg border border-border">
              {members.map((member) => (
                <li key={member.userId} className="flex flex-wrap items-center justify-between gap-2 px-3 py-2">
                  <span>
                    <span className="font-medium">{member.displayName}</span>{" "}
                    <span className="text-muted-foreground">{member.email}</span>
                  </span>
                  <span className="flex items-center gap-2">
                    <span className="text-xs text-muted-foreground">{ROLE_LABEL[member.role] ?? member.role}</span>
                    {member.role !== "owner" ? (
                      <Button
                        type="button"
                        variant="ghost"
                        size="sm"
                        disabled={removing === member.userId}
                        onClick={() => remove(member)}
                      >
                        {removing === member.userId ? "Removing…" : "Remove"}
                      </Button>
                    ) : null}
                  </span>
                </li>
              ))}
            </ul>
            <p className="text-xs text-muted-foreground">
              Removing a teacher also withdraws the teaching role when no other organisation grants it.
            </p>
          </section>
        ) : null}

        {canAdminister ? (
          <section aria-labelledby={`invite-${organization.id}`} className="space-y-3">
            <h2 id={`invite-${organization.id}`} className="flex items-center gap-2 text-sm font-semibold">
              <UserPlus className="h-4 w-4" aria-hidden /> Invite someone
            </h2>
            <form onSubmit={invite} className="flex flex-wrap items-end gap-2">
              <div className="min-w-[220px] flex-1">
                <Field id={`email-${organization.id}`} label="Email address" hint="The invitation only works for this address.">
                  <Input
                    id={`email-${organization.id}`}
                    type="email"
                    required
                    value={email}
                    onChange={(event) => setEmail(event.target.value)}
                    placeholder="teacher@school.edu.ng"
                  />
                </Field>
              </div>
              <div className="w-40">
                <Field id={`role-${organization.id}`} label="Role">
                  <Select
                    id={`role-${organization.id}`}
                    value={role}
                    onChange={(event) => setRole(event.target.value as Invitation["role"])}
                  >
                    <option value="teacher">Teacher</option>
                    <option value="admin">Administrator</option>
                    <option value="student">Student</option>
                  </Select>
                </Field>
              </div>
              <Button type="submit" disabled={inviting || email.trim() === ""}>
                {inviting ? <Loader2 className="mr-2 h-4 w-4 animate-spin" aria-hidden /> : null}
                Create invitation
              </Button>
            </form>

            {issued ? (
              <Alert tone="success" title={`Invitation emailed to ${issued.email}`}>
                <span className="block">
                  If it does not arrive, this is the fallback link. It expires in seven days and works once,
                  for that address only.
                </span>
                <span className="mt-1 block break-all font-mono text-xs">{issued.token}</span>
              </Alert>
            ) : null}
          </section>
        ) : null}
      </CardContent>
    </Card>
  );
}
