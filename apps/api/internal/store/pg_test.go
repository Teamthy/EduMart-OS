package store_test

import (
	"strings"
	"testing"

	"github.com/teamthy/edumart/apps/api/internal/store"
)

func TestRewritePlaceholders(t *testing.T) {
	cases := []struct {
		name  string
		query string
		want  string
	}{
		{
			name:  "simple",
			query: `SELECT * FROM users WHERE id = ? AND status = ?`,
			want:  `SELECT * FROM users WHERE id = $1 AND status = $2`,
		},
		{
			name:  "no placeholders",
			query: `SELECT 1`,
			want:  `SELECT 1`,
		},
		{
			name:  "question mark inside a string literal is data",
			query: `SELECT * FROM t WHERE note = 'why?' AND id = ?`,
			want:  `SELECT * FROM t WHERE note = 'why?' AND id = $1`,
		},
		{
			name:  "escaped quote does not end the literal",
			query: `SELECT * FROM t WHERE note = 'it''s ?' AND id = ?`,
			want:  `SELECT * FROM t WHERE note = 'it''s ?' AND id = $1`,
		},
		{
			name:  "double-quoted identifier",
			query: `SELECT "odd?column" FROM t WHERE id = ?`,
			want:  `SELECT "odd?column" FROM t WHERE id = $1`,
		},
		{
			name:  "line comment",
			query: "SELECT 1 -- what?\nFROM t WHERE id = ?",
			want:  "SELECT 1 -- what?\nFROM t WHERE id = $1",
		},
		{
			name:  "block comment",
			query: `SELECT /* really? */ 1 FROM t WHERE id = ?`,
			want:  `SELECT /* really? */ 1 FROM t WHERE id = $1`,
		},
		{
			name:  "dollar quoted body",
			query: `SELECT $$what?$$ FROM t WHERE id = ?`,
			want:  `SELECT $$what?$$ FROM t WHERE id = $1`,
		},
		{
			name:  "tagged dollar quote",
			query: `SELECT $body$what?$body$ FROM t WHERE id = ?`,
			want:  `SELECT $body$what?$body$ FROM t WHERE id = $1`,
		},
		{
			name:  "already numbered is left alone",
			query: `SELECT * FROM t WHERE id = $1`,
			want:  `SELECT * FROM t WHERE id = $1`,
		},
		{
			name:  "insert with many values",
			query: `INSERT INTO t (a, b, c) VALUES (?, ?, ?)`,
			want:  `INSERT INTO t (a, b, c) VALUES ($1, $2, $3)`,
		},
		{
			name:  "on conflict clause",
			query: `INSERT INTO t (a) VALUES (?) ON CONFLICT (a) DO UPDATE SET b = ?`,
			want:  `INSERT INTO t (a) VALUES ($1) ON CONFLICT (a) DO UPDATE SET b = $2`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := store.RewritePlaceholders(tc.query); got != tc.want {
				t.Fatalf("rewrite mismatch\n got: %s\nwant: %s", got, tc.want)
			}
		})
	}
}

func TestRewritePlaceholdersCountsArguments(t *testing.T) {
	query := `UPDATE users SET name = ?, email = ?, updated_at = ? WHERE id = ? AND status != 'closed?'`
	rewritten := store.RewritePlaceholders(query)
	// The literal keeps its question mark; only the four placeholders are numbered.
	if !strings.HasSuffix(rewritten, "AND status != 'closed?'") {
		t.Fatalf("a literal was rewritten: %s", rewritten)
	}
	for _, want := range []string{"$1", "$2", "$3", "$4"} {
		if !strings.Contains(rewritten, want) {
			t.Fatalf("missing %s in: %s", want, rewritten)
		}
	}
	if strings.Contains(rewritten, "$5") {
		t.Fatalf("numbering ran past the argument count: %s", rewritten)
	}
}
