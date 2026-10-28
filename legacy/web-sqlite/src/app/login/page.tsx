"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useState } from "react";
import { AuthFrame } from "@/components/AuthFrame";

const DEMOS = [
  { label: "Student", email: "student@edumart.ng", password: "Student123!" },
  { label: "Teacher", email: "teacher@edumart.ng", password: "Teacher123!" },
  { label: "Admin", email: "admin@edumart.ng", password: "Admin123!" },
];

export default function LoginPage() {
  const router = useRouter();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [showDemos, setShowDemos] = useState(false);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const res = await fetch("/api/v1/auth/login", {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ email, password }),
      });
      const data = await res.json();
      if (!res.ok) {
        setError(data?.error?.message ?? "Unable to sign in.");
        return;
      }
      router.push(data.next ?? "/dashboard");
      router.refresh();
    } catch {
      setError("Network problem. Check your connection and try again.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <AuthFrame
      title="Welcome back"
      subtitle="Sign in to continue your preparation."
      footer={
        <div className="stack-sm">
          <p className="small muted">
            New to Edumart? <Link href="/register">Create an account</Link>
          </p>
          <button type="button" className="btn ghost sm" style={{ justifyContent: "flex-start", paddingLeft: 0 }} onClick={() => setShowDemos((v) => !v)}>
            {showDemos ? "Hide demo accounts" : "Use a demo account"}
          </button>
          {showDemos ? (
            <div className="stack-sm">
              {DEMOS.map((demo) => (
                <button
                  key={demo.email}
                  type="button"
                  className="demo-account"
                  onClick={() => {
                    setEmail(demo.email);
                    setPassword(demo.password);
                  }}
                >
                  <span>
                    <span className="strong">{demo.label}</span>
                    <span className="tiny muted" style={{ display: "block" }}>{demo.email}</span>
                  </span>
                  <span className="badge neutral">Fill</span>
                </button>
              ))}
            </div>
          ) : null}
        </div>
      }
    >
      <form onSubmit={submit} className="stack" noValidate>
        {error ? <div className="banner error" role="alert"><span className="ico">!</span>{error}</div> : null}
        <div className="field">
          <label htmlFor="email">Email address</label>
          <input
            id="email"
            className="input"
            type="email"
            autoComplete="email"
            required
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            placeholder="you@example.com"
          />
        </div>
        <div className="field">
          <label htmlFor="password">Password</label>
          <input
            id="password"
            className="input"
            type="password"
            autoComplete="current-password"
            required
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            placeholder="Your password"
          />
          <div className="row-between">
            <span className="hint">Minimum 8 characters.</span>
            <Link href="/forgot" className="tiny">Forgot password?</Link>
          </div>
        </div>
        <button className="btn lg block" disabled={busy} type="submit">
          {busy ? "Signing in…" : "Sign in"}
        </button>
      </form>
    </AuthFrame>
  );
}
