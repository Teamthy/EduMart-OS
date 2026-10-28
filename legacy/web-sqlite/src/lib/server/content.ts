import { q } from "./db";

export type Pathway = { id: string; key: string; name: string; body: string; blurb: string };

export function listPathways(): Pathway[] {
  return q.all<Pathway>(
    "SELECT id, key, name, body, blurb FROM exam_pathways WHERE active = 1 ORDER BY CASE key WHEN 'jamb' THEN 1 WHEN 'waec' THEN 2 WHEN 'neco' THEN 3 ELSE 4 END",
  );
}

export function listSubjects(pathwayId?: string) {
  const rows = pathwayId
    ? q.all<any>(
        `SELECT s.id, s.key, s.name, s.icon, ep.key AS pathway_key FROM subjects s
           JOIN exam_pathways ep ON ep.id = s.exam_pathway_id
          WHERE s.exam_pathway_id = ? AND s.active = 1 ORDER BY s.name`,
        pathwayId,
      )
    : q.all<any>(
        `SELECT s.id, s.key, s.name, s.icon, ep.key AS pathway_key FROM subjects s
           JOIN exam_pathways ep ON ep.id = s.exam_pathway_id
          WHERE s.active = 1 ORDER BY ep.key, s.name`,
      );

  const counts = q.all<{ subject_id: string; n: number }>(
    `SELECT q.subject_id AS subject_id, COUNT(DISTINCT q.id) AS n
       FROM questions q JOIN question_versions qv ON qv.question_id = q.id
      WHERE qv.review_state = 'approved' AND qv.published_at IS NOT NULL AND q.status = 'published'
      GROUP BY q.subject_id`,
  );
  const byId = new Map(counts.map((c) => [c.subject_id, Number(c.n)]));
  return rows.map((r) => ({ ...r, question_count: byId.get(r.id) ?? 0 }));
}

export function listTopics(subjectId: string) {
  const rows = q.all<any>(
    `SELECT t.id, t.key, t.name, t.sort_order,
            (SELECT COUNT(DISTINCT q.id) FROM questions q JOIN question_versions qv ON qv.question_id = q.id
              WHERE q.topic_id = t.id AND qv.review_state = 'approved' AND qv.published_at IS NOT NULL) AS question_count,
            (SELECT COUNT(*) FROM topic_performance tp WHERE tp.topic_id = t.id AND tp.user_id = ?) AS practiced
       FROM topics t WHERE t.subject_id = ? ORDER BY t.sort_order`,
    "",
    subjectId,
  );
  return rows;
}

export function subjectById(subjectId: string) {
  return q.get<any>(
    `SELECT s.id, s.key, s.name, s.icon, ep.id AS pathway_id, ep.key AS pathway_key, ep.name AS pathway_name
       FROM subjects s JOIN exam_pathways ep ON ep.id = s.exam_pathway_id WHERE s.id = ?`,
    subjectId,
  );
}

export function pathwayByKey(key: string) {
  return q.get<any>("SELECT * FROM exam_pathways WHERE key = ?", key);
}

/** Public exam templates a learner can start. Class-only assessments are excluded here. */
export function listMockDefinitions() {
  return q.all<any>(
    `SELECT d.id, d.title, d.type, d.duration_seconds, d.attempt_limit, d.availability_start, d.availability_end,
            d.class_id, d.config_json,
            (SELECT COUNT(*) FROM assessment_items ai WHERE ai.definition_id = d.id) AS question_count,
            (SELECT COUNT(*) FROM assessment_sessions s WHERE s.definition_id = d.id) AS attempts
       FROM assessment_definitions d
      WHERE d.status = 'published' AND d.class_id IS NULL
      ORDER BY d.created_at DESC`,
  );
}

export function definitionById(definitionId: string) {
  return q.get<any>(
    `SELECT d.*, (SELECT COUNT(*) FROM assessment_items ai WHERE ai.definition_id = d.id) AS question_count,
            c.name AS class_name
       FROM assessment_definitions d LEFT JOIN classes c ON c.id = d.class_id
      WHERE d.id = ?`,
    definitionId,
  );
}
