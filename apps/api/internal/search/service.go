// Package search answers "where is the thing I am looking for" across the catalogue and the syllabus.
//
// The design is deliberately small and boring, and three decisions carry it:
//
//  1. **The index is a projection, not the truth.** `search_tokens` decides *matching*; the source tables
//     decide *visibility*. A stale index therefore costs recall — a product published five minutes ago may
//     not be found — but never correctness: an unpublished product cannot leak out of an endpoint that
//     re-checks `products.status` on every query.
//
//  2. **Tokens, not `LIKE '%term%'`.** A leading wildcard cannot use an index on either engine, so every
//     search would cost a scan of the whole text. Tokens turn a search into `token LIKE 'alg%'`, which is
//     a prefix range scan, and keeps one SQL statement working on SQLite and PostgreSQL. A search feature
//     that only works on the production engine is a search feature CI cannot test.
//
//  3. **Ranking in Go, not in SQL.** `ts_rank` and SQLite's FTS5 rank on different scales with different
//     tokenisers, so the same query would order results differently on the two engines — a difference that
//     no test would catch and every user would. One scorer, one order, both engines.
//
// What this is not: semantic search, stemming beyond a plural rule, fuzzy matching, or typo tolerance.
// "aljebra" finds nothing, and the empty state has to say so rather than pretend.
package search

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/teamthy/edumart/apps/api/internal/store"
)

// MaxQueryTerms bounds how much work one request can ask for. Beyond this a query is a paragraph, and a
// paragraph ANDed against every token matches nothing.
const MaxQueryTerms = 8

// MaxResults bounds the page, whatever the caller asks for.
const MaxResults = 50

// DefaultResults is the page size when the caller does not choose one.
const DefaultResults = 20

// candidateLimit caps how many documents are ranked before the page is cut. It exists so a one-letter
// prefix ("a") cannot pull the entire corpus into memory to display twenty rows.
const candidateLimit = 500

// Result is one findable thing.
type Result struct {
	Type     string `json:"type"` // product | subject | topic
	ID       string `json:"id"`
	Title    string `json:"title"`
	Subtitle string `json:"subtitle"`
	Snippet  string `json:"snippet,omitempty"`
	Href     string `json:"href"`
	Score    int    `json:"score"`
}

// Service reads and rebuilds the search projection.
type Service struct {
	db *store.Store
}

// New builds the service.
func New(db *store.Store) *Service { return &Service{db: db} }

// ErrEmptyQuery is returned when there is nothing to search for. It is a validation error, not a crash:
// a search box people can submit empty must answer sensibly.
var ErrEmptyQuery = errors.New("search: empty query")

// Terms turns a user's query into the tokens the index stores.
//
// It is exported because the ranking explanation depends on it: when a result looks wrong, the question is
// always "what did the server think I typed".
func Terms(query string) []string {
	seen := map[string]bool{}
	terms := make([]string, 0, MaxQueryTerms)
	for _, raw := range strings.Fields(strings.ToLower(query)) {
		cleaned := strings.Map(func(r rune) rune {
			switch {
			case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
				return r
			// Keep the letters Nigerians actually type: Yoruba, Hausa and Igbo are written with
			// characters outside ASCII, and stripping them turns "ọ̀rọ̀" into an empty search. The
			// ranges are Latin-1 Supplement through Latin Extended-B, Latin Extended Additional
			// (which holds ọ, ẹ, ṣ), and the combining marks that carry the tone.
			case r >= 0x00C0 && r <= 0x024F, r >= 0x1E00 && r <= 0x1EFF, r >= 0x0300 && r <= 0x036F:
				return r
			default:
				return ' '
			}
		}, raw)
		for _, token := range strings.Fields(cleaned) {
			if len([]rune(token)) < 2 || stopwords[token] {
				continue
			}
			if seen[token] {
				continue
			}
			seen[token] = true
			terms = append(terms, token)
			if len(terms) == MaxQueryTerms {
				return terms
			}
		}
	}
	return terms
}

// stopwords are dropped because they match almost every document and therefore order nothing. "the
// mathematics of physics" should be a search for mathematics and physics.
var stopwords = map[string]bool{
	"a": true, "an": true, "and": true, "are": true, "as": true, "at": true, "be": true, "by": true,
	"for": true, "from": true, "in": true, "is": true, "it": true, "of": true, "on": true, "or": true,
	"that": true, "the": true, "to": true, "was": true, "with": true,
}

// Search matches the query against the index, then filters by what the caller is allowed to see.
func (s *Service) Search(ctx context.Context, query, entityType string, limit int) ([]Result, []string, error) {
	terms := Terms(query)
	if len(terms) == 0 {
		return nil, terms, ErrEmptyQuery
	}
	if limit <= 0 {
		limit = DefaultResults
	}
	if limit > MaxResults {
		limit = MaxResults
	}

	// Intersect term by term. Starting from the smallest set would be faster; starting from the first term
	// keeps the code readable, and the bound is the same either way.
	var candidates map[string]bool
	for _, term := range terms {
		ids, err := s.documentsMatching(ctx, term)
		if err != nil {
			return nil, terms, err
		}
		if candidates == nil {
			candidates = ids
			continue
		}
		for id := range candidates {
			if !ids[id] {
				delete(candidates, id)
			}
		}
		if len(candidates) == 0 {
			// An empty slice, not nil: `null` in a JSON array field forces every client to special-case
			// it, and a client that forgets renders an error where it should render "no results".
			return []Result{}, terms, nil
		}
	}

	docs, err := s.load(ctx, candidates)
	if err != nil {
		return nil, terms, err
	}

	visible, err := s.visible(ctx, docs)
	if err != nil {
		return nil, terms, err
	}

	results := make([]Result, 0, len(docs))
	for _, doc := range docs {
		if !visible[doc.entityType+"\x00"+doc.entityID] {
			continue
		}
		if entityType != "" && entityType != "all" && doc.entityType != entityType {
			continue
		}
		results = append(results, Result{
			Type:     doc.entityType,
			ID:       doc.entityID,
			Title:    doc.title,
			Subtitle: doc.subtitle,
			Snippet:  snippet(doc.body, terms),
			Href:     doc.href,
			Score:    score(doc, terms),
		})
	}

	// Stable and total: a search that reorders the same results between two identical requests looks
	// broken to the person using it, and makes a flaky test look like a real one.
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		if results[i].Title != results[j].Title {
			return results[i].Title < results[j].Title
		}
		if results[i].Type != results[j].Type {
			return results[i].Type < results[j].Type
		}
		return results[i].ID < results[j].ID
	})
	if len(results) > limit {
		results = results[:limit]
	}
	return results, terms, nil
}

// documentsMatching returns the ids whose tokens start with the term.
func (s *Service) documentsMatching(ctx context.Context, term string) (map[string]bool, error) {
	rows, err := s.db.DB.QueryContext(ctx,
		`SELECT DISTINCT document_id FROM search_tokens WHERE token LIKE ? LIMIT ?`,
		escapeLike(term)+"%", candidateLimit)
	if err != nil {
		return nil, fmt.Errorf("search tokens: %w", err)
	}
	defer func() { _ = rows.Close() }()

	ids := make(map[string]bool, 64)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids[id] = true
	}
	return ids, rows.Err()
}

// escapeLike neutralises the two wildcards a user can type. Without this, a query of "%" matches the whole
// corpus and "_" matches any single character: both are surprises rather than searches.
func escapeLike(term string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return replacer.Replace(term)
}

type document struct {
	id         string
	entityType string
	entityID   string
	title      string
	subtitle   string
	body       string
	href       string
}

func (s *Service) load(ctx context.Context, ids map[string]bool) ([]document, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	placeholders := make([]string, 0, len(ids))
	args := make([]any, 0, len(ids))
	for id := range ids {
		placeholders = append(placeholders, "?")
		args = append(args, id)
	}
	query := `SELECT id, entity_type, entity_id, title, subtitle, body, href FROM search_documents
		WHERE id IN (` + strings.Join(placeholders, ",") + `)`

	rows, err := s.db.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("search documents: %w", err)
	}
	defer func() { _ = rows.Close() }()

	docs := make([]document, 0, len(ids))
	for rows.Next() {
		var doc document
		if err := rows.Scan(&doc.id, &doc.entityType, &doc.entityID, &doc.title, &doc.subtitle, &doc.body, &doc.href); err != nil {
			return nil, err
		}
		docs = append(docs, doc)
	}
	return docs, rows.Err()
}

// visible re-checks every candidate against the table that owns it.
//
// This is the query that makes the whole design safe. The index can be minutes stale; the source row cannot
// be. A product that was unpublished, a subject that was deactivated or a topic whose subject was
// deactivated must disappear from results at the moment that happens, not at the next reindex.
func (s *Service) visible(ctx context.Context, docs []document) (map[string]bool, error) {
	byType := map[string][]string{}
	for _, doc := range docs {
		byType[doc.entityType] = append(byType[doc.entityType], doc.entityID)
	}

	visible := make(map[string]bool, len(docs))
	checks := map[string]string{
		"product": `SELECT id FROM products WHERE id IN (%s) AND status = 'published'`,
		// A topic inherits its subject's availability: a deactivated subject must not keep surfacing its
		// topics, or turning a subject off would not actually turn it off.
		"subject": `SELECT s.id FROM subjects s WHERE s.id IN (%s) AND s.active = 1`,
		"topic": `SELECT t.id FROM topics t JOIN subjects s ON s.id = t.subject_id
			WHERE t.id IN (%s) AND s.active = 1`,
	}
	for entityType, ids := range byType {
		query, ok := checks[entityType]
		if !ok {
			// An entity type with no visibility rule is a programming error, and the safe reading of an
			// unknown rule is "not visible" rather than "visible".
			continue
		}
		placeholders := make([]string, 0, len(ids))
		args := make([]any, 0, len(ids))
		for _, id := range ids {
			placeholders = append(placeholders, "?")
			args = append(args, id)
		}
		rows, err := s.db.DB.QueryContext(ctx, fmt.Sprintf(query, strings.Join(placeholders, ",")), args...)
		if err != nil {
			return nil, fmt.Errorf("search visibility: %w", err)
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				_ = rows.Close()
				return nil, err
			}
			visible[entityType+"\x00"+id] = true
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		_ = rows.Close()
	}
	return visible, nil
}

// score ranks a document for a set of terms.
//
// The weights are a judgement, written down so they can be argued with: a title match is worth three, a
// subtitle two, a body mention one, and an exact title ("algebra" for "Algebra") earns a bonus that puts it
// above the topic called "Algebraic manipulation" every time.
func score(doc document, terms []string) int {
	title := strings.ToLower(doc.title)
	subtitle := strings.ToLower(doc.subtitle)
	body := strings.ToLower(doc.body)

	total := 0
	for _, term := range terms {
		if strings.Contains(title, term) {
			total += 3
		}
		if strings.Contains(subtitle, term) {
			total += 2
		}
		if strings.Contains(body, term) {
			total++
		}
	}
	for _, term := range terms {
		if title == term {
			total += 5
		}
	}
	return total
}

// snippet returns a short window of the body around the first matching term, so a result explains itself.
//
// It is bounded hard: a search endpoint that can be made to echo an arbitrarily long document is a
// bandwidth amplifier, and the excerpt is only ever shown in a card.
func snippet(body string, terms []string) string {
	if body == "" {
		return ""
	}
	lowered := strings.ToLower(body)
	at := -1
	for _, term := range terms {
		if i := strings.Index(lowered, term); i >= 0 && (at < 0 || i < at) {
			at = i
		}
	}
	if at < 0 {
		return ""
	}
	const width = 80
	start := at - width/2
	if start < 0 {
		start = 0
	}
	end := start + width
	runes := []rune(body)
	if end > len(runes) {
		end = len(runes)
	}
	if start > len(runes) {
		return ""
	}
	excerpt := strings.TrimSpace(string(runes[start:end]))
	if start > 0 {
		excerpt = "…" + excerpt
	}
	if end < len(runes) {
		excerpt += "…"
	}
	return excerpt
}

// Stats describes the projection, for the admin endpoint and for the readiness of a reindex.
type Stats struct {
	Documents int       `json:"documents"`
	Tokens    int       `json:"tokens"`
	BuiltAt   time.Time `json:"built_at"`
}

// Reindex rebuilds the whole projection from the source tables.
//
// Full rebuild rather than incremental updates, on purpose: the corpus is small, the operation is
// idempotent, and an incremental path that misses one write site is a bug that only shows up as "my new
// product is not searchable", which nobody reports as a bug. When the corpus is large enough that a
// rebuild is slow, this becomes a background job — but it stays one code path.
func (s *Service) Reindex(ctx context.Context) (Stats, error) {
	started := time.Now()
	now := store.Now()

	type row struct {
		entityType, entityID, title, subtitle, body, href string
	}
	var rows []row

	productRows, err := s.db.DB.QueryContext(ctx, `
		SELECT p.id, p.title, p.subtitle, p.description, p.slug, p.subject_key, p.exam_key
		FROM products p WHERE p.status = 'published'`)
	if err != nil {
		return Stats{}, fmt.Errorf("index products: %w", err)
	}
	for productRows.Next() {
		var id, title, subtitle, description, _, subjectKey, examKey string
		if err := productRows.Scan(&id, &title, &subtitle, &description, new(string), &subjectKey, &examKey); err != nil {
			_ = productRows.Close()
			return Stats{}, err
		}
		// The subject and exam keys are part of what a learner would search for ("jamb physics book").
		rows = append(rows, row{
			entityType: "product", entityID: id, title: title, subtitle: subtitle,
			body: strings.TrimSpace(description + " " + subjectKey + " " + examKey),
			href: "/checkout/new?productId=" + id,
		})
	}
	if err := productRows.Err(); err != nil {
		_ = productRows.Close()
		return Stats{}, err
	}
	_ = productRows.Close()

	subjectRows, err := s.db.DB.QueryContext(ctx, `
		SELECT s.id, s.name, p.name FROM subjects s
		JOIN exam_pathways p ON p.id = s.exam_pathway_id
		WHERE s.active = 1`)
	if err != nil {
		return Stats{}, fmt.Errorf("index subjects: %w", err)
	}
	for subjectRows.Next() {
		var id, name, examName string
		if err := subjectRows.Scan(&id, &name, &examName); err != nil {
			_ = subjectRows.Close()
			return Stats{}, err
		}
		rows = append(rows, row{
			entityType: "subject", entityID: id, title: name, subtitle: examName,
			body: name + " " + examName, href: "/practice/" + id,
		})
	}
	if err := subjectRows.Err(); err != nil {
		_ = subjectRows.Close()
		return Stats{}, err
	}
	_ = subjectRows.Close()

	topicRows, err := s.db.DB.QueryContext(ctx, `
		SELECT t.id, t.name, s.name, s.id FROM topics t
		JOIN subjects s ON s.id = t.subject_id
		WHERE s.active = 1`)
	if err != nil {
		return Stats{}, fmt.Errorf("index topics: %w", err)
	}
	for topicRows.Next() {
		var id, name, subjectName, subjectID string
		if err := topicRows.Scan(&id, &name, &subjectName, &subjectID); err != nil {
			_ = topicRows.Close()
			return Stats{}, err
		}
		// A topic result opens the practice form for its subject with that topic already ticked. Landing
		// somebody on a list of subjects and asking them to find the word they just searched for is not
		// search, it is a suggestion to search again.
		rows = append(rows, row{
			entityType: "topic", entityID: id, title: name, subtitle: subjectName,
			body: name + " " + subjectName, href: "/practice/" + subjectID + "?topic=" + id,
		})
	}
	if err := topicRows.Err(); err != nil {
		_ = topicRows.Close()
		return Stats{}, err
	}
	_ = topicRows.Close()

	documentCount := 0
	tokenCount := 0
	err = s.db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM search_tokens`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM search_documents`); err != nil {
			return err
		}
		for _, item := range rows {
			id := item.entityType + ":" + item.entityID
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO search_documents (id, entity_type, entity_id, title, subtitle, body, href, updated_at)
				VALUES (?,?,?,?,?,?,?,?)`,
				id, item.entityType, item.entityID, item.title, item.subtitle, item.body, item.href, now); err != nil {
				return err
			}
			documentCount++
			// Deduplicate per (document, token, field) here rather than relying on the primary key to
			// reject duplicates: the insert would then error on the second occurrence of a common word.
			tokens := map[string]bool{}
			fields := []struct{ name, text string }{
				{"title", item.title},
				{"subtitle", item.subtitle},
				{"body", item.body},
			}
			for _, field := range fields {
				for _, token := range Terms(field.text) {
					key := field.name + "\x00" + token
					if tokens[key] {
						continue
					}
					tokens[key] = true
					if _, err := tx.ExecContext(ctx,
						`INSERT INTO search_tokens (document_id, token, field) VALUES (?,?,?)`,
						id, token, field.name); err != nil {
						return err
					}
					tokenCount++
				}
			}
		}
		return nil
	})
	if err != nil {
		return Stats{}, err
	}

	return Stats{Documents: documentCount, Tokens: tokenCount, BuiltAt: started}, nil
}
