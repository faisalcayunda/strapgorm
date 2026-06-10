package populate

import (
	"errors"
	"testing"

	"github.com/faisalcayunda/strapgorm/schema"
)

// user is the root fixture. It carries a to-one Organization relation and a
// to-many Roles relation so the tests exercise both relation cardinalities, plus
// a scalar Name to confirm scalar fields are never resolvable as relations.
type user struct {
	ID           uint         `json:"id" gorm:"primaryKey"`
	Name         string       `json:"name"`
	Organization organization `json:"organization"`
	Roles        []role       `json:"roles" gorm:"many2many:user_roles"`
}

// organization is a related model that itself carries an Owner relation, used to
// exercise nested dotted resolution (organization.owner) and to confirm the
// emitted association path uses Go field names. Owner is a pointer to break the
// user<->organization value-type cycle (and to read as a BelongsTo relation).
type organization struct {
	ID    uint   `json:"id" gorm:"primaryKey"`
	Slug  string `json:"slug"`
	Owner *user  `json:"owner"`
}

// role is the leaf related model for the many2many relation; it has no relations
// of its own.
type role struct {
	ID   uint   `json:"id" gorm:"primaryKey"`
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

func TestPlan(t *testing.T) {
	tests := []struct {
		name string
		raw  []string
		want []Spec
	}{
		{
			name: "nil raw populates nothing",
			raw:  nil,
			want: nil,
		},
		{
			name: "empty slice populates nothing",
			raw:  []string{},
			want: nil,
		},
		{
			name: "only blank entries populate nothing",
			raw:  []string{"", "   "},
			want: nil,
		},
		{
			name: "single top-level relation by json name",
			raw:  []string{"organization"},
			want: []Spec{{Association: "Organization"}},
		},
		{
			name: "single to-many relation by json name",
			raw:  []string{"roles"},
			want: []Spec{{Association: "Roles"}},
		},
		{
			name: "relation by lowercased go-field alias",
			raw:  []string{"organization"},
			want: []Spec{{Association: "Organization"}},
		},
		{
			name: "multiple relations preserve input order",
			raw:  []string{"roles", "organization"},
			want: []Spec{{Association: "Roles"}, {Association: "Organization"}},
		},
		{
			name: "nested dotted path joins go field names",
			raw:  []string{"organization.owner"},
			want: []Spec{{Association: "Organization.Owner"}},
		},
		{
			name: "deeper nested dotted path",
			raw:  []string{"organization.owner.organization"},
			want: []Spec{{Association: "Organization.Owner.Organization"}},
		},
		{
			name: "duplicate entries are deduplicated",
			raw:  []string{"organization", "organization"},
			want: []Spec{{Association: "Organization"}},
		},
		{
			name: "blank entries are skipped among real ones",
			raw:  []string{"", "organization", "  "},
			want: []Spec{{Association: "Organization"}},
		},
		{
			name: "wildcard requests preload all",
			raw:  []string{"*"},
			want: []Spec{{All: true}},
		},
		{
			name: "wildcard takes precedence over other entries",
			raw:  []string{"organization", "*", "roles"},
			want: []Spec{{All: true}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Plan(mustModel[user](t), tt.raw)
			if err != nil {
				t.Fatalf("Plan returned error: %v", err)
			}
			if !equalSpecs(got, tt.want) {
				t.Errorf("Plan = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestPlanUnknownRelation(t *testing.T) {
	tests := []struct {
		name string
		raw  []string
	}{
		{
			name: "unknown top-level relation",
			raw:  []string{"nope"},
		},
		{
			name: "scalar field is not a relation",
			raw:  []string{"name"},
		},
		{
			name: "unknown leaf in nested path",
			raw:  []string{"organization.nope"},
		},
		{
			name: "unknown root in nested path",
			raw:  []string{"nope.owner"},
		},
		{
			name: "trailing dot yields blank segment",
			raw:  []string{"organization."},
		},
		{
			name: "doubled dot yields blank segment",
			raw:  []string{"organization..owner"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Plan(mustModel[user](t), tt.raw)
			if err == nil {
				t.Fatal("Plan returned nil error, want ErrUnknownRelation")
			}
			if !errors.Is(err, schema.ErrUnknownRelation) {
				t.Errorf("error = %v, want errors.Is schema.ErrUnknownRelation", err)
			}
			if got != nil {
				t.Errorf("specs = %#v, want nil on error", got)
			}
		})
	}
}

// equalSpecs compares two Spec slices element-wise, treating nil and empty as
// equal only when both are empty.
func equalSpecs(a, b []Spec) bool {
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
