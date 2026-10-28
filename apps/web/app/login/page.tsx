import type { Metadata } from "next";
import Link from "next/link";
import { redirect } from "next/navigation";

import { getCurrentUser } from "@/lib/session";
import { LoginForm } from "./login-form";

export const metadata: Metadata = { title: "Sign in" };

export default async function LoginPage({
  searchParams,
}: {
  searchParams: Promise<{ next?: string }>;
}) {
  const user = await getCurrentUser();
  const params = await searchParams;
  if (user) redirect(params.next && params.next.startsWith("/") ? params.next : "/dashboard");

  return (
    <div className="flex min-h-dvh flex-col">
      <header className="border-b border-border">
        <div className="container-page flex items-center justify-between py-4">
          <Link href="/" className="flex items-center gap-2">
            <span className="grid size-9 place-items-center rounded-lg bg-primary font-serif text-lg text-primary-foreground">E</span>
            <span className="font-serif text-lg">Edumart</span>
          </Link>
          <Link href="/register" className="text-sm font-semibold text-primary underline-offset-4 hover:underline">
            Create account
          </Link>
        </div>
      </header>

      <main id="main" className="container-page flex flex-1 items-start justify-center py-10 md:items-center">
        <div className="w-full max-w-md">
          <h1 className="mb-1 text-2xl">Welcome back</h1>
          <p className="mb-6 text-sm text-muted-foreground">Your progress, streak and saved attempts are waiting.</p>
          <LoginForm nextPath={params.next ?? "/dashboard"} />
          <p className="mt-6 text-center text-sm text-muted-foreground">
            New to Edumart?{" "}
            <Link href="/register" className="font-semibold text-primary underline-offset-4 hover:underline">
              Start with 5 quick steps
            </Link>
          </p>
        </div>
      </main>
    </div>
  );
}
