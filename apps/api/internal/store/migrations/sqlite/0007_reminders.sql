-- Scheduled reminders: a message the system promises to send *later*, recorded when it is decided.
--
-- Mirrors ../postgres/0007_reminders.sql.
--
-- The delivery queue in 0004 records a message the moment it is about to be attempted. A reminder is a
-- different shape of promise: decided on Monday, due on Thursday. If the decision lives only in memory,
-- a restart on Wednesday silently cancels it, and nobody can audit what was promised.
--
-- Two invariants this table exists to enforce:
--
--   dedupe_key     One row per reminder per learner per window. "Send a study nudge every day" must not
--                  become five nudges because the planner ran five times. A unique index rather than a
--                  check in Go, because two instances can run the planner at the same moment.
--   status         A reminder is claimed before it is sent (pending -> claimed -> sent), so two workers
--                  sweeping at once cannot both deliver it. Revalidation happens after the claim: a
--                  deadline reminder written on Monday must not tell a learner to submit on Thursday if
--                  they submitted on Tuesday.

CREATE TABLE IF NOT EXISTS scheduled_notifications (
  id           TEXT PRIMARY KEY,
  user_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  kind         TEXT NOT NULL,
  template_key TEXT NOT NULL,
  subject      TEXT NOT NULL,
  body         TEXT NOT NULL,
  -- The row this reminder is about (an assessment definition, a study plan). Empty when it is about
  -- nothing in particular, as with a daily nudge.
  resource_id  TEXT NOT NULL DEFAULT '',
  due_at       TEXT NOT NULL,
  dedupe_key   TEXT NOT NULL,
  status       TEXT NOT NULL DEFAULT 'pending'
               CHECK (status IN ('pending', 'claimed', 'sent', 'skipped', 'cancelled')),
  created_at   TEXT NOT NULL,
  claimed_at   TEXT,
  settled_at   TEXT,
  delivery_id  TEXT,
  note         TEXT NOT NULL DEFAULT ''
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_scheduled_dedupe ON scheduled_notifications(dedupe_key);
CREATE INDEX IF NOT EXISTS idx_scheduled_due ON scheduled_notifications(status, due_at);
CREATE INDEX IF NOT EXISTS idx_scheduled_recent ON scheduled_notifications(created_at);
