import { cleanup } from "@testing-library/react";
import { afterEach } from "vitest";

// Testing Library does not auto-clean under Vitest unless globals are enabled, and a leaked render
// makes queries match elements from a previous test.
afterEach(() => {
  cleanup();
});
