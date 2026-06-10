package filter

import (
	"reflect"
	"testing"

	"github.com/faisalcayunda/strapgorm/dialect"
)

func TestDefaultRegistryHasAllOperators(t *testing.T) {
	reg := Default()
	want := []string{
		"$eq", "$ne", "$lt", "$lte", "$gt", "$gte",
		"$in", "$notIn",
		"$contains", "$containsi", "$startsWith", "$endsWith",
		"$null", "$notNull", "$between",
		"$and", "$or",
	}
	if len(want) != 17 {
		t.Fatalf("test setup: expected 17 operator names, listed %d", len(want))
	}
	for _, name := range want {
		if _, ok := reg.Get(name); !ok {
			t.Errorf("Default registry missing operator %q", name)
		}
	}
}

func TestRegistryRegisterAndGet(t *testing.T) {
	reg := Default()
	if _, ok := reg.Get("$custom"); ok {
		t.Fatal("unexpected $custom operator before registration")
	}
	reg.Register("$custom", func(col string, value any, _ dialect.Dialect) (string, []any, error) {
		return col + " ~ ?", []any{value}, nil
	})
	fn, ok := reg.Get("$custom")
	if !ok {
		t.Fatal("$custom not found after Register")
	}
	sql, args, err := fn("users.name", "x", dialect.Postgres)
	if err != nil {
		t.Fatalf("custom op error: %v", err)
	}
	if sql != "users.name ~ ?" || !reflect.DeepEqual(args, []any{"x"}) {
		t.Errorf("custom op = %q / %v", sql, args)
	}
}

// TestOperatorsSQLAndArgs drives each built-in operator directly with an
// already-converted operand and asserts the exact SQL and bound arguments.
func TestOperatorsSQLAndArgs(t *testing.T) {
	const col = "users.age"
	tests := []struct {
		name     string
		op       string
		value    any
		d        dialect.Dialect
		wantSQL  string
		wantArgs []any
	}{
		{"eq", "$eq", 18, dialect.Postgres, "users.age = ?", []any{18}},
		{"ne", "$ne", 18, dialect.Postgres, "users.age <> ?", []any{18}},
		{"lt", "$lt", 18, dialect.Postgres, "users.age < ?", []any{18}},
		{"lte", "$lte", 18, dialect.Postgres, "users.age <= ?", []any{18}},
		{"gt", "$gt", 18, dialect.Postgres, "users.age > ?", []any{18}},
		{"gte", "$gte", 18, dialect.Postgres, "users.age >= ?", []any{18}},
		{"in", "$in", []any{1, 2, 3}, dialect.Postgres, "users.age IN ?", []any{[]any{1, 2, 3}}},
		{"notIn", "$notIn", []any{1, 2}, dialect.Postgres, "users.age NOT IN ?", []any{[]any{1, 2}}},
		{"contains pg", "$contains", "John", dialect.Postgres, "users.age LIKE ?", []any{"%John%"}},
		{"containsi pg", "$containsi", "John", dialect.Postgres, "users.age ILIKE ?", []any{"%John%"}},
		{"containsi mysql", "$containsi", "John", dialect.MySQL, "LOWER(users.age) LIKE LOWER(?)", []any{"%John%"}},
		{"containsi sqlite", "$containsi", "John", dialect.SQLite, "LOWER(users.age) LIKE LOWER(?)", []any{"%John%"}},
		{"startsWith", "$startsWith", "Jo", dialect.Postgres, "users.age LIKE ?", []any{"Jo%"}},
		{"endsWith", "$endsWith", "hn", dialect.Postgres, "users.age LIKE ?", []any{"%hn"}},
		{"null true", "$null", true, dialect.Postgres, "users.age IS NULL", nil},
		{"null false", "$null", false, dialect.Postgres, "users.age IS NOT NULL", nil},
		{"notNull true", "$notNull", true, dialect.Postgres, "users.age IS NOT NULL", nil},
		{"notNull false", "$notNull", false, dialect.Postgres, "users.age IS NULL", nil},
		{"between", "$between", []any{10, 20}, dialect.Postgres, "users.age BETWEEN ? AND ?", []any{10, 20}},
	}

	reg := Default()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fn, ok := reg.Get(tt.op)
			if !ok {
				t.Fatalf("operator %q not registered", tt.op)
			}
			sql, args, err := fn(col, tt.value, tt.d)
			if err != nil {
				t.Fatalf("op %q error: %v", tt.op, err)
			}
			if sql != tt.wantSQL {
				t.Errorf("sql = %q, want %q", sql, tt.wantSQL)
			}
			if !reflect.DeepEqual(args, tt.wantArgs) {
				t.Errorf("args = %#v, want %#v", args, tt.wantArgs)
			}
		})
	}
}

// TestOperatorInputErrors covers the operator-level validation failures.
func TestOperatorInputErrors(t *testing.T) {
	reg := Default()
	tests := []struct {
		name  string
		op    string
		value any
	}{
		{"in not a slice", "$in", "notaslice"},
		{"in empty", "$in", []any{}},
		{"notIn not a slice", "$notIn", 5},
		{"contains not a string", "$contains", 5},
		{"null not a bool", "$null", "notabool"},
		{"between not a slice", "$between", "x"},
		{"between wrong length", "$between", []any{1, 2, 3}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fn, _ := reg.Get(tt.op)
			if _, _, err := fn("users.age", tt.value, dialect.Postgres); err == nil {
				t.Errorf("op %q with %v: expected error, got nil", tt.op, tt.value)
			}
		})
	}
}

func TestGroupingPlaceholderErrors(t *testing.T) {
	reg := Default()
	for _, name := range []string{"$and", "$or"} {
		fn, ok := reg.Get(name)
		if !ok {
			t.Fatalf("grouping %q not registered", name)
		}
		if _, _, err := fn("users.x", nil, dialect.Postgres); err == nil {
			t.Errorf("grouping placeholder %q should error when invoked directly", name)
		}
	}
}
