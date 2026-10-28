"use client";

import Link from "next/link";
import { useSearchParams } from "next/navigation";
import { Suspense, useState } from "react";
import { AuthFrame } from "@/components/AuthFrame";

function ResetForm() {
  const params = useSearchParams();
  const token = params.get("token") ?? "";
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [state, setState] = useState<"idle" | "saving" | "done" | "error">("idle");
  const [message, setMessage] = useState<string | null>(null);

  if (!token) {
    return (
      <div className="stack">
        <div className="banner error"><span className="ico">!</span>This reset link is missing its token.</div>
        <Link className="btn secondary block" href="/forgot">Request a new link</Link>
      </div>
    );
  }

  if (state === "done") {
    return (
      <div className="stack">
        <div className="banner info"><span className="ico">✓</span>Password updated. Every existing session has been signed out.</div>
        <Link className="btn block" href="/login">Sign in with your new password</Link>
      </div>
    );
  }

  return (
    <form
      className="stack"
      onSubmit={async (e) => {
        e.preventDefault();
        if (password !== confirm) {
          setState("error");
          setMessage("The two passwords do not match.");
          return;
        }
        setState("saving");
        setMessage(null);
        const res = await fetch("/api/v1/auth/password/reset", {
          method: "POST",
          headers: { "content-type": "application/json" },
          body: JSON.stringify({ token, password }),
        }).catch(() => null);
        const data = res ? await res.json().catch(() => ({})) : {};
        if (!res || !res.ok) {
          setState("error");
          setMessage(data?.error?.message ?? "Could not reset your password.");
          return;
        }
        setState("done");
      }}
    >
      {message ? <div className="banner error"><span className="ico">!</span>{message}</div> : null}
      <div className="field">
        <label htmlFor="password">New password</label>
        <input
          id="password"
          className="input"
          type="password"
          autoComplete="new-password"
          minLength={8}
          required
          value={password}
          onChange={(e) => setPassword(e.target.value)}
        />
        <span className="hint">At least 8 characters.</span>
      </div>
      <div className="field">
        <label htmlFor="confirm">Confirm new password</label>
        <input
          id="confirm"
          className="input"
          type="password"
          autoComplete="new-password"
          minLength={8}
          required
          value={confirm}
          onChange={(e) => setConfirm(e.target.value)}
        />
      </div>
      <button className="btn lg block" type="submit" disabled={state === "saving"}>
        {state === "saving" ? "Updating…" : "Set new password"}
      </button>
    </form>
  );
}

export default function ResetPasswordPage() {
  return (
    <AuthFrame title="Choose a new password" subtitle="Reset links are single-use and expire after 30 minutes.">
      <Suspense fallback={<div className="skeleton" style={{ height: 160 }} />}>
        <ResetForm />
      </Suspense>
    </AuthFrame>
  );
}
