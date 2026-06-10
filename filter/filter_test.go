package filter

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/faisalcayunda/strapgorm/dialect"
	"github.com/faisalcayunda/strapgorm/internal/joins"
	"github.com/faisalcayunda/strapgorm/schema"
)

// --- Test models ---------------------------------------------------------

type Base struct {
	ID        uint      `json:"id" gorm:"primaryKey"`
	CreatedAt time.Time `json:"created_at"`
}

type Organization struct {
	Base
	Name string `json:"name"`
	Slug string `json:"slug" gorm:"column:org_slug"`
}

type Tag struct {
	Base
	Label string `json:"label"`
}

type Post struct {
	Base
	Title  string `json:"title"`
	UserID uint   `json:"user_id"`
}

type Role struct {
	Base
	Title string `json:"title"`
}

type User struct {
	Base
	Name     string    `json:"name"`
	Email    string    `json:"email"`
	Age      int       `json:"age"`
	Score    float64   `json:"score"`
	Active   bool      `json:"active"`
	Bio      *string   `json:"bio"`
	Quota    uint      `json:"quota"`
	JoinedAt time.Time `json:"joined_at"`

	OrganizationID uint         `json:"organization_id"`
	Organization   Organization `json:"organization"`                    // BelongsTo
	Role           *Role        `json:"role"`                            // HasOne (no RoleID)
	Tags           []Tag        `json:"tags" gorm:"many2many:user_tags"` // Many2Many
	Posts          []Post       `json:"posts"`                           // HasMany
}

// --- Helpers -------------------------------------------------------------

func userModel(t *testing.T) *schema.Model {
	t.Helper()
	m, err := schema.Get(reflect.TypeOf(User{}))
	if err != nil {
		t.Fatalf("schema.Get(User): %v", err)
	}
	return m
}

func build(t *testing.T, raw map[string]any, d dialect.Dialect) (Clause, *joins.Registry, error) {
	t.Helper()
	m := userModel(t)
	jr := joins.NewRegistry(m.Table)
	c, err := Build(m, raw, d, Default(), jr)
	return c, jr, err
}

// --- Field-level builder tests -------------------------------------------

func TestBuildSingleField(t *testing.T) {
	c, _, err := build(t, map[string]any{
		"name": map[string]any{"$contains": "John"},
	}, dialect.Postgres)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if c.SQL != "users.name LIKE ?" {
		t.Errorf("SQL = %q", c.SQL)
	}
	if !reflect.DeepEqual(c.Args, []any{"%John%"}) {
		t.Errorf("Args = %#v", c.Args)
	}
}

func TestBuildMultipleFieldsAreAnded(t *testing.T) {
	c, _, err := build(t, map[string]any{
		"name": map[string]any{"$eq": "John"},
		"age":  map[string]any{"$gte": "18"},
	}, dialect.Postgres)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	// Keys are sorted lexically: "age" before "name".
	want := "(users.age >= ? AND users.name = ?)"
	if c.SQL != want {
		t.Errorf("SQL = %q, want %q", c.SQL, want)
	}
	if !reflect.DeepEqual(c.Args, []any{int64(18), "John"}) {
		t.Errorf("Args = %#v", c.Args)
	}
}

func TestBuildMultipleOperatorsOnOneField(t *testing.T) {
	c, _, err := build(t, map[string]any{
		"age": map[string]any{"$gte": "18", "$lte": "65"},
	}, dialect.Postgres)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	// Operator keys sorted: "$gte" before "$lte".
	want := "(users.age >= ? AND users.age <= ?)"
	if c.SQL != want {
		t.Errorf("SQL = %q, want %q", c.SQL, want)
	}
	if !reflect.DeepEqual(c.Args, []any{int64(18), int64(65)}) {
		t.Errorf("Args = %#v", c.Args)
	}
}

func TestBuildColumnTagRespected(t *testing.T) {
	// Organization.Slug maps to column org_slug; filter through the relation.
	c, jr, err := build(t, map[string]any{
		"organization.slug": map[string]any{"$eq": "acme"},
	}, dialect.Postgres)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if c.SQL != "organizations.org_slug = ?" {
		t.Errorf("SQL = %q", c.SQL)
	}
	jlist := jr.Joins()
	if len(jlist) != 1 || jlist[0].SQL != "LEFT JOIN organizations ON organizations.id = users.organization_id" {
		t.Errorf("joins = %#v", jlist)
	}
}

func TestBuildEmptyFilter(t *testing.T) {
	c, _, err := build(t, map[string]any{}, dialect.Postgres)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if c.SQL != "" || len(c.Args) != 0 {
		t.Errorf("empty filter produced SQL=%q args=%#v", c.SQL, c.Args)
	}
}

// --- Logical grouping tests ----------------------------------------------

func TestBuildOrGrouping(t *testing.T) {
	c, _, err := build(t, map[string]any{
		"$or": []any{
			map[string]any{"name": map[string]any{"$eq": "John"}},
			map[string]any{"email": map[string]any{"$endsWith": "@x.com"}},
		},
	}, dialect.Postgres)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	want := "(users.name = ? OR users.email LIKE ?)"
	if c.SQL != want {
		t.Errorf("SQL = %q, want %q", c.SQL, want)
	}
	if !reflect.DeepEqual(c.Args, []any{"John", "%@x.com"}) {
		t.Errorf("Args = %#v", c.Args)
	}
}

func TestBuildAndGrouping(t *testing.T) {
	c, _, err := build(t, map[string]any{
		"$and": []any{
			map[string]any{"age": map[string]any{"$gte": "18"}},
			map[string]any{"active": map[string]any{"$eq": "true"}},
		},
	}, dialect.Postgres)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	want := "(users.age >= ? AND users.active = ?)"
	if c.SQL != want {
		t.Errorf("SQL = %q, want %q", c.SQL, want)
	}
	if !reflect.DeepEqual(c.Args, []any{int64(18), true}) {
		t.Errorf("Args = %#v", c.Args)
	}
}

func TestBuildNestedGroupingMixedWithField(t *testing.T) {
	c, _, err := build(t, map[string]any{
		"active": map[string]any{"$eq": "true"},
		"$or": []any{
			map[string]any{"name": map[string]any{"$eq": "A"}},
			map[string]any{"name": map[string]any{"$eq": "B"}},
		},
	}, dialect.Postgres)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	// Sorted keys: "$or" before "active".
	want := "((users.name = ? OR users.name = ?) AND users.active = ?)"
	if c.SQL != want {
		t.Errorf("SQL = %q, want %q", c.SQL, want)
	}
	if !reflect.DeepEqual(c.Args, []any{"A", "B", true}) {
		t.Errorf("Args = %#v", c.Args)
	}
}

// --- Relation / JOIN tests -----------------------------------------------

func TestBuildBelongsToDotNotation(t *testing.T) {
	c, jr, err := build(t, map[string]any{
		"organization.name": map[string]any{"$contains": "Inc"},
	}, dialect.Postgres)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if c.SQL != "organizations.name LIKE ?" {
		t.Errorf("SQL = %q", c.SQL)
	}
	if !reflect.DeepEqual(c.Args, []any{"%Inc%"}) {
		t.Errorf("Args = %#v", c.Args)
	}
	jlist := jr.Joins()
	if len(jlist) != 1 {
		t.Fatalf("expected 1 join, got %d", len(jlist))
	}
	if jlist[0].SQL != "LEFT JOIN organizations ON organizations.id = users.organization_id" {
		t.Errorf("join SQL = %q", jlist[0].SQL)
	}
}

func TestBuildHasManyJoin(t *testing.T) {
	c, jr, err := build(t, map[string]any{
		"posts.title": map[string]any{"$eq": "Hello"},
	}, dialect.Postgres)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if c.SQL != "posts.title = ?" {
		t.Errorf("SQL = %q", c.SQL)
	}
	jlist := jr.Joins()
	if len(jlist) != 1 || jlist[0].SQL != "LEFT JOIN posts ON posts.user_id = users.id" {
		t.Errorf("joins = %#v", jlist)
	}
}

func TestBuildHasOneJoin(t *testing.T) {
	c, jr, err := build(t, map[string]any{
		"role.title": map[string]any{"$eq": "admin"},
	}, dialect.Postgres)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if c.SQL != "roles.title = ?" {
		t.Errorf("SQL = %q", c.SQL)
	}
	jlist := jr.Joins()
	if len(jlist) != 1 || jlist[0].SQL != "LEFT JOIN roles ON roles.user_id = users.id" {
		t.Errorf("joins = %#v", jlist)
	}
}

func TestBuildMany2ManyJoin(t *testing.T) {
	c, jr, err := build(t, map[string]any{
		"tags.label": map[string]any{"$eq": "go"},
	}, dialect.Postgres)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if c.SQL != "tags.label = ?" {
		t.Errorf("SQL = %q", c.SQL)
	}
	jlist := jr.Joins()
	if len(jlist) != 1 {
		t.Fatalf("expected 1 join, got %d", len(jlist))
	}
	want := "LEFT JOIN user_tags ON user_tags.user_id = users.id LEFT JOIN tags ON tags.id = user_tags.tag_id"
	if jlist[0].SQL != want {
		t.Errorf("join SQL = %q, want %q", jlist[0].SQL, want)
	}
}

func TestBuildRelationObjectForm(t *testing.T) {
	// Relation key whose value is an object of subfields (not dot notation).
	c, jr, err := build(t, map[string]any{
		"organization": map[string]any{
			"name": map[string]any{"$eq": "Acme"},
			"slug": map[string]any{"$eq": "acme"},
		},
	}, dialect.Postgres)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	// Subfields sorted: name before slug.
	want := "(organizations.name = ? AND organizations.org_slug = ?)"
	if c.SQL != want {
		t.Errorf("SQL = %q, want %q", c.SQL, want)
	}
	if !reflect.DeepEqual(c.Args, []any{"Acme", "acme"}) {
		t.Errorf("Args = %#v", c.Args)
	}
	if len(jr.Joins()) != 1 {
		t.Errorf("expected 1 join, got %d", len(jr.Joins()))
	}
}

func TestBuildJoinDeduplicated(t *testing.T) {
	// Two references to the same relation must produce exactly one JOIN.
	_, jr, err := build(t, map[string]any{
		"organization.name": map[string]any{"$eq": "A"},
		"organization.slug": map[string]any{"$eq": "a"},
	}, dialect.Postgres)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if got := len(jr.Joins()); got != 1 {
		t.Errorf("expected 1 deduplicated join, got %d", got)
	}
}

// --- Type conversion tests -----------------------------------------------

func TestTypeConversionSuccess(t *testing.T) {
	tests := []struct {
		name     string
		raw      map[string]any
		wantArgs []any
	}{
		{
			"string to int",
			map[string]any{"age": map[string]any{"$eq": "42"}},
			[]any{int64(42)},
		},
		{
			"string to uint",
			map[string]any{"quota": map[string]any{"$eq": "7"}},
			[]any{uint64(7)},
		},
		{
			"string to float",
			map[string]any{"score": map[string]any{"$eq": "3.5"}},
			[]any{float64(3.5)},
		},
		{
			"string to bool true",
			map[string]any{"active": map[string]any{"$eq": "1"}},
			[]any{true},
		},
		{
			"string to bool false",
			map[string]any{"active": map[string]any{"$eq": "false"}},
			[]any{false},
		},
		{
			"in splits comma string",
			map[string]any{"age": map[string]any{"$in": "1,2,3"}},
			[]any{[]any{int64(1), int64(2), int64(3)}},
		},
		{
			"between converts bounds",
			map[string]any{"age": map[string]any{"$between": []any{"10", "20"}}},
			[]any{int64(10), int64(20)},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _, err := build(t, tt.raw, dialect.Postgres)
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			if !reflect.DeepEqual(c.Args, tt.wantArgs) {
				t.Errorf("Args = %#v, want %#v", c.Args, tt.wantArgs)
			}
		})
	}
}

func TestTypeConversionTime(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want time.Time
	}{
		{"rfc3339", "2026-01-02T15:04:05Z", time.Date(2026, 1, 2, 15, 4, 5, 0, time.UTC)},
		{"date only", "2026-01-02", time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)},
		{"datetime space", "2026-01-02 15:04:05", time.Date(2026, 1, 2, 15, 4, 5, 0, time.UTC)},
		{"slash date", "2026/01/02", time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _, err := build(t, map[string]any{
				"joined_at": map[string]any{"$gte": tt.in},
			}, dialect.Postgres)
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			if len(c.Args) != 1 {
				t.Fatalf("expected 1 arg, got %d", len(c.Args))
			}
			got, ok := c.Args[0].(time.Time)
			if !ok {
				t.Fatalf("arg type = %T, want time.Time", c.Args[0])
			}
			if !got.Equal(tt.want) {
				t.Errorf("time = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTypeConversionFailures(t *testing.T) {
	tests := []struct {
		name string
		raw  map[string]any
	}{
		{"bad int", map[string]any{"age": map[string]any{"$eq": "notanint"}}},
		{"bad uint negative", map[string]any{"quota": map[string]any{"$eq": "-1"}}},
		{"bad float", map[string]any{"score": map[string]any{"$eq": "x.y"}}},
		{"bad bool", map[string]any{"active": map[string]any{"$eq": "maybe"}}},
		{"bad time", map[string]any{"joined_at": map[string]any{"$gte": "not-a-date"}}},
		{"between wrong count", map[string]any{"age": map[string]any{"$between": []any{"1"}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, _, err := build(t, tt.raw, dialect.Postgres); err == nil {
				t.Errorf("expected conversion error, got nil")
			}
		})
	}
}

// --- Error / whitelist tests ---------------------------------------------

func TestUnknownFieldError(t *testing.T) {
	_, _, err := build(t, map[string]any{
		"nope": map[string]any{"$eq": "x"},
	}, dialect.Postgres)
	if !errors.Is(err, schema.ErrUnknownField) {
		t.Errorf("err = %v, want ErrUnknownField", err)
	}
}

func TestUnknownRelationFieldError(t *testing.T) {
	_, _, err := build(t, map[string]any{
		"organization.nope": map[string]any{"$eq": "x"},
	}, dialect.Postgres)
	if !errors.Is(err, schema.ErrUnknownField) {
		t.Errorf("err = %v, want ErrUnknownField", err)
	}
}

func TestUnknownOperatorError(t *testing.T) {
	_, _, err := build(t, map[string]any{
		"name": map[string]any{"$regex": "x"},
	}, dialect.Postgres)
	if !errors.Is(err, schema.ErrUnknownOperator) {
		t.Errorf("err = %v, want ErrUnknownOperator", err)
	}
}

func TestUnknownRelationError(t *testing.T) {
	// A dot-notation key whose head is neither a field nor a relation is treated
	// as an unknown field at the top level.
	_, _, err := build(t, map[string]any{
		"ghost.name": map[string]any{"$eq": "x"},
	}, dialect.Postgres)
	if err == nil {
		t.Fatal("expected error for unknown relation path")
	}
}

func TestBuildNilArgsValidation(t *testing.T) {
	m := userModel(t)
	jr := joins.NewRegistry(m.Table)
	if _, err := Build(nil, map[string]any{}, dialect.Postgres, Default(), jr); err == nil {
		t.Error("expected error for nil model")
	}
	if _, err := Build(m, map[string]any{}, dialect.Postgres, nil, jr); err == nil {
		t.Error("expected error for nil registry")
	}
	if _, err := Build(m, map[string]any{}, dialect.Postgres, Default(), nil); err == nil {
		t.Error("expected error for nil joins registry")
	}
}

func TestMalformedGroupingErrors(t *testing.T) {
	tests := []struct {
		name string
		raw  map[string]any
	}{
		{"or not a list", map[string]any{"$or": "x"}},
		{"or element not a map", map[string]any{"$or": []any{"x"}}},
		{"field value not an op map", map[string]any{"name": "x"}},
		{"relation value not a map", map[string]any{"organization": "x"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, _, err := build(t, tt.raw, dialect.Postgres); err == nil {
				t.Errorf("expected error, got nil")
			}
		})
	}
}

func TestContainsiAcrossDialects(t *testing.T) {
	raw := map[string]any{"name": map[string]any{"$containsi": "jo"}}

	c, _, err := build(t, raw, dialect.MySQL)
	if err != nil {
		t.Fatalf("Build mysql: %v", err)
	}
	if c.SQL != "LOWER(users.name) LIKE LOWER(?)" {
		t.Errorf("mysql SQL = %q", c.SQL)
	}

	c, _, err = build(t, raw, dialect.Postgres)
	if err != nil {
		t.Fatalf("Build pg: %v", err)
	}
	if c.SQL != "users.name ILIKE ?" {
		t.Errorf("pg SQL = %q", c.SQL)
	}
}

func TestNullOperators(t *testing.T) {
	c, _, err := build(t, map[string]any{
		"bio": map[string]any{"$null": "true"},
	}, dialect.Postgres)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if c.SQL != "users.bio IS NULL" || len(c.Args) != 0 {
		t.Errorf("SQL = %q args = %#v", c.SQL, c.Args)
	}

	c, _, err = build(t, map[string]any{
		"bio": map[string]any{"$notNull": "true"},
	}, dialect.Postgres)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if c.SQL != "users.bio IS NOT NULL" || len(c.Args) != 0 {
		t.Errorf("SQL = %q args = %#v", c.SQL, c.Args)
	}
}
