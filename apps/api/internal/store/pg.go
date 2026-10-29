package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"

	"github.com/lib/pq"
)

// PostgreSQL adapter.
//
// The query layer is written once, in portable SQL with `?` placeholders — that is what lets the
// same code run on SQLite (local, CI, demo) and PostgreSQL (production). PostgreSQL speaks `$1, $2`
// instead, so the translation happens here, at the driver boundary, rather than in 500 call sites.
//
// Doing it in a `database/sql` wrapper rather than a string helper on every query also means the
// rewrite cannot be forgotten: nothing reaches PostgreSQL without passing through this connector.
//
// Driver choice: lib/pq, not pgx. pgx is the more modern client, but its module graph pulls
// `gopkg.in/yaml.v3` (via a test dependency of pgservicefile), and gopkg.in is unreachable from the
// build environment used to verify this work. lib/pq has no external dependencies, supports
// SCRAM-SHA-256, and swapping it later is a change to this one file.

// postgresConnector wraps a lib/pq connector and rewrites statements on the way through.
type postgresConnector struct {
	inner driver.Connector
}

// NewPostgresConnector builds a connector for a PostgreSQL DSN.
func NewPostgresConnector(dsn string) (driver.Connector, error) {
	inner, err := pq.NewConnector(dsn)
	if err != nil {
		return nil, fmt.Errorf("postgres: parse DSN: %w", err)
	}
	return postgresConnector{inner: inner}, nil
}

func (c postgresConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.inner.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &rewritingConn{Conn: conn}, nil
}

func (c postgresConnector) Driver() driver.Driver {
	return rewritingDriver{base: c.inner.Driver()}
}

// rewritingDriver exists only to satisfy driver.Connector. Connections are always taken through the
// connector, so Open is unreachable in practice and says so rather than silently opening an
// un-rewritten connection.
type rewritingDriver struct {
	base driver.Driver
}

func (d rewritingDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("postgres: connections must go through the connector, not Driver.Open")
}

// rewritingConn translates `?` placeholders before PostgreSQL sees the statement.
type rewritingConn struct {
	driver.Conn
}

func (c *rewritingConn) Prepare(query string) (driver.Stmt, error) {
	return c.prepare(context.Background(), query)
}

func (c *rewritingConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	return c.prepare(ctx, query)
}

func (c *rewritingConn) prepare(ctx context.Context, query string) (driver.Stmt, error) {
	rewritten := RewritePlaceholders(query)

	var (
		stmt driver.Stmt
		err  error
	)
	if preparer, ok := c.Conn.(driver.ConnPrepareContext); ok {
		stmt, err = preparer.PrepareContext(ctx, rewritten)
	} else {
		stmt, err = c.Conn.Prepare(rewritten)
	}
	if err != nil {
		return nil, err
	}
	return &rewritingStmt{Stmt: stmt}, nil
}

// ExecContext and QueryContext rewrite and delegate when the driver supports the fast path, and
// otherwise return ErrSkip so database/sql falls back to Prepare (which rewrites too).
func (c *rewritingConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	execer, ok := c.Conn.(driver.ExecerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	return execer.ExecContext(ctx, RewritePlaceholders(query), args)
}

func (c *rewritingConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	queryer, ok := c.Conn.(driver.QueryerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	return queryer.QueryContext(ctx, RewritePlaceholders(query), args)
}

// rewritingStmt keeps context-aware execution working through the prepared path.
type rewritingStmt struct {
	driver.Stmt
}

func (s *rewritingStmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	if execer, ok := s.Stmt.(driver.StmtExecContext); ok {
		return execer.ExecContext(ctx, args)
	}
	return nil, driver.ErrSkip
}

func (s *rewritingStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	if queryer, ok := s.Stmt.(driver.StmtQueryContext); ok {
		return queryer.QueryContext(ctx, args)
	}
	return nil, driver.ErrSkip
}

// RewritePlaceholders converts `?` placeholders to PostgreSQL's numbered `$1, $2 …` form.
//
// A naive strings.Replace would corrupt queries: schema defaults contain `?` inside string literals
// (e.g. `DEFAULT 'web?'`), and a literal containing an odd question mark would shift every later
// argument. So this walks the string once, tracking the states where a `?` is data rather than a
// placeholder:
//
//   - 'single quoted' strings, where ” is an escaped quote
//   - "double quoted" identifiers
//   - $tag$ dollar-quoted $tag$ bodies
//   - -- line comments
//   - /* block comments */
//
// Anything already written as `$1` (a raw PostgreSQL query) is left alone, and a `?` with no
// following argument simply becomes the next number — the driver will then report a bind-count
// mismatch, which is a better error than a silently wrong result.
func RewritePlaceholders(query string) string {
	var out strings.Builder
	out.Grow(len(query) + 8)

	var (
		number   int
		inSingle bool
		inDouble bool
		dollar   string // active dollar-quote tag, e.g. "$$" or "$body$"
	)

	for i := 0; i < len(query); i++ {
		c := query[i]

		switch {
		case inSingle:
			out.WriteByte(c)
			if c == '\'' {
				if i+1 < len(query) && query[i+1] == '\'' {
					out.WriteByte('\'')
					i++
					continue
				}
				inSingle = false
			}
			continue

		case inDouble:
			out.WriteByte(c)
			if c == '"' {
				if i+1 < len(query) && query[i+1] == '"' {
					out.WriteByte('"')
					i++
					continue
				}
				inDouble = false
			}
			continue

		case dollar != "":
			if strings.HasPrefix(query[i:], dollar) {
				out.WriteString(dollar)
				i += len(dollar) - 1
				dollar = ""
				continue
			}
			out.WriteByte(c)
			continue
		}

		switch c {
		case '\'':
			inSingle = true
			out.WriteByte(c)

		case '"':
			inDouble = true
			out.WriteByte(c)

		case '-':
			// Line comment: copy verbatim to end of line.
			if i+1 < len(query) && query[i+1] == '-' {
				end := strings.IndexByte(query[i:], '\n')
				if end < 0 {
					out.WriteString(query[i:])
					i = len(query)
					continue
				}
				out.WriteString(query[i : i+end+1])
				i += end
				continue
			}
			out.WriteByte(c)

		case '/':
			// Block comment.
			if i+1 < len(query) && query[i+1] == '*' {
				end := strings.Index(query[i+2:], "*/")
				if end < 0 {
					out.WriteString(query[i:])
					i = len(query)
					continue
				}
				out.WriteString(query[i : i+2+end+2])
				i += 2 + end + 1
				continue
			}
			out.WriteByte(c)

		case '$':
			// Dollar-quoted string or an existing $n placeholder: copy as-is.
			if tag := dollarTag(query[i:]); tag != "" {
				dollar = tag
				out.WriteString(tag)
				i += len(tag) - 1
				continue
			}
			out.WriteByte(c)

		case '?':
			number++
			fmt.Fprintf(&out, "$%d", number)

		default:
			out.WriteByte(c)
		}
	}
	return out.String()
}

// dollarTag recognises the opening of a dollar-quoted string: `$$` or `$tag$`.
func dollarTag(s string) string {
	if !strings.HasPrefix(s, "$") {
		return ""
	}
	if strings.HasPrefix(s, "$$") {
		return "$$"
	}
	for i := 1; i < len(s); i++ {
		c := s[i]
		if c == '$' {
			return s[:i+1]
		}
		if !(c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')) {
			return ""
		}
	}
	return ""
}

// openPostgres builds the pool through the rewriting connector and tunes it for a real server.
func openPostgres(dsn string) (*sql.DB, error) {
	connector, err := NewPostgresConnector(dsn)
	if err != nil {
		return nil, err
	}
	db := sql.OpenDB(connector)
	// PostgreSQL handles concurrent writers properly, unlike SQLite: a real pool, not one connection.
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(10)
	return db, nil
}
