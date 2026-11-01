-- Let a host decide who comes in, and let a learner buy credits with money.
--
-- Both changes are expand-only: new defaulted columns, new tables, new indexes. A replica running the previous
-- build keeps working against this schema, which is the rule the deploy phase sets.

-- ── admission ─────────────────────────────────────────────────────────────────────────────────────────
--
-- A class had one door: anyone holding the link got a seat. That is right for a public clinic and wrong for a
-- paid tutorial with fifty seats, where the host has to recognise the names. `admission` is the policy and
-- `status` is where one person stands under it.
--
-- Existing rows default to 'open' and 'admitted', so every room that exists today keeps behaving exactly as it
-- did: same door, same seat.
ALTER TABLE virtual_classes ADD COLUMN admission TEXT NOT NULL DEFAULT 'open';
ALTER TABLE virtual_class_attendees ADD COLUMN status TEXT NOT NULL DEFAULT 'admitted';
ALTER TABLE virtual_class_attendees ADD COLUMN decided_at TEXT;

-- The room sweep and the roster both ask "who is admitted", so the index follows the same question.
CREATE INDEX IF NOT EXISTS virtual_class_attendees_status
  ON virtual_class_attendees (class_id, status);

-- One policy column cannot be a CHECK constraint on SQLite without a table rebuild, so the values are enforced
-- in the service (create) and named here for anyone reading the schema.
--   admission: 'open' | 'approval'
--   status:    'pending' | 'admitted' | 'rejected'

-- ── the wallet ───────────────────────────────────────────────────────────────────────────────────────
--
-- Credits were only ever earned (referrals, rewards). A learner with no credits could not buy a seat in a paid
-- class, which makes the paid half of the product unusable without an administrator doing it by hand.
--
-- A top-up is money, so it is its own table rather than a row in orders: an order is a product purchase with
-- items and entitlements, a top-up is a balance change with a fixed exchange rate and a minimum. Mixing them
-- would make every "what did this learner buy" query mean two different things.
CREATE TABLE IF NOT EXISTS wallet_topups (
  id              TEXT PRIMARY KEY,
  user_id         TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  reference       TEXT NOT NULL UNIQUE,
  amount_minor    INTEGER NOT NULL,
  currency        TEXT NOT NULL DEFAULT 'NGN',
  points          INTEGER NOT NULL,
  -- One point costs NAIRA_PER_POINT naira; the rate is stored on the row so a later price change cannot
  -- rewrite history, and a refund can be computed from what was actually promised.
  naira_per_point INTEGER NOT NULL,
  status          TEXT NOT NULL DEFAULT 'pending',
  provider        TEXT NOT NULL DEFAULT 'sandbox',
  provider_ref    TEXT,
  created_at      TEXT NOT NULL,
  paid_at         TEXT
);

-- Top-up history is read newest-first per learner.
CREATE INDEX IF NOT EXISTS wallet_topups_user_created
  ON wallet_topups (user_id, created_at);

-- The ledger already has a unique idempotency key, which is what makes crediting a top-up safe to retry:
-- 'topup:<topup id>'. A second confirmation finds the existing row and credits nothing.
