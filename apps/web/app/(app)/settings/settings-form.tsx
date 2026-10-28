"use client";

import { useRouter } from "next/navigation";
import { useState } from "react";
import { Check, Loader2 } from "lucide-react";

import {
  ApiError,
  studyLoadAdvice,
  type Pathway,
  type Subject,
  type User,
} from "@edumart/shared";

import { Alert } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Field } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { api } from "@/lib/browser-api";
import { cn } from "@/lib/utils";

export function SettingsForm({ user, subjects, pathways }: { user: User; subjects: Subject[]; pathways: Pathway[] }) {
  const router = useRouter();
  const [displayName, setDisplayName] = useState(user.displayName);
  const [targetDate, setTargetDate] = useState(user.targetDate ?? "");
  const [dailyMinutes, setDailyMinutes] = useState(user.dailyMinutes);
  const [pathwayId, setPathwayId] = useState(user.examPathwayId ?? "");
  const [subjectIds, setSubjectIds] = useState<string[]>(user.subjectIds);
  const [saving, setSaving] = useState(false);
  const [saved, setSaved] = useState(false);
  const [error, setError] = useState<ApiError | null>(null);

  const toggleSubject = (id: string) =>
    setSubjectIds((current) => (current.includes(id) ? current.filter((item) => item !== id) : [...current, id]));

  async function save(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setSaving(true);
    setSaved(false);
    setError(null);
    try {
      await api().updateMe({
        displayName,
        targetDate: targetDate || null,
        dailyMinutes,
        examPathwayId: pathwayId || null,
        subjectIds,
      });
      setSaved(true);
      router.refresh();
    } catch (caught) {
      setError(caught instanceof ApiError ? caught : null);
    } finally {
      setSaving(false);
    }
  }

  return (
    <form onSubmit={save} className="space-y-4">
      {error ? (
        <Alert tone="error" title="We couldn't save your settings">
          {error.message}
        </Alert>
      ) : null}
      {saved ? (
        <Alert tone="success" title="Saved">
          Your settings are stored on the server.
        </Alert>
      ) : null}

      <Card>
        <CardHeader>
          <CardTitle>Your plan</CardTitle>
          <CardDescription>These drive your countdown, reminders and practice mix.</CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <Field id="displayName" label="Name" required>
            <Input id="displayName" value={displayName} onChange={(event) => setDisplayName(event.target.value)} />
          </Field>

          <div className="space-y-2">
            <Field id="pathwayId" label="Exam pathway">
              <Select id="pathwayId" value={pathwayId} onChange={(event) => setPathwayId(event.target.value)}>
                <option value="">Not set</option>
                {pathways.map((pathway) => (
                  <option key={pathway.id} value={pathway.id}>
                    {pathway.name}
                  </option>
                ))}
              </Select>
            </Field>
          </div>

          <Field id="targetDate" label="Exam date">
            <Input
              id="targetDate"
              type="date"
              value={targetDate}
              onChange={(event) => setTargetDate(event.target.value)}
            />
          </Field>

          <Field id="dailyMinutes" label="Daily study minutes" hint={studyLoadAdvice(dailyMinutes)}>
            <Input
              id="dailyMinutes"
              type="number"
              min={10}
              max={240}
              step={5}
              value={dailyMinutes}
              onChange={(event) => setDailyMinutes(Number(event.target.value))}
            />
          </Field>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Subjects</CardTitle>
          <CardDescription>Used to order your practice and highlight weak topics.</CardDescription>
        </CardHeader>
        <CardContent>
          <div className="grid gap-2 sm:grid-cols-2">
            {subjects.map((subject) => {
              const active = subjectIds.includes(subject.id);
              return (
                <label
                  key={subject.id}
                  className={cn(
                    "flex min-h-11 cursor-pointer items-center gap-3 rounded-lg border px-3 text-sm",
                    active ? "border-primary bg-secondary" : "border-border bg-card",
                  )}
                >
                  <input
                    type="checkbox"
                    checked={active}
                    onChange={() => toggleSubject(subject.id)}
                    className="size-4 accent-[#0d5c46]"
                  />
                  <span className="truncate">{subject.name}</span>
                  {active ? <Check aria-hidden="true" className="ml-auto size-4 text-primary" /> : null}
                </label>
              );
            })}
          </div>
        </CardContent>
      </Card>

      <Button type="submit" disabled={saving}>
        {saving ? <Loader2 aria-hidden="true" className="animate-spin" /> : null}
        {saving ? "Saving…" : "Save changes"}
      </Button>
    </form>
  );
}
