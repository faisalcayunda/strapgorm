package fields

import (
	"errors"
	"testing"

	"github.com/faisalcayunda/strapgorm/schema"
)

// user is the primary fixture: a default "id" primary key plus a field whose
// column is remapped by a gorm column tag to confirm Build returns the resolved
// column, not the JSON name. The organization relation exercises the
// relation-qualified (dotted) skip path.
type user struct {
	ID           uint         `json:"id" gorm:"primaryKey"`
	Name         string       `json:"name"`
	Email        string       `json:"display_email" gorm:"column:email_address"`
	Organization organization `json:"organization"`
}

// organization is a related model used only to give user a relation; its own
// fields are never projected by the root SELECT.
type organization struct {
	ID   uint   `json:"id" gorm:"primaryKey"`
	Slug string `json:"slug"`
}

// customPK uses a non-default primary-key column to confirm the always-include
// behavior targets Model.PrimaryKey rather than a hard-coded "id".
type customPK struct {
	UUID string `json:"uuid" gorm:"column:uuid;primaryKey"`
	Name string `json:"name"`
}

func mustModel[T any](t *testing.T) *schema.Model {
	t.Helper()
	m, err := schema.Of[T]()
	if err != nil {
		t.Fatalf("schema.Of: %v", err)
	}
	return m
}

func TestBuild(t *testing.T) {
	tests := []struct {
		name  string
		model *schema.Model
		raw   []string
		want  []string
	}{
		{
			name:  "empty raw selects all (nil)",
			model: mustModel[user](t),
			raw:   nil,
			want:  nil,
		},
		{
			name:  "empty slice selects all (nil)",
			model: mustModel[user](t),
			raw:   []string{},
			want:  nil,
		},
		{
			name:  "subset resolves to columns plus primary key",
			model: mustModel[user](t),
			raw:   []string{"name"},
			want:  []string{"name", "id"},
		},
		{
			name:  "resolves mapped column not json name",
			model: mustModel[user](t),
			raw:   []string{"display_email"},
			want:  []string{"email_address", "id"},
		},
		{
			name:  "primary key requested is not duplicated",
			model: mustModel[user](t),
			raw:   []string{"id", "name"},
			want:  []string{"id", "name"},
		},
		{
			name:  "duplicate fields are deduplicated",
			model: mustModel[user](t),
			raw:   []string{"name", "name", "name"},
			want:  []string{"name", "id"},
		},
		{
			name:  "blank entries are skipped",
			model: mustModel[user](t),
			raw:   []string{"", "  ", "name"},
			want:  []string{"name", "id"},
		},
		{
			name:  "relation-qualified entries are skipped",
			model: mustModel[user](t),
			raw:   []string{"name", "organization.slug"},
			want:  []string{"name", "id"},
		},
		{
			name:  "only relation-qualified entries select all (nil)",
			model: mustModel[user](t),
			raw:   []string{"organization.slug"},
			want:  nil,
		},
		{
			name:  "input order is preserved with pk appended",
			model: mustModel[user](t),
			raw:   []string{"name", "display_email"},
			want:  []string{"name", "email_address", "id"},
		},
		{
			name:  "custom primary key column is always included",
			model: mustModel[customPK](t),
			raw:   []string{"name"},
			want:  []string{"name", "uuid"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Build(tt.model, tt.raw)
			if err != nil {
				t.Fatalf("Build returned error: %v", err)
			}
			if !equalStrings(got, tt.want) {
				t.Errorf("Build = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestBuildUnknownField(t *testing.T) {
	m := mustModel[user](t)
	got, err := Build(m, []string{"name", "nope"})
	if err == nil {
		t.Fatal("Build returned nil error, want ErrUnknownField")
	}
	if !errors.Is(err, schema.ErrUnknownField) {
		t.Errorf("error = %v, want errors.Is schema.ErrUnknownField", err)
	}
	if got != nil {
		t.Errorf("columns = %#v, want nil on error", got)
	}
}

// equalStrings compares two string slices element-wise, treating nil and empty
// as equal only when both are empty; a nil want must match a nil got because nil
// is the documented "select all" signal.
func equalStrings(a, b []string) bool {
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
