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

// Category and Connection are the fixture for relation-scope tests: a Category
// HasMany Connections, and a connection carries the UserID it belongs to, so a
// scope like "connections.user_id = ?" restricts a relation to one user's rows.
type Category struct {
	ID          uint         `json:"id" gorm:"primaryKey"`
	Name        string       `json:"name"`
	Connections []Connection `json:"connections"`
}

// Connection is the scoped relation row.
type Connection struct {
	ID         uint   `json:"id" gorm:"primaryKey"`
	CategoryID uint   `json:"category_id"`
	UserID     uint   `json:"user_id"`
	Status     string `json:"status"`
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

func TestToSQLRelationScopeInJoinOn(t *testing.T) {
	db := newDryRunDB(t)
	// The scope predicate must land in the relation's JOIN ON clause (ANDed onto
	// the join condition), never in a top-level WHERE, so LEFT JOIN semantics for
	// populate are preserved.
	sql, vars, err := New[Category](db).
		WithRelationScope("connections", "connections.user_id = ?", uint(42)).
		Filters(map[string]any{"connections.status": map[string]any{"$eq": "connected"}}).
		ToSQL(context.Background(), defaultPage())
	if err != nil {
		t.Fatalf("ToSQL: %v", err)
	}
	// The scope is appended to the ON clause of the connections join.
	assertContains(t, sql, "ON connections.category_id = categories.id AND (connections.user_id = ?)")
	// The filter on the scoped relation still compiles to a WHERE predicate.
	assertContains(t, sql, "connections.status = ?")
	// The scope's operand is bound, never interpolated.
	assertVarsContain(t, vars, uint(42))
}

func TestToSQLRelationScopeMultipleAndTogether(t *testing.T) {
	db := newDryRunDB(t)
	// Two scopes on the same relation AND together inside the ON clause.
	sql, vars, err := New[Category](db).
		WithRelationScope("connections", "connections.user_id = ?", uint(7)).
		WithRelationScope("connections", "connections.status <> ?", "revoked").
		Sort([]string{"connections.status:asc"}).
		ToSQL(context.Background(), defaultPage())
	if err != nil {
		t.Fatalf("ToSQL: %v", err)
	}
	assertContains(t, sql, "AND (connections.user_id = ?) AND (connections.status <> ?)")
	assertVarsContain(t, vars, uint(7))
	assertVarsContain(t, vars, "revoked")
}

func TestRelationScopeUnknownRelationErrors(t *testing.T) {
	db := newDryRunDB(t)
	_, _, err := New[Category](db).
		WithRelationScope("nope", "nope.user_id = ?", 1).
		ToSQL(context.Background(), defaultPage())
	if err == nil {
		t.Fatal("expected error for unknown relation, got nil")
	}
}

// TestRelationScopePopulateReturnsBaseRowsScoped runs against a real (non-DryRun)
// sqlite database to prove the populate semantics ToSQL cannot show: every base
// row is returned (LEFT JOIN semantics), with only the scoped relation rows
// attached.
func TestRelationScopePopulateReturnsBaseRowsScoped(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("gorm.Open: %v", err)
	}
	if err := db.AutoMigrate(&Category{}, &Connection{}); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}

	// Two categories. cat 1 has connections for user 42 and user 99; cat 2 has a
	// single connection for user 42. A scope to user 42 must keep both categories
	// but drop user 99's connection.
	seed := []Category{
		{ID: 1, Name: "alpha", Connections: []Connection{
			{ID: 1, CategoryID: 1, UserID: 42, Status: "connected"},
			{ID: 2, CategoryID: 1, UserID: 99, Status: "connected"},
		}},
		{ID: 2, Name: "beta", Connections: []Connection{
			{ID: 3, CategoryID: 2, UserID: 42, Status: "disconnected"},
		}},
	}
	if err := db.Create(&seed).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	cats, err := New[Category](db).
		WithRelationScope("connections", "connections.user_id = ?", uint(42)).
		Populate([]string{"connections"}).
		Sort([]string{"id:asc"}).
		Find(context.Background())
	if err != nil {
		t.Fatalf("Find: %v", err)
	}

	// Populate uses a LEFT JOIN / separate preload query, so both base rows remain.
	if len(cats) != 2 {
		t.Fatalf("got %d categories, want 2 (all base rows must survive the scope)", len(cats))
	}
	for _, c := range cats {
		for _, conn := range c.Connections {
			if conn.UserID != 42 {
				t.Errorf("category %d has a connection for user %d; scope should keep only user 42", c.ID, conn.UserID)
			}
		}
	}
	// cat 1 had two connections; only user 42's should remain.
	if got := len(cats[0].Connections); got != 1 {
		t.Errorf("category 1 attached %d connections, want 1 (only user 42)", got)
	}
	if got := len(cats[1].Connections); got != 1 {
		t.Errorf("category 2 attached %d connections, want 1", got)
	}
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
