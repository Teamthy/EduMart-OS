-- Refunds: a payment is corrected by appending, never by editing.
--
-- Mirrors ../sqlite/0009_refunds.sql.
--
-- `orders.total_minor` is what the customer was charged. A refund must not rewrite it: an order that
-- took ₦5,000 and then returned ₦2,000 is a ₦5,000 charge and a ₦2,000 refund, and a report that says
-- "₦3,000" loses the fact that both movements happened. This table is the refund half of that record.
--
-- Two invariants the schema and the service enforce together:
--
--   idempotency_key  One refund per key. A payment provider or an operator retrying a timed-out
--                    request must not refund twice; the unique index is what makes that true even
--                    when two requests arrive at the same instant.
--   amount_minor     Positive, and the sum per order may never exceed the order total. The check is in
--                    the service (inside the transaction, against the current sum) because a CHECK
--                    constraint cannot see other rows.

CREATE TABLE IF NOT EXISTS refunds (
  id                TEXT PRIMARY KEY,
  order_id          TEXT NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
  amount_minor      INTEGER NOT NULL CHECK (amount_minor > 0),
  currency          TEXT NOT NULL DEFAULT 'NGN',
  reason            TEXT NOT NULL,
  -- Set when the refund is a full one, so "was this order given back in full?" is a column rather
  -- than a comparison every report has to re-derive.
  full_refund       INTEGER NOT NULL DEFAULT 0,
  actor_user_id     TEXT REFERENCES users(id) ON DELETE SET NULL,
  provider          TEXT NOT NULL DEFAULT 'sandbox',
  provider_reference TEXT NOT NULL DEFAULT '',
  idempotency_key   TEXT NOT NULL UNIQUE,
  created_at        TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_refunds_order ON refunds(order_id);
CREATE INDEX IF NOT EXISTS idx_refunds_recent ON refunds(created_at);
