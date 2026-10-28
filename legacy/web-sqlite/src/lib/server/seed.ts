import type { DatabaseSync } from "node:sqlite";
import { randomUUID, createHash } from "node:crypto";
import { SUBJECT_BANK, PRODUCTS } from "../content/seed-content";
import { hashPassword } from "./password";

/**
 * Demo/reference content loader.
 *
 * Loads exam taxonomy, a reviewed original question bank, publications, an institution, and a
 * small amount of learner history so every screen has realistic data. All accounts are
 * synthetic and use documented demo passwords — never reuse these seeds in production.
 */

const iso = (d: Date) => d.toISOString();
const daysAgo = (n: number, hour = 9, minute = 0) => {
  const d = new Date();
  d.setUTCDate(d.getUTCDate() - n);
  d.setUTCHours(hour, minute, 0, 0);
  return d;
};

const PATHWAYS = [
  { key: "jamb", name: "JAMB / UTME", body: "Joint Admissions and Matriculation Board", blurb: "Computer-based UTME preparation across four subjects." },
  { key: "waec", name: "WAEC SSCE", body: "West African Examinations Council", blurb: "SSCE objective and theory practice with marking guides." },
  { key: "neco", name: "NECO SSCE", body: "National Examinations Council", blurb: "NECO-aligned practice and timed mocks." },
  { key: "post-utme", name: "Post-UTME", body: "University screening", blurb: "Institution screening tests and subject-combination drills." },
];

const SOURCE = {
  sourceName: "Edumart original item bank",
  licenseBasis: "Original content authored for Edumart; platform distribution cleared",
  attribution: "Edumart Content Studio",
};

export function seedDatabase(db: DatabaseSync) {
  const stmt = (sql: string) => db.prepare(sql);
  const insertUser = stmt(
    `INSERT INTO users (id, email_normalized, password_hash, display_name, status, email_verified_at, exam_pathway_id, target_date, timezone, created_at, updated_at)
     VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
  );
  const insertRole = stmt("INSERT INTO role_assignments (id, user_id, role, scope) VALUES (?,?,?,?)");

  const userIds: Record<string, string> = {};
  const users = [
    { key: "student", email: "student@edumart.ng", name: "Amara Okonkwo", pw: "Student123!", pathway: "jamb", target: 124 },
    { key: "student2", email: "student2@edumart.ng", name: "Tunde Bello", pw: "Student123!", pathway: "jamb", target: 96 },
    { key: "student3", email: "student3@edumart.ng", name: "Zainab Yusuf", pw: "Student123!", pathway: "jamb", target: 150 },
    { key: "teacher", email: "teacher@edumart.ng", name: "Mr. Chidi Eze", pw: "Teacher123!", pathway: null, target: null },
    { key: "admin", email: "admin@edumart.ng", name: "Platform Administrator", pw: "Admin123!", pathway: null, target: null },
    { key: "editor", email: "editor@edumart.ng", name: "Ngozi Adeyemi", pw: "Editor123!", pathway: null, target: null },
  ];

  const now = new Date();
  const targetDate = (days: number) => iso(new Date(now.getTime() + days * 86400000));

  // ── taxonomy ────────────────────────────────────────────────────────────
  const insertPathway = stmt(
    "INSERT INTO exam_pathways (id, key, name, body, blurb, active) VALUES (?,?,?,?,?,1)",
  );
  const insertSubject = stmt(
    "INSERT INTO subjects (id, key, name, exam_pathway_id, icon, active) VALUES (?,?,?,?,?,1)",
  );
  const insertTopic = stmt(
    "INSERT INTO topics (id, key, name, subject_id, parent_id, sort_order, syllabus_version) VALUES (?,?,?,?,NULL,?,?)",
  );

  const pathwayIds: Record<string, string> = {};
  for (const p of PATHWAYS) {
    const pid = randomUUID();
    pathwayIds[p.key] = pid;
    insertPathway.run(pid, p.key, p.name, p.body, p.blurb);
  }

  for (const u of users) {
    const uid = randomUUID();
    userIds[u.key] = uid;
    insertUser.run(
      uid,
      u.email,
      hashPassword(u.pw),
      u.name,
      "active",
      iso(new Date(now.getTime() - 30 * 86400000)),
      u.pathway ? pathwayIds[u.pathway] : null,
      u.target ? targetDate(u.target) : null,
      "Africa/Lagos",
      iso(daysAgo(40)),
      iso(now),
    );
    const role =
      u.key === "teacher" ? "teacher" : u.key === "admin" ? "platform_admin" : u.key === "editor" ? "content_editor" : "student";
    insertRole.run(randomUUID(), uid, role, "global");
    if (u.key === "teacher") insertRole.run(randomUUID(), uid, "org_admin", "global");
  }

  // ── subjects, topics, questions (JAMB + WAEC get full banks) ────────────
  const insertQuestion = stmt(
    "INSERT INTO questions (id, subject_id, topic_id, type, difficulty, status, created_by, created_at) VALUES (?,?,?,'mcq',?,?,?,?)",
  );
  const insertVersion = stmt(
    `INSERT INTO question_versions (id, question_id, version, stem, options_json, answer_option_id, explanation,
       source_name, license_basis, attribution, review_state, reviewer_id, published_at, created_at)
     VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
  );

  type BankRow = { qvId: string; questionId: string; subjectId: string; topicId: string; subjectName: string; topicName: string; difficulty: string };
  const bank: Record<string, BankRow[]> = {}; // keyed by `${pathway}:${subjectKey}`

  const contentPathways = ["jamb", "waec", "neco", "post-utme"];
  for (const pw of contentPathways) {
    for (const subject of SUBJECT_BANK) {
      // NECO and Post-UTME reuse the taxonomy with a lighter published subset.
      if (pw === "neco" && !["mathematics", "english", "biology"].includes(subject.key)) continue;
      if (pw === "post-utme" && !["mathematics", "english"].includes(subject.key)) continue;

      const sid = randomUUID();
      insertSubject.run(sid, `${pw}-${subject.key}`, subject.name, pathwayIds[pw], subject.icon);

      const topicIds: Record<string, string> = {};
      subject.topics.forEach((t, i) => {
        const tid = randomUUID();
        topicIds[t.key] = tid;
        insertTopic.run(tid, `${pw}-${t.key}`, t.name, sid, i, "2025");
      });

      const rows: BankRow[] = [];
      subject.questions.forEach((question, index) => {
        const qid = randomUUID();
        const qvid = randomUUID();
        const topicId = topicIds[question.topic] ?? null;
        // Keep two items in review per pathway so the moderation queue is not empty in the demo.
        const inReview = pw === "jamb" && subject.key === "mathematics" && index >= 8;
        insertQuestion.run(
          qid,
          sid,
          topicId,
          question.difficulty,
          inReview ? "in_review" : "published",
          userIds.editor,
          iso(daysAgo(30)),
        );
        const options = question.options.map((label, i) => ({ id: `option-${String.fromCharCode(97 + i)}`, label }));
        insertVersion.run(
          qvid,
          qid,
          1,
          question.stem,
          JSON.stringify(options),
          options[question.answer].id,
          question.explanation,
          SOURCE.sourceName,
          SOURCE.licenseBasis,
          SOURCE.attribution,
          inReview ? "in_review" : "approved",
          userIds.editor,
          inReview ? null : iso(daysAgo(25)),
          iso(daysAgo(30)),
        );
        rows.push({
          qvId: qvid,
          questionId: qid,
          subjectId: sid,
          topicId: topicId as string,
          subjectName: subject.name,
          topicName: subject.topics.find((t) => t.key === question.topic)?.name ?? "",
          difficulty: question.difficulty,
        });
      });
      bank[`${pw}:${subject.key}`] = rows;
    }
  }

  // ── institution, class, memberships ────────────────────────────────────
  const orgId = randomUUID();
  stmt("INSERT INTO organizations (id, name, slug, status, created_at) VALUES (?,?,?,?,?)").run(
    orgId,
    "Lekki Grammar School",
    "lekki-grammar",
    "active",
    iso(daysAgo(60)),
  );
  stmt(
    "INSERT INTO organization_memberships (id, organization_id, user_id, role, status, joined_at) VALUES (?,?,?,?,?,?)",
  ).run(randomUUID(), orgId, userIds.teacher, "org_admin", "active", iso(daysAgo(60)));
  for (const s of ["student", "student2", "student3"]) {
    stmt(
      "INSERT INTO organization_memberships (id, organization_id, user_id, role, status, joined_at) VALUES (?,?,?,?,?,?)",
    ).run(randomUUID(), orgId, userIds[s], "student", "active", iso(daysAgo(45)));
  }

  const jambMathSubjectId = bank["jamb:mathematics"][0].subjectId;
  const classId = randomUUID();
  stmt(
    "INSERT INTO classes (id, organization_id, owner_user_id, name, subject_id, join_code, status, created_at) VALUES (?,?,?,?,?,?,?,?)",
  ).run(classId, orgId, userIds.teacher, "SS2 Science — Mathematics", jambMathSubjectId, "LEKKI24", "active", iso(daysAgo(45)));

  stmt("INSERT INTO class_memberships (id, class_id, user_id, role, status, joined_at) VALUES (?,?,?,?,?,?)").run(
    randomUUID(), classId, userIds.teacher, "teacher", "active", iso(daysAgo(45)),
  );
  for (const s of ["student", "student2", "student3"]) {
    stmt("INSERT INTO class_memberships (id, class_id, user_id, role, status, joined_at) VALUES (?,?,?,?,?,?)").run(
      randomUUID(), classId, userIds[s], "student", "active", iso(daysAgo(44)),
    );
  }

  // ── assessment definitions ─────────────────────────────────────────────
  const insertDefinition = stmt(
    `INSERT INTO assessment_definitions (id, owner_user_id, organization_id, class_id, title, type, duration_seconds,
       availability_start, availability_end, attempt_limit, scoring_policy_json, config_json, result_release, status, published_at, created_at)
     VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
  );
  const insertItem = stmt(
    "INSERT INTO assessment_items (id, definition_id, question_version_id, points, position) VALUES (?,?,?,?,?)",
  );

  const mockDefId = randomUUID();
  insertDefinition.run(
    mockDefId,
    userIds.admin,
    null,
    null,
    "JAMB Mathematics Mock — Set A",
    "mock",
    1800,
    iso(daysAgo(10)),
    iso(new Date(now.getTime() + 21 * 86400000)),
    3,
    '{"version":1,"correct":1,"wrong":0,"unanswered":0,"negativeMarking":false}',
    JSON.stringify({ subject: "Mathematics", pathway: "jamb", questionCount: 10 }),
    "immediate",
    "published",
    iso(daysAgo(12)),
    iso(daysAgo(13)),
  );
  const mathBank = bank["jamb:mathematics"].filter((row) => row.difficulty !== "hard" || true).slice(0, 10);
  mathBank.forEach((row, i) => insertItem.run(randomUUID(), mockDefId, row.qvId, 1, i + 1));

  const classDefId = randomUUID();
  insertDefinition.run(
    classDefId,
    userIds.teacher,
    orgId,
    classId,
    "Week 4 — Algebra & Number Bases Quiz",
    "class",
    1200,
    iso(daysAgo(6)),
    iso(new Date(now.getTime() + 7 * 86400000)),
    2,
    '{"version":1,"correct":1,"wrong":0,"unanswered":0,"negativeMarking":false}',
    JSON.stringify({ subject: "Mathematics", pathway: "jamb", questionCount: 6 }),
    "immediate",
    "published",
    iso(daysAgo(6)),
    iso(daysAgo(7)),
  );
  mathBank.slice(0, 6).forEach((row, i) => insertItem.run(randomUUID(), classDefId, row.qvId, 1, i + 1));

  // ── learner history (finalized attempts → dashboard, insights, reports) ─
  const insertSession = stmt(
    `INSERT INTO assessment_sessions (id, user_id, definition_id, class_id, mode, attempt_no, status, started_at, deadline_at,
       submitted_at, score_raw, score_max, score_percent, snapshot_json, idempotency_key, release_at, created_at)
     VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
  );
  const insertResponse = stmt(
    `INSERT INTO assessment_responses (id, session_id, question_version_id, selected_option_id, is_correct, awarded_points,
       answer_version, client_sequence, saved_at, updated_at)
     VALUES (?,?,?,?,?,?,?,?,?,?)`,
  );

  const answerKeyFor = (qvId: string): string | null => {
    const row = db.prepare("SELECT answer_option_id FROM question_versions WHERE id = ?").get(qvId) as any;
    return row ? row.answer_option_id : null;
  };
  const optionsFor = (qvId: string): { id: string; label: string }[] =>
    JSON.parse((db.prepare("SELECT options_json FROM question_versions WHERE id = ?").get(qvId) as any).options_json);

  const buildSnapshot = (rows: BankRow[]) =>
    JSON.stringify({
      policy: { version: 1, correct: 1, wrong: 0, unanswered: 0, negativeMarking: false },
      questions: rows.map((row, i) => {
        const v = db.prepare("SELECT stem, options_json, answer_option_id, explanation FROM question_versions WHERE id = ?").get(row.qvId) as any;
        return {
          qvId: row.qvId,
          questionId: row.questionId,
          subjectId: row.subjectId,
          subjectName: row.subjectName,
          topicId: row.topicId,
          topicName: row.topicName,
          stem: v.stem,
          options: JSON.parse(v.options_json),
          answerOptionId: v.answer_option_id,
          explanation: v.explanation,
          difficulty: row.difficulty,
          points: 1,
          position: i + 1,
        };
      }),
    });

  type History = { user: string; rows: BankRow[]; correct: number; days: number; mode: string; defId: string | null; classId: string | null; doneAt: string; duration: number };
  const histories: History[] = [
    { user: "student", rows: mathBank.slice(0, 8), correct: 6, days: 7, mode: "practice", defId: null, classId: null, doneAt: "10:20", duration: 900 },
    { user: "student", rows: mathBank.slice(0, 10), correct: 7, days: 3, mode: "mock", defId: mockDefId, classId: null, doneAt: "16:45", duration: 1800 },
    { user: "student", rows: mathBank.slice(2, 8), correct: 4, days: 1, mode: "class", defId: classDefId, classId, doneAt: "11:05", duration: 1200 },
    { user: "student2", rows: mathBank.slice(0, 6), correct: 5, days: 2, mode: "class", defId: classDefId, classId, doneAt: "09:30", duration: 1200 },
    { user: "student3", rows: mathBank.slice(0, 6), correct: 3, days: 2, mode: "class", defId: classDefId, classId, doneAt: "14:10", duration: 1200 },
  ];

  for (const h of histories) {
    const sid = randomUUID();
    const started = new Date(daysAgo(h.days, Number(h.doneAt.split(":")[0]), Number(h.doneAt.split(":")[1])));
    const submitted = new Date(started.getTime() + Math.min(h.duration, 900) * 1000);
    const snapshot = buildSnapshot(h.rows);
    const max = h.rows.length;
    insertSession.run(
      sid,
      userIds[h.user],
      h.defId,
      h.classId,
      h.mode,
      1,
      "submitted",
      iso(started),
      iso(new Date(started.getTime() + h.duration * 1000)),
      iso(submitted),
      h.correct,
      max,
      Math.round((h.correct / max) * 100),
      snapshot,
      null,
      iso(submitted),
      iso(started),
    );
    h.rows.forEach((row, i) => {
      const key = answerKeyFor(row.qvId);
      const options = optionsFor(row.qvId);
      const isCorrect = i < h.correct;
      const selected = isCorrect ? key : options.find((o) => o.id !== key)?.id ?? null;
      insertResponse.run(
        randomUUID(),
        sid,
        row.qvId,
        selected,
        isCorrect ? 1 : 0,
        isCorrect ? 1 : 0,
        1,
        i + 1,
        iso(submitted),
        iso(submitted),
      );
      db.prepare(
        `INSERT INTO topic_performance (id, user_id, topic_id, attempts, correct, last_practiced_at)
         VALUES (?,?,?,?,?,?)
         ON CONFLICT(user_id, topic_id) DO UPDATE SET
           attempts = attempts + excluded.attempts,
           correct = correct + excluded.correct,
           last_practiced_at = excluded.last_practiced_at`,
      ).run(randomUUID(), userIds[h.user], row.topicId, 1, isCorrect ? 1 : 0, iso(submitted));
    });
    // Points are awarded per completed attempt with an idempotency key tied to the session.
    db.prepare(
      `INSERT INTO points_ledger_entries (id, user_id, amount, entry_type, source_type, source_id, rule_version, idempotency_key, note, created_at)
       VALUES (?,?,?,?,?,?,?,?,?,?)`,
    ).run(
      randomUUID(),
      userIds[h.user],
      h.mode === "practice" ? 10 : h.mode === "mock" ? 40 : 25,
      "earn",
      h.mode === "practice" ? "practice_completed" : h.mode === "mock" ? "mock_completed" : "class_completed",
      sid,
      "v1",
      `points:attempt:${sid}`,
      "Attempt completed",
      iso(submitted),
    );
  }

  // ── publications & commerce ────────────────────────────────────────────
  const publishers: Record<string, string> = {};
  const insertPublisher = stmt("INSERT INTO publishers (id, name, status, email) VALUES (?,?,?,?)");
  for (const [name, status, email] of [
    ["Edumart Studio", "approved", "studio@edumart.ng"],
    ["Sabon Press", "approved", "hello@sabonpress.ng"],
    ["Lagos Learning Co.", "pending", "contact@lagoslearning.ng"],
  ] as const) {
    const pid = randomUUID();
    publishers[name] = pid;
    insertPublisher.run(pid, name, status, email);
  }

  const productIds: string[] = [];
  const insertProduct = stmt(
    `INSERT INTO products (id, publisher_id, slug, title, subtitle, description, type, subject_key, exam_key,
       price_minor, currency, status, pages, file_key, cover_tone, created_at)
     VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
  );
  for (const p of PRODUCTS) {
    const pid = randomUUID();
    productIds.push(pid);
    insertProduct.run(
      pid,
      publishers[p.publisher],
      p.slug,
      p.title,
      p.subtitle,
      p.description,
      p.type,
      p.subjectKey,
      p.examKey,
      p.priceMinor,
      "NGN",
      "published",
      p.pages,
      `private/products/${p.slug}.pdf`,
      p.coverTone,
      iso(daysAgo(20)),
    );
  }

  // Student owns one paid order with an entitlement, one pending order, and referral rewards.
  const paidOrderId = randomUUID();
  const reference = `EDM-${createHash("sha1").update(paidOrderId).digest("hex").slice(0, 10).toUpperCase()}`;
  stmt(
    `INSERT INTO orders (id, user_id, status, currency, subtotal_minor, discount_minor, total_minor, reference, created_at, paid_at)
     VALUES (?,?,?,?,?,?,?,?,?,?)`,
  ).run(paidOrderId, userIds.student, "paid", "NGN", PRODUCTS[0].priceMinor, 0, PRODUCTS[0].priceMinor, reference, iso(daysAgo(9)), iso(daysAgo(9)));
  stmt(
    "INSERT INTO order_items (id, order_id, product_id, title_snapshot, unit_amount_minor, quantity) VALUES (?,?,?,?,?,1)",
  ).run(randomUUID(), paidOrderId, productIds[0], PRODUCTS[0].title, PRODUCTS[0].priceMinor);
  stmt(
    `INSERT INTO payment_attempts (id, order_id, provider, provider_reference, status, amount_minor, currency, idempotency_key, created_at)
     VALUES (?,?,?,?,?,?,?,?,?)`,
  ).run(randomUUID(), paidOrderId, "sandbox", `sbx_${paidOrderId.slice(0, 12)}`, "succeeded", PRODUCTS[0].priceMinor, "NGN", `pay:${paidOrderId}`, iso(daysAgo(9)));
  stmt(
    "INSERT INTO entitlements (id, user_id, product_id, order_id, status, starts_at, expires_at, revoked_at) VALUES (?,?,?,?,?,?,NULL,NULL)",
  ).run(randomUUID(), userIds.student, productIds[0], paidOrderId, "active", iso(daysAgo(9)));
  // Free product: explicit free grant, still an entitlement (authorization always checks it).
  stmt(
    "INSERT INTO entitlements (id, user_id, product_id, order_id, status, starts_at, expires_at, revoked_at) VALUES (?,?,?,NULL,?,?,NULL,NULL)",
  ).run(randomUUID(), userIds.student, productIds[4], "active", iso(daysAgo(5)));

  stmt("INSERT INTO referral_codes (id, owner_user_id, code, status, created_at) VALUES (?,?,?,?,?)").run(
    randomUUID(), userIds.student, "AMARA-2026", "active", iso(daysAgo(20)),
  );
  stmt(
    `INSERT INTO commission_ledger_entries (id, user_id, amount_minor, currency, entry_type, source_type, source_id, status, idempotency_key, created_at)
     VALUES (?,?,?,?,?,?,?,?,?,?)`,
  ).run(randomUUID(), userIds.student, 25000, "NGN", "accrue", "referral_qualifying_hours", userIds.student2, "eligible", "commission:demo:1", iso(daysAgo(4)));

  // ── operations queue examples ──────────────────────────────────────────
  stmt(
    "INSERT INTO content_reports (id, question_version_id, reported_by, reason, details, status, created_at) VALUES (?,?,?,?,?,?,?)",
  ).run(
    randomUUID(),
    mathBank[1].qvId,
    userIds.student,
    "unclear_explanation",
    "The explanation jumps a step between the simultaneous equations.",
    "open",
    iso(daysAgo(2)),
  );
  stmt(
    "INSERT INTO support_cases (id, opened_by, category, priority, status, subject, body, assigned_to, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?,?)",
  ).run(
    randomUUID(),
    userIds.student,
    "payment",
    "high",
    "open",
    "Payment confirmed but I cannot open the mathematics pack",
    "I paid on Saturday and received a receipt, but the reader still says I need to purchase.",
    userIds.admin,
    iso(daysAgo(3)),
    iso(daysAgo(3)),
  );

  stmt(
    "INSERT INTO audit_logs (id, actor_user_id, action, resource_type, resource_id, reason, request_id, metadata_json, occurred_at) VALUES (?,?,?,?,?,?,?,?,?)",
  ).run(
    randomUUID(),
    userIds.editor,
    "question.published",
    "question_version",
    mathBank[0].qvId,
    "Editorial review complete",
    "seed",
    JSON.stringify({ source: SOURCE.sourceName, reviewer: "Ngozi Adeyemi" }),
    iso(daysAgo(25)),
  );
}
