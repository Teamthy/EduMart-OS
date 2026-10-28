"use client";

import { useRouter } from "next/navigation";
import { useState } from "react";
import { Loader2 } from "lucide-react";

import { ApiError } from "@edumart/shared";

import { Alert } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { api } from "@/lib/browser-api";

export function StartAssignment({ definitionId, label }: { definitionId: string; label: string }) {
  const router = useRouter();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function start() {
    setBusy(true);
    setError(null);
    try {
      const { session } = await api().startDefinition(definitionId);
      router.push(`/attempt/${session.session_id}`);
    } catch (caught) {
      setError(caught instanceof ApiError ? caught.message : "We couldn't start that assessment.");
      setBusy(false);
    }
  }

  return (
    <div className="space-y-2">
      {error ? <Alert tone="error">{error}</Alert> : null}
      <Button onClick={() => void start()} disabled={busy} size="sm">
        {busy ? <Loader2 aria-hidden="true" className="animate-spin" /> : null}
        {busy ? "Starting…" : label}
      </Button>
    </div>
  );
}
