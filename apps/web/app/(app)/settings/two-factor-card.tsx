"use client";

import { useEffect, useState } from "react";
import { AlertTriangle, Copy, KeyRound, Loader2, ShieldCheck, Smartphone } from "lucide-react";

import { ApiError, type TwoFactorEnrolment, type TwoFactorStatus } from "@edumart/shared";

import { Alert } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Field } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { api } from "@/lib/browser-api";

/**
 * Two-factor authentication, stated in the state it is actually in.
 *
 * Three states, and the UI shows which: off, **pending** (a secret exists but no code has confirmed it — an abandoned
 * enrolment, which must not read as "off" or the user will try again and wonder why the secret changed), and **on**.
 *
 * What this card refuses to do:
 *  - It never shows the secret again after confirmation. The API cannot return it, so neither can this.
 *  - It never says "enabled" before the server says so. Confirmation is the server's opinion of a code, not a local
 *    flag flipped by a click.
 *  - It never hides the recovery codes behind a second click *after* they are issued: they are displayed once, next to
 *    a plain warning that this is the only time they will be visible. A user who dismisses that panel without saving
 *    them has a phone-shaped single point of failure, and the honest thing is to say so loudly before it happens.
 */
export function TwoFactorCard() {
  const [status, setStatus] = useState<TwoFactorStatus | null>(null);
  const [loading, setLoading] = useState(true);
  const [enrolment, setEnrolment] = useState<TwoFactorEnrolment | null>(null);
  const [recoveryCodes, setRecoveryCodes] = useState<string[] | null>(null);
  const [code, setCode] = useState("");
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState<"begin" | "confirm" | "disable" | null>(null);
  const [error, setError] = useState<ApiError | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [disabling, setDisabling] = useState(false);
  const [copied, setCopied] = useState(false);

  useEffect(() => {
    let alive = true;
    api()
      .twoFactorStatus()
      .then((value) => {
        if (alive) setStatus(value);
      })
      .catch((caught: unknown) => {
        if (alive) setError(caught instanceof ApiError ? caught : null);
      })
      .finally(() => {
        if (alive) setLoading(false);
      });
    return () => {
      alive = false;
    };
  }, []);

  async function begin() {
    setBusy("begin");
    setError(null);
    setNotice(null);
    try {
      setEnrolment(await api().beginTwoFactor());
    } catch (caught) {
      setError(caught instanceof ApiError ? caught : null);
    } finally {
      setBusy(null);
    }
  }

  async function confirm(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setBusy("confirm");
    setError(null);
    try {
      const result = await api().confirmTwoFactor(code.trim());
      setCode("");
      setEnrolment(null);
      setRecoveryCodes(result.recovery_codes ?? []);
      setStatus(await api().twoFactorStatus());
      setNotice("Two-factor authentication is on.");
    } catch (caught) {
      setError(caught instanceof ApiError ? caught : null);
    } finally {
      setBusy(null);
    }
  }

  async function disable(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setBusy("disable");
    setError(null);
    try {
      await api().disableTwoFactor(password, code.trim());
      setPassword("");
      setCode("");
      setDisabling(false);
      setRecoveryCodes(null);
      setStatus(await api().twoFactorStatus());
      setNotice("Two-factor authentication is off. Your recovery codes no longer work.");
    } catch (caught) {
      setError(caught instanceof ApiError ? caught : null);
    } finally {
      setBusy(null);
    }
  }

  async function copyCodes() {
    if (!recoveryCodes) return;
    try {
      await navigator.clipboard.writeText(recoveryCodes.join("\n"));
      setCopied(true);
    } catch {
      // Clipboard permission is not something to argue with: the codes are on screen, which is enough.
      setCopied(false);
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <ShieldCheck aria-hidden="true" className="size-4" />
          Two-factor authentication
          {status?.enabled ? <Badge variant="success">On</Badge> : null}
          {status?.pending && !status.enabled ? <Badge variant="warning">Not finished</Badge> : null}
        </CardTitle>
        <CardDescription>
          A code from an authenticator app, required after your password. This is what stops a stolen password being
          enough on its own.
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        {error ? (
          <Alert tone="error" title="That did not work">
            {error.message}
          </Alert>
        ) : null}
        {notice ? <Alert tone="success">{notice}</Alert> : null}

        {loading ? (
          <p className="flex items-center gap-2 text-sm text-muted-foreground">
            <Loader2 aria-hidden="true" className="size-4 animate-spin" /> Checking…
          </p>
        ) : null}

        {/* Recovery codes, shown exactly once and loud about it. */}
        {recoveryCodes ? (
          <div className="space-y-3 rounded-lg border border-warning/40 bg-warning/10 p-4">
            <p className="flex items-center gap-2 text-sm font-semibold">
              <AlertTriangle aria-hidden="true" className="size-4" />
              Save these now — they will not be shown again.
            </p>
            <p className="text-sm text-muted-foreground">
              Each code signs you in once if you lose your phone. Keep them somewhere that is not the phone.
            </p>
            <ul className="grid grid-cols-1 gap-1 font-mono text-sm sm:grid-cols-2">
              {recoveryCodes.map((value) => (
                <li key={value} className="rounded bg-background/60 px-2 py-1">
                  {value}
                </li>
              ))}
            </ul>
            <Button type="button" variant="outline" size="sm" onClick={copyCodes}>
              <Copy aria-hidden="true" className="size-4" />
              {copied ? "Copied" : "Copy all"}
            </Button>
          </div>
        ) : null}

        {/* Off (or an unfinished enrolment the user can restart). */}
        {!loading && status && !status.enabled && !enrolment ? (
          <div className="space-y-3">
            <p className="text-sm text-muted-foreground">
              {status.pending
                ? "You started setting this up but never confirmed a code, so it is not protecting the account yet. Start again to get a fresh secret."
                : "You will need an authenticator app — Google Authenticator, Authy, 1Password or any other TOTP app."}
            </p>
            <Button type="button" onClick={begin} disabled={busy !== null}>
              {busy === "begin" ? <Loader2 aria-hidden="true" className="size-4 animate-spin" /> : <Smartphone aria-hidden="true" className="size-4" />}
              {status.pending ? "Start again" : "Set up authenticator app"}
            </Button>
          </div>
        ) : null}

        {/* Mid-enrolment: the secret, the QR payload, and the code that proves it worked. */}
        {enrolment ? (
          <div className="space-y-4">
            <div className="space-y-2 rounded-lg border border-border p-4">
              <p className="text-sm font-semibold">1. Add this account to your app</p>
              <p className="text-sm text-muted-foreground">
                Scan the code below, or type the key in by hand. Both are the same secret.
              </p>
              {/* Rendered from the provisioning URI the server produced, so what is scanned and what is typed agree. */}
              <QrCode value={enrolment.uri} />
              <p className="break-all rounded bg-muted px-2 py-1 font-mono text-xs">{enrolment.secret}</p>
            </div>
            <form onSubmit={confirm} className="space-y-3">
              <p className="text-sm font-semibold">2. Enter the six-digit code your app shows</p>
              <Field id="totp-code" label="Code" required>
                <Input
                  id="totp-code"
                  name="code"
                  autoComplete="one-time-code"
                  inputMode="numeric"
                  required
                  value={code}
                  onChange={(event) => setCode(event.target.value)}
                  placeholder="123456"
                />
              </Field>
              <div className="flex gap-2">
                <Button type="submit" disabled={busy !== null || code.trim() === ""}>
                  {busy === "confirm" ? <Loader2 aria-hidden="true" className="size-4 animate-spin" /> : <KeyRound aria-hidden="true" className="size-4" />}
                  Turn on
                </Button>
                <Button
                  type="button"
                  variant="ghost"
                  onClick={() => {
                    setEnrolment(null);
                    setCode("");
                  }}
                >
                  Cancel
                </Button>
              </div>
            </form>
          </div>
        ) : null}

        {/* On: the state, the codes left, and the way off. */}
        {!loading && status?.enabled && !recoveryCodes ? (
          <div className="space-y-3">
            <p className="text-sm">
              On{status.confirmed_at ? ` since ${new Date(status.confirmed_at).toLocaleDateString()}` : ""}.{" "}
              <span className={status.recovery_codes_remaining === 0 ? "font-semibold text-warning-foreground" : ""}>
                {status.recovery_codes_remaining} recovery code
                {status.recovery_codes_remaining === 1 ? "" : "s"} left.
              </span>
            </p>
            {status.recovery_codes_remaining === 0 ? (
              <p className="text-sm text-muted-foreground">
                With no codes left, a lost phone means contacting support to get back in. Turn this off and on again to
                get a fresh set.
              </p>
            ) : null}
            {disabling ? (
              <form onSubmit={disable} className="space-y-3 rounded-lg border border-destructive/40 p-4">
                <p className="text-sm font-semibold">Turn two-factor authentication off</p>
                <p className="text-sm text-muted-foreground">
                  Your password and a current code are both required — a session alone must never be enough to remove
                  the factor.
                </p>
                <Field id="disable-password" label="Password" required>
                  <Input
                    id="disable-password"
                    type="password"
                    autoComplete="current-password"
                    required
                    value={password}
                    onChange={(event) => setPassword(event.target.value)}
                  />
                </Field>
                <Field id="disable-code" label="Code" required>
                  <Input
                    id="disable-code"
                    autoComplete="one-time-code"
                    inputMode="numeric"
                    required
                    value={code}
                    onChange={(event) => setCode(event.target.value)}
                  />
                </Field>
                <div className="flex gap-2">
                  <Button type="submit" variant="destructive" disabled={busy !== null || !password || !code}>
                    {busy === "disable" ? <Loader2 aria-hidden="true" className="size-4 animate-spin" /> : null}
                    Turn off
                  </Button>
                  <Button type="button" variant="ghost" onClick={() => setDisabling(false)}>
                    Keep it on
                  </Button>
                </div>
              </form>
            ) : (
              <Button type="button" variant="outline" onClick={() => setDisabling(true)}>
                Turn off…
              </Button>
            )}
          </div>
        ) : null}
      </CardContent>
    </Card>
  );
}

/**
 * The QR code, drawn locally from the provisioning URI.
 *
 * It is rendered in the browser by `qrcode` — no third-party image service, because the payload *is* the second
 * factor: posting it to someone else's server to be turned into a picture would hand them the secret. The library
 * does the Reed-Solomon work rather than hand-rolled maths in this repository, because a QR code that is one module
 * wrong is a code nobody can scan and nothing in the test suite would notice.
 *
 * If rendering fails, the secret below it is still shown: typing the key in by hand is the fallback, and a broken
 * picture must not be a dead end.
 */
function QrCode({ value }: { value: string }) {
  const [svg, setSvg] = useState<string | null>(null);
  const [failed, setFailed] = useState(false);

  useEffect(() => {
    let alive = true;
    import("qrcode")
      .then((qr) =>
        qr.toString(value, {
          type: "svg",
          errorCorrectionLevel: "M",
          margin: 0,
          width: 176,
          color: { dark: "#000000", light: "#ffffff" },
        }),
      )
      .then((markup) => {
        if (alive) setSvg(markup);
      })
      .catch(() => {
        if (alive) setFailed(true);
      });
    return () => {
      alive = false;
    };
  }, [value]);

  if (failed || !svg) {
    return failed ? (
      <p className="text-sm text-muted-foreground">The QR code could not be drawn here. Type the key below instead.</p>
    ) : null;
  }
  // The markup is generated by the library from a string we control, and carries no script.
  return <div className="w-44 rounded bg-white p-2" dangerouslySetInnerHTML={{ __html: svg }} />;
}
