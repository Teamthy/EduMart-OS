# Accessibility: what has been checked, and what has not

The product claims WCAG 2.2 AA. A claim is not a control, so this document separates the three things that
are usually blurred together: **what a machine verified**, **what a person verified**, and **what nobody has
verified yet**. Right now the first column has grown and the second and third have not.

```bash
npm run test --workspace @edumart/web     # contrast maths + axe + static scans
```

## 1. Colour contrast — checked against the tokens

`apps/web/tests/a11y-contrast.test.ts` computes WCAG relative luminance from the real token values and fails
the build when a pair is below its threshold: **4.5:1** for anything that renders text (WCAG 1.4.3), **3:1**
for the visual information that identifies a control or a state (WCAG 1.4.11). It reads `:root` out of
`globals.css` and cross-checks it against `packages/shared/src/design-tokens.ts`, so the palette the test
reasons about is the palette the browser paints.

It cannot be done with axe. axe's `color-contrast` rule needs layout and computed styles, and the unit suite
runs in jsdom, which has neither — the rule is explicitly disabled in the axe suite rather than left to pass
vacuously. Doing the arithmetic directly is both stricter and cheaper.

### What it found

Seven real failures, all shipped, none of which any test or review had caught.

| Pair | Was | Now | Criterion |
| --- | --- | --- | --- |
| `amber` subject chip text on its own fill | **2.72:1** | 4.70:1 | 1.4.3 — this is the product type on every store card |
| Focus ring against a filled primary button | **2.24:1** | n/a — see below | 1.4.11 |
| Field / checkbox / radio border on a card | **1.55:1** | 3.61:1 | 1.4.11 — the border is the *only* boundary of a text field |
| Muted text on a muted panel | **4.39:1** | 5.12:1 | 1.4.3 — a near miss, invisible until muted text lands on a muted panel |
| `warning` icon on the page | **2.86:1** | 4.96:1 | 1.4.11 |
| `teal` subject chip text | **4.24:1** | 4.66:1 | 1.4.3 |
| `emerald` subject chip text | **4.46:1** | 4.77:1 | 1.4.3 |

The focus ring is the interesting one, because the honest answer is that **the colours were fine and the
styling was doing the work**. `outline-offset: 2px` puts the ring on the page behind the button (3.55:1)
rather than on the button's own fill, with a visible gap. That is a legitimate WCAG-compliant technique —
but it means the indicator's compliance depends on a class name that looks like decoration. Removing
`focus-visible:outline-offset-2` from `Button` would silently drop it to 2.24:1, so a test now asserts the
offset is present rather than trusting that nobody tidies it away.

Two design tokens changed shape as a result:

- `--warning` darkened to `#936106`, because it carries text on the amber chip as well as icons, so it has to
  clear the text ratio rather than the icon one. Its foreground flipped from ink to white, which is what that
  darkness requires.
- `--input` darkened from `#d3cfc6` to `#8f8672`. This is a visible change to every form on the site: fields
  now have a boundary you can actually see. It is the fix WCAG 1.4.11 asks for, and the alternative is a
  form that fails for anyone with low vision or a bad screen in sunlight.

### The drift the check also caught

The same test immediately failed on `--success`, because `globals.css` still held the old `emerald600` after
the palette moved. That is precisely the failure mode of two hand-maintained copies of one palette, and it is
now impossible to ship.

Component-local colour literals are the other half of that problem: `Alert` and the offline banner carried
their own five hardcoded hex triples, invisible to any token check. They now read `statusTones` from the
shared package, so the banners are measured too.

## 2. Structure — checked with axe

`apps/web/tests/a11y-axe.test.tsx` runs axe-core against real rendered markup: the primitives, the app shell,
the login form, the signup stepper and a live attempt.

### What it found

- **The question navigator in a live attempt was a broken list.** It used `role="listitem"` on `<button>`,
  which is not an allowed role for that element; screen-reader users got a list whose items were not
  announced as items. It is now a real `<ul>` of `<li>` wrapping the button — and this is the highest-stakes
  screen in the product, used under time pressure by someone who cannot afford to lose their place.
- **The desktop sidebar and the mobile tab bar were both labelled "Main".** They are never on screen
  together, but only because one is `display: none` at each breakpoint. Relying on CSS to keep two landmarks
  distinguishable is fragile, so the tab bar is now "Tab bar".

It also asserts the WCAG 2.2 target-size minimum (SC 2.5.8, 24×24) against the button variants, because
the small ones are where a future "compact" size would quietly drop below the floor.

Plus static scans for the defects that no markup test can see: a bare `outline-none` (the ring only exists in
a pseudo-class axe never enters), a missing `lang`, and the offset that keeps the focus ring legible.

## 3. What none of this proves

This is the part that matters, and it is the part still missing.

- **No screen-reader pass.** Nothing has been read with NVDA, JAWS, TalkBack or VoiceOver. A tree that axe
  accepts can still be unusable: announcements in the wrong order, a live region that fires on every
  keystroke, a timer that interrupts the question.
- **No keyboard-only pass.** Nothing has been driven with the keyboard alone through signup, checkout or a
  timed attempt. Focus order, focus trapping in dialogs and "can I reach submit without a mouse" are all
  unverified.
- **No zoom or reflow check.** 200% zoom and 320 CSS-pixel reflow are untested. `WCAG 1.4.10` failures are
  layout failures, and layout is exactly what jsdom does not have.
- **No real device.** Nothing has been run on a low-end Android phone on a 3G connection — the device most
  learners will actually use, and the one where a 2.2 MB first load and a 300 ms input delay live.
- **No mobile-app audit.** `apps/mobile` shares the tokens and the touch-target guidance, but no accessibility
  tool has been run against it.
- **No user testing with assistive-technology users.** The only way to find out whether this is usable.

## The device matrix: not started

Phase 29's other exit criterion is a real device matrix. It is not started, and pretending a jsdom run
covers it would be the same dishonesty this document exists to avoid. The minimum credible matrix:

| Axis | Minimum | Why |
| --- | --- | --- |
| Low-end Android (≤ 2 GB RAM, 3G throttled) | 2 devices | The median learner's device, not a flagship |
| Mid Android | 1 device | Chrome/Chrome-like, current |
| iOS | 1 device | Safari's behaviour differs in the ways that break timed attempts |
| Screen reader | 1 Android (TalkBack), 1 iOS (VoiceOver), 1 desktop (NVDA) | Each announces live regions differently |
| Zoom / text size | 200% and largest OS text size | Layout and truncation failures |
| Network | 3G, and offline mid-attempt | Autosave and the offline banner claim to handle this |

None of these rows has been run.
