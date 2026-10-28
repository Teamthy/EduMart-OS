"use client";

import { useRouter } from "next/navigation";
import { useState } from "react";
import { Loader2 } from "lucide-react";

import { ApiError } from "@edumart/shared";

import { Button } from "@/components/ui/button";
import { api } from "@/lib/browser-api";

export function CaseActions({ caseId }: { caseId: string }) {
  const router = useRouter();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function resolve() {
    setBusy(true);
    setError(null);
    try {
      await api().resolveCase(caseId);
      router.refresh();
    } catch (caught) {
      setError(caught instanceof ApiError ? caught.message : "Could not resolve the case.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="flex items-center gap-2">
      {error ? <span className="text-xs text-destructive">{error}</span> : null}
      <Button size="sm" variant="outline" onClick={() => void resolve()} disabled={busy}>
        {busy ? <Loader2 aria-hidden="true" className="animate-spin" /> : null}
        Resolve
      </Button>
    </div>
  );
}
