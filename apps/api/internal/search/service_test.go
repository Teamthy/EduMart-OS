package search

import (
	"strings"
	"testing"
)

// The tokeniser is the contract between what a person types and what the index stores. Every surprising
// search result starts here, so the rules are pinned rather than left to "it seems to work".
func TestTerms(t *testing.T) {
	cases := []struct {
		name  string
		query string
		want  []string
	}{
		{"lowercases", "Algebra", []string{"algebra"}},
		{"splits on punctuation", "algebra, geometry; trig!", []string{"algebra", "geometry", "trig"}},
		{"drops stopwords", "the mathematics of physics", []string{"mathematics", "physics"}},
		{"drops single letters", "a b c calculus", []string{"calculus"}},
		{"deduplicates", "biology biology BIOLOGY", []string{"biology"}},
		{"keeps digits", "waec 2025", []string{"waec", "2025"}},
		// Diacritics are kept: stripping them would make a correctly spelled Yoruba word unsearchable,
		// and the syllabus contains those words.
		{"keeps non-ascii letters", "ọ̀rọ̀", []string{"ọ̀rọ̀"}},
		{"caps at the term limit", "one two three four five six seven eight nine ten eleven", nil},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := Terms(testCase.query)
			if testCase.name == "caps at the term limit" {
				// "one", "two", "ten", "eleven" are all real terms; the cap has to hold whatever they are.
				if len(got) != MaxQueryTerms {
					t.Fatalf("expected %d terms, got %d: %v", MaxQueryTerms, len(got), got)
				}
				return
			}
			if strings.Join(got, ",") != strings.Join(testCase.want, ",") {
				t.Fatalf("Terms(%q) = %v, want %v", testCase.query, got, testCase.want)
			}
		})
	}
}

func TestTermsOnPunctuationOnly(t *testing.T) {
	// A query of "%" or "!!!" must produce no terms rather than matching everything. Without the
	// escaping in escapeLike, "%" is a wildcard that returns the whole corpus — a search that answers a
	// question nobody asked, and one that costs a full scan to answer.
	if got := Terms("%%% ___ !!!"); len(got) != 0 {
		t.Fatalf("expected no terms, got %v", got)
	}
}

func TestEscapeLike(t *testing.T) {
	// If these are not escaped, a user's wildcard does whatever the database decides a wildcard means.
	cases := map[string]string{
		"algebra": "algebra",
		"100%":    `100\%`,
		"a_b":     `a\_b`,
		`c:\x`:    `c:\\x`,
	}
	for input, want := range cases {
		if got := escapeLike(input); got != want {
			t.Fatalf("escapeLike(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestScoreRanksATitleAboveABodyMention(t *testing.T) {
	title := document{title: "Algebra", subtitle: "", body: "unrelated"}
	bodyOnly := document{title: "Geometry", subtitle: "", body: "some algebra inside"}
	if score(title, []string{"algebra"}) <= score(bodyOnly, []string{"algebra"}) {
		t.Fatal("a title match must outrank a body mention, or the first result is never the obvious one")
	}
}

func TestScoreRewardsAnExactTitle(t *testing.T) {
	exact := document{title: "algebra"}
	partial := document{title: "Algebraic manipulation"}
	if score(exact, []string{"algebra"}) <= score(partial, []string{"algebra"}) {
		t.Fatal("an exact title must beat a longer title that merely contains the term")
	}
}

func TestScoreComposition(t *testing.T) {
	// The formula is a judgement, so it is pinned: title 3, subtitle 2, body 1, plus 5 for an exact title.
	// Writing it down here is what makes a future "improvement" to ranking a decision rather than a diff.
	doc := document{title: "physics", subtitle: "jamb physics", body: "physics physics"}
	if got := score(doc, []string{"physics"}); got != 3+2+1+5 {
		t.Fatalf("expected title+subtitle+body+exact = 11, got %d", got)
	}
	// A term the document does not contain must not change its score at all.
	if score(doc, []string{"physics", "mathematics"}) != score(doc, []string{"physics"}) {
		t.Fatal("a missing term changed the score")
	}
}

func TestSnippetIsBoundedAndCentred(t *testing.T) {
	body := strings.Repeat("filler ", 40) + "algebra is the study of symbols" + strings.Repeat(" filler", 40)
	got := snippet(body, []string{"algebra"})
	if got == "" {
		t.Fatal("expected a snippet around the matching term")
	}
	// runes, not bytes: the window is measured in characters so a multi-byte word cannot blow past it
	if len([]rune(got)) > 84 {
		t.Fatalf("snippet is %d runes, which is not the bounded window it claims to be: %q", len([]rune(got)), got)
	}
	if !strings.Contains(got, "algebra") {
		t.Fatalf("the snippet does not contain the term it was built around: %q", got)
	}
	if !strings.HasPrefix(got, "…") {
		t.Fatalf("a snippet cut from the middle should say so: %q", got)
	}
}

func TestSnippetIsEmptyWhenNothingMatches(t *testing.T) {
	// Returning the first 80 characters of an unrelated document is worse than returning nothing: it
	// looks like a match and reads like noise.
	if got := snippet("a document about geometry", []string{"algebra"}); got != "" {
		t.Fatalf("expected no snippet, got %q", got)
	}
}
