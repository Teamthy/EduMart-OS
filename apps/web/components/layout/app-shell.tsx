import Link from "next/link";
import {
  BarChart3,
  Building2,
  BookOpen,
  ClipboardList,
  Flame,
  GraduationCap,
  Home,
  Library,
  Radio,
  Search,
  Settings,
  Shield,
  Sparkles,
  Users,
  Wallet,
} from "lucide-react";

import type { Role, User } from "@edumart/shared";
import { cn } from "@/lib/utils";
import { OfflineBanner } from "@/components/layout/offline-banner";
import { Badge } from "@/components/ui/badge";

interface NavItem {
  href: string;
  label: string;
  icon: typeof Home;
  /** Shown in the mobile tab bar (max five). */
  primary?: boolean;
  roles?: Role[];
}

const NAV: NavItem[] = [
  { href: "/dashboard", label: "Home", icon: Home, primary: true },
  { href: "/practice", label: "Practice", icon: Sparkles, primary: true },
  { href: "/progress", label: "Progress", icon: BarChart3, primary: true },
  { href: "/library", label: "Library", icon: Library, primary: true },
  { href: "/settings", label: "Settings", icon: Settings, primary: true },
  { href: "/search", label: "Search", icon: Search },
  { href: "/classes", label: "Classes", icon: Users },
  { href: "/live", label: "Live classes", icon: Radio },
  { href: "/exams", label: "Exams", icon: ClipboardList },
  { href: "/consistency", label: "Consistency", icon: Flame },
  { href: "/institutions", label: "Institutions", icon: Building2 },
  { href: "/teacher", label: "Teaching", icon: GraduationCap, roles: ["teacher", "org_admin", "platform_admin"] },
  { href: "/admin", label: "Operations", icon: Shield, roles: ["content_editor", "platform_admin", "support", "finance"] },
  { href: "/rewards", label: "Rewards", icon: BookOpen },
  { href: "/wallet", label: "Wallet", icon: Wallet },
];

function visibleFor(user: User): NavItem[] {
  return NAV.filter(
    (item) =>
      !item.roles ||
      user.roles.includes("platform_admin") ||
      item.roles.some((role) => user.roles.includes(role)),
  );
}

export function AppShell({ user, children }: { user: User; children: React.ReactNode }) {
  const items = visibleFor(user);
  const primary = items.filter((item) => item.primary).slice(0, 5);
  const secondary = items.filter((item) => !item.primary);

  return (
    <div className="min-h-dvh bg-background">
      <OfflineBanner />
      <div className="mx-auto flex w-full max-w-[1400px]">
        {/* Desktop sidebar */}
        <aside className="sticky top-0 hidden h-dvh w-64 shrink-0 flex-col border-r border-border bg-card/60 p-4 lg:flex">
          <Link href="/dashboard" className="mb-6 flex items-center gap-2 px-2 py-1">
            <span className="grid size-9 place-items-center rounded-lg bg-primary font-serif text-lg text-primary-foreground">E</span>
            <span className="font-serif text-lg tracking-tight">Edumart</span>
          </Link>

          <nav aria-label="Main" className="flex-1 space-y-1">
            {items.map((item) => (
              <Link
                key={item.href}
                href={item.href}
                className="flex items-center gap-3 rounded-lg px-3 py-2.5 text-sm font-medium text-foreground/80 transition-colors hover:bg-secondary hover:text-secondary-foreground"
              >
                <item.icon aria-hidden="true" className="size-4" />
                {item.label}
              </Link>
            ))}
          </nav>

          <div className="mt-4 rounded-lg border border-border bg-card p-3">
            <p className="truncate text-sm font-semibold">{user.displayName}</p>
            <p className="truncate text-xs text-muted-foreground">{user.email}</p>
            {user.roles.includes("platform_admin") ? (
              <Badge variant="success" className="mt-2">
                Staff
              </Badge>
            ) : null}
            <form action="/api/session/logout" method="post" className="mt-3">
              <button type="submit" className="text-xs font-semibold text-primary underline-offset-4 hover:underline">
                Sign out
              </button>
            </form>
          </div>
        </aside>

        <div className="flex min-w-0 flex-1 flex-col pb-20 lg:pb-0">
          {/* Mobile header */}
          <header className="sticky top-0 z-20 flex items-center justify-between gap-3 border-b border-border bg-background/95 px-4 py-3 backdrop-blur lg:hidden">
            <Link href="/dashboard" className="flex items-center gap-2">
              <span className="grid size-8 place-items-center rounded-lg bg-primary font-serif text-base text-primary-foreground">E</span>
              <span className="font-serif text-base">Edumart</span>
            </Link>
            <Link href="/settings" className="text-sm font-medium text-primary">
              {user.displayName.split(" ")[0]}
            </Link>
          </header>

          <main id="main" className="container-page flex-1 py-6">
            {children}
          </main>

          {secondary.length > 0 ? (
            <nav aria-label="Secondary" className="container-page pb-6">
              <ul className="flex flex-wrap gap-2">
                {secondary.map((item) => (
                  <li key={item.href}>
                    <Link
                      href={item.href}
                      className="inline-flex items-center gap-2 rounded-full border border-border bg-card px-3 py-1.5 text-xs font-semibold text-foreground/80"
                    >
                      <item.icon aria-hidden="true" className="size-3.5" />
                      {item.label}
                    </Link>
                  </li>
                ))}
              </ul>
            </nav>
          ) : null}
        </div>
      </div>

      {/* Mobile tab bar: five destinations, 44px targets.
          Deliberately *not* labelled "Main" like the sidebar. The two are never on screen together, but
          only because one is `display: none` at each breakpoint — relying on CSS to keep two landmarks
          apart is fragile, and if it ever regressed a screen-reader user would hear "navigation" twice
          with nothing to tell them apart. */}
      <nav aria-label="Tab bar" className="fixed inset-x-0 bottom-0 z-30 border-t border-border bg-card/95 backdrop-blur lg:hidden">
        <ul className="grid grid-cols-5">
          {primary.map((item) => (
            <li key={item.href}>
              <Link
                href={item.href}
                className={cn(
                  "flex min-h-14 flex-col items-center justify-center gap-1 text-[11px] font-medium text-muted-foreground",
                  "active:text-primary",
                )}
              >
                <item.icon aria-hidden="true" className="size-5" />
                {item.label}
              </Link>
            </li>
          ))}
        </ul>
      </nav>
    </div>
  );
}
