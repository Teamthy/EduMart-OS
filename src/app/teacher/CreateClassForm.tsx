"use client";

import { useRouter } from "next/navigation";
import { useState } from "react";

export function CreateClassForm({ subjects }: { subjects: { id: string; name: string }[] }) {
  const router = useRouter();
  const [name, setName] = useState("");
  const [subjectId, setSubjectId] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  return (
    <form
      className="stack-sm"
      onSubmit={async (e) => {
        e.preventDefault();
        setBusy(true);
        setError(null);
        const res = await fetch("/api/v1/classes", {
          method: "POST",
          headers: { "content-type": "application/json" },
          body: JSON.stringify({ name, subjectId: subjectId || null }),
        }).catch(() => null);
        const data = res ? await res.json().catch(() => ({})) : {};
        setBusy(false);
        if (!res || !res.ok) {
          setError(data?.error?.message ?? "Could not create that class.");
          return;
        }
        setName("");
        router.refresh();
        router.push(`/teacher/classes/${data.class.id}`);
      }}
    >
      <div className="field">
        <label htmlFor="className">Class name</label>
        <input id="className" className="input" placeholder="SS2 Science — Physics" value={name} onChange={(e) => setName(e.target.value)} required />
      </div>
      <div className="field">
        <label htmlFor="classSubject">Subject (optional)</label>
        <select id="classSubject" className="select" value={subjectId} onChange={(e) => setSubjectId(e.target.value)}>
          <option value="">Mixed</option>
          {subjects.map((s) => (
            <option key={s.id} value={s.id}>{s.name}</option>
          ))}
        </select>
      </div>
      {error ? <div className="banner error tiny">{error}</div> : null}
      <button className="btn block sm" type="submit" disabled={busy}>{busy ? "Creating…" : "Create class"}</button>
    </form>
  );
}
