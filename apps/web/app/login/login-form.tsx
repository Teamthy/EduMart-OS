"use client";

import { useRouter } from "next/navigation";
import { useState, useTransition } from "react";
import { Loader2 } from "lucide-react";

import { ApiError, errorTitle, isTwoFactorChallenge } from "@edumart/shared";

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
  // Set when the password was right and a code is owed. `token` is the challenge, not a session: it grants nothing
  // on its own, which is why it can sit in component state.
  const [challenge, setChallenge] = useState<{ token: string; expiresAt: string; recovery: boolean } | null>(null);
  const [code, setCode] = useState("");

  async function onSubmit(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setError(null);
    setFieldErrors({});
    setSubmitting(true);
    try {
      const result = await api().login(email, password, "web");
      if (isTwoFactorChallenge(result)) {
        // Not a failure and not a session: ask for the code. Clearing the password here means a shoulder-surfer
        // reading the screen during the second step has nothing to carry away.
        setChallenge({ token: result.token, expiresAt: result.expires_at, recovery: result.recovery_available });
        setPassword("");
        setSubmitting(false);
        return;
      }
      finish(result.next);
    } catch (caught) {
      const apiError = caught instanceof ApiError ? caught : null;
      setError(apiError);
      const fields: Record<string, string> = {};
      for (const detail of apiError?.fieldErrors ?? []) fields[detail.field] = detail.message;
      setFieldErrors(fields);
      setSubmitting(false);
    }
  }

  // finish lands a completed sign-in: the API's `next` respects role, an explicit ?next= wins when it is a safe path.
  function finish(apiNext: string) {
    const destination = nextPath.startsWith("/") ? nextPath : apiNext;
    startTransition(() => {
      router.replace(destination);
      router.refresh();
    });
  }

  async function onSubmitCode(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!challenge) return;
    setError(null);
    setSubmitting(true);
    try {
      const session = await api().completeLogin(challenge.token, code.trim(), "web");
      finish(session.next);
    } catch (caught) {
      const apiError = caught instanceof ApiError ? caught : null;
      setError(apiError);
      setCode("");
      setSubmitting(false);
      // A spent or expired challenge cannot be retried; send the person back to the password step with an explanation
      // rather than letting them type codes into a token the server has already thrown away.
      if (apiError?.code === "TWO_FACTOR_REQUIRED") setChallenge(null);
    }
  }

  if (challenge) {
    return (
      <form onSubmit={onSubmitCode} className="space-y-4" noValidate>
        {error ? (
          <Alert tone="error" title={errorTitle(error)}>
            {error.message}
          </Alert>
        ) : null}
        <div className="space-y-1 rounded-lg border border-border bg-muted/40 p-3 text-sm">
          <p className="font-semibold">Two-step verification</p>
          <p className="text-muted-foreground">
            Your password was accepted. Enter the six-digit code from your authenticator app.
          </p>
        </div>
        <Field id="code" label="Verification code" required>
          <Input
            id="code"
            name="code"
            // `one-time-code` lets iOS and Android offer the code from the notification, which is the difference
            // between a second step and a chore.
            autoComplete="one-time-code"
            inputMode={challenge.recovery ? "text" : "numeric"}
            autoFocus
            required
            value={code}
            onChange={(event) => setCode(event.target.value)}
            placeholder={challenge.recovery ? "123456 or a recovery code" : "123456"}
          />
        </Field>
        <Button type="submit" className="w-full" disabled={submitting || pending || code.trim() === ""}>
          {submitting || pending ? <Loader2 aria-hidden="true" className="animate-spin" /> : null}
          {submitting ? "Checking…" : "Verify and sign in"}
        </Button>
        <div className="space-y-1 text-center text-xs text-muted-foreground">
          <button
            type="button"
            className="underline underline-offset-4"
            onClick={() => {
              setChallenge(null);
              setCode("");
              setError(null);
            }}
          >
            Use a different account
          </button>
          {challenge.recovery ? <p>Lost your phone? Use one of the recovery codes you saved when you turned this on.</p> : null}
        </div>
      </form>
    );
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
