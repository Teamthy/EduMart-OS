-- Edumart schema (SQLite dialect for local development, CI and demos).
-- The PostgreSQL dialect in ../postgres/0001_init.sql mirrors this table-for-table.
--
-- Conventions: opaque text ids, ISO-8601 UTC timestamps stored as TEXT (sortable, timezone-explicit),
-- money as INTEGER minor units with an explicit currency column, append-only ledgers.

PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS schema_meta (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS users (
  id                TEXT PRIMARY KEY,
  email_normalized  TEXT NOT NULL UNIQUE,
  password_hash     TEXT NOT NULL,
  display_name      TEXT NOT NULL,
  status            TEXT NOT NULL DEFAULT 'active',
  email_verified_at TEXT,
  phone_e164        TEXT,
  exam_pathway_id   TEXT,
  target_date       TEXT,
  timezone          TEXT NOT NULL DEFAULT 'Africa/Lagos',
  daily_minutes     INTEGER NOT NULL DEFAULT 30,
  onboarding_step   INTEGER NOT NULL DEFAULT 0,
  onboarding_done_at TEXT,
  created_at        TEXT NOT NULL,
  updated_at        TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS sessions (
  id         TEXT PRIMARY KEY,
  user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  user_agent TEXT,
  client     TEXT NOT NULL DEFAULT 'web',
  created_at TEXT NOT NULL,
  expires_at TEXT NOT NULL,
  revoked_at TEXT
);
CREATE INDEX IF NOT EXISTS idx_sessions_user ON sessions(user_id, expires_at);

CREATE TABLE IF NOT EXISTS password_reset_tokens (
  id         TEXT PRIMARY KEY,
  user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at TEXT NOT NULL,
  expires_at TEXT NOT NULL,
  used_at    TEXT
);

CREATE TABLE IF NOT EXISTS role_assignments (
  id     TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  role   TEXT NOT NULL,
  scope  TEXT NOT NULL DEFAULT 'global',
  UNIQUE (user_id, role, scope)
);

CREATE TABLE IF NOT EXISTS user_subjects (
  user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  subject_id TEXT NOT NULL REFERENCES subjects(id) ON DELETE CASCADE,
  created_at TEXT NOT NULL,
  PRIMARY KEY (user_id, subject_id)
);

-- Anonymous signup drafts: the wizard's progress exists on the server before an account does,
-- so a refresh, a lost connection or a switch to the mobile app resumes at the same step.
CREATE TABLE IF NOT EXISTS signup_drafts (
  id                TEXT PRIMARY KEY,
  token_hash        TEXT NOT NULL UNIQUE,
  draft_json        TEXT NOT NULL DEFAULT '{}',
  current_step      TEXT NOT NULL DEFAULT 'pathway',
  referral_code     TEXT NOT NULL DEFAULT '',
  created_at        TEXT NOT NULL,
  updated_at        TEXT NOT NULL,
  completed_user_id TEXT,
  completed_at      TEXT
);

CREATE TABLE IF NOT EXISTS organizations (
  id         TEXT PRIMARY KEY,
  name       TEXT NOT NULL,
  slug       TEXT NOT NULL UNIQUE,
  status     TEXT NOT NULL DEFAULT 'active',
  created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS organization_memberships (
  id              TEXT PRIMARY KEY,
  organization_id TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  user_id         TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  role            TEXT NOT NULL,
  status          TEXT NOT NULL DEFAULT 'active',
  joined_at       TEXT NOT NULL,
  UNIQUE (organization_id, user_id)
);

CREATE TABLE IF NOT EXISTS classes (
  id              TEXT PRIMARY KEY,
  organization_id TEXT REFERENCES organizations(id) ON DELETE CASCADE,
  owner_user_id   TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name            TEXT NOT NULL,
  subject_id      TEXT,
  join_code       TEXT NOT NULL UNIQUE,
  status          TEXT NOT NULL DEFAULT 'active',
  created_at      TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS class_memberships (
  id        TEXT PRIMARY KEY,
  class_id  TEXT NOT NULL REFERENCES classes(id) ON DELETE CASCADE,
  user_id   TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  role      TEXT NOT NULL DEFAULT 'student',
  status    TEXT NOT NULL DEFAULT 'active',
  joined_at TEXT NOT NULL,
  UNIQUE (class_id, user_id)
);

CREATE TABLE IF NOT EXISTS invitations (
  id              TEXT PRIMARY KEY,
  organization_id TEXT REFERENCES organizations(id) ON DELETE CASCADE,
  class_id        TEXT,
  email           TEXT,
  token_hash      TEXT NOT NULL UNIQUE,
  invited_by      TEXT NOT NULL,
  intended_role   TEXT NOT NULL DEFAULT 'student',
  expires_at      TEXT NOT NULL,
  accepted_at     TEXT,
  accepted_by     TEXT,
  max_uses        INTEGER NOT NULL DEFAULT 1,
  use_count       INTEGER NOT NULL DEFAULT 0,
  created_at      TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS exam_pathways (
  id     TEXT PRIMARY KEY,
  key    TEXT NOT NULL UNIQUE,
  name   TEXT NOT NULL,
  body   TEXT NOT NULL,
  blurb  TEXT NOT NULL DEFAULT '',
  active INTEGER NOT NULL DEFAULT 1
);

CREATE TABLE IF NOT EXISTS subjects (
  id              TEXT PRIMARY KEY,
  key             TEXT NOT NULL UNIQUE,
  name            TEXT NOT NULL,
  exam_pathway_id TEXT NOT NULL REFERENCES exam_pathways(id) ON DELETE CASCADE,
  icon            TEXT NOT NULL DEFAULT '',
  active          INTEGER NOT NULL DEFAULT 1
);

CREATE TABLE IF NOT EXISTS topics (
  id               TEXT PRIMARY KEY,
  key              TEXT NOT NULL,
  name             TEXT NOT NULL,
  subject_id       TEXT NOT NULL REFERENCES subjects(id) ON DELETE CASCADE,
  parent_id        TEXT,
  sort_order       INTEGER NOT NULL DEFAULT 0,
  syllabus_version TEXT NOT NULL DEFAULT '2025',
  UNIQUE (subject_id, key)
);

CREATE TABLE IF NOT EXISTS questions (
  id         TEXT PRIMARY KEY,
  subject_id TEXT NOT NULL REFERENCES subjects(id) ON DELETE CASCADE,
  topic_id   TEXT REFERENCES topics(id) ON DELETE SET NULL,
  type       TEXT NOT NULL DEFAULT 'mcq',
  difficulty TEXT NOT NULL DEFAULT 'medium',
  status     TEXT NOT NULL DEFAULT 'draft',
  created_by TEXT,
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_questions_subject ON questions(subject_id, status);

CREATE TABLE IF NOT EXISTS question_versions (
  id              TEXT PRIMARY KEY,
  question_id     TEXT NOT NULL REFERENCES questions(id) ON DELETE CASCADE,
  version         INTEGER NOT NULL,
  stem            TEXT NOT NULL,
  options_json    TEXT NOT NULL,
  answer_option_id TEXT NOT NULL,
  explanation     TEXT NOT NULL DEFAULT '',
  source_name     TEXT NOT NULL DEFAULT '',
  license_basis   TEXT NOT NULL DEFAULT '',
  attribution     TEXT NOT NULL DEFAULT '',
  review_state    TEXT NOT NULL DEFAULT 'draft',
  reviewer_id     TEXT,
  published_at    TEXT,
  created_at      TEXT NOT NULL,
  UNIQUE (question_id, version)
);
CREATE INDEX IF NOT EXISTS idx_qv_review ON question_versions(review_state, published_at);

CREATE TABLE IF NOT EXISTS content_reports (
  id                  TEXT PRIMARY KEY,
  question_version_id TEXT NOT NULL,
  reported_by         TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  reason              TEXT NOT NULL,
  details             TEXT NOT NULL DEFAULT '',
  status              TEXT NOT NULL DEFAULT 'open',
  created_at          TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS assessment_definitions (
  id                 TEXT PRIMARY KEY,
  owner_user_id      TEXT NOT NULL,
  organization_id    TEXT REFERENCES organizations(id) ON DELETE SET NULL,
  class_id           TEXT REFERENCES classes(id) ON DELETE SET NULL,
  title              TEXT NOT NULL,
  type               TEXT NOT NULL DEFAULT 'mock',
  duration_seconds   INTEGER NOT NULL DEFAULT 1800,
  availability_start TEXT,
  availability_end   TEXT,
  attempt_limit      INTEGER NOT NULL DEFAULT 1,
  scoring_policy_json TEXT NOT NULL DEFAULT '{"version":1,"correct":1,"wrong":0,"unanswered":0,"negativeMarking":false}',
  config_json        TEXT NOT NULL DEFAULT '{}',
  result_release     TEXT NOT NULL DEFAULT 'immediate',
  status             TEXT NOT NULL DEFAULT 'draft',
  published_at       TEXT,
  created_at         TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS assessment_items (
  id                  TEXT PRIMARY KEY,
  definition_id       TEXT NOT NULL REFERENCES assessment_definitions(id) ON DELETE CASCADE,
  question_version_id TEXT NOT NULL,
  points              INTEGER NOT NULL DEFAULT 1,
  position            INTEGER NOT NULL,
  UNIQUE (definition_id, position)
);

CREATE TABLE IF NOT EXISTS assessment_sessions (
  id              TEXT PRIMARY KEY,
  user_id         TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  definition_id   TEXT REFERENCES assessment_definitions(id) ON DELETE SET NULL,
  class_id        TEXT,
  mode            TEXT NOT NULL DEFAULT 'mock',
  attempt_no      INTEGER NOT NULL DEFAULT 1,
  status          TEXT NOT NULL DEFAULT 'in_progress',
  started_at      TEXT NOT NULL,
  deadline_at     TEXT NOT NULL,
  submitted_at    TEXT,
  score_raw       INTEGER,
  score_max       INTEGER,
  score_percent   INTEGER,
  snapshot_json   TEXT NOT NULL,
  idempotency_key TEXT UNIQUE,
  release_at      TEXT,
  created_at      TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_sessions_user ON assessment_sessions(user_id, started_at);
CREATE INDEX IF NOT EXISTS idx_sessions_status ON assessment_sessions(status, deadline_at);
CREATE INDEX IF NOT EXISTS idx_sessions_def ON assessment_sessions(definition_id, status);
CREATE UNIQUE INDEX IF NOT EXISTS idx_sessions_attempt ON assessment_sessions(user_id, definition_id, attempt_no);

CREATE TABLE IF NOT EXISTS assessment_responses (
  id                  TEXT PRIMARY KEY,
  session_id          TEXT NOT NULL REFERENCES assessment_sessions(id) ON DELETE CASCADE,
  question_version_id TEXT NOT NULL,
  selected_option_id  TEXT,
  is_correct          INTEGER,
  awarded_points      INTEGER NOT NULL DEFAULT 0,
  answer_version      INTEGER NOT NULL DEFAULT 1,
  client_sequence     INTEGER,
  saved_at            TEXT NOT NULL,
  updated_at          TEXT NOT NULL,
  UNIQUE (session_id, question_version_id)
);

CREATE TABLE IF NOT EXISTS idempotency_records (
  id            TEXT PRIMARY KEY,
  key           TEXT NOT NULL UNIQUE,
  scope         TEXT NOT NULL,
  user_id       TEXT,
  request_hash  TEXT NOT NULL DEFAULT '',
  response_json TEXT NOT NULL,
  status_code   INTEGER NOT NULL,
  created_at    TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS publishers (
  id     TEXT PRIMARY KEY,
  name   TEXT NOT NULL UNIQUE,
  status TEXT NOT NULL DEFAULT 'pending',
  email  TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS products (
  id           TEXT PRIMARY KEY,
  publisher_id TEXT REFERENCES publishers(id) ON DELETE SET NULL,
  slug         TEXT NOT NULL UNIQUE,
  title        TEXT NOT NULL,
  subtitle     TEXT NOT NULL DEFAULT '',
  description  TEXT NOT NULL DEFAULT '',
  type         TEXT NOT NULL DEFAULT 'book',
  subject_key  TEXT NOT NULL DEFAULT '',
  exam_key     TEXT NOT NULL DEFAULT '',
  price_minor  INTEGER NOT NULL DEFAULT 0,
  currency     TEXT NOT NULL DEFAULT 'NGN',
  status       TEXT NOT NULL DEFAULT 'published',
  pages        INTEGER NOT NULL DEFAULT 0,
  file_key     TEXT NOT NULL DEFAULT '',
  cover_tone   TEXT NOT NULL DEFAULT 'indigo',
  created_at   TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS orders (
  id             TEXT PRIMARY KEY,
  user_id        TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  status         TEXT NOT NULL DEFAULT 'pending_payment',
  currency       TEXT NOT NULL DEFAULT 'NGN',
  subtotal_minor INTEGER NOT NULL DEFAULT 0,
  discount_minor INTEGER NOT NULL DEFAULT 0,
  total_minor    INTEGER NOT NULL DEFAULT 0,
  reference      TEXT NOT NULL UNIQUE,
  created_at     TEXT NOT NULL,
  paid_at        TEXT
);

CREATE TABLE IF NOT EXISTS order_items (
  id                TEXT PRIMARY KEY,
  order_id          TEXT NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
  product_id        TEXT NOT NULL REFERENCES products(id),
  title_snapshot    TEXT NOT NULL,
  unit_amount_minor INTEGER NOT NULL,
  quantity          INTEGER NOT NULL DEFAULT 1
);

CREATE TABLE IF NOT EXISTS payment_attempts (
  id                 TEXT PRIMARY KEY,
  order_id           TEXT NOT NULL UNIQUE REFERENCES orders(id) ON DELETE CASCADE,
  provider           TEXT NOT NULL DEFAULT 'sandbox',
  provider_reference TEXT NOT NULL UNIQUE,
  status             TEXT NOT NULL DEFAULT 'pending',
  amount_minor       INTEGER NOT NULL,
  currency           TEXT NOT NULL,
  idempotency_key    TEXT,
  created_at         TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS provider_events (
  id                TEXT PRIMARY KEY,
  provider          TEXT NOT NULL,
  event_id          TEXT NOT NULL,
  signature_valid   INTEGER NOT NULL DEFAULT 0,
  processing_status TEXT NOT NULL DEFAULT 'received',
  payload_hash      TEXT NOT NULL,
  received_at       TEXT NOT NULL,
  processed_at      TEXT,
  UNIQUE (provider, event_id)
);

CREATE TABLE IF NOT EXISTS entitlements (
  id         TEXT PRIMARY KEY,
  user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  product_id TEXT NOT NULL REFERENCES products(id) ON DELETE CASCADE,
  order_id   TEXT,
  status     TEXT NOT NULL DEFAULT 'active',
  starts_at  TEXT NOT NULL,
  expires_at TEXT,
  revoked_at TEXT,
  UNIQUE (user_id, product_id)
);

CREATE TABLE IF NOT EXISTS referral_codes (
  id            TEXT PRIMARY KEY,
  owner_user_id TEXT NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,
  code          TEXT NOT NULL UNIQUE,
  status        TEXT NOT NULL DEFAULT 'active',
  created_at    TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS referral_attributions (
  id                  TEXT PRIMARY KEY,
  referred_user_id    TEXT NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,
  referral_code_id    TEXT NOT NULL REFERENCES referral_codes(id) ON DELETE CASCADE,
  attributed_at       TEXT NOT NULL,
  qualifying_event_id TEXT,
  status              TEXT NOT NULL DEFAULT 'pending'
);

CREATE TABLE IF NOT EXISTS points_ledger_entries (
  id              TEXT PRIMARY KEY,
  user_id         TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  amount          INTEGER NOT NULL,
  entry_type      TEXT NOT NULL,
  source_type     TEXT NOT NULL,
  source_id       TEXT NOT NULL,
  rule_version    TEXT NOT NULL DEFAULT 'v1',
  idempotency_key TEXT NOT NULL UNIQUE,
  note            TEXT NOT NULL DEFAULT '',
  created_at      TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS commission_ledger_entries (
  id              TEXT PRIMARY KEY,
  user_id         TEXT NOT NULL,
  amount_minor    INTEGER NOT NULL,
  currency        TEXT NOT NULL DEFAULT 'NGN',
  entry_type      TEXT NOT NULL,
  source_type     TEXT NOT NULL,
  source_id       TEXT NOT NULL,
  status          TEXT NOT NULL DEFAULT 'pending',
  idempotency_key TEXT NOT NULL UNIQUE,
  created_at      TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS topic_performance (
  id                TEXT PRIMARY KEY,
  user_id           TEXT NOT NULL,
  topic_id          TEXT NOT NULL,
  attempts          INTEGER NOT NULL DEFAULT 0,
  correct           INTEGER NOT NULL DEFAULT 0,
  last_practiced_at TEXT NOT NULL,
  UNIQUE (user_id, topic_id)
);

CREATE TABLE IF NOT EXISTS study_plans (
  id              TEXT PRIMARY KEY,
  user_id         TEXT NOT NULL UNIQUE,
  exam_pathway_id TEXT,
  target_date     TEXT,
  daily_minutes   INTEGER NOT NULL DEFAULT 30,
  focus_topics_json TEXT NOT NULL DEFAULT '[]',
  updated_at      TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS audit_logs (
  id             TEXT PRIMARY KEY,
  actor_user_id  TEXT REFERENCES users(id) ON DELETE SET NULL,
  action         TEXT NOT NULL,
  resource_type  TEXT NOT NULL,
  resource_id    TEXT NOT NULL DEFAULT '',
  reason         TEXT NOT NULL DEFAULT '',
  request_id     TEXT NOT NULL DEFAULT '',
  metadata_json  TEXT NOT NULL DEFAULT '{}',
  occurred_at    TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS support_cases (
  id          TEXT PRIMARY KEY,
  opened_by   TEXT NOT NULL,
  category    TEXT NOT NULL,
  priority    TEXT NOT NULL DEFAULT 'normal',
  status      TEXT NOT NULL DEFAULT 'open',
  subject     TEXT NOT NULL,
  body        TEXT NOT NULL DEFAULT '',
  assigned_to TEXT,
  created_at  TEXT NOT NULL,
  updated_at  TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS job_queue_items (
  id           TEXT PRIMARY KEY,
  kind         TEXT NOT NULL,
  payload_json TEXT NOT NULL DEFAULT '{}',
  status       TEXT NOT NULL DEFAULT 'pending',
  attempts     INTEGER NOT NULL DEFAULT 0,
  last_error   TEXT NOT NULL DEFAULT '',
  run_after    TEXT NOT NULL,
  created_at   TEXT NOT NULL,
  updated_at   TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS learning_events (
  id            TEXT PRIMARY KEY,
  user_id       TEXT NOT NULL,
  event_type    TEXT NOT NULL,
  entity_type   TEXT NOT NULL DEFAULT '',
  entity_id     TEXT NOT NULL DEFAULT '',
  metadata_json TEXT NOT NULL DEFAULT '{}',
  occurred_at   TEXT NOT NULL
);
