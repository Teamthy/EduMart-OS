"use client";

import Link from "next/link";
import { useActionState, useState } from "react";
import { Eye, EyeOff, Loader2 } from "lucide-react";

import { ACCOUNT_TYPES, type SignupDraft } from "@edumart/shared";

import { Alert } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Field } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { createAccount } from "@/lib/signup-actions";
import { EMPTY_STATE } from "@/lib/signup-state";

export function AccountForm({ draft }: { draft: SignupDraft }) {
  const [state, formAction, pending] = useActionState(createAccount, EMPTY_STATE);
  const [showPassword, setShowPassword] = useState(false);

  return (
    <form action={formAction} className="space-y-4" noValidate>
      {state.message && Object.keys(state.errors).length === 0 ? (
        <Alert tone="error" title="We couldn't create your account">
          {state.message}
          {state.requestId ? <span className="mt-1 block text-xs opacity-70">Request {state.requestId}</span> : null}
        </Alert>
      ) : null}

      <fieldset className="space-y-2">
        <legend className="text-sm font-medium">What kind of account is this?</legend>
        <p id="accountType-hint" className="text-xs text-muted-foreground">
          This shapes your onboarding. It grants no permissions: an institution is verified by an administrator
          before it can run classes, and a tutor is invited by a school before teaching inside one.
        </p>
        <div className="grid gap-2 sm:grid-cols-3">
          {ACCOUNT_TYPES.map((option) => (
            <label
              key={option.value}
              className="flex cursor-pointer items-start gap-2 rounded-lg border border-border p-3 text-sm has-[:checked]:border-primary has-[:checked]:bg-primary/5"
            >
              <input
                type="radio"
                name="accountType"
                value={option.value}
                defaultChecked={draft.accountType === option.value}
                className="mt-1 size-4 accent-current"
                aria-describedby="accountType-hint"
              />
              <span>
                <span className="block font-medium">{option.label}</span>
                <span className="block text-xs text-muted-foreground">{option.description}</span>
              </span>
            </label>
          ))}
        </div>
        {state.errors.accountType ? (
          <p className="text-xs text-destructive" role="alert">
            {state.errors.accountType}
          </p>
        ) : null}
      </fieldset>

      <Field id="displayName" label="Full name" required error={state.errors.displayName}>
        <Input
          id="displayName"
          name="displayName"
          autoComplete="name"
          defaultValue={draft.displayName}
          aria-invalid={Boolean(state.errors.displayName)}
          aria-describedby={state.errors.displayName ? "displayName-error" : undefined}
          required
        />
      </Field>

      <Field id="email" label="Email" required error={state.errors.email} hint="We use this to sign you in on any device.">
        <Input
          id="email"
          name="email"
          type="email"
          inputMode="email"
          autoComplete="email"
          defaultValue={draft.email}
          aria-invalid={Boolean(state.errors.email)}
          aria-describedby={state.errors.email ? "email-error" : "email-hint"}
          required
        />
      </Field>

      <Field
        id="password"
        label="Password"
        required
        error={state.errors.password}
        hint="At least 8 characters, including a letter and a number."
      >
        <div className="relative">
          <Input
            id="password"
            name="password"
            type={showPassword ? "text" : "password"}
            autoComplete="new-password"
            minLength={8}
            aria-invalid={Boolean(state.errors.password)}
            aria-describedby={state.errors.password ? "password-error" : "password-hint"}
            required
            className="pr-12"
          />
          <button
            type="button"
            onClick={() => setShowPassword((value) => !value)}
            className="absolute inset-y-0 right-0 grid w-12 place-items-center text-muted-foreground"
            aria-label={showPassword ? "Hide password" : "Show password"}
            aria-pressed={showPassword}
          >
            {showPassword ? <EyeOff aria-hidden="true" className="size-4" /> : <Eye aria-hidden="true" className="size-4" />}
          </button>
        </div>
      </Field>

      <Field id="referralCode" label="Referral code (optional)" hint="If a friend invited you, add their code here.">
        <Input
          id="referralCode"
          name="referralCode"
          defaultValue={draft.referralCode}
          autoCapitalize="characters"
          placeholder="AMARA-2026"
        />
      </Field>

      <p className="text-xs text-muted-foreground">
        By creating an account you agree to our terms and privacy notice. We never sell your data,
        and you can delete your account from Settings at any time.
      </p>

      <div className="flex items-center justify-between gap-3 pt-1">
        <Button asChild variant="ghost">
          <Link href="/register/schedule">Back</Link>
        </Button>
        <Button type="submit" disabled={pending} size="lg">
          {pending ? <Loader2 aria-hidden="true" className="animate-spin" /> : null}
          {pending ? "Creating account…" : "Create account"}
        </Button>
      </div>
    </form>
  );
}
