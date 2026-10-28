import Link from "next/link";
import { getCurrentUser, type AuthUser } from "@/lib/server/auth";
import { SignOutButton } from "./SignOutButton";

type NavItem = { href: string; label: string; icon: string; match?: string };

const learnerNav: NavItem[] = [
  { href: "/dashboard", label: "Dashboard", icon: "◈" },
  { href: "/practice", label: "Practice", icon: "✎" },
  { href: "/exam", label: "Mock exams", icon: "⏱" },
  { href: "/classes", label: "Classes", icon: "👥" },
  { href: "/progress", label: "Progress", icon: "◔" },
  { href: "/library", label: "Library", icon: "▤" },
  { href: "/rewards", label: "Rewards", icon: "✦" },
];

const teacherNav: NavItem[] = [
  { href: "/teacher", label: "Overview", icon: "◈" },
  { href: "/classes", label: "Classes", icon: "👥" },
  { href: "/exam", label: "Assessments", icon: "⏱" },
  { href: "/library", label: "Library", icon: "▤" },
];

const adminNav: NavItem[] = [
  { href: "/admin", label: "Operations", icon: "⛨" },
  { href: "/admin#content", label: "Content review", icon: "▤" },
  { href: "/admin#orders", label: "Orders", icon: "₦" },
  { href: "/admin#audit", label: "Audit log", icon: "≡" },
];

export function navFor(user: AuthUser): NavItem[] {
  if (user.roles.includes("platform_admin") || user.roles.includes("content_editor")) return adminNav;
  if (user.roles.includes("teacher") || user.roles.includes("org_admin")) return teacherNav;
  return learnerNav;
}

export function initials(name: string): string {
  return name
    .split(/\s+/)
    .filter(Boolean)
    .slice(0, 2)
    .map((part) => part[0]?.toUpperCase() ?? "")
    .join("");
}

export function roleLabel(roles: string[]): string {
  if (roles.includes("platform_admin")) return "Platform admin";
  if (roles.includes("content_editor")) return "Content editor";
  if (roles.includes("org_admin")) return "Institution admin";
  if (roles.includes("teacher")) return "Teacher";
  return "Student";
}

export async function Shell({
  children,
  title,
  subtitle,
  action,
  activePath,
  wide,
}: {
  children: React.ReactNode;
  title: string;
  subtitle?: string;
  action?: React.ReactNode;
  activePath?: string;
  wide?: boolean;
}) {
  const user = await getCurrentUser();
  const nav = user ? navFor(user) : [];

  return (
    <div className="shell">
      <aside className="sidebar" aria-label="Primary">
        <Link href={user ? "/dashboard" : "/"} className="brand">
          <span className="brand-mark" aria-hidden>E</span>
          <span>
            <span className="brand-name">Edumart</span>
            <span className="brand-sub">Learn · Assess · Advance</span>
          </span>
        </Link>

        <nav className="nav-group">
          <span className="nav-label">Workspace</span>
          {nav.map((item) => (
            <Link
              key={item.href}
              href={item.href}
              className={`nav-link ${activePath === item.href ? "active" : ""}`}
            >
              <span className="ico" aria-hidden>{item.icon}</span>
              {item.label}
            </Link>
          ))}
          <span className="nav-label">Account</span>
          <Link href="/account" className={`nav-link ${activePath === "/account" ? "active" : ""}`}>
            <span className="ico" aria-hidden>☺</span> Settings
          </Link>
        </nav>

        <div className="sidebar-foot">
          {user ? (
            <>
              <div className="user-chip">
                <span className="avatar" aria-hidden>{initials(user.displayName)}</span>
                <span>
                  <span className="name">{user.displayName}</span>
                  <span className="role">{roleLabel(user.roles)}</span>
                </span>
              </div>
              <SignOutButton />
            </>
          ) : (
            <div className="btn-row">
              <Link className="btn gold block sm" href="/register">Create account</Link>
              <Link className="btn ghost block sm" href="/login" style={{ color: "#dfeee6" }}>Sign in</Link>
            </div>
          )}
        </div>
      </aside>

      <div className="main">
        <header className="topbar">
          <div>
            <h1>{title}</h1>
            {subtitle ? <div className="tiny muted">{subtitle}</div> : null}
          </div>
          <span className="spacer" />
          {action}
        </header>

        <main className="page" style={wide ? { maxWidth: 1320 } : undefined}>
          {children}
        </main>

        {user ? (
          <nav className="mobile-nav" aria-label="Mobile navigation">
            {nav.slice(0, 5).map((item) => (
              <Link key={item.href} href={item.href} className={activePath === item.href ? "active" : ""}>
                <span className="ico" aria-hidden>{item.icon}</span>
                <span>{item.label}</span>
              </Link>
            ))}
          </nav>
        ) : null}
      </div>
    </div>
  );
}
