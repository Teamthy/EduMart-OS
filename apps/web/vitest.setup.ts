import { cleanup } from "@testing-library/react";
import { afterEach } from "vitest";

// Radix measures its triggers with ResizeObserver, which jsdom does not implement. Without this the
// component throws on mount and the accessibility run tests nothing at all — a false pass, which is
// worse than no check.
if (typeof globalThis.ResizeObserver === "undefined") {
  globalThis.ResizeObserver = class {
    observe() {}
    unobserve() {}
    disconnect() {}
  } as unknown as typeof ResizeObserver;
}

// Testing Library does not auto-clean under Vitest unless globals are enabled, and a leaked render
// makes queries match elements from a previous test.
afterEach(() => {
  cleanup();
});
