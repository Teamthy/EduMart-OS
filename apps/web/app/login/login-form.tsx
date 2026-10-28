"use client";

import { useRouter } from "next/navigation";
import { useState, useTransition } from "react";
import { Loader2 } from "lucide-react";

import { ApiError, errorTitle } from "@edumart/shared";

import { Alert } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Field } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { api } from "@/lib/browser-api";

export function LoginForm({ nextPath }: { nextPath: string }) {
  const router = useRouter();
  const [pending, startTransition] = useTransition();
  const [submitting, setSubmitting] = useState(false);
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<ApiError | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});

  async function onSubmit(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setError(null);
    setFieldErrors({});
    setSubmitting(true);
    try {
      const result = await api().login(email, password, "web");
      // The API's `next` respects role; an explicit ?next= wins when it is a safe in-app path.
      const destination = nextPath.startsWith("/") ? nextPath : result.next;
      startTransition(() => {
        router.replace(destination);
        router.refresh();
      });
    } catch (caught) {
      const apiError = caught instanceof ApiError ? caught : null;
      setError(apiError);
      const fields: Record<string, string> = {};
      for (const detail of apiError?.fieldErrors ?? []) fields[detail.field] = detail.message;
      setFieldErrors(fields);
      setSubmitting(false);
    }
  }

  return (
    <form onSubmit={onSubmit} className="space-y-4" noValidate>
      {error && error.fieldErrors.length === 0 ? (
        <Alert tone="error" title={errorTitle(error)}>
          {error.message}
          {error.requestId ? <span className="mt-1 block text-xs opacity-70">Request {error.requestId}</span> : null}
        </Alert>
      ) : null}

      <Field id="email" label="Email" required error={fieldErrors.email}>
        <Input
          id="email"
          name="email"
          type="email"
          autoComplete="email"
          inputMode="email"
          required
          value={email}
          aria-invalid={Boolean(fieldErrors.email)}
          aria-describedby={fieldErrors.email ? "email-error" : undefined}
          onChange={(event) => setEmail(event.target.value)}
          placeholder="you@example.com"
        />
      </Field>

      <Field id="password" label="Password" required error={fieldErrors.password}>
        <Input
          id="password"
          name="password"
          type="password"
          autoComplete="current-password"
          required
          value={password}
          aria-invalid={Boolean(fieldErrors.password)}
          aria-describedby={fieldErrors.password ? "password-error" : undefined}
          onChange={(event) => setPassword(event.target.value)}
        />
      </Field>

      <Button type="submit" className="w-full" disabled={submitting || pending}>
        {submitting || pending ? <Loader2 aria-hidden="true" className="animate-spin" /> : null}
        {submitting ? "Signing in…" : "Sign in"}
      </Button>

      <p className="text-center text-xs text-muted-foreground">
        <a href="/forgot-password" className="underline underline-offset-4">
          Forgot your password?
        </a>
      </p>
    </form>
  );
}
