// Package strapgorm facade tests.
//
// IMPORTANT: these tests import gorm.io/gorm and a SQLite driver, so they CANNOT
// run without first resolving the module dependencies. Because this repository
// is developed offline (GORM is not in the module cache), run:
//
//	go mod tidy   # requires network access to fetch gorm + the sqlite driver
//	go test .
//
// before executing them. They are written to pass once the dependencies are
// resolved; they do not run in the offline foundation environment.
//
// The tests drive the Builder against a DryRun *gorm.DB (no database is opened
// or queried) and assert on the SQL rendered by ToSQL, which captures the root
// statement and bound parameters without executing anything.
package strapgorm

import (
	"context"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/faisalcayunda/strapgorm/dialect"
	"github.com/faisalcayunda/strapgorm/paginate"
	"github.com/faisalcayunda/strapgorm/parser"
)

// User is the root fixture model. It carries scalar columns, a BelongsTo
// Organization relation (FK on this table), and declares searchable fields.
type User struct {
	ID             uint          `json:"id" gorm:"primaryKey"`
	Name           string        `json:"name"`
	Email          string        `json:"email"`
	Age            int           `json:"age"`
	Active         bool          `json:"active"`
	OrganizationID uint          `json:"organization_id"`
	Organization   *Organization `json:"organization"`
}

// Searchable declares the columns the free-text search term matches against.
func (User) Searchable() []string { return []string{"name", "email"} }

// Organization is the related model reached through User.Organization.
type Organization struct {
	ID   uint   `json:"id" gorm:"primaryKey"`
	Slug string `json:"slug"`
	Name string `json:"name"`
}

// newDryRunDB opens a DryRun *gorm.DB backed by the pure-Go SQLite driver. No
// real database file is created and no statement is executed; the session only
// renders SQL. It fails the test if the driver cannot be opened.
func newDryRunDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{DryRun: true})
	if err != nil {
		t.Fatalf("gorm.Open: %v", err)
	}
	return db
}

// defaultPage is a small page request used by tests that do not exercise
// pagination specifically.
func defaultPage() paginate.Params {
	return paginate.Params{Mode: paginate.ModePage, Page: 1, PageSize: 25}
}

func TestNewSelectsDialectFromDriver(t *testing.T) {
	db := newDryRunDB(t)
	b := New[User](db)
	if b.dialect == nil {
		t.Fatal("dialect not set")
	}
	if got := b.dialect.Name(); got != dialect.SQLite.Name() {
		t.Errorf("dialect = %q, want %q", got, dialect.SQLite.Name())
	}
}

func TestToSQLFilterEq(t *testing.T) {
	db := newDryRunDB(t)
	sql, vars, err := New[User](db).
		Filters(map[string]any{"name": map[string]any{"$eq": "John"}}).
		ToSQL(context.Background(), defaultPage())
	if err != nil {
		t.Fatalf("ToSQL: %v", err)
	}
	assertContains(t, sql, "users.name = ?")
	assertContains(t, sql, "FROM `users`")
	assertVarsContain(t, vars, "John")
}

func TestToSQLFilterContainsBindsPattern(t *testing.T) {
	db := newDryRunDB(t)
	sql, vars, err := New[User](db).
		Filters(map[string]any{"name": map[string]any{"$contains": "oh"}}).
		ToSQL(context.Background(), defaultPage())
	if err != nil {
		t.Fatalf("ToSQL: %v", err)
	}
	assertContains(t, sql, "LIKE ?")
	// The wildcard padding is bound as a parameter, never concatenated into SQL.
	assertVarsContain(t, vars, "%oh%")
}

func TestToSQLSearchOrsSearchableColumns(t *testing.T) {
	db := newDryRunDB(t)
	sql, vars, err := New[User](db).
		Search("acme").
		ToSQL(context.Background(), defaultPage())
	if err != nil {
		t.Fatalf("ToSQL: %v", err)
	}
	// SQLite ILike falls back to LOWER(col) LIKE LOWER(?); both searchable
	// columns participate, OR-combined.
	assertContains(t, sql, "users.name")
	assertContains(t, sql, "users.email")
	assertContains(t, sql, " OR ")
	assertVarsContain(t, vars, "%acme%")
}

func TestToSQLSortOrderBy(t *testing.T) {
	db := newDryRunDB(t)
	sql, _, err := New[User](db).
		Sort([]string{"name:desc", "age:asc"}).
		ToSQL(context.Background(), defaultPage())
	if err != nil {
		t.Fatalf("ToSQL: %v", err)
	}
	assertContains(t, sql, "ORDER BY")
	assertContains(t, sql, "users.name DESC")
	assertContains(t, sql, "users.age ASC")
}

func TestToSQLFieldsProjection(t *testing.T) {
	db := newDryRunDB(t)
	sql, _, err := New[User](db).
		Fields([]string{"name", "email"}).
		ToSQL(context.Background(), defaultPage())
	if err != nil {
		t.Fatalf("ToSQL: %v", err)
	}
	assertContains(t, sql, "users.name")
	assertContains(t, sql, "users.email")
	// The primary key is always projected so downstream scanning has the key.
	assertContains(t, sql, "users.id")
}

func TestToSQLPaginationLimitOffset(t *testing.T) {
	db := newDryRunDB(t)
	sql, _, err := New[User](db).
		ToSQL(context.Background(), paginate.Params{Mode: paginate.ModePage, Page: 3, PageSize: 10})
	if err != nil {
		t.Fatalf("ToSQL: %v", err)
	}
	assertContains(t, sql, "LIMIT 10")
	// Page 3 at size 10 => OFFSET 20.
	assertContains(t, sql, "OFFSET 20")
}

func TestToSQLRelationFilterJoinsOnce(t *testing.T) {
	db := newDryRunDB(t)
	// Filtering and sorting both reference the organization relation; the shared
	// joins registry must emit exactly one JOIN clause for it.
	sql, _, err := New[User](db).
		Filters(map[string]any{"organization.slug": map[string]any{"$eq": "acme"}}).
		Sort([]string{"organization.name:asc"}).
		ToSQL(context.Background(), defaultPage())
	if err != nil {
		t.Fatalf("ToSQL: %v", err)
	}
	if n := strings.Count(sql, "JOIN organizations"); n != 1 {
		t.Errorf("expected exactly one organizations JOIN, got %d in: %s", n, sql)
	}
	assertContains(t, sql, "organizations.slug = ?")
	assertContains(t, sql, "organizations.name ASC")
}

func TestToSQLFromParams(t *testing.T) {
	db := newDryRunDB(t)
	qp := parser.QueryParams{
		Filters: map[string]any{"age": map[string]any{"$gte": "18"}},
		Sort:    []string{"name:asc"},
	}
	sql, vars, err := New[User](db).
		FromParams(qp).
		ToSQL(context.Background(), defaultPage())
	if err != nil {
		t.Fatalf("ToSQL: %v", err)
	}
	assertContains(t, sql, "users.age >= ?")
	assertContains(t, sql, "users.name ASC")
	// The string operand is coerced to the field's int kind before binding.
	assertVarsContain(t, vars, int64(18))
}

func TestUnknownFieldErrors(t *testing.T) {
	db := newDryRunDB(t)
	_, _, err := New[User](db).
		Filters(map[string]any{"nope": map[string]any{"$eq": "x"}}).
		ToSQL(context.Background(), defaultPage())
	if err == nil {
		t.Fatal("expected error for unknown field, got nil")
	}
}

func TestUnknownOperatorErrors(t *testing.T) {
	db := newDryRunDB(t)
	_, _, err := New[User](db).
		Filters(map[string]any{"name": map[string]any{"$bogus": "x"}}).
		ToSQL(context.Background(), defaultPage())
	if err == nil {
		t.Fatal("expected error for unknown operator, got nil")
	}
}

func TestCustomOperator(t *testing.T) {
	db := newDryRunDB(t)
	// Register a custom "$isNull" operator and confirm it renders.
	sql, _, err := New[User](db).
		Operator("$isNull", func(col string, _ any, _ dialect.Dialect) (string, []any, error) {
			return col + " IS NULL", nil, nil
		}).
		Filters(map[string]any{"name": map[string]any{"$isNull": true}}).
		ToSQL(context.Background(), defaultPage())
	if err != nil {
		t.Fatalf("ToSQL: %v", err)
	}
	assertContains(t, sql, "users.name IS NULL")
}

// assertContains fails the test when substr is not present in s.
func assertContains(t *testing.T, s, substr string) {
	t.Helper()
	if !strings.Contains(s, substr) {
		t.Errorf("SQL %q does not contain %q", s, substr)
	}
}

// assertVarsContain fails the test when want is not among the bound vars.
func assertVarsContain(t *testing.T, vars []any, want any) {
	t.Helper()
	for _, v := range vars {
		if v == want {
			return
		}
	}
	t.Errorf("bound vars %#v do not contain %#v", vars, want)
}
