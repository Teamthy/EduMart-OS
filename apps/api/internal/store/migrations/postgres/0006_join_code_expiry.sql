-- Join codes get an expiry date, and the code stops being guessable.
--
-- Mirrors ../sqlite/0006_join_code_expiry.sql.
--
-- A join code was a permanent, shared secret built from the class name plus two random characters:
-- "LEKKI24". Two characters of entropy is roughly 1,300 guesses for anyone who knows the school's name,
-- and the code never changed, so one leaked code was a permanent door into the class. This column is the
-- expiry half of the fix; the generator in classroom/service.go is the entropy half.
--
-- Existing classes are backfilled with 30 days from creation rather than left open forever: a column
-- that is null for every row predating the feature is the same permanent secret with extra steps. A class
-- whose backfilled date has already passed is not broken — joining is refused with a clear message and
-- the teacher rotates the code in one click.

ALTER TABLE classes ADD COLUMN join_code_expires_at TEXT;

UPDATE classes
   SET join_code_expires_at = to_char((created_at::timestamptz + interval '30 days') AT TIME ZONE 'UTC',
                                     'YYYY-MM-DD"T"HH24:MI:SS"Z"')
 WHERE join_code_expires_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_classes_join_code ON classes(join_code);
