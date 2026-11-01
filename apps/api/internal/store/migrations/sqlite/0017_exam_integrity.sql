-- Integrity signals recorded during a sitting.
--
-- A synchronised exam has a deadline and a shuffled paper, which stops a candidate seeing a neighbour's screen.
-- It does not stop a candidate leaving the tab for four minutes and coming back with an answer. This table
-- records what the *client reported* about attention — tab lost, fullscreen left, text pasted — so the host's
-- report can say "this candidate left the paper six times" instead of presenting a clean score that hides it.
--
-- What it is not: video proctoring. Nothing here proves who was at the keyboard, and the events are the
-- candidate's own browser reporting on itself. The value is the record and the comparison across candidates,
-- and the docs say so.
--
-- Expand-only: a new table and two indexes.
CREATE TABLE IF NOT EXISTS exam_integrity_events (
  id            TEXT PRIMARY KEY,
  -- The attempt the event belongs to. A sitting is a definition, but the report is per attempt.
  session_id    TEXT NOT NULL REFERENCES assessment_sessions(id) ON DELETE CASCADE,
  -- Denormalised from the session so the host's report does not need a join per row, and so an event keeps
  -- its meaning if a session row is ever pruned.
  definition_id TEXT,
  user_id       TEXT NOT NULL,
  kind          TEXT NOT NULL,
  detail        TEXT,
  -- occurred_at is the candidate's clock; received_at is ours. They are stored separately because the
  -- difference is itself a signal: a claim that an event happened an hour ago is not the same fact as one
  -- that arrived while the paper was open.
  occurred_at   TEXT NOT NULL,
  received_at   TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS exam_integrity_events_session
  ON exam_integrity_events (session_id);

CREATE INDEX IF NOT EXISTS exam_integrity_events_definition
  ON exam_integrity_events (definition_id, user_id);
