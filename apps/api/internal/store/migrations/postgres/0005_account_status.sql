-- Account status: suspension, with the reason and the administrator who made the call.
--
-- Mirrors ../sqlite/0005_account_status.sql.
--
-- `users.status` has always existed and always held 'active'; nothing could set it to anything else and
-- nothing checked it. That meant an abusive account could only be dealt with by editing the database by
-- hand, and a suspended account's existing sessions would have kept working anyway. This adds the
-- record of *why* and *by whom*, so a suspension is an auditable decision rather than a silent edit.

ALTER TABLE users ADD COLUMN suspended_at TEXT;
ALTER TABLE users ADD COLUMN suspended_reason TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN suspended_by TEXT REFERENCES users(id) ON DELETE SET NULL;

-- Support answers "why can't I sign in?" by status, and the monthly review lists every suspended
-- account, so status is worth an index of its own.
CREATE INDEX IF NOT EXISTS idx_users_status ON users(status);
