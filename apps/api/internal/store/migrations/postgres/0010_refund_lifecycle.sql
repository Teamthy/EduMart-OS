-- A refund is a request until a provider confirms it.
--
-- Mirrors ../sqlite/0010_refund_lifecycle.sql. See that file for the reasoning; the only differences here
-- are PostgreSQL's syntax for adding columns.
--
--   pending       accepted, waiting for the provider. Reserved against the order total.
--   succeeded     the provider confirmed it. The only status the ledger counts as money returned.
--   failed        the provider refused it. No money moved.
--   needs_review  the outcome is unknown (timeout, 5xx). Never retried automatically.
--
-- Existing rows default to 'succeeded' because that is what they meant when they were written.

ALTER TABLE refunds ADD COLUMN status TEXT NOT NULL DEFAULT 'succeeded';
ALTER TABLE refunds ADD COLUMN provider_status TEXT NOT NULL DEFAULT '';
ALTER TABLE refunds ADD COLUMN failure_reason TEXT NOT NULL DEFAULT '';
ALTER TABLE refunds ADD COLUMN settled_at TEXT;

CREATE INDEX IF NOT EXISTS idx_refunds_status ON refunds(status);
