-- Institutions: onboarding state, verification, and the invitation lifecycle.
--
-- Mirrors ../sqlite/0003_organizations.sql.
-- `organizations`, `organization_memberships` and `invitations` shipped in the first migration but
-- nothing used them. This adds the columns those workflows need. Unlike the baseline, this file is
-- not required to be idempotent: it runs once against a database with a migration ledger, so a plain
-- ALTER is honest here (see the adoption contract in migrate.go).

-- An organisation is not trusted on creation. It starts unverified and a platform administrator
-- approves it before it can hold classes that matter or appear in institution reporting.
ALTER TABLE organizations ADD COLUMN kind TEXT NOT NULL DEFAULT 'school';
ALTER TABLE organizations ADD COLUMN created_by TEXT REFERENCES users(id) ON DELETE SET NULL;
ALTER TABLE organizations ADD COLUMN verified_at TEXT;
ALTER TABLE organizations ADD COLUMN verified_by TEXT REFERENCES users(id) ON DELETE SET NULL;
ALTER TABLE organizations ADD COLUMN updated_at TEXT;

-- Withdrawal is separate from acceptance: a revoked invitation must be distinguishable from one that
-- was never sent, and from one that expired unused.
ALTER TABLE invitations ADD COLUMN revoked_at TEXT;

CREATE INDEX IF NOT EXISTS idx_org_memberships_user ON organization_memberships(user_id, status);
CREATE INDEX IF NOT EXISTS idx_invitations_org ON invitations(organization_id, accepted_at);
CREATE INDEX IF NOT EXISTS idx_invitations_email ON invitations(email, accepted_at);
