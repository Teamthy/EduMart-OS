import Link from "next/link";
import { ArrowRight, Clock, ShieldCheck, Sparkles, TrendingUp } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { getCurrentUser } from "@/lib/session";

export default async function LandingPage() {
  const user = await getCurrentUser();

  return (
    <div className="min-h-dvh bg-background">
      <header className="border-b border-border">
        <div className="container-page flex items-center justify-between py-4">
          <Link href="/" className="flex items-center gap-2">
            <span className="grid size-9 place-items-center rounded-lg bg-primary font-serif text-lg text-primary-foreground">E</span>
            <span className="font-serif text-lg">Edumart</span>
          </Link>
          <nav className="flex items-center gap-2">
            {user ? (
              <Button asChild size="sm">
                <Link href="/dashboard">Open dashboard</Link>
              </Button>
            ) : (
              <>
                <Button asChild variant="ghost" size="sm">
                  <Link href="/login">Sign in</Link>
                </Button>
                <Button asChild size="sm">
                  <Link href="/register">Get started</Link>
                </Button>
              </>
            )}
          </nav>
        </div>
      </header>

      <main id="main">
        <section className="container-page grid gap-10 py-12 md:grid-cols-2 md:items-center md:py-20">
          <div className="space-y-5">
            <p className="inline-flex items-center gap-2 rounded-full border border-border bg-card px-3 py-1 text-xs font-semibold text-primary">
              <Sparkles aria-hidden="true" className="size-3.5" />
              Built for JAMB, WAEC and NECO
            </p>
            <h1 className="text-4xl leading-[1.1] md:text-5xl">
              Practice that respects your data, your time and your exam date.
            </h1>
            <p className="max-w-prose text-base text-muted-foreground">
              Timed mocks that end when the clock ends — not when the tab closes. Answers save the
              moment you tap, so a dropped connection never costs you a mark. Every score is
              verifiable, every question is reviewed, and your results are yours.
            </p>
            <div className="flex flex-wrap gap-3">
              <Button asChild size="lg">
                <Link href="/register">
                  Start free <ArrowRight aria-hidden="true" />
                </Link>
              </Button>
              <Button asChild size="lg" variant="outline">
                <Link href="/login">I have an account</Link>
              </Button>
            </div>
            <p className="text-xs text-muted-foreground">
              Demo accounts for reviewers: <strong>student@edumart.ng</strong> · Student123!
            </p>
          </div>

          <div className="grid gap-4 sm:grid-cols-2">
            {[
              { icon: Clock, title: "Server-owned clock", copy: "The deadline is set and enforced by the server, with a 5-second grace window for slow networks." },
              { icon: ShieldCheck, title: "Answer keys withheld", copy: "Correct answers and explanations are only sent after the attempt is closed and released." },
              { icon: Sparkles, title: "Answers that never lose", copy: "Every save is versioned and idempotent. The Saved badge means the server has it." },
              { icon: TrendingUp, title: "Progress you can trust", copy: "Topic mastery is derived from your responses, not self-reported." },
            ].map((item) => (
              <Card key={item.title}>
                <CardContent className="space-y-2 p-5">
                  <item.icon aria-hidden="true" className="size-5 text-primary" />
                  <p className="font-semibold">{item.title}</p>
                  <p className="text-sm text-muted-foreground">{item.copy}</p>
                </CardContent>
              </Card>
            ))}
          </div>
        </section>

        <section className="border-t border-border bg-card/50">
          <div className="container-page grid gap-6 py-10 sm:grid-cols-3">
            <div>
              <p className="font-serif text-2xl">50</p>
              <p className="text-sm text-muted-foreground">reviewed questions across six subjects at launch</p>
            </div>
            <div>
              <p className="font-serif text-2xl">Two shells, one product</p>
              <p className="text-sm text-muted-foreground">Installable PWA and an Android/iOS app sharing one API</p>
            </div>
            <div>
              <p className="font-serif text-2xl">WCAG 2.2 AA</p>
              <p className="text-sm text-muted-foreground">Keyboard-first, screen-reader labelled, 44px targets</p>
            </div>
          </div>
        </section>
      </main>

      <footer className="border-t border-border">
        <div className="container-page flex flex-wrap items-center justify-between gap-3 py-6 text-xs text-muted-foreground">
          <p>© {new Date().getFullYear()} Edumart. Lagos, Nigeria.</p>
          <nav className="flex gap-4">
            <Link href="/login" className="hover:underline">Sign in</Link>
            <Link href="/register" className="hover:underline">Create account</Link>
            <Link href="/offline" className="hover:underline">Offline help</Link>
          </nav>
        </div>
      </footer>
    </div>
  );
}
