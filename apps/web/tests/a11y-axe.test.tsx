import { readFileSync, readdirSync } from "node:fs";
import path from "node:path";

import { render, screen } from "@testing-library/react";
import axe from "axe-core";
import type { ReactElement } from "react";
import { describe, expect, it, vi } from "vitest";

import type { AssessmentSessionView } from "@edumart/shared";

vi.mock("next/navigation", () => ({
  useRouter: () => ({ replace: vi.fn(), refresh: vi.fn(), push: vi.fn() }),
}));

import type { User } from "@edumart/shared";

import { LoginForm } from "@/app/login/login-form";
import { AppShell } from "@/components/layout/app-shell";
import { AttemptRunner } from "@/components/attempt/attempt-runner";
import { SignupStepper } from "@/components/signup/stepper";
import { Alert } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button, buttonVariants } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { EmptyState } from "@/components/ui/empty-state";
import { Field } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Progress } from "@/components/ui/progress";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import { Select } from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";

/**
 * Structural accessibility, checked by axe against real rendered markup.
 *
 * What this can and cannot see is the point, so it is written down rather than implied:
 *
 * - **It cannot check colour.** axe's `color-contrast` rule needs layout and computed styles, and jsdom
 *   has neither — the rule is disabled here on purpose instead of being left to pass vacuously. Contrast
 *   is checked directly against the tokens in `a11y-contrast.test.ts`.
 * - **It cannot check that anything is usable with a keyboard or a screen reader.** A passing axe run on
 *   a div soup is a passing axe run. The manual passes live in `docs/ACCESSIBILITY.md` and are not done.
 *
 * What it does catch is the class of defect that appears silently in a component library: an input with
 * no accessible name, a form control whose label points at an id that does not exist, a live region with
 * no role, an icon-only button, a skipped heading level, a duplicated id.
 */

const RULES_NEEDING_LAYOUT = { "color-contrast": { enabled: false } };

async function violations(element: ReactElement): Promise<string[]> {
  const { container } = render(element);
  const results = await axe.run(container, { rules: RULES_NEEDING_LAYOUT });
  return results.violations.map((violation) => {
    const targets = violation.nodes.map((node) => node.target.join(" ")).join(", ");
    return `${violation.id} (${violation.impact}): ${violation.help} — ${targets}`;
  });
}

async function expectClean(element: ReactElement) {
  expect(await violations(element)).toEqual([]);
}

describe("the primitives are structurally sound", () => {
  it("renders buttons", async () => {
    await expectClean(
      <div>
        <Button>Save answer</Button>
        <Button variant="outline" size="sm">
          Cancel
        </Button>
        <Button variant="destructive">Delete</Button>
      </div>,
    );
  });

  it("renders a labelled field with an inline error", async () => {
    // The error path is where label/description wiring usually breaks: aria-invalid with no
    // aria-describedby leaves a screen-reader user with an error they are never told about.
    await expectClean(
      <form>
        <Field id="email" label="Email address" hint="We send your receipt here" required>
          <Input id="email" type="email" autoComplete="email" aria-invalid="true" aria-describedby="email-error" />
        </Field>
        <Field id="password" label="Password" error="Password is too short">
          <Input id="password" type="password" aria-invalid="true" aria-describedby="password-error" />
        </Field>
      </form>,
    );
  });

  it("renders the rest of the form controls", async () => {
    await expectClean(
      <form>
        <Field id="subject" label="Subject">
          <Select id="subject" defaultValue="maths">
            <option value="maths">Mathematics</option>
            <option value="english">English</option>
          </Select>
        </Field>
        <Field id="notes" label="Notes" hint="Optional">
          <Textarea id="notes" />
        </Field>
        <div className="flex items-center gap-2">
          <Checkbox id="terms" />
          <label htmlFor="terms">I accept the terms</label>
        </div>
        <RadioGroup defaultValue="jamb" aria-label="Exam pathway">
          <div className="flex items-center gap-2">
            <RadioGroupItem value="jamb" id="jamb" />
            <label htmlFor="jamb">JAMB</label>
          </div>
          <div className="flex items-center gap-2">
            <RadioGroupItem value="waec" id="waec" />
            <label htmlFor="waec">WAEC</label>
          </div>
        </RadioGroup>
      </form>,
    );
  });

  it("renders status, badges and progress", async () => {
    await expectClean(
      <div>
        <Alert tone="error" title="Payment failed">
          Your card was declined.
        </Alert>
        <Alert tone="success" title="Saved">
          Your progress is stored.
        </Alert>
        <Alert tone="offline" title="You are offline">
          Answers are saved on this device.
        </Alert>
        <Badge variant="success">Owned</Badge>
        <Badge variant="outline">Digital</Badge>
        <Progress value={60} aria-label="Attempt progress" />
        <EmptyState title="Nothing here yet" description="Published titles appear here." />
      </div>,
    );
  });

  it("renders the signup stepper", async () => {
    await expectClean(<SignupStepper current="schedule" />);
    expect(screen.getByText(/step 3 of 5/i)).toBeDefined();
  });
});

const learner: User = {
  id: "usr_1",
  email: "amara@example.com",
  displayName: "Amara Okafor",
  status: "active",
  examPathwayId: "pw_1",
  targetDate: null,
  timezone: "Africa/Lagos",
  dailyMinutes: 30,
  onboardingStep: 3,
  onboardingComplete: true,
  roles: ["student", "platform_admin"],
  subjectIds: ["sub_1"],
  phone: null,
  emailVerified: true,
  phoneVerified: false,
  createdAt: new Date().toISOString(),
};

describe("the shell every page sits inside is structurally sound", () => {
  it("renders the navigation, landmarks and account block", async () => {
    await expectClean(
      <AppShell user={learner}>
        <h1>Dashboard</h1>
        <p>Welcome back.</p>
      </AppShell>,
    );
    // Two navs with distinct names, because a screen reader announces the landmark label and "navigation"
    // twice tells the user nothing about which is which.
    expect(screen.getByRole("navigation", { name: "Main" })).toBeDefined();
    expect(screen.getByRole("navigation", { name: "Secondary" })).toBeDefined();
  });
});

describe("the screens that take money or answers are structurally sound", () => {
  it("renders the login form", async () => {
    await expectClean(<LoginForm nextPath="/dashboard" />);
  });

  it("renders a live attempt", async () => {
    const session: AssessmentSessionView = {
      session_id: "sess_1",
      mode: "practice",
      status: "in_progress",
      attempt_no: 1,
      started_at: new Date().toISOString(),
      deadline_at: new Date(Date.now() + 600_000).toISOString(),
      submitted_at: null,
      server_time: new Date().toISOString(),
      question_count: 1,
      result_released: false,
      remaining_seconds: 600,
      score: null,
      questions: [
        {
          qvId: "qv_1",
          stem: "What is 2 + 2?",
          options: [
            { id: "opt_1", label: "A. 3" },
            { id: "opt_2", label: "B. 4" },
          ],
          position: 1,
          selectedOptionId: null,
          answerVersion: 0,
        },
      ],
    };
    globalThis.fetch = vi.fn(async () => ({ ok: true, status: 200, text: async () => "{}" })) as unknown as typeof fetch;
    await expectClean(<AttemptRunner session={session} />);
  });
});

describe("nothing in the tree silently removes the focus outline", () => {
  // Removing the outline is the single most common way a design system breaks keyboard navigation, and it
  // is invisible to axe: the ring only exists in a pseudo-class axe never enters. So the source is scanned.
  const sources = (function walk(dir: string): string[] {
    return readdirSync(dir, { withFileTypes: true }).flatMap((entry) => {
      const full = path.join(dir, entry.name);
      if (entry.isDirectory()) return walk(full);
      return entry.name.endsWith(".tsx") ? [full] : [];
    });
  })(path.resolve(__dirname, "..", "components")).concat(
    (function walk(dir: string): string[] {
      return readdirSync(dir, { withFileTypes: true }).flatMap((entry) => {
        const full = path.join(dir, entry.name);
        if (entry.isDirectory()) return walk(full);
        return entry.name.endsWith(".tsx") ? [full] : [];
      });
    })(path.resolve(__dirname, "..", "app")),
  );

  it("finds the component files it is supposed to scan", () => {
    // A scan over an empty list passes for the wrong reason.
    expect(sources.length).toBeGreaterThan(20);
  });

  it("has no bare outline-none", () => {
    const offenders = sources.filter((file) => {
      const text = readFileSync(file, "utf8");
      // `outline-none` is only acceptable next to a visible replacement (a ring or a shadow).
      return [...text.matchAll(/outline-none/g)].some((match) => {
        const after = text.slice(match.index! - 200, match.index! + 200);
        return !/ring-|focus-visible:outline|shadow-/.test(after);
      });
    });
    expect(offenders.map((file) => path.relative(process.cwd(), file))).toEqual([]);
  });

  it("declares the document language", () => {
    const layout = readFileSync(path.resolve(__dirname, "..", "app", "layout.tsx"), "utf8");
    // A missing or wrong `lang` makes a screen reader pronounce Nigerian English with the wrong phoneme
    // table — the failure is subtle, universal and never reported by a sighted reviewer.
    expect(layout).toMatch(/<html lang="en-NG"/);
  });
});

describe("buttons clear the WCAG 2.2 target-size minimum", () => {
  // SC 2.5.8 asks for 24×24 CSS px. The design system aims at 44px, but the *small* variants are where a
  // future "compact" size would quietly drop below the floor, so the floor is asserted rather than assumed.
  it.each(["default", "sm", "lg", "icon"] as const)("%s is at least 24px tall", (size) => {
    const classes = buttonVariants({ size });
    const height = /(?:^|\s)h-(\d+)(?:\s|$)/.exec(classes);
    expect(height, `no explicit height in the ${size} variant`).not.toBeNull();
    expect(Number(height![1]) * 4).toBeGreaterThanOrEqual(24);
  });
});

describe("the focus indicator survives the component's own styling", () => {
  // Not an axe rule, and it matters more than most of them. The ring colour (#1a9a73) against the
  // primary button's fill (#0d5c46) is 2.24:1 — below the 3:1 WCAG 1.4.11 wants. It passes today only
  // because the outline is offset by 2px, so the ring sits on the page/card behind the button (3.55:1)
  // with a visible gap, rather than on the button. Remove the offset and the indicator quietly fails.
  it("keeps the outline offset that makes the focus ring legible", () => {
    const { container } = render(<Button>Continue</Button>);
    const button = container.querySelector("button")!;
    expect(button.className).toContain("focus-visible:outline-2");
    expect(button.className).toContain("focus-visible:outline-offset-2");
  });
});
