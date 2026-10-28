"use client";

import { useRouter } from "next/navigation";
import { useState } from "react";
import { Loader2 } from "lucide-react";

import { ApiError } from "@edumart/shared";

import { Alert } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Field } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { api } from "@/lib/browser-api";

export function CreateClassForm() {
  const router = useRouter();
  const [name, setName] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<ApiError | null>(null);
  const [created, setCreated] = useState<{ name: string; joinCode: string } | null>(null);

  async function submit(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const { class: createdClass } = await api().createClass({ name });
      setCreated({ name: createdClass.name, joinCode: createdClass.joinCode });
      setName("");
      router.refresh();
    } catch (caught) {
      setError(caught instanceof ApiError ? caught : null);
    } finally {
      setBusy(false);
    }
  }

  return (
    <form onSubmit={submit} className="space-y-3">
      {error ? <Alert tone="error" title="Could not create the class">{error.message}</Alert> : null}
      {created ? (
        <Alert tone="success" title={`${created.name} created`}>
          Share this code with your learners: <strong className="font-mono tracking-widest">{created.joinCode}</strong>
        </Alert>
      ) : null}
      <div className="flex flex-wrap items-end gap-3">
        <Field id="className" label="Class name" className="min-w-56 flex-1">
          <Input
            id="className"
            value={name}
            onChange={(event) => setName(event.target.value)}
            placeholder="Lekki Study Circle"
            minLength={3}
            required
          />
        </Field>
        <Button type="submit" disabled={busy || name.trim().length < 3}>
          {busy ? <Loader2 aria-hidden="true" className="animate-spin" /> : null}
          {busy ? "Creating…" : "Create class"}
        </Button>
      </div>
    </form>
  );
}
