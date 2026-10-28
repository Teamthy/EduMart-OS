import Link from "next/link";
import type { Metadata } from "next";

export const metadata: Metadata = { title: "Create your account" };

export default function RegisterLayout({ children }: { children: React.ReactNode }) {
  return (
    <div className="flex min-h-dvh flex-col bg-background">
      <header className="border-b border-border">
        <div className="container-page flex items-center justify-between py-4">
          <Link href="/" className="flex items-center gap-2">
            <span className="grid size-9 place-items-center rounded-lg bg-primary font-serif text-lg text-primary-foreground">E</span>
            <span className="font-serif text-lg">Edumart</span>
          </Link>
          <Link href="/login" className="text-sm font-semibold text-primary underline-offset-4 hover:underline">
            Sign in instead
          </Link>
        </div>
      </header>
      <main id="main" className="container-page w-full max-w-2xl flex-1 py-8">
        {children}
      </main>
      <footer className="container-page py-6 text-center text-xs text-muted-foreground">
        Your answers are saved on our server as you go — you can close this and continue on your phone.
      </footer>
    </div>
  );
}
