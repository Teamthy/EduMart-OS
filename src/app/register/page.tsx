"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useEffect, useState } from "react";
import { AuthFrame } from "@/components/AuthFrame";

type Pathway = { id: string; key: string; name: string; body: string };

export default function RegisterPage() {
  const router = useRouter();
  const [pathways, setPathways] = useState<Pathway[]>([]);
  const [form, setForm] = useState({
    displayName: "",
    email: "",
    password: "",
    examPathwayId: "",
    targetDate: "",
    referralCode: "",
  });
  const [error, setError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    fetch("/api/v1/exam-pathways")
      .then((r) => r.json())
      .then((d) => setPathways(d.pathways ?? []))
      .catch(() => setPathways([]));
  }, []);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    setFieldErrors({});
    try {
      const res = await fetch("/api/v1/auth/register", {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({
          displayName: form.displayName,
          email: form.email,
          password: form.password,
          examPathwayId: form.examPathwayId || null,
          targetDate: form.targetDate ? new Date(form.targetDate).toISOString() : null,
          referralCode: form.referralCode || undefined,
        }),
      });
      const data = await res.json();
      if (!res.ok) {
        setError(data?.error?.message ?? "Unable to create your account.");
        const details = data?.error?.details;
        if (Array.isArray(details)) {
          const mapped: Record<string, string> = {};
          for (const d of details) if (d?.field) mapped[d.field] = d.message;
          setFieldErrors(mapped);
        }
        return;
      }
      router.push(data.next ?? "/onboarding");
      router.refresh();
    } catch {
      setError("Network problem. Check your connection and try again.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <AuthFrame
      title="Create your account"
      subtitle="Two minutes now, then straight into practice."
      footer={
        <p className="small muted">
          Already registered? <Link href="/login">Sign in</Link>
        </p>
      }
    >
      <form onSubmit={submit} className="stack" noValidate>
        {error ? <div className="banner error" role="alert"><span className="ico">!</span>{error}</div> : null}

        <div className="field">
          <label htmlFor="displayName">Full name</label>
          <input
            id="displayName"
            className="input"
            value={form.displayName}
            onChange={(e) => setForm({ ...form, displayName: e.target.value })}
            aria-invalid={Boolean(fieldErrors.displayName)}
            autoComplete="name"
            required
          />
          {fieldErrors.displayName ? <span className="err">{fieldErrors.displayName}</span> : null}
        </div>

        <div className="field">
          <label htmlFor="email">Email address</label>
          <input
            id="email"
            className="input"
            type="email"
            value={form.email}
            onChange={(e) => setForm({ ...form, email: e.target.value })}
            aria-invalid={Boolean(fieldErrors.email)}
            autoComplete="email"
            required
          />
          {fieldErrors.email ? <span className="err">{fieldErrors.email}</span> : null}
        </div>

        <div className="field">
          <label htmlFor="password">Password</label>
          <input
            id="password"
            className="input"
            type="password"
            value={form.password}
            onChange={(e) => setForm({ ...form, password: e.target.value })}
            aria-invalid={Boolean(fieldErrors.password)}
            autoComplete="new-password"
            minLength={8}
            required
          />
          {fieldErrors.password ? <span className="err">{fieldErrors.password}</span> : null}
          <span className="hint">At least 8 characters. Stored as a salted scrypt hash.</span>
        </div>

        <div className="field">
          <label htmlFor="pathway">Which exam are you preparing for?</label>
          <select
            id="pathway"
            className="select"
            value={form.examPathwayId}
            onChange={(e) => setForm({ ...form, examPathwayId: e.target.value })}
          >
            <option value="">Decide later</option>
            {pathways.map((p) => (
              <option key={p.id} value={p.id}>{p.name}</option>
            ))}
          </select>
        </div>

        <div className="split" style={{ gap: 10 }}>
          <div className="field">
            <label htmlFor="targetDate">Target exam date</label>
            <input
              id="targetDate"
              className="input"
              type="date"
              value={form.targetDate}
              onChange={(e) => setForm({ ...form, targetDate: e.target.value })}
            />
          </div>
          <div className="field">
            <label htmlFor="referralCode">Referral code (optional)</label>
            <input
              id="referralCode"
              className="input"
              value={form.referralCode}
              onChange={(e) => setForm({ ...form, referralCode: e.target.value })}
              placeholder="AMARA-2026"
            />
          </div>
        </div>

        <button className="btn lg block" disabled={busy} type="submit">
          {busy ? "Creating account…" : "Create account"}
        </button>
        <p className="tiny muted">
          By creating an account you agree to our terms and privacy notice. You can export or delete
          your data from settings.
        </p>
      </form>
    </AuthFrame>
  );
}
