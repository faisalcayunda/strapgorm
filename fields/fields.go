// Package fields translates a Strapi-style fields selection into the safe set of
// database columns to place in a SELECT projection for the root model.
//
// A Strapi query restricts the returned columns with repeated fields entries,
// for example fields=name&fields=email. Build resolves each requested field
// json-name against the model's whitelist and returns the corresponding column
// names. The primary-key column is always included (deduplicated) even when it
// was not requested, so that downstream row scanning and relation stitching
// (populate) always have the key available.
//
// # Relation-qualified entries
//
// Strapi also accepts dot-notation entries that select a field on a related
// model (for example organization.slug). Nested field selection on relations is
// the responsibility of the populate layer, not the root SELECT, so Build skips
// any dotted entry: it never contributes a column to the root projection and is
// never treated as an unknown field. Resolving and projecting relation subfields
// is handled where the relation itself is preloaded.
//
// # Security model
//
// Field json-names are untrusted input drawn from the query string. Every entry
// is resolved against schema metadata (the whitelist) via Model.ColumnFor before
// any column name is emitted; an unrecognized field yields a wrapped
// schema.ErrUnknownField and is never interpolated into SQL. The only strings
// returned are column identifiers taken from schema metadata, never raw
// query-string tokens.
package fields

import (
	"fmt"
	"strings"

	"github.com/faisalcayunda/strapgorm/schema"
)

// Build resolves a list of Strapi-style field json-names into the database
// columns to select for the root model m.
//
// Each entry in raw is a field json-name. Blank entries (after trimming) are
// skipped. Relation-qualified entries written in dot notation (for example
// "organization.slug") are skipped for the root projection and documented to be
// handled by the populate layer rather than rejected. Every remaining entry is
// resolved against m's whitelist and contributes its column to the result.
//
// The model's primary-key column is always included exactly once, appended if it
// was not among the requested fields, so that downstream scanning and relation
// stitching always have the key available. Duplicate requests for the same field
// resolve to a single column entry; the order of the returned columns follows the
// order of first appearance in raw, with the primary key last when it was not
// explicitly requested.
//
// An empty raw slice (or one containing only blank/relation-qualified entries
// that select nothing) is treated specially: when no root column is selected,
// Build returns a nil slice, signaling the caller to select all columns. An
// unrecognized field yields a wrapped schema.ErrUnknownField; on any error Build
// returns a nil slice and the error.
func Build(m *schema.Model, raw []string) (columns []string, err error) {
	if len(raw) == 0 {
		return nil, nil
	}

	seen := make(map[string]struct{}, len(raw)+1)

	for _, entry := range raw {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}

		// Relation-qualified entries (dot notation) select a column on a related
		// model; that projection is owned by the populate layer, so skip it here
		// rather than resolving it against the root model's whitelist.
		if strings.Contains(entry, ".") {
			continue
		}

		column, ok := m.ColumnFor(entry)
		if !ok {
			return nil, fmt.Errorf("strapgorm/fields: %w: %q", schema.ErrUnknownField, entry)
		}

		if _, dup := seen[column]; dup {
			continue
		}
		seen[column] = struct{}{}
		columns = append(columns, column)
	}

	// An explicit selection that resolved to no root columns (only blank or
	// relation-qualified entries) is treated as "select all": return nil so the
	// caller does not constrain the projection.
	if len(columns) == 0 {
		return nil, nil
	}

	// Always include the primary key so downstream scanning and relation
	// stitching have the key available, deduplicating if it was requested.
	if _, ok := seen[m.PrimaryKey]; !ok {
		columns = append(columns, m.PrimaryKey)
	}

	return columns, nil
}
