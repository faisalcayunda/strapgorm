package filter

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/faisalcayunda/strapgorm/dialect"
	"github.com/faisalcayunda/strapgorm/internal/joins"
	"github.com/faisalcayunda/strapgorm/schema"
)

// Clause is a rendered WHERE fragment together with the values bound to its
// placeholders.
//
// SQL is empty when the filter object produced no conditions; in that case Args
// is also empty and the caller should omit the WHERE clause entirely.
type Clause struct {
	// SQL is the WHERE fragment using "?" placeholders only. It is unwrapped at
	// the top level (no enclosing parentheses) and parenthesized internally
	// wherever a logical grouping was applied.
	SQL string
	// Args holds the bound values in left-to-right placeholder order.
	Args []any
}

// Build renders the decoded Strapi filter object raw into a single Clause for
// model m.
//
// raw is the map produced by decoding the "filters" portion of a query string:
// keys are field JSON names, relation names, dot-notation relation paths, or the
// "$and"/"$or" grouping keys; values are operator maps or, for groupings, a
// []any of nested filter objects. d supplies dialect-specific SQL, reg supplies
// the operator implementations, and jr accumulates the LEFT JOINs required by
// any relation references (its RootTable is used to qualify root-level columns).
//
// Conditions contributed by sibling keys are combined with AND. Every untrusted
// token is validated against the schema whitelist and the operator registry;
// an unknown field, relation, or operator yields a wrapped schema sentinel
// error. Operand values are coerced to the target field's kind and bound as
// parameters.
func Build(m *schema.Model, raw map[string]any, d dialect.Dialect, reg *Registry, jr *joins.Registry) (Clause, error) {
	if m == nil {
		return Clause{}, fmt.Errorf("strapgorm/filter: nil model")
	}
	if reg == nil {
		return Clause{}, fmt.Errorf("strapgorm/filter: nil operator registry")
	}
	if jr == nil {
		return Clause{}, fmt.Errorf("strapgorm/filter: nil joins registry")
	}
	return buildNode(m, raw, d, reg, jr)
}

// buildNode recursively renders one filter object, ANDing its keys together.
func buildNode(m *schema.Model, raw map[string]any, d dialect.Dialect, reg *Registry, jr *joins.Registry) (Clause, error) {
	var parts []string
	var args []any

	for _, key := range sortedKeys(raw) {
		value := raw[key]

		switch key {
		case OpAnd, OpOr:
			clause, err := buildGrouping(m, key, value, d, reg, jr)
			if err != nil {
				return Clause{}, err
			}
			if clause.SQL != "" {
				parts = append(parts, clause.SQL)
				args = append(args, clause.Args...)
			}
			continue
		}

		var (
			clause Clause
			err    error
		)
		if isRelationKey(m, key) {
			clause, err = buildRelation(m, key, value, d, reg, jr)
		} else {
			clause, err = buildField(m, key, value, d, reg, jr)
		}
		if err != nil {
			return Clause{}, err
		}
		if clause.SQL != "" {
			parts = append(parts, clause.SQL)
			args = append(args, clause.Args...)
		}
	}

	return Clause{SQL: joinAnd(parts), Args: args}, nil
}

// buildGrouping renders a "$and"/"$or" key whose value is a list of nested
// filter objects, combining them with the corresponding logical connective and
// wrapping the result in parentheses.
func buildGrouping(m *schema.Model, key string, value any, d dialect.Dialect, reg *Registry, jr *joins.Registry) (Clause, error) {
	list, ok := value.([]any)
	if !ok {
		return Clause{}, fmt.Errorf("strapgorm/filter: %s requires a list of filter objects, got %T", key, value)
	}

	var parts []string
	var args []any
	for _, item := range list {
		sub, ok := item.(map[string]any)
		if !ok {
			return Clause{}, fmt.Errorf("strapgorm/filter: %s list element must be a filter object, got %T", key, item)
		}
		clause, err := buildNode(m, sub, d, reg, jr)
		if err != nil {
			return Clause{}, err
		}
		if clause.SQL != "" {
			parts = append(parts, clause.SQL)
			args = append(args, clause.Args...)
		}
	}

	if len(parts) == 0 {
		return Clause{}, nil
	}

	connective := " AND "
	if key == OpOr {
		connective = " OR "
	}
	return Clause{SQL: "(" + strings.Join(parts, connective) + ")", Args: args}, nil
}

// buildField renders the operator map applied to a single scalar field of m,
// qualifying the column against the registry's root table and ANDing multiple
// operators on the same field.
func buildField(m *schema.Model, jsonName string, value any, d dialect.Dialect, reg *Registry, jr *joins.Registry) (Clause, error) {
	field, ok := m.Field(jsonName)
	if !ok || field.Hidden {
		return Clause{}, fmt.Errorf("%w: %q", schema.ErrUnknownField, jsonName)
	}
	col := qualify(jr.RootTable(), field.Column)
	return applyOperators(col, field, value, d, reg)
}

// buildRelation renders a relation reference, either as a relation key whose
// value is an operator/field map on the related model, or as a dot-notation
// "relation.subfield" path. It registers the LEFT JOIN and resolves the subfield
// against the related model's schema.
func buildRelation(m *schema.Model, key string, value any, d dialect.Dialect, reg *Registry, jr *joins.Registry) (Clause, error) {
	relName, rest, nested := strings.Cut(key, ".")

	rel, ok := m.Relation(relName)
	if !ok || rel.Hidden {
		return Clause{}, fmt.Errorf("%w: %q", schema.ErrUnknownRelation, relName)
	}

	related, err := schema.Get(rel.RelatedType)
	if err != nil {
		return Clause{}, fmt.Errorf("strapgorm/filter: resolving relation %q: %w", relName, err)
	}

	alias := ensureJoin(jr, rel)

	if nested {
		// Dot-notation path: "relation.subfield" (possibly deeper).
		return buildRelationField(related, alias, rest, value, d, reg, jr)
	}

	// Bare relation key whose value is a map of subfield => operator map (or a
	// further nested relation path).
	subMap, ok := value.(map[string]any)
	if !ok {
		return Clause{}, fmt.Errorf("strapgorm/filter: relation %q requires an object value, got %T", relName, value)
	}

	var parts []string
	var args []any
	for _, subKey := range sortedKeys(subMap) {
		clause, err := buildRelationField(related, alias, subKey, subMap[subKey], d, reg, jr)
		if err != nil {
			return Clause{}, err
		}
		if clause.SQL != "" {
			parts = append(parts, clause.SQL)
			args = append(args, clause.Args...)
		}
	}
	return Clause{SQL: joinAnd(parts), Args: args}, nil
}

// buildRelationField resolves subKey against the related model. subKey is either
// a scalar field (whose value is an operator map) or a further "relation.field"
// path, supporting at least one level of nesting and deeper on a best-effort
// basis.
func buildRelationField(related *schema.Model, alias, subKey string, value any, d dialect.Dialect, reg *Registry, jr *joins.Registry) (Clause, error) {
	deeperName, _, deeper := strings.Cut(subKey, ".")
	if deeper || isRelationKey(related, deeperName) {
		// Deeper relation traversal: best-effort recursion through the related
		// model's own relations.
		return buildRelation(related, subKey, value, d, reg, jr)
	}

	field, ok := related.Field(subKey)
	if !ok || field.Hidden {
		return Clause{}, fmt.Errorf("%w: %q", schema.ErrUnknownField, subKey)
	}
	col := qualify(alias, field.Column)
	return applyOperators(col, field, value, d, reg)
}

// applyOperators renders the operator map ops against the qualified column col
// for the given field, ANDing each operator's fragment. Operands are coerced to
// the field's kind before binding.
func applyOperators(col string, field schema.Field, ops any, d dialect.Dialect, reg *Registry) (Clause, error) {
	opMap, ok := ops.(map[string]any)
	if !ok {
		return Clause{}, fmt.Errorf("strapgorm/filter: field %q requires an operator object, got %T", field.JSON, ops)
	}

	var parts []string
	var args []any
	for _, opName := range sortedKeys(opMap) {
		fn, ok := reg.Get(opName)
		if !ok {
			return Clause{}, fmt.Errorf("%w: %q", schema.ErrUnknownOperator, opName)
		}

		converted, err := convertOperand(opName, field, opMap[opName])
		if err != nil {
			return Clause{}, err
		}

		sql, opArgs, err := fn(col, converted, d)
		if err != nil {
			return Clause{}, err
		}
		if sql != "" {
			parts = append(parts, sql)
			args = append(args, opArgs...)
		}
	}
	return Clause{SQL: joinAnd(parts), Args: args}, nil
}

// convertOperand coerces a raw operand to the representation the operator named
// opName expects for field.
//
//   - $null/$notNull operands are coerced to bool.
//   - $in/$notIn operands are coerced to a []any whose elements match the field
//     kind, splitting a comma-separated string when the parser left it whole.
//   - $between operands are coerced to a 2-element []any matching the field kind.
//   - LIKE operators keep the operand as a string (pattern matching is textual).
//   - All other operators coerce the scalar operand to the field kind.
func convertOperand(opName string, field schema.Field, raw any) (any, error) {
	switch opName {
	case "$null", "$notNull":
		return toBool(raw)

	case "$in", "$notIn":
		elems, err := toList(raw)
		if err != nil {
			return nil, fmt.Errorf("strapgorm/filter: %s on %q: %w", opName, field.JSON, err)
		}
		out := make([]any, len(elems))
		for i, e := range elems {
			v, err := coerceScalar(field, e)
			if err != nil {
				return nil, fmt.Errorf("strapgorm/filter: %s on %q element %d: %w", opName, field.JSON, i, err)
			}
			out[i] = v
		}
		return out, nil

	case "$between":
		elems, err := toList(raw)
		if err != nil {
			return nil, fmt.Errorf("strapgorm/filter: $between on %q: %w", field.JSON, err)
		}
		if len(elems) != 2 {
			return nil, fmt.Errorf("strapgorm/filter: $between on %q requires exactly 2 bounds, got %d", field.JSON, len(elems))
		}
		out := make([]any, 2)
		for i, e := range elems {
			v, err := coerceScalar(field, e)
			if err != nil {
				return nil, fmt.Errorf("strapgorm/filter: $between on %q bound %d: %w", field.JSON, i, err)
			}
			out[i] = v
		}
		return out, nil

	case "$contains", "$containsi", "$startsWith", "$endsWith":
		return toString(raw)

	default:
		return coerceScalar(field, raw)
	}
}

// coerceScalar converts a single operand to the Go representation matching
// field.Kind. Strings are parsed; numeric and boolean operands already of the
// right shape pass through. Failures return a wrapped, descriptive error.
func coerceScalar(field schema.Field, raw any) (any, error) {
	switch field.Kind {
	case schema.KindString, schema.KindBytes, schema.KindOther:
		return toString(raw)
	case schema.KindInt:
		return toInt(raw)
	case schema.KindUint:
		return toUint(raw)
	case schema.KindFloat:
		return toFloat(raw)
	case schema.KindBool:
		return toBool(raw)
	case schema.KindTime:
		return toTime(raw)
	default:
		return raw, nil
	}
}

// toList normalizes a list-valued operand. It accepts a []any directly, splits a
// comma-separated string into trimmed elements, and otherwise wraps a single
// scalar into a one-element list.
func toList(raw any) ([]any, error) {
	switch v := raw.(type) {
	case []any:
		return v, nil
	case []string:
		out := make([]any, len(v))
		for i, s := range v {
			out[i] = s
		}
		return out, nil
	case string:
		fields := strings.Split(v, ",")
		out := make([]any, 0, len(fields))
		for _, f := range fields {
			out = append(out, strings.TrimSpace(f))
		}
		return out, nil
	default:
		return []any{raw}, nil
	}
}

// toString renders an operand as a string. Non-string scalars are formatted with
// fmt so that, for example, a JSON number used as a LIKE operand still works.
func toString(raw any) (string, error) {
	switch v := raw.(type) {
	case string:
		return v, nil
	case fmt.Stringer:
		return v.String(), nil
	case nil:
		return "", fmt.Errorf("strapgorm/filter: nil string operand")
	default:
		return fmt.Sprintf("%v", v), nil
	}
}

// toInt coerces an operand to int64.
func toInt(raw any) (int64, error) {
	switch v := raw.(type) {
	case int:
		return int64(v), nil
	case int8:
		return int64(v), nil
	case int16:
		return int64(v), nil
	case int32:
		return int64(v), nil
	case int64:
		return v, nil
	case float32:
		return int64(v), nil
	case float64:
		return int64(v), nil
	case string:
		n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		if err != nil {
			return 0, fmt.Errorf("strapgorm/filter: cannot parse %q as integer: %w", v, err)
		}
		return n, nil
	default:
		return 0, fmt.Errorf("strapgorm/filter: cannot coerce %T to integer", raw)
	}
}

// toUint coerces an operand to uint64, rejecting negative values.
func toUint(raw any) (uint64, error) {
	switch v := raw.(type) {
	case uint:
		return uint64(v), nil
	case uint8:
		return uint64(v), nil
	case uint16:
		return uint64(v), nil
	case uint32:
		return uint64(v), nil
	case uint64:
		return v, nil
	case int:
		if v < 0 {
			return 0, fmt.Errorf("strapgorm/filter: cannot coerce negative %d to unsigned integer", v)
		}
		return uint64(v), nil
	case int64:
		if v < 0 {
			return 0, fmt.Errorf("strapgorm/filter: cannot coerce negative %d to unsigned integer", v)
		}
		return uint64(v), nil
	case float64:
		if v < 0 {
			return 0, fmt.Errorf("strapgorm/filter: cannot coerce negative %v to unsigned integer", v)
		}
		return uint64(v), nil
	case string:
		n, err := strconv.ParseUint(strings.TrimSpace(v), 10, 64)
		if err != nil {
			return 0, fmt.Errorf("strapgorm/filter: cannot parse %q as unsigned integer: %w", v, err)
		}
		return n, nil
	default:
		return 0, fmt.Errorf("strapgorm/filter: cannot coerce %T to unsigned integer", raw)
	}
}

// toFloat coerces an operand to float64.
func toFloat(raw any) (float64, error) {
	switch v := raw.(type) {
	case float32:
		return float64(v), nil
	case float64:
		return v, nil
	case int:
		return float64(v), nil
	case int64:
		return float64(v), nil
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil {
			return 0, fmt.Errorf("strapgorm/filter: cannot parse %q as float: %w", v, err)
		}
		return f, nil
	default:
		return 0, fmt.Errorf("strapgorm/filter: cannot coerce %T to float", raw)
	}
}

// toBool coerces an operand to bool, accepting Go bools, the numeric 0/1, and
// the strings true/false/1/0 (case-insensitive, trimmed), emulating Strapi's
// lenient boolean handling.
func toBool(raw any) (bool, error) {
	switch v := raw.(type) {
	case bool:
		return v, nil
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "true", "1":
			return true, nil
		case "false", "0":
			return false, nil
		default:
			return false, fmt.Errorf("strapgorm/filter: cannot parse %q as boolean", v)
		}
	case int:
		return v != 0, nil
	case int64:
		return v != 0, nil
	case float64:
		return v != 0, nil
	default:
		return false, fmt.Errorf("strapgorm/filter: cannot coerce %T to boolean", raw)
	}
}

// timeLayouts is the ordered set of layouts toTime tries, emulating the leniency
// of python-dateutil for the formats Strapi clients commonly emit.
var timeLayouts = []string{
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05",
	"2006-01-02 15:04",
	"2006-01-02",
	"2006/01/02",
	"15:04:05",
	time.RFC1123Z,
	time.RFC1123,
}

// toTime coerces an operand to time.Time. A time.Time passes through; a string
// is parsed against timeLayouts in order; a numeric value is interpreted as Unix
// seconds. Unparseable strings return a wrapped error naming the value.
func toTime(raw any) (any, error) {
	switch v := raw.(type) {
	case time.Time:
		return v, nil
	case string:
		s := strings.TrimSpace(v)
		for _, layout := range timeLayouts {
			if t, err := time.Parse(layout, s); err == nil {
				return t, nil
			}
		}
		return nil, fmt.Errorf("strapgorm/filter: cannot parse %q as a time value", v)
	case int:
		return time.Unix(int64(v), 0).UTC(), nil
	case int64:
		return time.Unix(v, 0).UTC(), nil
	case float64:
		return time.Unix(int64(v), 0).UTC(), nil
	default:
		return nil, fmt.Errorf("strapgorm/filter: cannot coerce %T to time", raw)
	}
}

// ensureJoin registers (once) the LEFT JOIN for rel against the root table and
// returns the alias relation columns must be qualified with.
//
// The JOIN shape depends on the relation type:
//
//   - BelongsTo: the FK lives on the root table, so the related table is joined
//     on relTable.<ownerKey|id> = rootTable.<fkColumn>.
//   - HasOne/HasMany: the FK lives on the related table, so the join is
//     relTable.<fkColumn> = rootTable.<ownerKey|id>.
//   - Many2Many: the owning and related tables are bridged through the join
//     table using the conventional <singular>_id foreign keys.
func ensureJoin(jr *joins.Registry, rel schema.Relation) string {
	return jr.Ensure(rel.Name, func() joins.Join {
		root := jr.RootTable()
		relTable := rel.Table
		ownerKey := rel.OwnerKey
		if ownerKey == "" {
			ownerKey = "id"
		}

		switch rel.Type {
		case schema.BelongsTo:
			on := fmt.Sprintf("%s = %s",
				qualify(relTable, ownerKey),
				qualify(root, rel.FKColumn))
			return joins.Join{
				SQL:   fmt.Sprintf("LEFT JOIN %s ON %s", relTable, on),
				Alias: relTable,
			}

		case schema.Many2Many:
			joinTable := rel.JoinTable
			rootFK := singular(root) + "_id"
			relFK := singular(relTable) + "_id"
			on1 := fmt.Sprintf("%s = %s",
				qualify(joinTable, rootFK),
				qualify(root, ownerKey))
			on2 := fmt.Sprintf("%s = %s",
				qualify(relTable, "id"),
				qualify(joinTable, relFK))
			return joins.Join{
				SQL: fmt.Sprintf("LEFT JOIN %s ON %s LEFT JOIN %s ON %s",
					joinTable, on1, relTable, on2),
				Alias: relTable,
			}

		default: // HasOne, HasMany
			on := fmt.Sprintf("%s = %s",
				qualify(relTable, rel.FKColumn),
				qualify(root, ownerKey))
			return joins.Join{
				SQL:   fmt.Sprintf("LEFT JOIN %s ON %s", relTable, on),
				Alias: relTable,
			}
		}
	})
}

// isRelationKey reports whether key names a relation of m, accepting both a bare
// relation name and the "relation.subfield" dot-notation form. A hidden relation
// is not a relation here, so its key falls through to the unknown-field error.
func isRelationKey(m *schema.Model, key string) bool {
	name, _, _ := strings.Cut(key, ".")
	rel, ok := m.Relation(name)
	return ok && !rel.Hidden
}

// qualify joins a table/alias and a column with a dot. Both arguments originate
// from schema metadata, never from raw user input.
func qualify(table, column string) string {
	return table + "." + column
}

// singular strips a trailing pluralizing "s" from a table name for use in
// conventional Many2Many join-key derivation. It is a best-effort heuristic
// consistent with the schema package's documented fallback pluralizer.
func singular(table string) string {
	if strings.HasSuffix(table, "ies") {
		return table[:len(table)-3] + "y"
	}
	if strings.HasSuffix(table, "ses") || strings.HasSuffix(table, "xes") ||
		strings.HasSuffix(table, "zes") || strings.HasSuffix(table, "ches") ||
		strings.HasSuffix(table, "shes") {
		return table[:len(table)-2]
	}
	if strings.HasSuffix(table, "s") {
		return table[:len(table)-1]
	}
	return table
}

// joinAnd combines fragments with " AND ", wrapping the result in parentheses
// only when more than one fragment is present so a single condition stays
// unparenthesized.
func joinAnd(parts []string) string {
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	default:
		return "(" + strings.Join(parts, " AND ") + ")"
	}
}

// sortedKeys returns the keys of m in lexical order so that generated SQL is
// deterministic regardless of Go's randomized map iteration.
func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
