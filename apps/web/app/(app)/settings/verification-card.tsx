"use client";

import { useRouter } from "next/navigation";
import { useState } from "react";
import { BadgeCheck, Loader2, ShieldQuestion } from "lucide-react";

import { ApiError, type User } from "@edumart/shared";

import { Alert } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Field } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { api } from "@/lib/browser-api";

/**
 * Proving the address is real.
 *
 * The code is never shown in the UI — it arrives by email or SMS — and the panel never claims
 * "sent" without the server having accepted the request. Confirming is the only thing that flips
 * the badge, because verification is the server's opinion, not the browser's.
 */
export function VerificationCard({ user }: { user: User }) {
  const router = useRouter();
  const [sent, setSent] = useState<{ destination: string; expiresAt: string } | null>(null);
  const [code, setCode] = useState("");
  const [sending, setSending] = useState(false);
  const [confirming, setConfirming] = useState(false);
  const [error, setError] = useState<ApiError | null>(null);

  async function request() {
    setSending(true);
    setError(null);
    try {
      const issue = await api().requestVerification("email");
      setSent({ destination: issue.destination, expiresAt: issue.expires_at });
    } catch (caught) {
      setError(caught instanceof ApiError ? caught : null);
    } finally {
      setSending(false);
    }
  }

  async function confirm(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setConfirming(true);
    setError(null);
    try {
      await api().confirmVerification("email", code.trim());
      setCode("");
      setSent(null);
      router.refresh();
    } catch (caught) {
      setError(caught instanceof ApiError ? caught : null);
    } finally {
      setConfirming(false);
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          {user.emailVerified ? (
            <BadgeCheck className="h-4 w-4 text-primary" aria-hidden />
          ) : (
            <ShieldQuestion className="h-4 w-4 text-muted-foreground" aria-hidden />
          )}
          Email address
        </CardTitle>
        <CardDescription>
          {user.emailVerified
            ? `${user.email} is confirmed.`
            : "Confirm your address to receive receipts and refunds, and to buy anything."}
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-4 text-sm">
        {user.emailVerified ? (
          <p className="text-muted-foreground">
            A confirmed address is also how account recovery works if you lose your password.
          </p>
        ) : (
          <>
            {error ? (
              <Alert tone="error" title="That didn't work">
                {error.message}
              </Alert>
            ) : null}

            {sent ? (
              <form onSubmit={confirm} className="space-y-3">
                <p className="text-muted-foreground">
                  We sent a six-digit code to <span className="font-semibold">{sent.destination}</span>. It
                  expires at {new Date(sent.expiresAt).toLocaleTimeString()}.
                </p>
                <Field id="code" label="Your code" hint="Six digits. Five wrong tries and the code is spent.">
                  <Input
                    id="code"
                    inputMode="numeric"
                    autoComplete="one-time-code"
                    pattern="[0-9]*"
                    maxLength={6}
                    value={code}
                    onChange={(event) => setCode(event.target.value.replace(/\D/g, ""))}
                  />
                </Field>
                <div className="flex flex-wrap gap-2">
                  <Button type="submit" disabled={confirming || code.length !== 6}>
                    {confirming ? <Loader2 className="mr-2 h-4 w-4 animate-spin" aria-hidden /> : null}
                    Confirm address
                  </Button>
                  <Button type="button" variant="ghost" onClick={request} disabled={sending}>
                    {sending ? "Sending…" : "Send a new code"}
                  </Button>
                </div>
              </form>
            ) : (
              <Button type="button" onClick={request} disabled={sending}>
                {sending ? <Loader2 className="mr-2 h-4 w-4 animate-spin" aria-hidden /> : null}
                Send verification code
              </Button>
            )}
          </>
        )}
      </CardContent>
    </Card>
  );
}
