"use client";

import { useRouter } from "next/navigation";
import { useState } from "react";
import { Loader2 } from "lucide-react";

import { Alert } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Field } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";

/** Schedules a class. The price is in learning credits; 0 means free. */
export function HostClassForm() {
  const router = useRouter();
  const [title, setTitle] = useState("");
  const [description, setDescription] = useState("");
  const [creditsCost, setCreditsCost] = useState(0);
  const [capacity, setCapacity] = useState(8);
  const [durationMinutes, setDurationMinutes] = useState(60);
  const [startsAt, setStartsAt] = useState(() => new Date(Date.now() + 3_600_000).toISOString().slice(0, 16));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [created, setCreated] = useState<{ slug: string; joinUrl: string } | null>(null);

  async function submit(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setBusy(true);
    setError(null);
    setCreated(null);
    try {
      const response = await fetch("/v1/virtual-classes", {
        method: "POST",
        headers: { "Content-Type": "application/json", Accept: "application/json" },
        body: JSON.stringify({
          title,
          description,
          creditsCost,
          capacity,
          durationMinutes,
          // The input is local time; the API stores UTC. Sending a Date-derived ISO string keeps the
          // conversion in one place instead of hoping the server's zone matches the host's.
          startsAt: new Date(startsAt).toISOString(),
        }),
      });
      const payload = await response.json();
      if (!response.ok) {
        setError(payload?.error?.message ?? "Could not schedule the class.");
        return;
      }
      setCreated({ slug: payload.class.slug, joinUrl: payload.class.joinUrl });
      setTitle("");
      setDescription("");
      router.refresh();
    } catch {
      setError("The API is unreachable. Please try again.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <form onSubmit={submit} className="space-y-4">
      {error ? <Alert tone="error" title="Could not schedule">{error}</Alert> : null}
      {created ? (
        <Alert tone="success" title="Class scheduled">
          Share this link:{" "}
          <span className="font-mono break-all">{created.joinUrl}</span>
        </Alert>
      ) : null}

      <Field id="title" label="Title">
        <Input id="title" value={title} onChange={(event) => setTitle(event.target.value)} placeholder="JSS3 Algebra clinic" required minLength={3} />
      </Field>

      <Field id="description" label="What is it about?" hint="Optional. Learners see this on the invite.">
        <Textarea id="description" value={description} onChange={(event) => setDescription(event.target.value)} rows={3} />
      </Field>

      <div className="grid gap-3 sm:grid-cols-3">
        <Field id="startsAt" label="Starts">
          <Input id="startsAt" type="datetime-local" value={startsAt} onChange={(event) => setStartsAt(event.target.value)} required />
        </Field>
        <Field id="durationMinutes" label="Minutes">
          <Input
            id="durationMinutes"
            type="number"
            min={15}
            max={480}
            value={durationMinutes}
            onChange={(event) => setDurationMinutes(Number(event.target.value))}
          />
        </Field>
        <Field id="capacity" label="Seats" hint="0 means unlimited.">
          <Input id="capacity" type="number" min={0} max={5000} value={capacity} onChange={(event) => setCapacity(Number(event.target.value))} />
        </Field>
      </div>

      <Field
        id="creditsCost"
        label="Price in credits"
        hint={
          creditsCost > 0
            ? `${creditsCost} learning credits, or nothing with a discount code. Credits are earned by studying — no card is involved.`
            : "Free. Learners still have to sign in so the roster is real."
        }
      >
        <Input
          id="creditsCost"
          type="number"
          min={0}
          max={10000}
          value={creditsCost}
          onChange={(event) => setCreditsCost(Number(event.target.value))}
        />
      </Field>

      {capacity > 8 ? (
        <Alert tone="warning" title="Rooms are mesh-connected">
          Above about eight people, every participant&apos;s uplink carries a stream to everybody else. The room
          will still open, but the video will degrade before the server notices.
        </Alert>
      ) : null}

      <Button type="submit" disabled={busy || title.trim().length < 3}>
        {busy ? <Loader2 aria-hidden="true" className="animate-spin" /> : null}
        {busy ? "Scheduling…" : "Schedule class"}
      </Button>
    </form>
  );
}
