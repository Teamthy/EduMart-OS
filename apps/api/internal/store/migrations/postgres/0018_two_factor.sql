-- Two-factor authentication.
--
-- A password is one secret, and it is the secret people reuse and paste into phishing pages. A second factor turns
-- a stolen password from "an account" into "nothing without the device". The factor here is a TOTP secret (RFC
-- 6238) — an authenticator app, not SMS — because a SIM swap defeats SMS and this population's phone number is
-- usually also its banking recovery route.
--
-- Three columns carry the state and each one means something different:
--
--   totp_secret       set as soon as enrolment starts, so a half-finished enrolment is a recoverable state rather
--                     than a mystery. It does nothing until confirmed_at is set.
--   totp_confirmed_at set only after the user has returned a code computed from the secret. An unconfirmed secret
--                     must never gate a sign-in, or a mis-scanned QR code locks an account out.
--   totp_last_step    the highest time step already accepted. A TOTP code is valid for its whole 30-second step, so
--                     without this the second use of a code is a second sign-in.
--
-- Expand-only: three nullable columns on users, no rewrite of any existing row.
ALTER TABLE users ADD COLUMN totp_secret TEXT;
ALTER TABLE users ADD COLUMN totp_confirmed_at TEXT;
ALTER TABLE users ADD COLUMN totp_last_step INTEGER NOT NULL DEFAULT 0;

-- Single-use recovery codes, stored as a hash of (user id, code) so a dump of this table is not a set of keys.
-- `used_at` makes a code single-use; the row stays so the count of remaining codes is honest and a used code cannot
-- be quietly re-issued.
CREATE TABLE IF NOT EXISTS recovery_codes (
  id         TEXT PRIMARY KEY,
  user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at TEXT NOT NULL,
  used_at    TEXT
);
CREATE INDEX IF NOT EXISTS idx_recovery_codes_user ON recovery_codes(user_id, used_at);

-- A half-finished sign-in: the password was right, the second factor has not been presented yet.
--
-- Deliberately not a session. A row here grants nothing on its own — it cannot read an endpoint — and it expires in
-- five minutes and after five wrong codes, so it is a short-lived claim rather than a credential with a long life.
CREATE TABLE IF NOT EXISTS pending_signins (
  id         TEXT PRIMARY KEY,
  user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  attempts   INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL,
  expires_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_pending_signins_expiry ON pending_signins(expires_at);
