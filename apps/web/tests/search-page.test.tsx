import { render, screen } from "@testing-library/react";
import axe from "axe-core";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { SearchResponse } from "@edumart/shared";

// The page is a server component: it calls the API on the server and forwards cookies. Both are mocked,
// so what is under test is the rendering decision — including the one that matters most, which is that
// "we found nothing" and "we could not look" are never the same screen.
const fetchMock = vi.fn();
vi.mock("@/lib/session", () => ({
  requireUser: vi.fn(async () => ({ id: "usr_1", displayName: "Amara" })),
}));
vi.mock("@/lib/server-api", () => ({
  ServerApiError: class ServerApiError extends Error {
    isNotFound = false;
    isUnauthenticated = false;
  },
  serverFetch: (...args: unknown[]) => fetchMock(...args),
}));

import SearchPage from "@/app/(app)/search/page";

function page(q?: string) {
  return SearchPage({ searchParams: Promise.resolve({ q }) });
}

const withResults: SearchResponse = {
  query: "algebra",
  terms: ["algebra"],
  count: 2,
  took_ms: 3,
  results: [
    {
      type: "topic",
      id: "top_1",
      title: "Algebra & Equations",
      subtitle: "Mathematics",
      href: "/practice/sub_1?topic=top_1",
      score: 8,
    },
    {
      type: "product",
      id: "prod_1",
      title: "JAMB Mathematics Mastery",
      subtitle: "Workbook",
      snippet: "…algebra and geometry in one volume…",
      href: "/checkout/new?productId=prod_1",
      score: 4,
    },
  ],
};

describe("the search page", () => {
  // A block body, not an arrow that returns the mock: Vitest treats a returned function from a hook as a
  // teardown callback, so `beforeEach(() => mock.mockReset())` quietly ends up *calling the mock* after
  // every test — which is how a rejection in one test surfaces as a failure attributed to another.
  beforeEach(() => {
    fetchMock.mockReset();
  });

  it("does not call the API until there is something to search for", async () => {
    // An empty box that hits the API on every visit is a bill with no user-visible benefit.
    render(await page());
    expect(fetchMock).not.toHaveBeenCalled();
    expect(screen.queryByRole("heading", { name: /^search$/i })).toBeDefined();
  });

  it("renders results with a link each, and the kind of thing they are", async () => {
    fetchMock.mockResolvedValue(withResults);
    render(await page("algebra"));

    expect(fetchMock).toHaveBeenCalledWith("/v1/search?q=algebra");
    const links = screen.getAllByRole("link", { name: /open/i });
    expect(links.map((link) => link.getAttribute("href"))).toEqual([
      "/practice/sub_1?topic=top_1",
      "/checkout/new?productId=prod_1",
    ]);
    expect(screen.getByText("Algebra & Equations")).toBeDefined();
    // The snippet is shown when the body matched, so a result explains why it is there.
    expect(screen.getByText(/algebra and geometry in one volume/)).toBeDefined();
    expect(screen.getByText(/2 results for “algebra”/)).toBeDefined();
  });

  it("reports an empty result set as an answer, not a failure", async () => {
    fetchMock.mockResolvedValue({ ...withResults, query: "zzzz", terms: ["zzzz"], results: [], count: 0 });
    render(await page("zzzz"));
    expect(screen.getByText(/no results for “zzzz”/i)).toBeDefined();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("reports an API failure as a failure, because it is not the same thing", async () => {
    // Showing "no results" when the API is down makes a broken search look like an empty catalogue: the
    // learner concludes the thing does not exist and stops looking.
    // `mockRejectedValue` creates the rejected promise immediately, and an unhandled rejection fails the
    // run in a way that looks like a page bug. Throwing inside the implementation rejects only when called.
    fetchMock.mockImplementation(async () => {
      throw new Error("boom");
    });
    render(await page("algebra"));
    expect(screen.getByRole("alert")).toBeDefined();
    expect(screen.getByText(/search is unavailable right now/i)).toBeDefined();
    // The body must add information rather than repeat the title.
    expect(screen.getByText(/please try again in a moment/i)).toBeDefined();
  });

  it("says what it actually searched for when the query was rewritten", async () => {
    fetchMock.mockResolvedValue({ ...withResults, query: "the algebra of it", terms: ["algebra"] });
    render(await page("the algebra of it"));
    // Stopwords dropped: the user typed five words, the server searched for one. Saying so is the
    // difference between a puzzling result and an understood one.
    expect(screen.getByText(/searched for algebra/i)).toBeDefined();
  });

  it("suggests what to do when a stopword-only search finds nothing", async () => {
    fetchMock.mockResolvedValue({ ...withResults, results: [], count: 0, terms: [] });
    render(await page("the of and"));
    expect(screen.getByText(/try a subject or topic name/i)).toBeDefined();
  });

  it("is structurally sound", async () => {
    fetchMock.mockResolvedValue(withResults);
    const { container } = render(await page("algebra"));
    // colour-contrast needs layout, which jsdom does not have; it is checked against the tokens instead.
    const results = await axe.run(container, { rules: { "color-contrast": { enabled: false } } });
    expect(results.violations.map((v) => `${v.id}: ${v.help}`)).toEqual([]);
  });
});
