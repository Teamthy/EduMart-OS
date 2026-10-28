"use client";

import { useRouter } from "next/navigation";
import { useState } from "react";
import { Loader2 } from "lucide-react";

import { ApiError } from "@edumart/shared";

import { Alert } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { api } from "@/lib/browser-api";

export function ReviewActions({ qvId }: { qvId: string }) {
  const router = useRouter();
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState<"approve" | "reject" | null>(null);
  const [error, setError] = useState<string | null>(null);

  async function decide(decision: "approve" | "reject") {
    setBusy(decision);
    setError(null);
    try {
      await api().reviewQuestion(qvId, decision, reason || "Reviewed against the syllabus.");
      router.refresh();
    } catch (caught) {
      setError(caught instanceof ApiError ? caught.message : "That decision could not be saved.");
    } finally {
      setBusy(null);
    }
  }

  return (
    <div className="space-y-2">
      {error ? <Alert tone="error">{error}</Alert> : null}
      <Input
        value={reason}
        onChange={(event) => setReason(event.target.value)}
        placeholder="Reason (recorded in the audit log)"
        aria-label="Review reason"
        className="h-9 text-sm"
      />
      <div className="flex gap-2">
        <Button size="sm" onClick={() => void decide("approve")} disabled={busy !== null}>
          {busy === "approve" ? <Loader2 aria-hidden="true" className="animate-spin" /> : null}
          Approve and publish
        </Button>
        <Button size="sm" variant="outline" onClick={() => void decide("reject")} disabled={busy !== null}>
          {busy === "reject" ? <Loader2 aria-hidden="true" className="animate-spin" /> : null}
          Reject
        </Button>
      </div>
    </div>
  );
}
