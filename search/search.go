// Package search builds the case-insensitive multi-column search predicate that
// backs Strapi's free-text "search" query parameter.
//
// Strapi exposes a single search term that is matched, case-insensitively,
// against a model-defined set of fields. strapgorm models declare those fields
// by implementing schema.Searchable; the resulting JSON names are recorded on
// Model.SearchableFields. Build turns a search term into an OR of per-column
// ILIKE matches:
//
//	(users.name ILIKE ? OR users.email ILIKE ? OR users.bio ILIKE ?)
//
// with one bound "%term%" argument per searchable column.
//
// # Security
//
// The search term is untrusted and is always bound as a parameter, never
// interpolated into SQL. The columns searched come exclusively from the model's
// schema whitelist (Model.SearchableFields resolved through Model.ColumnFor):
// a searchable JSON name that does not resolve to a known column is a model
// misconfiguration and is reported as an error rather than silently skipped or
// injected. The case-insensitivity is expressed by dialect.ILike, which emits
// "?" placeholders on every supported database.
package search

import (
	"errors"
	"fmt"
	"strings"

	"github.com/faisalcayunda/strapgorm/dialect"
	"github.com/faisalcayunda/strapgorm/schema"
)

// ErrNoSearchableFields is returned by Build when a non-empty search term is
// supplied but the model declares no searchable fields. It lets callers detect
// the misconfiguration (a model that should be searchable but does not
// implement schema.Searchable) instead of silently producing no predicate.
var ErrNoSearchableFields = errors.New("strapgorm/search: model has no searchable fields")

// Clause is a single composable WHERE fragment: a SQL expression with its bound
// arguments.
//
// A zero Clause (empty SQL, nil Args) is a no-op that callers may safely append
// without altering the surrounding query.
type Clause struct {
	// SQL is the search predicate, parenthesized when it spans more than one
	// column, with "?" placeholders for every bound argument.
	SQL string
	// Args holds one bound "%term%" value per searchable column, positionally
	// matching the placeholders in SQL.
	Args []any
}

// Build constructs the search predicate for term against the searchable fields
// of m, qualified by rootTable and made case-insensitive by d.
//
// When term is empty Build returns the zero Clause and a nil error: search is a
// no-op rather than a failure. When term is non-empty but m declares no
// searchable fields, Build returns a wrapped ErrNoSearchableFields so the
// caller can surface the misconfiguration.
//
// Each searchable JSON field is resolved to its database column through m's
// whitelist; an unresolved searchable name yields a wrapped
// schema.ErrUnknownField. The resulting predicate is an OR of per-column ILIKE
// matches, parenthesized when more than one column participates, with the term
// bound as "%term%" once per column.
func Build(m *schema.Model, rootTable, term string, d dialect.Dialect) (Clause, error) {
	if term == "" {
		return Clause{}, nil
	}

	if len(m.SearchableFields) == 0 {
		return Clause{}, fmt.Errorf("%w: %s", ErrNoSearchableFields, m.Table)
	}

	pattern := "%" + term + "%"

	conditions := make([]string, 0, len(m.SearchableFields))
	args := make([]any, 0, len(m.SearchableFields))

	for _, jsonName := range m.SearchableFields {
		column, ok := m.ColumnFor(jsonName)
		if !ok {
			return Clause{}, fmt.Errorf("%w: searchable field %q", schema.ErrUnknownField, jsonName)
		}
		qualified := rootTable + "." + column
		conditions = append(conditions, d.ILike(qualified))
		args = append(args, pattern)
	}

	sql := strings.Join(conditions, " OR ")
	if len(conditions) > 1 {
		sql = "(" + sql + ")"
	}

	return Clause{SQL: sql, Args: args}, nil
}
