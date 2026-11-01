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
  // The room carries fifty people; the input starts there because that is the number the forwarder is built for.
  const [capacity, setCapacity] = useState(50);
  // Who decides who comes in. `approval` is a waiting room: each learner knocks, the host admits or declines,
  // and a request costs nothing until it is accepted.
  const [admission, setAdmission] = useState<"open" | "approval">("open");
  // A free session is capped at 45 minutes by the server. The form follows the price so the limit is visible
  // before the request rather than as a 400 after it.
  const [durationMinutes, setDurationMinutes] = useState(45);
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
          admission,
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
      setDurationMinutes(45);
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
        <Field
          id="durationMinutes"
          label="Minutes"
          hint={creditsCost === 0 ? "A free session is capped at 45 minutes." : "Up to 480 minutes once it is priced."}
        >
          <Input
            id="durationMinutes"
            type="number"
            min={5}
            max={creditsCost === 0 ? 45 : 480}
            value={durationMinutes}
            onChange={(event) => setDurationMinutes(Number(event.target.value))}
          />
        </Field>
        <Field id="capacity" label="Seats" hint="Up to 50 — the room carries fifty connections.">
          <Input id="capacity" type="number" min={2} max={50} value={capacity} onChange={(event) => setCapacity(Number(event.target.value))} />
        </Field>
      </div>

      <fieldset className="space-y-2">
        <legend className="text-sm font-medium">Who lets people in?</legend>
        <div className="grid gap-2 sm:grid-cols-2">
          {([
            {
              value: "open" as const,
              title: "Anyone with a seat",
              blurb: "Learners join as soon as they pay or tap join.",
            },
            {
              value: "approval" as const,
              title: "I approve each learner",
              blurb: "A waiting room: you admit or decline, and nobody is charged until you accept.",
            },
          ]).map((option) => (
            <button
              key={option.value}
              type="button"
              aria-pressed={admission === option.value}
              onClick={() => setAdmission(option.value)}
              className={
                admission === option.value
                  ? "rounded-lg border border-primary bg-secondary px-4 py-3 text-left"
                  : "rounded-lg border border-border bg-card px-4 py-3 text-left"
              }
            >
              <span className="block text-sm font-semibold">{option.title}</span>
              <span className="mt-1 block text-xs text-muted-foreground">{option.blurb}</span>
            </button>
          ))}
        </div>
        <p className="text-xs text-muted-foreground">
          This is fixed when the class is created: flipping a room to approval mid-lesson would turn out learners who
          are already inside.
        </p>
      </fieldset>

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

      {creditsCost === 0 && durationMinutes > 45 ? (
        <Alert tone="warning" title="Free sessions stop at 45 minutes">
          Set a credit price to run a longer session, or the API will refuse this one.
        </Alert>
      ) : null}

      <p className="text-xs text-muted-foreground">
        Fifty seats, six of them on stage: you plus five speakers you promote during the class. Everybody else
        watches and chats. Media is forwarded by the media server, so a full room does not depend on every phone
        uploading fifty streams.
      </p>

      <Button type="submit" disabled={busy || title.trim().length < 3}>
        {busy ? <Loader2 aria-hidden="true" className="animate-spin" /> : null}
        {busy ? "Scheduling…" : "Schedule class"}
      </Button>
    </form>
  );
}
