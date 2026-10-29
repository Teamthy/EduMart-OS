-- A refund is a request until a provider confirms it.
--
-- Mirrors ../postgres/0010_refund_lifecycle.sql.
--
-- Until now a refund row meant "money went back", because the only provider was the sandbox and it
-- answered instantly. A real provider does not: it accepts a refund, processes it against the acquiring
-- bank, and confirms minutes or days later — or fails. Recording a request as a settled movement would
-- make the ledger describe an intention and call it a fact.
--
-- So a refund now has a lifecycle, and the money effects (order status, entitlement revocation, the
-- commission reversal) happen at *settlement*, not at request time:
--
--   pending       accepted, waiting for the provider. Reserved against the order total so two refunds
--                 cannot together exceed what was charged.
--   succeeded     the provider confirmed it. This is the only status the ledger counts as money returned.
--   failed        the provider refused it. No money moved, and the reservation is released.
--   needs_review  the outcome is unknown — a timeout, a 5xx, a dropped connection. Deliberately not
--                 retried automatically: a blind retry against an API that may already have paid out is
--                 how you refund twice. A human checks the provider, then resolves it.
--
-- Existing rows default to 'succeeded' because that is what they meant when they were written.

ALTER TABLE refunds ADD COLUMN status TEXT NOT NULL DEFAULT 'succeeded';

-- The provider's own word for the state, kept verbatim. It is the difference between "we think it is
-- pending" and "Paystack says it is pending", and only one of those is evidence.
ALTER TABLE refunds ADD COLUMN provider_status TEXT NOT NULL DEFAULT '';

-- Why it failed, or why a human resolved it the way they did.
ALTER TABLE refunds ADD COLUMN failure_reason TEXT NOT NULL DEFAULT '';

-- When the money actually moved, as distinct from when the refund was requested.
ALTER TABLE refunds ADD COLUMN settled_at TEXT;

CREATE INDEX IF NOT EXISTS idx_refunds_status ON refunds(status);
