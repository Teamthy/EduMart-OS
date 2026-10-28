"use client";

import { useRouter } from "next/navigation";
import { useState } from "react";

export function SignOutButton() {
  const router = useRouter();
  const [busy, setBusy] = useState(false);

  return (
    <button
      type="button"
      className="btn ghost sm block"
      style={{ color: "rgba(231,241,234,0.8)", justifyContent: "flex-start", paddingLeft: 8 }}
      disabled={busy}
      onClick={async () => {
        setBusy(true);
        await fetch("/api/v1/auth/logout", { method: "POST", headers: { "content-type": "application/json" }, body: "{}" });
        router.push("/login");
        router.refresh();
      }}
    >
      <span aria-hidden>⏻</span> {busy ? "Signing out…" : "Sign out"}
    </button>
  );
}
