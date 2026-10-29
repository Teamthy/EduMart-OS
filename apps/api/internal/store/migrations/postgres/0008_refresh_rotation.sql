-- Refresh-token rotation: a session now has a short-lived access token and a rotating refresh token.
--
-- Mirrors ../sqlite/0008_refresh_rotation.sql.
--
-- A single long-lived token has one failure mode that matters: once it leaks, the thief has the same
-- standing as the owner for its whole lifetime, and nobody can tell the difference. Rotation fixes
-- that — every exchange issues a new refresh token and retires the old one, so a token that is used
-- twice is proof that two parties hold it. Reuse is a detection signal, not an inconvenience, which is
-- why it revokes the whole family rather than just failing the request.
--
-- `family_id` is what makes "the whole family" a thing: every token descended from one sign-in shares
-- it, so a single revoked ancestor ends every descendant at once.
--
-- Sessions that predate this migration have no refresh token and simply cannot be refreshed; they keep
-- working until their access token expires (7 days), then the user signs in again. Inventing a refresh
-- token for them is impossible — there is no plaintext to derive one from — and pretending otherwise
-- would be a security claim the schema cannot back.

ALTER TABLE sessions ADD COLUMN refresh_token_hash TEXT;
ALTER TABLE sessions ADD COLUMN refresh_expires_at TEXT;
ALTER TABLE sessions ADD COLUMN family_id TEXT;
ALTER TABLE sessions ADD COLUMN rotated_at TEXT;
ALTER TABLE sessions ADD COLUMN replaced_by TEXT;

-- A refresh lookup is by hash, on every exchange, from every device.
CREATE UNIQUE INDEX IF NOT EXISTS idx_sessions_refresh ON sessions(refresh_token_hash);
-- Family revocation is one UPDATE over a handful of rows.
CREATE INDEX IF NOT EXISTS idx_sessions_family ON sessions(family_id);
