"use client";

import { useState } from "react";
import { Loader2 } from "lucide-react";

import { ApiError } from "@edumart/shared";

import { Alert } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Field } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import { api } from "@/lib/browser-api";

export function SupportForm() {
  const [subject, setSubject] = useState("");
  const [category, setCategory] = useState("general");
  const [body, setBody] = useState("");
  const [sending, setSending] = useState(false);
  const [sent, setSent] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function submit(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setSending(true);
    setError(null);
    try {
      await api().openCase({ subject, category, body });
      setSent(true);
      setSubject("");
      setBody("");
    } catch (caught) {
      setError(caught instanceof ApiError ? caught.message : "We couldn't send that. Please try again.");
    } finally {
      setSending(false);
    }
  }

  if (sent) {
    return (
      <Alert tone="success" title="Request received">
        Our team has it. You&apos;ll get a reply at your registered email.
        <button type="button" className="mt-2 block text-xs font-semibold underline" onClick={() => setSent(false)}>
          Send another
        </button>
      </Alert>
    );
  }

  return (
    <form onSubmit={submit} className="space-y-4">
      {error ? <Alert tone="error">{error}</Alert> : null}

      <Field id="subject" label="What's the problem?" required>
        <Input
          id="subject"
          value={subject}
          onChange={(event) => setSubject(event.target.value)}
          maxLength={120}
          required
          placeholder="My mock ended early"
        />
      </Field>

      <Field id="category" label="Category">
        <Select id="category" value={category} onChange={(event) => setCategory(event.target.value)}>
          <option value="general">General</option>
          <option value="assessment">Assessment or score</option>
          <option value="payment">Payment or access</option>
          <option value="content">Question or content</option>
          <option value="account">Account</option>
        </Select>
      </Field>

      <Field id="body" label="Details" hint="Include what you expected and what happened.">
        <Textarea id="body" value={body} onChange={(event) => setBody(event.target.value)} maxLength={2000} />
      </Field>

      <Button type="submit" disabled={sending || subject.trim().length === 0}>
        {sending ? <Loader2 aria-hidden="true" className="animate-spin" /> : null}
        {sending ? "Sending…" : "Send request"}
      </Button>
    </form>
  );
}
