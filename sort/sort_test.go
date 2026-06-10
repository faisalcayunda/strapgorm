package sort

import (
	"database/sql"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/faisalcayunda/strapgorm/internal/joins"
	"github.com/faisalcayunda/strapgorm/schema"
)

// --- Sample models -------------------------------------------------------

// Base is an embedded struct whose fields are promoted onto the parent.
type Base struct {
	ID        uint      `json:"id" gorm:"primaryKey"`
	CreatedAt time.Time `json:"created_at"`
}

type Organization struct {
	Base
	Name string `json:"name"`
	Slug string `json:"slug" gorm:"column:org_slug"`
}

type Role struct {
	Base
	Title string `json:"title"`
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

type User struct {
	Base
	Name     string         `json:"name"`
	Email    string         `json:"email"`
	Bio      *string        `json:"bio"`
	Age      int            `json:"age"`
	Nickname sql.NullString `json:"nickname"`

	OrganizationID uint         `json:"organization_id"`
	Organization   Organization `json:"organization"`                    // BelongsTo
	Role           *Role        `json:"role"`                            // HasOne
	Tags           []Tag        `json:"tags" gorm:"many2many:user_tags"` // Many2Many
	Posts          []Post       `json:"posts"`                           // HasMany
}

// mustModel parses t or fails the test.
func mustModel(t *testing.T, v any) *schema.Model {
	t.Helper()
	m, err := schema.Get(reflect.TypeOf(v))
	if err != nil {
		t.Fatalf("schema.Get(%T): %v", v, err)
	}
	return m
}

// --- Success cases -------------------------------------------------------

func TestBuildOrders(t *testing.T) {
	m := mustModel(t, User{})

	tests := []struct {
		name string
		raw  []string
		want []string
	}{
		{
			name: "single field default direction",
			raw:  []string{"name"},
			want: []string{"users.name ASC"},
		},
		{
			name: "single field explicit asc",
			raw:  []string{"name:asc"},
			want: []string{"users.name ASC"},
		},
		{
			name: "single field desc",
			raw:  []string{"name:desc"},
			want: []string{"users.name DESC"},
		},
		{
			name: "uppercase strapi direction",
			raw:  []string{"name:ASC", "age:DESC"},
			want: []string{"users.name ASC", "users.age DESC"},
		},
		{
			name: "mixed case direction",
			raw:  []string{"name:Asc", "email:DeSc"},
			want: []string{"users.name ASC", "users.email DESC"},
		},
		{
			name: "multi field preserves order",
			raw:  []string{"age:desc", "name:asc", "email"},
			want: []string{"users.age DESC", "users.name ASC", "users.email ASC"},
		},
		{
			name: "gorm column tag honored on root",
			raw:  []string{"created_at:desc"},
			want: []string{"users.created_at DESC"},
		},
		{
			name: "blank and whitespace entries skipped",
			raw:  []string{"", "   ", "name:asc", "\t"},
			want: []string{"users.name ASC"},
		},
		{
			name: "trailing colon defaults to asc",
			raw:  []string{"name:"},
			want: []string{"users.name ASC"},
		},
		{
			name: "surrounding whitespace trimmed",
			raw:  []string{"  name:desc  "},
			want: []string{"users.name DESC"},
		},
		{
			name: "empty input yields no orders",
			raw:  []string{},
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			jr := joins.NewRegistry(m.Table)
			got, err := Build(m, tt.raw, jr)
			if err != nil {
				t.Fatalf("Build: unexpected error %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("orders = %v, want %v", got, tt.want)
			}
			if len(jr.Joins()) != 0 {
				t.Errorf("root-only sort registered joins: %v", jr.Joins())
			}
		})
	}
}

// --- Relation sorting ----------------------------------------------------

func TestBuildRelationBelongsTo(t *testing.T) {
	m := mustModel(t, User{})
	jr := joins.NewRegistry(m.Table)

	got, err := Build(m, []string{"organization.slug:desc"}, jr)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	// org_slug is the gorm column override on Organization.Slug.
	want := []string{"organizations.org_slug DESC"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("orders = %v, want %v", got, want)
	}

	js := jr.Joins()
	if len(js) != 1 {
		t.Fatalf("len(Joins()) = %d, want 1", len(js))
	}
	wantJoin := "LEFT JOIN organizations ON organizations.id = users.organization_id"
	if js[0].SQL != wantJoin {
		t.Errorf("join SQL = %q, want %q", js[0].SQL, wantJoin)
	}
	if js[0].Alias != "organizations" {
		t.Errorf("join alias = %q, want %q", js[0].Alias, "organizations")
	}
}

func TestBuildRelationHasOne(t *testing.T) {
	m := mustModel(t, User{})
	jr := joins.NewRegistry(m.Table)

	got, err := Build(m, []string{"role.title:asc"}, jr)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	want := []string{"roles.title ASC"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("orders = %v, want %v", got, want)
	}

	js := jr.Joins()
	if len(js) != 1 {
		t.Fatalf("len(Joins()) = %d, want 1", len(js))
	}
	// HasOne FK lives on the related table: owner name + "_id".
	wantJoin := "LEFT JOIN roles ON roles.user_id = users.id"
	if js[0].SQL != wantJoin {
		t.Errorf("join SQL = %q, want %q", js[0].SQL, wantJoin)
	}
}

func TestBuildRelationHasMany(t *testing.T) {
	m := mustModel(t, User{})
	jr := joins.NewRegistry(m.Table)

	got, err := Build(m, []string{"posts.title:desc"}, jr)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	want := []string{"posts.title DESC"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("orders = %v, want %v", got, want)
	}

	js := jr.Joins()
	if len(js) != 1 {
		t.Fatalf("len(Joins()) = %d, want 1", len(js))
	}
	wantJoin := "LEFT JOIN posts ON posts.user_id = users.id"
	if js[0].SQL != wantJoin {
		t.Errorf("join SQL = %q, want %q", js[0].SQL, wantJoin)
	}
}

func TestBuildRelationMany2Many(t *testing.T) {
	m := mustModel(t, User{})
	jr := joins.NewRegistry(m.Table)

	got, err := Build(m, []string{"tags.label:asc"}, jr)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	want := []string{"tags.label ASC"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("orders = %v, want %v", got, want)
	}

	js := jr.Joins()
	if len(js) != 1 {
		t.Fatalf("len(Joins()) = %d, want 1", len(js))
	}
	wantJoin := "LEFT JOIN user_tags ON user_tags.user_id = users.id " +
		"LEFT JOIN tags ON tags.id = user_tags.tag_id"
	if js[0].SQL != wantJoin {
		t.Errorf("join SQL = %q, want %q", js[0].SQL, wantJoin)
	}
	if js[0].Alias != "tags" {
		t.Errorf("join alias = %q, want %q", js[0].Alias, "tags")
	}
}

func TestBuildRelationByGoFieldAlias(t *testing.T) {
	// Relations are reachable by the lowercased Go field name as well as the
	// snake_case name; both must register the same single join.
	m := mustModel(t, User{})
	jr := joins.NewRegistry(m.Table)

	got, err := Build(m, []string{"organization.name:asc"}, jr)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	want := []string{"organizations.name ASC"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("orders = %v, want %v", got, want)
	}
}

func TestBuildJoinDedup(t *testing.T) {
	// Sorting twice on the same relation must register the join only once,
	// matching the shared-registry contract with the filter package.
	m := mustModel(t, User{})
	jr := joins.NewRegistry(m.Table)

	got, err := Build(m, []string{"organization.name:asc", "organization.slug:desc"}, jr)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	want := []string{"organizations.name ASC", "organizations.org_slug DESC"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("orders = %v, want %v", got, want)
	}
	if got := len(jr.Joins()); got != 1 {
		t.Errorf("len(Joins()) = %d, want 1 (join must be deduplicated)", got)
	}
}

func TestBuildMixedRootAndRelation(t *testing.T) {
	m := mustModel(t, User{})
	jr := joins.NewRegistry(m.Table)

	got, err := Build(m, []string{"name:asc", "organization.slug:desc", "age"}, jr)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	want := []string{"users.name ASC", "organizations.org_slug DESC", "users.age ASC"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("orders = %v, want %v", got, want)
	}
	if got := len(jr.Joins()); got != 1 {
		t.Errorf("len(Joins()) = %d, want 1", got)
	}
}

// --- Error cases ---------------------------------------------------------

func TestBuildErrors(t *testing.T) {
	m := mustModel(t, User{})

	tests := []struct {
		name    string
		raw     []string
		wantErr error
	}{
		{
			name:    "unknown root field",
			raw:     []string{"does_not_exist:asc"},
			wantErr: schema.ErrUnknownField,
		},
		{
			name:    "unknown relation",
			raw:     []string{"unknown_rel.name:asc"},
			wantErr: schema.ErrUnknownRelation,
		},
		{
			name:    "unknown subfield on known relation",
			raw:     []string{"organization.does_not_exist:asc"},
			wantErr: schema.ErrUnknownField,
		},
		{
			name:    "valid entry before unknown field still errors",
			raw:     []string{"name:asc", "nope:desc"},
			wantErr: schema.ErrUnknownField,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			jr := joins.NewRegistry(m.Table)
			got, err := Build(m, tt.raw, jr)
			if err == nil {
				t.Fatalf("Build = %v, want error %v", got, tt.wantErr)
			}
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("error = %v, want errors.Is %v", err, tt.wantErr)
			}
			if got != nil {
				t.Errorf("orders = %v, want nil on error", got)
			}
		})
	}
}

func TestBuildMalformedDirectives(t *testing.T) {
	m := mustModel(t, User{})

	tests := []struct {
		name string
		raw  []string
	}{
		{name: "unknown direction", raw: []string{"name:sideways"}},
		{name: "colon only", raw: []string{":"}},
		{name: "colon-prefixed empty field", raw: []string{":asc"}},
		{name: "relation empty subfield", raw: []string{"organization.:asc"}},
		{name: "relation empty subfield no dir", raw: []string{"organization."}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			jr := joins.NewRegistry(m.Table)
			got, err := Build(m, tt.raw, jr)
			if err == nil {
				t.Fatalf("Build = %v, want a malformed-directive error", got)
			}
			if got != nil {
				t.Errorf("orders = %v, want nil on error", got)
			}
		})
	}
}
