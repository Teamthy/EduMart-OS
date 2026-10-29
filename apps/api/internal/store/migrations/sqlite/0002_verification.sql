-- Ownership proof for the two channels an account can be contacted on: email and phone.
--
-- A verification code is stored as a keyed hash, never in the clear: a leaked database must not
-- hand an attacker a working code. Each row is single-use (`consumed_at`), short-lived
-- (`expires_at`), and attempt-limited (`attempts` / `max_attempts`), because a six-digit secret is
-- only safe when guessing it is both rate-limited and finite.

ALTER TABLE users ADD COLUMN phone_verified_at TEXT;

CREATE TABLE IF NOT EXISTS verification_codes (
  id           TEXT PRIMARY KEY,
  user_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  channel      TEXT NOT NULL CHECK (channel IN ('email', 'phone')),
  destination  TEXT NOT NULL,
  code_hash    TEXT NOT NULL,
  attempts     INTEGER NOT NULL DEFAULT 0,
  max_attempts INTEGER NOT NULL DEFAULT 5,
  created_at   TEXT NOT NULL,
  expires_at   TEXT NOT NULL,
  consumed_at  TEXT
);

-- The two access patterns: "the newest live code for this user and channel" (confirm) and
-- "how many codes went out in the last hour, and when was the last one" (throttling).
CREATE INDEX IF NOT EXISTS idx_verification_codes_live ON verification_codes(user_id, channel, consumed_at);
CREATE INDEX IF NOT EXISTS idx_verification_codes_sent ON verification_codes(user_id, channel, created_at);
