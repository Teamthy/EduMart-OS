# Search and discovery

Phase 26 was "no search endpoint; lists only". This is what replaced it, why it is built the way it is, and
what it deliberately does not do.

```bash
curl 'http://localhost:8080/v1/search?q=algebra'
curl 'http://localhost:8080/v1/search?q=physics&type=topic&limit=5'
```

## The three decisions

**1. The index is a projection, not the truth.**

`search_documents` and `search_tokens` decide *matching*. Visibility is re-checked against `products.status`
and `subjects.active` on **every** query. A stale index therefore costs recall — a product published five
minutes ago may not be found yet — but never correctness: an unpublished product cannot leak out of an
endpoint that asks the source table whether it is published. There is a test that withdraws a product,
deliberately skips the reindex, and asserts the result disappears.

That property is what makes the rebuild a convenience rather than a safety mechanism, and it is why
`POST /v1/admin/search/reindex` can be run at any time, twice, or not at all.

**2. Tokens, not `LIKE '%term%'`.**

A leading wildcard cannot use an index on SQLite or PostgreSQL, so every search would cost a scan of the
whole text. Storing one row per `(document, token, field)` turns a search into `token LIKE 'alg%'` — a
prefix range scan — and keeps **one** SQL statement working on both engines. On PostgreSQL the token index is
declared `text_pattern_ops`, because without the C-collation operator class the planner ignores a B-tree for
a `LIKE` prefix and silently falls back to a sequential scan: a performance cliff with no error and no
failing test.

A search feature that only works on the production engine is a search feature CI cannot test, which is why
the whole suite runs on both.

**3. Ranking in Go, not in SQL.**

`ts_rank` and SQLite's FTS5 rank on different scales with different tokenisers, so the same query would order
results differently on the two engines — a difference no test would catch and every user would. One scorer,
one order:

| Match | Weight |
| --- | --- |
| Title contains the term | 3 |
| Subtitle contains the term | 2 |
| Body contains the term | 1 |
| Title is exactly the term | +5 |

Ties break on title, then type, then id, so the same query twice returns the same order. A result list that
reshuffles between two identical requests looks broken, and makes a flaky test indistinguishable from a real
bug.

## The tokeniser

- Lowercased; punctuation and symbols become separators.
- Tokens shorter than two characters are dropped, as are a small English stopword list — "the mathematics of
  physics" is a search for *mathematics* and *physics*.
- Diacritics are **kept**: Latin-1 Supplement, Latin Extended-A/B, Latin Extended Additional (ọ, ẹ, ṣ) and
  combining marks. Stripping them, which is the obvious ASCII-only implementation, makes a correctly spelled
  Yoruba word return nothing. A test pins it.
- Capped at 8 terms. A paragraph ANDed against every token matches nothing.

## What it does not do

- **No stemming** beyond what prefix matching gives: "run" finds "running", "runs" does not find "run".
- **No typo tolerance.** "aljebra" finds nothing. The empty state says so and suggests a shorter word rather
  than pretending.
- **No semantic search.** It is keyword search with a relevance order, and calling it anything else would be
  a claim the code does not support.
- **No fuzzy ranking across types**: a subject called "Mathematics" always outranks a product whose
  description mentions mathematics, because the weights say so.
- **No question content.** Question stems and answers are not indexed. The question bank is the thing people
  pay for, and a public endpoint that echoes it in a snippet is a content leak with a helpful UI. Searching
  inside the bank, for someone entitled to it, is a separate feature with its own authorisation story.

## Operations

| Concern | How it is handled |
| --- | --- |
| Fresh database | The seed rebuilds the projection, so a developer's first search works |
| Existing database upgraded to this release | Call `POST /v1/admin/search/reindex` once. Until then search returns nothing, which is visible and honest rather than silently wrong |
| Stale `href` after a route change | `href` is resolved at index time, so a reindex is required after changing a web route. This is the one thing a stale index gets *wrong* rather than merely incomplete |
| Cost of a rebuild | One transaction, full delete and insert. Fine at this corpus size; at 10⁵ documents this becomes a background job, but it should stay one code path |

## Not measured

- **Latency at scale.** The corpus is under 100 documents (6 subjects, ~30 topics, 6 products). No measurement
  exists of search under a production-sized syllabus, and the candidate cap (500 documents per term) is a
  guess rather than a measured bound.
- **The mobile app has no search screen.** The endpoint and the shared `SearchResult` type are ready; the
  React Native shell still only lists. That is a screen, not an API gap, and it is not done.
- **Relevance quality.** The weights were chosen by argument, not by evaluating against real queries. There
  are no stopword lists for Nigerian English usage beyond the obvious, and nobody has looked at a page of
  results and said "these are the right ones".
