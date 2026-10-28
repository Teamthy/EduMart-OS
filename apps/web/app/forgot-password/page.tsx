import type { Metadata } from "next";
import Link from "next/link";

import { ForgotForm } from "./forgot-form";

export const metadata: Metadata = { title: "Reset your password" };

export default function ForgotPasswordPage() {
  return (
    <div className="flex min-h-dvh flex-col">
      <header className="border-b border-border">
        <div className="container-page flex items-center justify-between py-4">
          <Link href="/" className="flex items-center gap-2">
            <span className="grid size-9 place-items-center rounded-lg bg-primary font-serif text-lg text-primary-foreground">E</span>
            <span className="font-serif text-lg">Edumart</span>
          </Link>
          <Link href="/login" className="text-sm font-semibold text-primary underline-offset-4 hover:underline">
            Back to sign in
          </Link>
        </div>
      </header>
      <main id="main" className="container-page flex flex-1 items-start justify-center py-10">
        <div className="w-full max-w-md">
          <h1 className="mb-1 text-2xl">Reset your password</h1>
          <p className="mb-6 text-sm text-muted-foreground">
            Enter the email on your account. If it exists, we&apos;ll send a single-use link.
          </p>
          <ForgotForm />
        </div>
      </main>
    </div>
  );
}
