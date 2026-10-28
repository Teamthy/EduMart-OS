"use client";

import Link from "next/link";
import { useState } from "react";
import { Loader2 } from "lucide-react";

import { ApiError } from "@edumart/shared";

import { Alert } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Field } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { api } from "@/lib/browser-api";

export function ForgotForm() {
  const [email, setEmail] = useState("");
  const [busy, setBusy] = useState(false);
  const [sent, setSent] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  async function submit(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const response = await api().forgotPassword(email);
      setSent(response.message);
    } catch (caught) {
      setError(caught instanceof ApiError ? caught.message : "We couldn't send that. Please try again.");
    } finally {
      setBusy(false);
    }
  }

  if (sent) {
    return (
      <div className="space-y-4">
        <Alert tone="success" title="Check your inbox">{sent}</Alert>
        <p className="text-sm text-muted-foreground">
          The link expires in 30 minutes and can only be used once.{" "}
          <Link href="/login" className="font-semibold text-primary underline-offset-4 hover:underline">
            Back to sign in
          </Link>
        </p>
      </div>
    );
  }

  return (
    <form onSubmit={submit} className="space-y-4" noValidate>
      {error ? <Alert tone="error">{error}</Alert> : null}
      <Field id="email" label="Email" required>
        <Input
          id="email"
          type="email"
          inputMode="email"
          autoComplete="email"
          value={email}
          onChange={(event) => setEmail(event.target.value)}
          required
        />
      </Field>
      <Button type="submit" className="w-full" disabled={busy}>
        {busy ? <Loader2 aria-hidden="true" className="animate-spin" /> : null}
        {busy ? "Sending…" : "Send reset link"}
      </Button>
    </form>
  );
}
