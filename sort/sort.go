// Package sort translates Strapi-style sort directives into safe, qualified SQL
// ORDER BY column expressions.
//
// Each raw directive is a "field" or "field:direction" pair where direction is
// case-insensitively "asc" or "desc" (Strapi's "ASC"/"DESC" are also accepted);
// the default direction is ascending. A directive may address a column on the
// root model ("name:desc") or, via dot notation, a column on a directly related
// model ("organization.slug:asc"). Relation sorts register a LEFT JOIN through
// the shared joins.Registry so that the same relation referenced by both
// filtering and sorting contributes exactly one JOIN clause.
//
// # Security model
//
// Field names, relation names, and the subfield of a relation are untrusted
// input drawn from the query string. Every token is resolved against schema
// metadata (the whitelist) before being used: an unknown field yields
// schema.ErrUnknownField and an unknown relation yields schema.ErrUnknownRelation,
// and neither is ever interpolated into SQL. The only strings emitted into the
// generated ORDER BY expressions are column and table identifiers taken from
// schema metadata plus the fixed keywords ASC and DESC; no value from the query
// string reaches the SQL text.
package sort

import (
	"fmt"
	"strings"

	"github.com/faisalcayunda/strapgorm/internal/joins"
	"github.com/faisalcayunda/strapgorm/schema"
)

// direction is the resolved SQL sort direction keyword.
type direction string

const (
	ascending  direction = "ASC"
	descending direction = "DESC"
)

// Build resolves a list of Strapi-style sort directives into qualified ORDER BY
// column expressions for the model m.
//
// Each entry in raw is either a bare field name ("name"), a field with an
// explicit direction ("name:desc"), or a relation field in dot notation
// ("organization.slug:asc"). Direction parsing is case-insensitive and defaults
// to ascending when omitted. Blank entries (after trimming) are skipped. The
// order of the returned expressions follows the order of the input directives.
//
// Root-model directives resolve the field against m's whitelist and produce
// "<rootTable>.<column> <ASC|DESC>". Relation directives resolve the relation
// against m's whitelist, ensure a LEFT JOIN for it through jr (so the join is
// emitted at most once per relation across filtering and sorting), resolve the
// subfield against the related model's whitelist, and produce
// "<relAlias>.<column> <ASC|DESC>".
//
// An unrecognized field yields a wrapped schema.ErrUnknownField; an unrecognized
// relation yields a wrapped schema.ErrUnknownRelation. A malformed directive
// (for example an empty field name or an unrecognized direction token) yields a
// descriptive error. On any error Build returns a nil slice and the error, and
// the registry jr is left in whatever state prior successful directives produced.
func Build(m *schema.Model, raw []string, jr *joins.Registry) (orders []string, err error) {
	for _, entry := range raw {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}

		field, dir, err := splitDirective(entry)
		if err != nil {
			return nil, err
		}

		var order string
		if rel, sub, isRel := strings.Cut(field, "."); isRel {
			order, err = relationOrder(m, rel, sub, dir, jr)
		} else {
			order, err = rootOrder(m, field, dir)
		}
		if err != nil {
			return nil, err
		}
		orders = append(orders, order)
	}
	return orders, nil
}

// splitDirective parses a single "field" or "field:direction" entry into its
// field part and resolved direction. The entry is assumed already trimmed and
// non-empty. The direction token is matched case-insensitively against asc/desc;
// an empty field name or an unrecognized direction is an error.
func splitDirective(entry string) (field string, dir direction, err error) {
	field, dirToken, hasDir := strings.Cut(entry, ":")
	field = strings.TrimSpace(field)
	if field == "" {
		return "", "", fmt.Errorf("strapgorm/sort: malformed sort directive %q: empty field name", entry)
	}

	if !hasDir {
		return field, ascending, nil
	}

	switch strings.ToLower(strings.TrimSpace(dirToken)) {
	case "", "asc":
		return field, ascending, nil
	case "desc":
		return field, descending, nil
	default:
		return "", "", fmt.Errorf("strapgorm/sort: malformed sort directive %q: unknown direction %q", entry, dirToken)
	}
}

// rootOrder builds an ORDER BY expression for a field on the root model.
func rootOrder(m *schema.Model, field string, dir direction) (string, error) {
	f, ok := m.Field(field)
	if !ok || f.Hidden {
		return "", fmt.Errorf("strapgorm/sort: %w: %q", schema.ErrUnknownField, field)
	}
	return qualify(m.Table, f.Column, dir), nil
}

// relationOrder builds an ORDER BY expression for a subfield reached through a
// relation, registering the relation's LEFT JOIN with jr on first use.
func relationOrder(m *schema.Model, relName, sub string, dir direction, jr *joins.Registry) (string, error) {
	sub = strings.TrimSpace(sub)
	if sub == "" {
		return "", fmt.Errorf("strapgorm/sort: malformed sort directive: relation %q has empty subfield", relName)
	}

	rel, ok := m.Relation(relName)
	if !ok || rel.Hidden {
		return "", fmt.Errorf("strapgorm/sort: %w: %q", schema.ErrUnknownRelation, relName)
	}

	// Resolve the subfield against the related model's whitelist so that only
	// known columns are emitted into SQL.
	related, err := schema.Get(rel.RelatedType)
	if err != nil {
		return "", fmt.Errorf("strapgorm/sort: cannot introspect related model for relation %q: %w", relName, err)
	}
	subField, ok := related.Field(sub)
	if !ok || subField.Hidden {
		return "", fmt.Errorf("strapgorm/sort: %w: %q (relation %q)", schema.ErrUnknownField, sub, relName)
	}
	column := subField.Column

	alias := jr.Ensure(rel.Name, func() joins.Join {
		return buildJoin(m, rel, related)
	})

	return qualify(alias, column, dir), nil
}

// buildJoin constructs the LEFT JOIN clause for a relation from m to related.
//
// The clause shape depends on the relation cardinality, mirroring the foreign-
// key placement recorded by the schema package:
//
//   - BelongsTo: the FK lives on the root table, so the related table is joined
//     on its owner key against the root table's FK column.
//   - HasOne / HasMany: the FK lives on the related table, so the related table
//     is joined on its FK column against the root table's primary key.
//   - Many2Many: the relation is reached through its join table, producing two
//     JOIN clauses (root -> join table -> related table).
//
// All identifiers are schema-derived; no query-string token is interpolated.
func buildJoin(m *schema.Model, rel schema.Relation, related *schema.Model) joins.Join {
	switch rel.Type {
	case schema.BelongsTo:
		// LEFT JOIN <relTable> ON <relTable>.<ownerKey> = <rootTable>.<fkColumn>
		sql := fmt.Sprintf(
			"LEFT JOIN %s ON %s.%s = %s.%s",
			rel.Table,
			rel.Table, ownerKey(rel),
			m.Table, rel.FKColumn,
		)
		return joins.Join{SQL: sql, Alias: rel.Table}

	case schema.Many2Many:
		// LEFT JOIN <joinTable> ON <joinTable>.<owner>_id = <rootTable>.<pk>
		// LEFT JOIN <relTable>  ON <relTable>.<pk> = <joinTable>.<related>_id
		ownerFK := schema.ToSnakeCase(m.Type.Name()) + "_id"
		relatedFK := schema.ToSnakeCase(rel.RelatedType.Name()) + "_id"
		sql := fmt.Sprintf(
			"LEFT JOIN %s ON %s.%s = %s.%s LEFT JOIN %s ON %s.%s = %s.%s",
			rel.JoinTable,
			rel.JoinTable, ownerFK,
			m.Table, m.PrimaryKey,
			rel.Table,
			rel.Table, related.PrimaryKey,
			rel.JoinTable, relatedFK,
		)
		return joins.Join{SQL: sql, Alias: rel.Table}

	default: // HasOne, HasMany
		// LEFT JOIN <relTable> ON <relTable>.<fkColumn> = <rootTable>.<pk>
		sql := fmt.Sprintf(
			"LEFT JOIN %s ON %s.%s = %s.%s",
			rel.Table,
			rel.Table, rel.FKColumn,
			m.Table, m.PrimaryKey,
		)
		return joins.Join{SQL: sql, Alias: rel.Table}
	}
}

// ownerKey returns the referenced key on the owning side of a BelongsTo
// relation, defaulting to "id" when the schema did not record one.
func ownerKey(rel schema.Relation) string {
	if rel.OwnerKey == "" {
		return "id"
	}
	return rel.OwnerKey
}

// qualify renders a qualified "<table>.<column> <DIR>" ORDER BY fragment.
func qualify(table, column string, dir direction) string {
	return table + "." + column + " " + string(dir)
}
