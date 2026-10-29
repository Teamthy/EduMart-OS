-- Mirrors ../sqlite/0004_deliveries.sql.
-- Outbound message delivery: the durable record of what we tried to send, and whether it worked.
--
-- Sending a verification code is not a fire-and-forget side effect. A provider times out, an SMTP
-- server greylists, a mailbox is full. Recording every attempt means delivery failures are visible,
-- retries survive a restart, and a code nobody ever received can be withdrawn once retries run out.

CREATE TABLE IF NOT EXISTS notification_deliveries (
  id              TEXT PRIMARY KEY,
  channel         TEXT NOT NULL CHECK (channel IN ('email', 'phone')),
  recipient       TEXT NOT NULL,
  template_key    TEXT NOT NULL,
  subject         TEXT NOT NULL DEFAULT '',
  body            TEXT NOT NULL,
  status          TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'sent', 'failed')),
  attempts        INTEGER NOT NULL DEFAULT 0,
  max_attempts    INTEGER NOT NULL DEFAULT 5,
  last_error      TEXT NOT NULL DEFAULT '',
  -- The row this message is *about* (e.g. a verification code). If delivery is abandoned, that row
  -- must stop being usable: a credential nobody received is a liability, not an asset.
  resource_type   TEXT NOT NULL DEFAULT '',
  resource_id     TEXT NOT NULL DEFAULT '',
  created_at      TEXT NOT NULL,
  next_attempt_at TEXT NOT NULL,
  sent_at         TEXT,
  failed_at       TEXT
);

CREATE INDEX IF NOT EXISTS idx_deliveries_due ON notification_deliveries(status, next_attempt_at);
CREATE INDEX IF NOT EXISTS idx_deliveries_recent ON notification_deliveries(created_at);
