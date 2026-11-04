-- Proctoring a sitting: a host watching, and a host intervening. (PostgreSQL mirror)
--
-- Same two tables as the SQLite migration of this number, with the same columns and the same reasoning. The
-- only difference between the dialects here is that PostgreSQL would have let us write a CHECK on `kind`;
-- SQLite would too, and neither is used, because a CHECK on a text column is a constraint that has to be
-- dropped and recreated to add a fourth kind one day.

CREATE TABLE IF NOT EXISTS exam_proctor_warnings (
  id            TEXT PRIMARY KEY,
  -- The attempt the warning is addressed to. A warning to a sitting rather than to an attempt would be a
  -- broadcast; the host sends one per candidate so that "read" means read by that candidate.
  session_id    TEXT NOT NULL REFERENCES assessment_sessions(id) ON DELETE CASCADE,
  -- Denormalised from the session for the same reason exam_integrity_events does it: the host's console asks
  -- "every warning in this sitting" on a timer, and that must not be a join per row.
  definition_id TEXT,
  issued_by     TEXT NOT NULL,
  -- 'attention', 'time' or 'notice'. Closed on purpose: the text of the first two is composed by the server.
  kind          TEXT NOT NULL,
  note          TEXT,
  issued_at     TEXT NOT NULL,
  -- NULL until the candidate's browser reports that the warning was put in front of them. A warning nobody
  -- acknowledged is itself information — it means the tab was gone or the candidate ignored it.
  acknowledged_at TEXT
);

CREATE INDEX IF NOT EXISTS exam_proctor_warnings_session
  ON exam_proctor_warnings (session_id, issued_at);

CREATE INDEX IF NOT EXISTS exam_proctor_warnings_sitting
  ON exam_proctor_warnings (definition_id, issued_at);

CREATE TABLE IF NOT EXISTS exam_proctor_actions (
  id             TEXT PRIMARY KEY,
  session_id     TEXT NOT NULL REFERENCES assessment_sessions(id) ON DELETE CASCADE,
  definition_id  TEXT,
  -- Who intervened. Stored, not inferred: a report that says "this attempt was annulled" has to say by whom.
  actor_user_id  TEXT NOT NULL,
  action         TEXT NOT NULL,
  reason         TEXT,
  occurred_at    TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS exam_proctor_actions_session
  ON exam_proctor_actions (session_id, occurred_at);

CREATE INDEX IF NOT EXISTS exam_proctor_actions_sitting
  ON exam_proctor_actions (definition_id, occurred_at);
