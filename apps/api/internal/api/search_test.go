package api_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/teamthy/edumart/apps/api/internal/search"
)

// resultsOf pulls the result list out of a search response and, helpfully, says what the API actually sent
// when the shape is not what the test expected. A nil-pointer panic in a test is a wasted hour.
func resultsOf(t *testing.T, payload map[string]any) []map[string]any {
	t.Helper()
	raw, ok := payload["results"]
	if !ok {
		t.Fatalf("no results key in %v", payload)
	}
	// JSON arrays of objects decode to []any of map[string]any.
	list, ok := raw.([]any)
	if !ok {
		t.Fatalf("results is %T, not an array: %v", raw, payload)
	}
	out := make([]map[string]any, 0, len(list))
	for _, item := range list {
		entry, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("a result is %T, not an object", item)
		}
		out = append(out, entry)
	}
	return out
}

func titles(results []map[string]any) []string {
	out := make([]string, 0, len(results))
	for _, result := range results {
		out = append(out, fmt.Sprint(result["title"]))
	}
	return out
}

func TestSearchFindsCatalogueAndSyllabus(t *testing.T) {
	h := newHarness(t)

	// No token: discovery is public, because somebody deciding whether to create an account should be
	// able to check whether the thing they want exists.
	status, payload := h.do("GET", "/v1/search?q=mathematics", "", nil)
	if status != 200 {
		t.Fatalf("search returned %d: %v", status, payload)
	}

	results := resultsOf(t, payload)
	if len(results) == 0 {
		t.Fatal("a search for the most common subject in the syllabus returned nothing")
	}

	var sawSubject, sawProduct bool
	for _, result := range results {
		switch result["type"] {
		case "subject":
			sawSubject = true
		case "product":
			sawProduct = true
		}
		if result["href"] == "" {
			t.Fatalf("a result with nowhere to go is not a result: %v", result)
		}
	}
	if !sawSubject {
		t.Fatalf("expected the Mathematics subject in %v", titles(results))
	}
	if !sawProduct {
		t.Fatalf("expected at least one product mentioning mathematics in %v", titles(results))
	}

	// The terms echo back what the server searched for. When a result looks wrong, this is the first
	// thing anybody asks, and guessing is expensive.
	terms, _ := payload["terms"].([]any)
	if len(terms) != 1 || terms[0] != "mathematics" {
		t.Fatalf("expected terms [mathematics], got %v", payload["terms"])
	}
}

func TestSearchRanksTheSubjectAboveAMerelyRelatedProduct(t *testing.T) {
	h := newHarness(t)
	_, payload := h.do("GET", "/v1/search?q=mathematics", "", nil)
	results := resultsOf(t, payload)
	if len(results) < 2 {
		t.Fatalf("need at least two results to talk about order, got %v", titles(results))
	}
	// The subject is literally called "Mathematics"; the product conains the word in its title. An exact
	// match must come first, or the ranking is decoration.
	if !strings.EqualFold(fmt.Sprint(results[0]["title"]), "Mathematics") {
		t.Fatalf("expected the exact subject first, got %v", titles(results))
	}
	if results[0]["type"] != "subject" {
		t.Fatalf("expected a subject first, got %v", results[0])
	}
}

func TestSearchMatchesPrefixesAndMultipleTerms(t *testing.T) {
	h := newHarness(t)

	// Prefix matching is what makes the index work: without it, search is a substring scan.
	_, prefix := h.do("GET", "/v1/search?q=alg", "", nil)
	if len(resultsOf(t, prefix)) == 0 {
		t.Fatal("a prefix of a seeded topic (algebra) found nothing")
	}

	// Every term must match: "algebra physics" is a narrower question, not an either/or.
	_, both := h.do("GET", "/v1/search?q=algebra+physics", "", nil)
	for _, result := range resultsOf(t, both) {
		haystack := strings.ToLower(fmt.Sprint(result["title"], " ", result["subtitle"], " ", result["snippet"]))
		if !strings.Contains(haystack, "physics") {
			// A document that matched only the first term means the AND was not applied.
			t.Fatalf("result %q matched fewer than both terms", result["title"])
		}
	}
}

func TestSearchTreatsWildcardsAsText(t *testing.T) {
	h := newHarness(t)
	// Left unescaped, "%" is a wildcard that matches the entire corpus, so a search for "%" would be a
	// slow way to enumerate everything. It must be a search for nothing instead.
	status, payload := h.do("GET", "/v1/search?q=%25", "", nil)
	if status != 200 {
		t.Fatalf("search returned %d", status)
	}
	if results := resultsOf(t, payload); len(results) != 0 {
		t.Fatalf("%%= returned %d results; wildcards are not text", len(results))
	}
}

func TestSearchOnNothingIsAnEmptyAnswerNotAnError(t *testing.T) {
	h := newHarness(t)
	for _, query := range []string{"", "%20%20", "the%20of%20and"} {
		status, payload := h.do("GET", "/v1/search?q="+query, "", nil)
		if status != 200 {
			t.Fatalf("q=%q returned %d, want 200: %v", query, status, payload)
		}
		if results := resultsOf(t, payload); len(results) != 0 {
			t.Fatalf("q=%q returned %d results", query, len(results))
		}
	}
}

func TestSearchRejectsNonsense(t *testing.T) {
	h := newHarness(t)
	cases := map[string]string{
		"limit=0":   "/v1/search?q=algebra&limit=0",
		"limit=abc": "/v1/search?q=algebra&limit=abc",
		"limit=-4":  "/v1/search?q=algebra&limit=-4",
		"type":      "/v1/search?q=algebra&type=invoice",
	}
	for name, path := range cases {
		status, payload := h.do("GET", path, "", nil)
		if status != 400 {
			t.Fatalf("%s returned %d, want 400: %v", name, status, payload)
		}
		errBody, _ := payload["error"].(map[string]any)
		if errBody["code"] != "VALIDATION_ERROR" {
			t.Fatalf("%s returned code %v, want VALIDATION_ERROR", name, errBody["code"])
		}
		details, _ := errBody["details"].([]any)
		if len(details) == 0 {
			t.Fatalf("%s returned no field details, so the client cannot say what is wrong", name)
		}
	}
}

func TestSearchClampsThePageSize(t *testing.T) {
	h := newHarness(t)
	// Asking for more than the ceiling gets the ceiling, not an error and not ten thousand rows.
	status, payload := h.do("GET", "/v1/search?q=e&limit=5000", "", nil)
	if status != 200 {
		t.Fatalf("search returned %d", status)
	}
	if results := resultsOf(t, payload); len(results) > search.MaxResults {
		t.Fatalf("returned %d results, above the %d ceiling", len(results), search.MaxResults)
	}
}

// ── the safety property the whole design exists for ───────────────────────────────────────────────

func TestAnUnpublishedProductCannotBeFoundEvenWithAStaleIndex(t *testing.T) {
	h := newHarness(t)

	// Index it while it is published.
	if _, err := h.store.DB.Exec(
		`INSERT INTO products (id, slug, title, subtitle, description, status, created_at)
		 VALUES ('prod_secret', 'secret-guide', 'Secret Statistics Guide', 'for the test', 'a secret description', 'published', '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("insert product: %v", err)
	}
	if _, err := search.New(h.store).Reindex(t.Context()); err != nil {
		t.Fatalf("reindex: %v", err)
	}

	_, found := h.do("GET", "/v1/search?q=secret", "", nil)
	if len(resultsOf(t, found)) == 0 {
		t.Fatal("the published product should be findable before it is withdrawn")
	}

	// Withdraw it and deliberately do NOT reindex. The index still says it is published; the source table
	// says otherwise. Search must believe the source table, or an unpublished product stays discoverable
	// until somebody remembers to rebuild — which is exactly the window nobody monitors.
	if _, err := h.store.DB.Exec(`UPDATE products SET status = 'draft' WHERE id = 'prod_secret'`); err != nil {
		t.Fatalf("withdraw product: %v", err)
	}
	_, after := h.do("GET", "/v1/search?q=secret", "", nil)
	if results := resultsOf(t, after); len(results) != 0 {
		t.Fatalf("a withdrawn product was still returned from a stale index: %v", titles(results))
	}
}

func TestAWithdrawnSubjectHidesItsTopicsToo(t *testing.T) {
	h := newHarness(t)

	_, before := h.do("GET", "/v1/search?q=algebra", "", nil)
	if len(resultsOf(t, before)) == 0 {
		t.Fatal("algebra should be findable in the seeded syllabus")
	}

	// Turning a subject off must turn its topics off with it. A topic that keeps surfacing leads a
	// learner to a practice screen with nothing behind it.
	if _, err := h.store.DB.Exec(`UPDATE subjects SET active = 0 WHERE key = 'mathematics'`); err != nil {
		t.Fatalf("deactivate subject: %v", err)
	}
	_, after := h.do("GET", "/v1/search?q=algebra", "", nil)
	for _, result := range resultsOf(t, after) {
		if result["type"] == "topic" || result["type"] == "subject" {
			t.Fatalf("a deactivated subject's %v is still searchable: %v", result["type"], result["title"])
		}
	}
}

// ── the rebuild is an admin act ──────────────────────────────────────────────────────────────────

func TestReindexRequiresPrivilege(t *testing.T) {
	h := newHarness(t)

	status, _ := h.do("POST", "/v1/admin/search/reindex", "", nil)
	if status != 401 {
		t.Fatalf("an anonymous reindex returned %d, want 401", status)
	}

	studentToken := h.login("student@edumart.ng", "Student123!")
	status, _ = h.do("POST", "/v1/admin/search/reindex", studentToken, nil)
	if status != 403 {
		t.Fatalf("a learner could rebuild the search index (%d)", status)
	}
}

func TestReindexRebuildsAndReportsWhatItDid(t *testing.T) {
	h := newHarness(t)

	// Start from an empty projection, as a database upgraded from an older release would.
	if _, err := h.store.DB.Exec(`DELETE FROM search_tokens`); err != nil {
		t.Fatalf("clear tokens: %v", err)
	}
	if _, err := h.store.DB.Exec(`DELETE FROM search_documents`); err != nil {
		t.Fatalf("clear documents: %v", err)
	}
	_, empty := h.do("GET", "/v1/search?q=algebra", "", nil)
	if len(resultsOf(t, empty)) != 0 {
		t.Fatal("search returned results with an empty index, which means it is not using the index at all")
	}

	adminToken := h.login("admin@edumart.ng", "Admin123!")
	status, payload := h.do("POST", "/v1/admin/search/reindex", adminToken, nil)
	if status != 200 {
		t.Fatalf("reindex returned %d: %v", status, payload)
	}
	stats, _ := payload["search"].(map[string]any)
	if docs, _ := stats["documents"].(float64); docs < 10 {
		t.Fatalf("reindex reported %v documents for a seeded database, which is too few to be real", stats["documents"])
	}
	if tokens, _ := stats["tokens"].(float64); tokens < 50 {
		t.Fatalf("reindex reported %v tokens, which is too few to be real", stats["tokens"])
	}

	_, rebuilt := h.do("GET", "/v1/search?q=algebra", "", nil)
	if len(resultsOf(t, rebuilt)) == 0 {
		t.Fatal("search still finds nothing after a rebuild that claimed to index documents")
	}
}

// TestReindexDoesNotDuplicateTokens pins idempotency: the rebuild is safe to run repeatedly, which is what
// makes it reasonable to call from the seed, from a deploy step and from an operator's console.
func TestReindexDoesNotDuplicateTokens(t *testing.T) {
	h := newHarness(t)
	before, err := search.New(h.store).Reindex(t.Context())
	if err != nil {
		t.Fatalf("first reindex: %v", err)
	}
	after, err := search.New(h.store).Reindex(t.Context())
	if err != nil {
		t.Fatalf("second reindex: %v", err)
	}
	if before.Documents != after.Documents || before.Tokens != after.Tokens {
		t.Fatalf("reindex is not idempotent: %d/%d then %d/%d",
			before.Documents, before.Tokens, after.Documents, after.Tokens)
	}
}
