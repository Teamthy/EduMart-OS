"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useState } from "react";
import { Loader2 } from "lucide-react";

import { ApiError } from "@edumart/shared";

import { Alert } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Field } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { api } from "@/lib/browser-api";

export function ResetForm({ token }: { token: string }) {
  const router = useRouter();
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function submit(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setBusy(true);
    setError(null);
    try {
      await api().resetPassword(token, password);
      router.push("/login?reset=1");
    } catch (caught) {
      setError(caught instanceof ApiError ? caught.message : "We couldn't reset your password.");
      setBusy(false);
    }
  }

  if (!token) {
    return (
      <Alert tone="error" title="This link is incomplete">
        Reset links expire after 30 minutes.{" "}
        <Link href="/forgot-password" className="font-semibold underline">
          Request a new one
        </Link>
        .
      </Alert>
    );
  }

  return (
    <form onSubmit={submit} className="space-y-4">
      {error ? <Alert tone="error">{error}</Alert> : null}
      <Field id="password" label="New password" required hint="At least 8 characters with a letter and a number.">
        <Input
          id="password"
          type="password"
          autoComplete="new-password"
          minLength={8}
          value={password}
          onChange={(event) => setPassword(event.target.value)}
          required
        />
      </Field>
      <Button type="submit" className="w-full" disabled={busy}>
        {busy ? <Loader2 aria-hidden="true" className="animate-spin" /> : null}
        {busy ? "Saving…" : "Save new password"}
      </Button>
    </form>
  );
}
