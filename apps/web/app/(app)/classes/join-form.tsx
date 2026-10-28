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

export function JoinClassForm() {
  const router = useRouter();
  const [code, setCode] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<ApiError | null>(null);
  const [joined, setJoined] = useState<string | null>(null);

  async function join(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setBusy(true);
    setError(null);
    setJoined(null);
    try {
      const { class: joinedClass } = await api().joinClass(code);
      setJoined(joinedClass.name);
      setCode("");
      router.refresh();
    } catch (caught) {
      setError(caught instanceof ApiError ? caught : null);
    } finally {
      setBusy(false);
    }
  }

  return (
    <form onSubmit={join} className="space-y-3">
      {error ? <Alert tone="error" title="Could not join">{error.message}</Alert> : null}
      {joined ? <Alert tone="success">You joined {joined}.</Alert> : null}

      <div className="flex flex-wrap items-end gap-3">
        <Field id="joinCode" label="Class code" className="min-w-48 flex-1">
          <Input
            id="joinCode"
            value={code}
            onChange={(event) => setCode(event.target.value.toUpperCase())}
            placeholder="LEKKI24"
            maxLength={12}
            autoCapitalize="characters"
            className="font-mono uppercase tracking-widest"
          />
        </Field>
        <Button type="submit" disabled={busy || code.trim().length < 4}>
          {busy ? <Loader2 aria-hidden="true" className="animate-spin" /> : null}
          {busy ? "Joining…" : "Join class"}
        </Button>
      </div>
    </form>
  );
}
