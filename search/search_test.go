package search

import (
	"errors"
	"testing"

	"github.com/faisalcayunda/strapgorm/dialect"
	"github.com/faisalcayunda/strapgorm/schema"
)

// searchableUser declares three searchable fields and is used to exercise the
// multi-column OR predicate.
type searchableUser struct {
	ID    uint   `json:"id" gorm:"primaryKey"`
	Name  string `json:"name"`
	Email string `json:"email"`
	Bio   string `json:"bio"`
}

func (searchableUser) Searchable() []string { return []string{"name", "email", "bio"} }

// singleField declares exactly one searchable field, used to confirm that a
// single-column predicate is not wrapped in parentheses.
type singleField struct {
	ID   uint   `json:"id" gorm:"primaryKey"`
	Name string `json:"name"`
}

func (singleField) Searchable() []string { return []string{"name"} }

// plainModel declares no searchable fields.
type plainModel struct {
	ID   uint   `json:"id" gorm:"primaryKey"`
	Name string `json:"name"`
}

// taggedColumn maps a JSON name to a non-default column to confirm Build
// qualifies the resolved column, not the JSON name.
type taggedColumn struct {
	ID   uint   `json:"id" gorm:"primaryKey"`
	Name string `json:"display_name" gorm:"column:full_name"`
}

func (taggedColumn) Searchable() []string { return []string{"display_name"} }

func mustModel[T any](t *testing.T) *schema.Model {
	t.Helper()
	m, err := schema.Of[T]()
	if err != nil {
		t.Fatalf("schema.Of: %v", err)
	}
	return m
}

func TestBuildSQL(t *testing.T) {
	tests := []struct {
		name      string
		model     *schema.Model
		rootTable string
		term      string
		d         dialect.Dialect
		wantSQL   string
		wantArgs  []any
	}{
		{
			name:      "multi-field OR postgres ilike",
			model:     mustModel[searchableUser](t),
			rootTable: "users",
			term:      "John",
			d:         dialect.Postgres,
			wantSQL:   "(users.name ILIKE ? OR users.email ILIKE ? OR users.bio ILIKE ?)",
			wantArgs:  []any{"%John%", "%John%", "%John%"},
		},
		{
			name:      "multi-field OR mysql lower like",
			model:     mustModel[searchableUser](t),
			rootTable: "users",
			term:      "John",
			d:         dialect.MySQL,
			wantSQL:   "(LOWER(users.name) LIKE LOWER(?) OR LOWER(users.email) LIKE LOWER(?) OR LOWER(users.bio) LIKE LOWER(?))",
			wantArgs:  []any{"%John%", "%John%", "%John%"},
		},
		{
			name:      "multi-field OR sqlite lower like",
			model:     mustModel[searchableUser](t),
			rootTable: "users",
			term:      "John",
			d:         dialect.SQLite,
			wantSQL:   "(LOWER(users.name) LIKE LOWER(?) OR LOWER(users.email) LIKE LOWER(?) OR LOWER(users.bio) LIKE LOWER(?))",
			wantArgs:  []any{"%John%", "%John%", "%John%"},
		},
		{
			name:      "single field is not parenthesized",
			model:     mustModel[singleField](t),
			rootTable: "single_fields",
			term:      "ab",
			d:         dialect.Postgres,
			wantSQL:   "single_fields.name ILIKE ?",
			wantArgs:  []any{"%ab%"},
		},
		{
			name:      "resolves to mapped column not json name",
			model:     mustModel[taggedColumn](t),
			rootTable: "tagged_columns",
			term:      "x",
			d:         dialect.Postgres,
			wantSQL:   "tagged_columns.full_name ILIKE ?",
			wantArgs:  []any{"%x%"},
		},
		{
			name:      "term with percent is bound not interpolated",
			model:     mustModel[singleField](t),
			rootTable: "single_fields",
			term:      "50%off",
			d:         dialect.Postgres,
			wantSQL:   "single_fields.name ILIKE ?",
			wantArgs:  []any{"%50%off%"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Build(tt.model, tt.rootTable, tt.term, tt.d)
			if err != nil {
				t.Fatalf("Build returned error: %v", err)
			}
			if got.SQL != tt.wantSQL {
				t.Errorf("SQL = %q, want %q", got.SQL, tt.wantSQL)
			}
			if !equalArgs(got.Args, tt.wantArgs) {
				t.Errorf("Args = %#v, want %#v", got.Args, tt.wantArgs)
			}
		})
	}
}

func TestBuildEmptyTermIsNoOp(t *testing.T) {
	m := mustModel[searchableUser](t)
	got, err := Build(m, "users", "", dialect.Postgres)
	if err != nil {
		t.Fatalf("Build returned error: %v", err)
	}
	if got.SQL != "" {
		t.Errorf("SQL = %q, want empty", got.SQL)
	}
	if got.Args != nil {
		t.Errorf("Args = %#v, want nil", got.Args)
	}
}

func TestBuildNoSearchableFields(t *testing.T) {
	m := mustModel[plainModel](t)
	_, err := Build(m, "plain_models", "John", dialect.Postgres)
	if err == nil {
		t.Fatal("Build returned nil error, want ErrNoSearchableFields")
	}
	if !errors.Is(err, ErrNoSearchableFields) {
		t.Errorf("error = %v, want errors.Is ErrNoSearchableFields", err)
	}
}

func TestBuildNoSearchableFieldsEmptyTermStillNoOp(t *testing.T) {
	// An empty term short-circuits before the searchable-fields check, so a
	// non-searchable model with an empty term is a clean no-op.
	m := mustModel[plainModel](t)
	got, err := Build(m, "plain_models", "", dialect.Postgres)
	if err != nil {
		t.Fatalf("Build returned error: %v", err)
	}
	if got.SQL != "" || got.Args != nil {
		t.Errorf("Build = %+v, want zero Clause", got)
	}
}

// equalArgs compares two []any slices element-wise.
func equalArgs(a, b []any) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
