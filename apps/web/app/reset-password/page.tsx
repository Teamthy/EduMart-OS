import type { Metadata } from "next";
import Link from "next/link";

import { ResetForm } from "./reset-form";

export const metadata: Metadata = { title: "Choose a new password" };

export default async function ResetPasswordPage({ searchParams }: { searchParams: Promise<{ token?: string }> }) {
  const { token } = await searchParams;

  return (
    <div className="flex min-h-dvh flex-col">
      <header className="border-b border-border">
        <div className="container-page flex items-center justify-between py-4">
          <Link href="/" className="flex items-center gap-2">
            <span className="grid size-9 place-items-center rounded-lg bg-primary font-serif text-lg text-primary-foreground">E</span>
            <span className="font-serif text-lg">Edumart</span>
          </Link>
        </div>
      </header>
      <main id="main" className="container-page flex flex-1 items-start justify-center py-10">
        <div className="w-full max-w-md">
          <h1 className="mb-1 text-2xl">Choose a new password</h1>
          <p className="mb-6 text-sm text-muted-foreground">
            Setting a new password signs you out everywhere else.
          </p>
          <ResetForm token={token ?? ""} />
        </div>
      </main>
    </div>
  );
}
