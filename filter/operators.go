// Package filter translates the decoded Strapi filter object of a query string
// into a safe, parameterized SQL WHERE clause.
//
// A filter object is a recursively nested map such as
//
//	{
//	    "name":  {"$contains": "John"},
//	    "age":   {"$gte": 18},
//	    "$or":   [{"email": {"$endsWith": "@x.com"}}, {"role": {"$null": true}}],
//	}
//
// where leaf keys are field JSON names (or dot-notation relation paths) and the
// "$"-prefixed keys are operators or logical groupings.
//
// # Security model
//
// Field names, relation names, and operator names taken from the query string
// are untrusted. Every field and relation is resolved against the schema
// whitelist and every operator against the operator Registry; an unresolved
// token yields a wrapped schema.ErrUnknownField, schema.ErrUnknownRelation, or
// schema.ErrUnknownOperator and is never interpolated into SQL. Column and table
// identifiers are taken exclusively from schema metadata, and operand values are
// always bound as parameters ("?"), never concatenated into the SQL text.
package filter

import (
	"fmt"

	"github.com/faisalcayunda/strapgorm/dialect"
)

// OpFunc renders a single comparison operator into a SQL fragment plus its bound
// arguments.
//
// qualifiedCol is the already-qualified (and schema-derived) column expression,
// for example "users.age". value is the operand, already coerced to the target
// field's Go kind by the builder (for slice-valued operators such as $in and
// $between it is the converted []any). d supplies the dialect-specific
// case-insensitive LIKE form. The returned sql uses "?" placeholders only, and
// args holds exactly the values bound to those placeholders (nil for the
// argument-free null checks).
type OpFunc func(qualifiedCol string, value any, d dialect.Dialect) (sql string, args []any, err error)

// Logical grouping operator names. They are registered so that Registry.Get
// recognizes them, but the builder handles their recursion directly rather than
// invoking their (never-called) OpFunc.
const (
	// OpAnd is the Strapi logical-AND grouping key.
	OpAnd = "$and"
	// OpOr is the Strapi logical-OR grouping key.
	OpOr = "$or"
)

// Registry maps Strapi operator names to their OpFunc implementations.
//
// A Registry is not safe for concurrent modification; build it once (typically
// via Default) and treat it as read-only during query building. Register may be
// used at setup time to add user-defined operators.
type Registry struct {
	ops map[string]OpFunc
}

// Register adds or replaces the operator named name. The name should include the
// leading "$" to match the Strapi convention used in query strings.
func (r *Registry) Register(name string, fn OpFunc) {
	r.ops[name] = fn
}

// Get returns the OpFunc registered under name and whether it was found.
func (r *Registry) Get(name string) (OpFunc, bool) {
	fn, ok := r.ops[name]
	return fn, ok
}

// Default returns a Registry pre-populated with the 17 built-in Strapi
// operators: $eq, $ne, $lt, $lte, $gt, $gte, $in, $notIn, $contains,
// $containsi, $startsWith, $endsWith, $null, $notNull, $between, $and, and $or.
//
// $and and $or are registered only so they are recognized as known operators;
// their grouping semantics are implemented by the builder, and their OpFunc is
// never invoked.
func Default() *Registry {
	r := &Registry{ops: make(map[string]OpFunc)}

	r.ops["$eq"] = binaryOp("=")
	r.ops["$ne"] = binaryOp("<>")
	r.ops["$lt"] = binaryOp("<")
	r.ops["$lte"] = binaryOp("<=")
	r.ops["$gt"] = binaryOp(">")
	r.ops["$gte"] = binaryOp(">=")

	r.ops["$in"] = inOp("IN")
	r.ops["$notIn"] = inOp("NOT IN")

	r.ops["$contains"] = likeOp(patternContains, false)
	r.ops["$containsi"] = likeOp(patternContains, true)
	r.ops["$startsWith"] = likeOp(patternStartsWith, false)
	r.ops["$endsWith"] = likeOp(patternEndsWith, false)

	r.ops["$null"] = nullOp(true)
	r.ops["$notNull"] = nullOp(false)

	r.ops["$between"] = betweenOp

	// Logical groupings: recognized but handled by the builder.
	r.ops[OpAnd] = groupingPlaceholder
	r.ops[OpOr] = groupingPlaceholder

	return r
}

// binaryOp builds an OpFunc emitting "<col> <sqlOp> ?" with the single operand
// bound as an argument.
func binaryOp(sqlOp string) OpFunc {
	return func(col string, value any, _ dialect.Dialect) (string, []any, error) {
		return col + " " + sqlOp + " ?", []any{value}, nil
	}
}

// inOp builds an OpFunc emitting "<col> IN ?" (or "NOT IN") where the slice is
// passed as a single argument so GORM expands it into the placeholder list.
func inOp(sqlOp string) OpFunc {
	return func(col string, value any, _ dialect.Dialect) (string, []any, error) {
		slice, ok := value.([]any)
		if !ok {
			return "", nil, fmt.Errorf("strapgorm/filter: %s requires a list value, got %T", sqlOp, value)
		}
		if len(slice) == 0 {
			return "", nil, fmt.Errorf("strapgorm/filter: %s requires a non-empty list", sqlOp)
		}
		return col + " " + sqlOp + " ?", []any{slice}, nil
	}
}

// patternKind selects how a LIKE operand is wrapped with wildcards.
type patternKind uint8

const (
	patternContains   patternKind = iota // %value%
	patternStartsWith                    // value%
	patternEndsWith                      // %value
)

// likeOp builds an OpFunc emitting a LIKE/ILIKE fragment with the operand
// wrapped into the appropriate "%"-padded pattern. When insensitive is true the
// dialect's case-insensitive form is used.
func likeOp(kind patternKind, insensitive bool) OpFunc {
	return func(col string, value any, d dialect.Dialect) (string, []any, error) {
		s, ok := value.(string)
		if !ok {
			return "", nil, fmt.Errorf("strapgorm/filter: LIKE operators require a string value, got %T", value)
		}
		var pattern string
		switch kind {
		case patternStartsWith:
			pattern = s + "%"
		case patternEndsWith:
			pattern = "%" + s
		default:
			pattern = "%" + s + "%"
		}
		if insensitive {
			return d.ILike(col), []any{pattern}, nil
		}
		return d.Like(col), []any{pattern}, nil
	}
}

// nullOp builds an OpFunc for the $null / $notNull checks. The operand is a
// boolean: for $null, true => "IS NULL" and false => "IS NOT NULL"; $notNull
// inverts that mapping. No arguments are bound.
func nullOp(isNullOp bool) OpFunc {
	return func(col string, value any, _ dialect.Dialect) (string, []any, error) {
		b, ok := value.(bool)
		if !ok {
			return "", nil, fmt.Errorf("strapgorm/filter: null checks require a boolean value, got %T", value)
		}
		// wantNull reports whether the resulting fragment should be IS NULL.
		wantNull := b
		if !isNullOp {
			wantNull = !b
		}
		if wantNull {
			return col + " IS NULL", nil, nil
		}
		return col + " IS NOT NULL", nil, nil
	}
}

// betweenOp emits "<col> BETWEEN ? AND ?" from a two-element slice operand,
// binding the bounds as two arguments.
func betweenOp(col string, value any, _ dialect.Dialect) (string, []any, error) {
	slice, ok := value.([]any)
	if !ok {
		return "", nil, fmt.Errorf("strapgorm/filter: $between requires a 2-element list value, got %T", value)
	}
	if len(slice) != 2 {
		return "", nil, fmt.Errorf("strapgorm/filter: $between requires exactly 2 elements, got %d", len(slice))
	}
	return col + " BETWEEN ? AND ?", []any{slice[0], slice[1]}, nil
}

// groupingPlaceholder is registered for $and/$or so they resolve as known
// operators; the builder never invokes it because it handles grouping itself.
func groupingPlaceholder(_ string, _ any, _ dialect.Dialect) (string, []any, error) {
	return "", nil, fmt.Errorf("strapgorm/filter: logical grouping operators are handled by the builder")
}
