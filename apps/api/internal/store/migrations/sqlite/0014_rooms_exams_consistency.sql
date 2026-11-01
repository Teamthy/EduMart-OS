-- Rooms that carry fifty people, free sessions capped at 45 minutes, and synchronised exams.
--
-- Three things change here, and each is expand-only: a new nullable column, a new defaulted column, or a new
-- index. A replica running the previous build keeps serving traffic against this schema, which is the rule
-- the deploy phase sets — no column is renamed, dropped or made NOT NULL while an older build may still read it.

-- ── the stage ─────────────────────────────────────────────────────────────────────────────────────────
--
-- A fifty-seat room cannot be a mesh: every participant would have to upload one stream per peer. Media is
-- forwarded by a server-side unit instead, and the room has a fixed number of *stage slots* (host + five
-- speakers). A slot is a permanently bound audio+video track, so promoting a speaker or having one leave never
-- forces a renegotiation of anybody's connection mid-sentence.
--
-- `stage_role` is who may publish; `stage_slot` is where. Both are assigned by the server (a client asking for
-- a slot is asking, not deciding) and both default to "viewer", so every existing attendance row means the same
-- thing it did before: a person in the room, not necessarily a person on stage.
ALTER TABLE virtual_class_attendees ADD COLUMN stage_role TEXT NOT NULL DEFAULT 'audience';
ALTER TABLE virtual_class_attendees ADD COLUMN stage_slot INTEGER NOT NULL DEFAULT -1;

CREATE INDEX IF NOT EXISTS virtual_class_attendees_stage
  ON virtual_class_attendees (class_id, stage_slot);

-- ── synchronised exams ───────────────────────────────────────────────────────────────────────────────
--
-- An exam is an assessment definition with a *window*: everyone starts inside the same availability period,
-- everyone gets the same duration, and the attempt ends when the window closes whether or not the clock ran
-- out. Nothing new is needed to model that — assessment_definitions already carries availability_start,
-- availability_end, duration_seconds, attempt_limit and the immutable per-attempt snapshot that makes the
-- question set identical for every candidate.
--
-- What is missing is (a) an address, because an exam is shared as a link, and (b) the shuffle that makes two
-- neighbouring screens show the same questions in a different order.
ALTER TABLE assessment_definitions ADD COLUMN slug TEXT;
ALTER TABLE assessment_definitions ADD COLUMN shuffle_questions INTEGER NOT NULL DEFAULT 0;

-- Partial unique index: many definitions have no slug (practice sets drawn on the fly), and those NULLs must
-- not collide with each other. SQLite and PostgreSQL accept the same statement.
CREATE UNIQUE INDEX IF NOT EXISTS assessment_definitions_slug
  ON assessment_definitions (slug) WHERE slug IS NOT NULL;

-- The host's end-to-end view of a sitting asks "every attempt at this exam, and what state is each in". The
-- existing index is on (definition_id, status); this one makes the result ordering cheap.
CREATE INDEX IF NOT EXISTS assessment_sessions_sitting
  ON assessment_sessions (definition_id, submitted_at);
