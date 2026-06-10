// Package dialect abstracts the small SQL differences between the databases
// strapgorm supports (PostgreSQL, MySQL, SQLite).
//
// strapgorm targets GORM, which normalizes the "?" placeholder to the
// dialect-specific form (for example "$1" on PostgreSQL) at execution time.
// Dialect fragments therefore always emit "?" placeholders and never "$n".
//
// The column identifiers passed to the helpers are assumed to already be
// fully qualified and quoted by the caller (column identifiers always come from
// schema metadata, never from raw user input). The helpers only decide how the
// comparison operator and case-sensitivity are expressed for each database.
package dialect

import "strings"

// Dialect describes the database-specific SQL fragments strapgorm needs.
//
// Implementations are stateless and safe for concurrent use; the package
// exposes a single shared value per database via the Postgres, MySQL, and
// SQLite singletons.
type Dialect interface {
	// Name returns the canonical lowercase dialect name
	// ("postgres", "mysql", or "sqlite").
	Name() string

	// Quote wraps a single identifier (a column or table name) in the
	// database-specific quoting characters, escaping any embedded quote
	// characters. The input must be a bare identifier; callers that need to
	// qualify "table.column" should Quote each segment and join them with ".".
	Quote(ident string) string

	// Like returns a case-sensitive pattern-match fragment of the form
	// "<col> LIKE ?" for the already-qualified column expression col.
	Like(col string) string

	// ILike returns a case-insensitive pattern-match fragment for the
	// already-qualified column expression col. On PostgreSQL it uses the
	// native ILIKE operator ("<col> ILIKE ?"); on MySQL and SQLite it falls
	// back to "LOWER(<col>) LIKE LOWER(?)".
	ILike(col string) string
}

// postgres implements Dialect for PostgreSQL.
type postgres struct{}

func (postgres) Name() string { return "postgres" }

func (postgres) Quote(ident string) string {
	return `"` + strings.ReplaceAll(ident, `"`, `""`) + `"`
}

func (postgres) Like(col string) string { return col + " LIKE ?" }

func (postgres) ILike(col string) string { return col + " ILIKE ?" }

// mysql implements Dialect for MySQL.
type mysql struct{}

func (mysql) Name() string { return "mysql" }

func (mysql) Quote(ident string) string {
	return "`" + strings.ReplaceAll(ident, "`", "``") + "`"
}

func (mysql) Like(col string) string { return col + " LIKE ?" }

func (mysql) ILike(col string) string { return "LOWER(" + col + ") LIKE LOWER(?)" }

// sqlite implements Dialect for SQLite.
type sqlite struct{}

func (sqlite) Name() string { return "sqlite" }

func (sqlite) Quote(ident string) string {
	return `"` + strings.ReplaceAll(ident, `"`, `""`) + `"`
}

func (sqlite) Like(col string) string { return col + " LIKE ?" }

func (sqlite) ILike(col string) string { return "LOWER(" + col + ") LIKE LOWER(?)" }

// Postgres is the shared Dialect for PostgreSQL.
var Postgres Dialect = postgres{}

// MySQL is the shared Dialect for MySQL.
var MySQL Dialect = mysql{}

// SQLite is the shared Dialect for SQLite.
var SQLite Dialect = sqlite{}

// ByName maps a driver or dialect name to a Dialect.
//
// Recognized aliases (case-insensitive):
//
//	"postgres", "postgresql", "pg" -> Postgres
//	"mysql"                        -> MySQL
//	"sqlite", "sqlite3"            -> SQLite
//
// Any unrecognized name defaults to Postgres so that callers always receive a
// usable Dialect.
func ByName(name string) Dialect {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "postgres", "postgresql", "pg":
		return Postgres
	case "mysql":
		return MySQL
	case "sqlite", "sqlite3":
		return SQLite
	default:
		return Postgres
	}
}
