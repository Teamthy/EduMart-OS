-- A recharge gets a checkout page and a provider's own settlement event.
--
-- Expand-only, as every migration in this directory is: two nullable columns and one unique index. A replica
-- running the previous build keeps working against this schema, and keeps writing top-ups with no checkout —
-- which is exactly what a sandbox top-up is.
--
--   checkout_url     where the browser is sent to pay. Empty for the sandbox, which has no hosted page.
--   provider_event_id the provider's identity for the event that settled this top-up. It is recorded so a
--                    reconciliation can be traced from our row back to the provider's dashboard, and so a
--                    provider that changes its event ids is visible rather than silently double-crediting.
ALTER TABLE wallet_topups ADD COLUMN checkout_url TEXT;
ALTER TABLE wallet_topups ADD COLUMN provider_event_id TEXT;

-- A provider retry is the same event id. The index is partial because a sandbox top-up never has one, and
-- those NULLs must not collide with each other. SQLite and PostgreSQL accept the same statement.
CREATE UNIQUE INDEX IF NOT EXISTS wallet_topups_provider_event
  ON wallet_topups (provider_event_id) WHERE provider_event_id IS NOT NULL;
