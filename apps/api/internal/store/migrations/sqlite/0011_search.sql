-- Search: a projection for matching, never a source of truth for visibility.
--
-- Two tables, because the two jobs are different:
--
--   search_documents   one row per findable thing (a product, a subject, a topic), with the
--                      human-readable fields a result card needs.
--   search_tokens      one row per (document, token, field). Matching happens here.
--
-- Why tokens rather than `LIKE '%term%'` over the text: a leading wildcard cannot use an index on
-- either engine, so the cost of every search grows with the size of the corpus. A token table turns a
-- search into a prefix range scan (`token LIKE 'alg%'`), which is index-friendly — and it is the same
-- SQL on SQLite and PostgreSQL, which matters more than squeezing out the last millisecond, because a
-- feature that only works on the production engine is a feature CI cannot test.
--
-- Why the index is not consulted for visibility: a row here can be stale the moment something is
-- unpublished, and a stale row would leak a withdrawn product or a draft. So matching uses this table
-- and *visibility is re-checked against the source table at query time*. A stale index costs recall,
-- never correctness. POST /v1/admin/search/reindex rebuilds it, and the seed rebuilds it too.
--
-- Deliberately not indexed: question stems and answers. The question bank is the thing people pay for,
-- and a public endpoint that echoes paid content in a snippet is a content leak with a helpful UI.

CREATE TABLE IF NOT EXISTS search_documents (
  id          TEXT PRIMARY KEY,
  entity_type TEXT NOT NULL,
  entity_id   TEXT NOT NULL,
  title       TEXT NOT NULL,
  subtitle    TEXT NOT NULL DEFAULT '',
  body        TEXT NOT NULL DEFAULT '',
  href        TEXT NOT NULL DEFAULT '',
  updated_at  TEXT NOT NULL,
  UNIQUE (entity_type, entity_id)
);

CREATE TABLE IF NOT EXISTS search_tokens (
  document_id TEXT NOT NULL REFERENCES search_documents(id) ON DELETE CASCADE,
  token       TEXT NOT NULL,
  field       TEXT NOT NULL,
  PRIMARY KEY (document_id, token, field)
);

-- The index that makes a prefix match cheap. Without it a search is a scan of every token in the corpus.
CREATE INDEX IF NOT EXISTS idx_search_tokens_token ON search_tokens(token);
