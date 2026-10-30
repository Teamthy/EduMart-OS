import type { Metadata } from "next";
import Link from "next/link";
import { Search as SearchIcon } from "lucide-react";

import type { SearchResponse, SearchResult } from "@edumart/shared";

import { Alert } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { EmptyState } from "@/components/ui/empty-state";
import { Input } from "@/components/ui/input";
import { ServerApiError, serverFetch } from "@/lib/server-api";
import { requireUser } from "@/lib/session";

export const metadata: Metadata = { title: "Search" };
export const dynamic = "force-dynamic";

const TYPE_LABEL: Record<SearchResult["type"], string> = {
  product: "Catalogue",
  subject: "Subject",
  topic: "Topic",
};

/**
 * Search.
 *
 * A plain GET form rather than a client-side combobox: the result has to survive a refresh, a share and a
 * browser back button, and a query string does all three without any JavaScript. The server component then
 * renders the same answer the API gave — there is no second ranking implementation to disagree with it.
 */
export default async function SearchPage({
  searchParams,
}: {
  searchParams: Promise<{ q?: string }>;
}) {
  await requireUser("/search");
  const { q } = await searchParams;
  const query = (q ?? "").trim();

  let response: SearchResponse | null = null;
  let failure: string | null = null;
  if (query) {
    try {
      response = await serverFetch<SearchResponse>(`/v1/search?q=${encodeURIComponent(query)}`);
    } catch (error) {
      // An empty result set is not a failure and must not be presented as one: "we found nothing" and
      // "we could not look" are different answers, and only one of them is worth retrying.
      // The title already says what happened; the body has to add something. Repeating the same sentence
      // in both places is how an error message teaches people to stop reading error messages.
      failure =
        error instanceof ServerApiError
          ? error.message
          : "Please try again in a moment. Nothing you have saved is affected.";
    }
  }

  return (
    <div className="space-y-6">
      <header className="space-y-1">
        <h1 className="text-2xl">Search</h1>
        <p className="text-sm text-muted-foreground">
          Find a subject, a topic or something in the store.
        </p>
      </header>

      <form action="/search" method="get" role="search" className="flex flex-wrap items-end gap-2">
        <div className="min-w-0 flex-1">
          <label htmlFor="q" className="mb-1 block text-sm font-medium">
            What are you looking for?
          </label>
          <Input
            id="q"
            name="q"
            type="search"
            defaultValue={query}
            placeholder="algebra, physics, JAMB…"
            autoComplete="off"
            // The query is echoed back by the server; searching again must not require retyping it.
            autoFocus={!query}
          />
        </div>
        <Button type="submit">
          <SearchIcon aria-hidden="true" />
          Search
        </Button>
      </form>

      {failure ? (
        <Alert tone="error" title="Search is unavailable right now">
          {failure}
        </Alert>
      ) : null}

      {response ? (
        <section aria-live="polite" className="space-y-3">
          <p className="text-sm text-muted-foreground">
            {response.count === 0
              ? `No results for “${response.query}”.`
              : `${response.count} result${response.count === 1 ? "" : "s"} for “${response.query}”`}
            {response.terms.length > 0 && response.terms.join(" ") !== response.query.toLowerCase()
              ? ` — searched for ${response.terms.join(", ")}`
              : ""}
          </p>

          {response.results.length === 0 ? (
            <EmptyState
              title="Nothing matched"
              description={
                response.terms.length === 0
                  ? "Try a subject or topic name rather than a whole sentence."
                  : "Check the spelling, or try a shorter word — search matches word beginnings, so “alg” finds “Algebra”."
              }
            />
          ) : (
            <ul className="space-y-2">
              {response.results.map((result) => (
                <li key={`${result.type}-${result.id}`}>
                  <Card>
                    <CardContent className="flex flex-col gap-2 p-4 sm:flex-row sm:items-center sm:justify-between">
                      <div className="min-w-0 space-y-1">
                        <div className="flex flex-wrap items-center gap-2">
                          <Badge variant="outline">{TYPE_LABEL[result.type]}</Badge>
                          <span className="font-semibold">{result.title}</span>
                        </div>
                        {result.subtitle ? (
                          <p className="text-sm text-muted-foreground">{result.subtitle}</p>
                        ) : null}
                        {result.snippet ? (
                          <p className="text-sm text-muted-foreground">{result.snippet}</p>
                        ) : null}
                      </div>
                      <Button asChild variant="outline" size="sm" className="shrink-0">
                        <Link href={result.href}>Open</Link>
                      </Button>
                    </CardContent>
                  </Card>
                </li>
              ))}
            </ul>
          )}
        </section>
      ) : null}
    </div>
  );
}
