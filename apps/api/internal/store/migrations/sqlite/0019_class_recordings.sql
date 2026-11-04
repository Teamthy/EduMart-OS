-- Recordings of live classes.
--
-- What a recording is here, precisely: the host's browser — or a participant it designates — composes the stage it
-- can already see and hear, seals it in the same worker that encrypts the call, and uploads the sealed chunks. The
-- server stores ciphertext it cannot open. That shape is forced, not chosen: since audio and video became end-to-end
-- encrypted, the process that forwards packets never had the plaintext to record in the first place.
--
-- So the consent model has to be real. A recording made by a participant's browser can include anybody the mixer
-- includes, and only that browser can decide who that is. The server's job is therefore:
--
--   * make the fact of recording impossible to miss (every join of a recorded class says so),
--   * record who acknowledged the notice and when (class_recording_consents),
--   * hand the recorder the list of who acknowledged, so the mixer can exclude the rest,
--   * keep the bytes only for as long as the retention window says, and let the host delete them on demand.
--
-- Stated plainly because it is the kind of claim that is easy to overstate: acknowledgement is enforced by the
-- recorder's client, not by the server. The server cannot verify the contents of a composited frame.

CREATE TABLE IF NOT EXISTS class_recordings (
  id                TEXT PRIMARY KEY,
  class_id          TEXT NOT NULL REFERENCES virtual_classes(id) ON DELETE CASCADE,
  started_by        TEXT NOT NULL,
  -- recording -> sealing (capture stopped, chunks may still be arriving) -> ready | failed -> deleted
  status            TEXT NOT NULL,
  chunks            INTEGER NOT NULL DEFAULT 0,
  bytes             INTEGER NOT NULL DEFAULT 0,
  duration_ms       INTEGER NOT NULL DEFAULT 0,
  -- Why a recording failed, in words a host can read. Never a stack trace.
  failure_reason    TEXT,
  -- How long the bytes may be kept. Set at creation from the deployment's retention window, so changing the
  -- configuration later cannot silently shorten or lengthen the promise made to the people in the room.
  retain_until      TEXT NOT NULL,
  created_at        TEXT NOT NULL,
  completed_at      TEXT,
  deleted_at        TEXT
);
CREATE INDEX IF NOT EXISTS idx_class_recordings_class ON class_recordings(class_id, created_at);
CREATE INDEX IF NOT EXISTS idx_class_recordings_retain ON class_recordings(status, retain_until);

-- Who was told, and when. One row per person per recording.
CREATE TABLE IF NOT EXISTS class_recording_consents (
  recording_id      TEXT NOT NULL REFERENCES class_recordings(id) ON DELETE CASCADE,
  user_id           TEXT NOT NULL,
  acknowledged_at   TEXT NOT NULL,
  PRIMARY KEY (recording_id, user_id)
);

-- One uploaded chunk. `seq` is monotonic per recorder so a gap is visible: a recording with a missing chunk should
-- say so rather than play as if nothing happened.
CREATE TABLE IF NOT EXISTS class_recording_segments (
  id                TEXT PRIMARY KEY,
  recording_id      TEXT NOT NULL REFERENCES class_recordings(id) ON DELETE CASCADE,
  user_id           TEXT NOT NULL,
  seq               INTEGER NOT NULL,
  storage_key       TEXT NOT NULL,
  bytes             INTEGER NOT NULL,
  -- The digest the store computed from the bytes that landed, so a download can be checked and a truncated upload
  -- cannot pass as complete.
  sha256            TEXT NOT NULL,
  duration_ms       INTEGER NOT NULL DEFAULT 0,
  created_at        TEXT NOT NULL,
  UNIQUE (recording_id, user_id, seq)
);
CREATE INDEX IF NOT EXISTS idx_class_recording_segments ON class_recording_segments(recording_id, seq);
