-- Search: a projection for matching, never a source of truth for visibility.
--
-- Mirrors ../sqlite/0011_search.sql. See that file for the reasoning. The one addition here is
-- `text_pattern_ops`: PostgreSQL only uses a B-tree index for `LIKE 'prefix%'` when the index declares
-- the C-collation operator class. With the default collation the planner ignores the index and quietly
-- falls back to a sequential scan — a performance cliff that produces no error and no failing test.

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

-- Default collation: used for equality and for the AND-intersection lookups.
CREATE INDEX IF NOT EXISTS idx_search_tokens_token ON search_tokens(token);
-- C collation: what makes the prefix range scan (`token LIKE 'alg%'`) index-backed.
CREATE INDEX IF NOT EXISTS idx_search_tokens_prefix ON search_tokens(token text_pattern_ops);
