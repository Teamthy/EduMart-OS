-- Virtual classes: paid or free live sessions with a whiteboard, a roster and a signalling relay.
--
-- Nothing here stores media. The API relays WebRTC signalling messages (which describe how two peers reach
-- each other) between attendees and stores the whiteboard as **ciphertext**: the room key never leaves a
-- browser. That is what makes the "end-to-end encrypted" claim checkable rather than a diagram — a database
-- read yields base64 it cannot read, and the API rejects anything that is not opaque base64 so a client
-- cannot quietly start storing plaintext notes here.

CREATE TABLE IF NOT EXISTS virtual_classes (
  id               TEXT PRIMARY KEY,
  host_user_id     TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  -- The invite code in the URL. Unguessable on purpose: a class link is a key, so it is 16 characters of
  -- crypto randomness rather than a slug derived from the title.
  slug             TEXT NOT NULL UNIQUE,
  title            TEXT NOT NULL,
  description      TEXT NOT NULL DEFAULT '',
  subject_id       TEXT REFERENCES subjects(id) ON DELETE SET NULL,
  -- Price in learning credits. 0 means the class is free; there is no second currency here because a
  -- credit-plus-money checkout in a live room is a payments project, not a feature.
  credits_cost     INTEGER NOT NULL DEFAULT 0,
  -- 0 means unlimited. Enforced in the join transaction, not by counting on the way in.
  capacity         INTEGER NOT NULL DEFAULT 0,
  status           TEXT NOT NULL DEFAULT 'scheduled',
  starts_at        TEXT NOT NULL,
  duration_minutes INTEGER NOT NULL DEFAULT 60,
  created_at       TEXT NOT NULL,
  updated_at       TEXT NOT NULL,
  ended_at         TEXT
);

CREATE INDEX IF NOT EXISTS virtual_classes_starts_at ON virtual_classes (starts_at);
CREATE INDEX IF NOT EXISTS virtual_classes_host ON virtual_classes (host_user_id);

-- One row per "the room is open" period. A class can be started and ended more than once (a host who lost
-- their connection), and attendance is recorded against the session that was live at the time.
CREATE TABLE IF NOT EXISTS virtual_class_sessions (
  id         TEXT PRIMARY KEY,
  class_id   TEXT NOT NULL REFERENCES virtual_classes(id) ON DELETE CASCADE,
  started_by TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  started_at TEXT NOT NULL,
  ended_at   TEXT
);

CREATE INDEX IF NOT EXISTS virtual_class_sessions_class ON virtual_class_sessions (class_id, started_at);

-- Who paid, how, and how long they stayed. joined_at/left_at are the attendance record; last_seen_at is the
-- heartbeat that separates "still in the room" from "closed the tab", because a browser that crashes never
-- sends a leave.
CREATE TABLE IF NOT EXISTS virtual_class_attendees (
  id              TEXT PRIMARY KEY,
  class_id        TEXT NOT NULL REFERENCES virtual_classes(id) ON DELETE CASCADE,
  user_id         TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  role            TEXT NOT NULL DEFAULT 'attendee',
  session_id      TEXT,
  payment_method  TEXT NOT NULL DEFAULT 'free',
  credits_paid    INTEGER NOT NULL DEFAULT 0,
  discount_code   TEXT,
  joined_at       TEXT NOT NULL,
  left_at         TEXT,
  last_seen_at    TEXT NOT NULL,
  UNIQUE (class_id, user_id)
);

CREATE INDEX IF NOT EXISTS virtual_class_attendees_class ON virtual_class_attendees (class_id, joined_at);

CREATE TABLE IF NOT EXISTS virtual_class_discount_codes (
  id          TEXT PRIMARY KEY,
  class_id    TEXT NOT NULL REFERENCES virtual_classes(id) ON DELETE CASCADE,
  code        TEXT NOT NULL UNIQUE,
  -- A percentage of the credit price, 1-100. 100 is a free pass; there is no separate "free" flag, because
  -- two ways to express the same discount is two ways for them to disagree.
  percent_off INTEGER NOT NULL,
  max_uses    INTEGER NOT NULL DEFAULT 0,
  uses        INTEGER NOT NULL DEFAULT 0,
  expires_at  TEXT,
  created_at  TEXT NOT NULL
);

-- WebRTC signalling. Ephemeral by design: rows are pruned on write, because SDP is large, useless within
-- seconds, and carries IP addresses that have no business accumulating in a database.
CREATE TABLE IF NOT EXISTS virtual_class_signals (
  id           TEXT PRIMARY KEY,
  class_id     TEXT NOT NULL REFERENCES virtual_classes(id) ON DELETE CASCADE,
  seq          INTEGER NOT NULL,
  from_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  -- NULL means "to everyone in the room"; a peer-to-peer mesh needs addressed messages (offer/answer/ICE)
  -- as well as broadcast ones (presence).
  to_user_id   TEXT,
  kind         TEXT NOT NULL,
  payload      TEXT NOT NULL,
  created_at   TEXT NOT NULL,
  UNIQUE (class_id, seq)
);

CREATE INDEX IF NOT EXISTS virtual_class_signals_class ON virtual_class_signals (class_id, created_at);

-- The whiteboard, as opaque ciphertext, append-only and ordered.
--
-- Stored rather than relayed so a late joiner can replay the board — a whiteboard that only exists in live
-- messages is blank for the twentieth person in the room. `seq` is per class and monotonic, so a client
-- knows whether it has missed an event.
CREATE TABLE IF NOT EXISTS virtual_class_board_events (
  id             TEXT PRIMARY KEY,
  class_id       TEXT NOT NULL REFERENCES virtual_classes(id) ON DELETE CASCADE,
  seq            INTEGER NOT NULL,
  author_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  nonce          TEXT NOT NULL,
  ciphertext     TEXT NOT NULL,
  created_at     TEXT NOT NULL,
  UNIQUE (class_id, seq)
);

-- Per-class counters, so ordering does not depend on a clock two browsers disagree about, and two people
-- drawing at once cannot be assigned the same sequence number.
CREATE TABLE IF NOT EXISTS virtual_class_counters (
  class_id   TEXT PRIMARY KEY REFERENCES virtual_classes(id) ON DELETE CASCADE,
  board_seq  INTEGER NOT NULL DEFAULT 0,
  signal_seq INTEGER NOT NULL DEFAULT 0
);

-- Multi-level referrals: which rung of the chain a commission was earned on.
--
-- The chain itself needs no new table. An attribution already names the referrer's code, and a code names its
-- owner, so "who referred me, and who referred them" is a walk up that joins two existing tables. Storing a
-- second copy of the graph would be a second thing to keep correct.
ALTER TABLE commission_ledger_entries ADD COLUMN referral_level INTEGER NOT NULL DEFAULT 1;
