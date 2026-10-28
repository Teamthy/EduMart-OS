"use client";

import Link from "next/link";
import { useState } from "react";
import { AuthFrame } from "@/components/AuthFrame";

export default function ForgotPage() {
  const [email, setEmail] = useState("");
  const [sent, setSent] = useState(false);
  const [busy, setBusy] = useState(false);

  return (
    <AuthFrame title="Reset your password" subtitle="We will email you a link that expires in 30 minutes.">
      {sent ? (
        <div className="stack">
          <div className="banner info">
            <span className="ico">✉</span>
            If that email is registered, a reset link is on its way. Check your inbox and spam folder.
          </div>
          <Link className="btn secondary block" href="/login">Back to sign in</Link>
        </div>
      ) : (
        <form
          className="stack"
          onSubmit={async (e) => {
            e.preventDefault();
            setBusy(true);
            await fetch("/api/v1/auth/password/forgot", {
              method: "POST",
              headers: { "content-type": "application/json" },
              body: JSON.stringify({ email }),
            }).catch(() => null);
            setBusy(false);
            setSent(true);
          }}
        >
          <div className="field">
            <label htmlFor="email">Email address</label>
            <input
              id="email"
              className="input"
              type="email"
              required
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              autoComplete="email"
            />
              <span className="hint">We never reveal whether an address is registered.</span>
          </div>
          <button className="btn lg block" disabled={busy} type="submit">
            {busy ? "Sending…" : "Send reset link"}
          </button>
          <Link href="/login" className="small center">Back to sign in</Link>
        </form>
      )}
    </AuthFrame>
  );
}
