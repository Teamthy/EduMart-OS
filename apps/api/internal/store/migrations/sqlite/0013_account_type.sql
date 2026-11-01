-- Signup now asks what kind of account someone is creating: a student, a tutor, or an institution.
--
-- This is *intent*, not authority. It changes how onboarding greets the account and which setup steps are
-- offered; it grants no role, no organisation membership and no moderation power. Those still come from a
-- verified email plus an invitation or an approval by a platform administrator, so a self-selected
-- account type can never be used to reach another tenant's data.
--
-- Expand-only: a new nullable-by-default column with a default that matches every existing row, so replicas
-- running the previous build keep working.
ALTER TABLE users ADD COLUMN account_type TEXT NOT NULL DEFAULT 'student';
