package schema

import (
	"database/sql"
	"reflect"
	"testing"
	"time"
)

// --- Sample models -------------------------------------------------------

// Base is an embedded struct whose fields must be promoted onto the parent.
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

type User struct {
	Base
	Name           string         `json:"name"`
	Email          string         `json:"email"`
	Bio            *string        `json:"bio"`
	Age            int            `json:"age"`
	Nickname       sql.NullString `json:"nickname"`
	Internal       string         `json:"-" gorm:"-"`
	unexportedHide string         //nolint:unused

	OrganizationID uint         `json:"organization_id"`
	Organization   Organization `json:"organization"`                    // BelongsTo (has OrganizationID)
	Role           *Role        `json:"role"`                            // HasOne (no RoleID)
	Tags           []Tag        `json:"tags" gorm:"many2many:user_tags"` // Many2Many
	Posts          []Post       `json:"posts"`                           // HasMany
}

type Post struct {
	Base
	Title  string `json:"title"`
	UserID uint   `json:"user_id"`
}

func (User) Searchable() []string { return []string{"name", "email", "bio"} }

// NoSearch has no Searchable implementation.
type NoSearch struct {
	Base
	Value string `json:"value"`
}

// --- Tests ---------------------------------------------------------------

func TestParseTableAndPrimaryKey(t *testing.T) {
	m, err := Parse(reflect.TypeOf(User{}))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if m.Table != "users" {
		t.Errorf("Table = %q, want %q", m.Table, "users")
	}
	if m.PrimaryKey != "id" {
		t.Errorf("PrimaryKey = %q, want %q", m.PrimaryKey, "id")
	}
	if m.Type != reflect.TypeOf(User{}) {
		t.Errorf("Type = %v, want User", m.Type)
	}
}

func TestParseEmbeddedPromotion(t *testing.T) {
	m, _ := Parse(reflect.TypeOf(User{}))
	for _, jsonName := range []string{"id", "created_at"} {
		if _, ok := m.Field(jsonName); !ok {
			t.Errorf("embedded field %q not promoted", jsonName)
		}
	}
}

func TestParseFields(t *testing.T) {
	m, _ := Parse(reflect.TypeOf(User{}))

	tests := []struct {
		json     string
		wantCol  string
		wantKind Kind
		wantNull bool
	}{
		{"id", "id", KindUint, false},
		{"created_at", "created_at", KindTime, false},
		{"name", "name", KindString, false},
		{"email", "email", KindString, false},
		{"bio", "bio", KindString, true}, // *string -> nullable
		{"age", "age", KindInt, false},
		{"nickname", "nickname", KindString, true}, // sql.NullString -> nullable
		{"organization_id", "organization_id", KindUint, false},
	}
	for _, tt := range tests {
		t.Run(tt.json, func(t *testing.T) {
			f, ok := m.Field(tt.json)
			if !ok {
				t.Fatalf("field %q missing", tt.json)
			}
			if f.Column != tt.wantCol {
				t.Errorf("Column = %q, want %q", f.Column, tt.wantCol)
			}
			if f.Kind != tt.wantKind {
				t.Errorf("Kind = %d, want %d", f.Kind, tt.wantKind)
			}
			if f.Nullable != tt.wantNull {
				t.Errorf("Nullable = %v, want %v", f.Nullable, tt.wantNull)
			}
		})
	}
}

func TestParseGormColumnTag(t *testing.T) {
	m, _ := Parse(reflect.TypeOf(Organization{}))
	col, ok := m.ColumnFor("slug")
	if !ok {
		t.Fatal("slug field missing")
	}
	if col != "org_slug" {
		t.Errorf("ColumnFor(slug) = %q, want %q (gorm column tag)", col, "org_slug")
	}
}

func TestParseSkipsIgnoredAndUnexported(t *testing.T) {
	m, _ := Parse(reflect.TypeOf(User{}))
	if _, ok := m.Field("-"); ok {
		t.Error("gorm:\"-\" field was not skipped")
	}
	if _, ok := m.Field("internal"); ok {
		t.Error("json:\"-\" / gorm:\"-\" field leaked in")
	}
	if _, ok := m.Field("unexportedhide"); ok {
		t.Error("unexported field leaked in")
	}
}

func TestParseRelations(t *testing.T) {
	m, _ := Parse(reflect.TypeOf(User{}))

	tests := []struct {
		name        string
		wantType    RelationType
		wantGoField string
		wantTable   string
		wantJoin    string
	}{
		{"organization", BelongsTo, "Organization", "organizations", ""},
		{"role", HasOne, "Role", "roles", ""},
		{"tags", Many2Many, "Tags", "tags", "user_tags"},
		{"posts", HasMany, "Posts", "posts", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rel, ok := m.Relation(tt.name)
			if !ok {
				t.Fatalf("relation %q missing", tt.name)
			}
			if rel.Type != tt.wantType {
				t.Errorf("Type = %d, want %d", rel.Type, tt.wantType)
			}
			if rel.GoField != tt.wantGoField {
				t.Errorf("GoField = %q, want %q", rel.GoField, tt.wantGoField)
			}
			if rel.Table != tt.wantTable {
				t.Errorf("Table = %q, want %q", rel.Table, tt.wantTable)
			}
			if rel.JoinTable != tt.wantJoin {
				t.Errorf("JoinTable = %q, want %q", rel.JoinTable, tt.wantJoin)
			}
		})
	}
}

func TestParseBelongsToFKColumn(t *testing.T) {
	m, _ := Parse(reflect.TypeOf(User{}))
	rel, _ := m.Relation("organization")
	if rel.FKColumn != "organization_id" {
		t.Errorf("BelongsTo FKColumn = %q, want %q", rel.FKColumn, "organization_id")
	}
}

func TestParseHasManyFKColumn(t *testing.T) {
	m, _ := Parse(reflect.TypeOf(User{}))
	rel, _ := m.Relation("posts")
	// HasMany FK lives on the related table: owner name + "_id".
	if rel.FKColumn != "user_id" {
		t.Errorf("HasMany FKColumn = %q, want %q", rel.FKColumn, "user_id")
	}
}

func TestRelationGoFieldAlias(t *testing.T) {
	// A relation must be reachable by the lowercased Go field name as well as
	// by the snake_case dot-notation name. Use a multi-word relation to make
	// the alias distinct from the snake_case name.
	type Account struct {
		Base
		Name string `json:"name"`
	}
	type Holder struct {
		Base
		PrimaryAccountID uint    `json:"primary_account_id"`
		PrimaryAccount   Account `json:"primary_account"`
	}
	m, _ := Parse(reflect.TypeOf(Holder{}))

	if _, ok := m.Relation("primary_account"); !ok {
		t.Error("relation not reachable by snake_case name")
	}
	if _, ok := m.Relation("primaryaccount"); !ok {
		t.Error("relation not reachable by lowercased Go field alias")
	}
}

func TestParseSearchable(t *testing.T) {
	m, _ := Parse(reflect.TypeOf(User{}))
	want := []string{"name", "email", "bio"}
	if !reflect.DeepEqual(m.SearchableFields, want) {
		t.Errorf("SearchableFields = %v, want %v", m.SearchableFields, want)
	}
}

func TestParseNoSearchable(t *testing.T) {
	m, _ := Parse(reflect.TypeOf(NoSearch{}))
	if len(m.SearchableFields) != 0 {
		t.Errorf("SearchableFields = %v, want empty", m.SearchableFields)
	}
}

func TestParseNonStruct(t *testing.T) {
	if _, err := Parse(reflect.TypeOf(42)); err == nil {
		t.Error("Parse(int) should error")
	}
	if _, err := Parse(nil); err == nil {
		t.Error("Parse(nil) should error")
	}
}

func TestParseDerefsPointer(t *testing.T) {
	m, err := Parse(reflect.TypeOf(&User{}))
	if err != nil {
		t.Fatalf("Parse(*User): %v", err)
	}
	if m.Table != "users" {
		t.Errorf("Table = %q, want users", m.Table)
	}
}

func TestModelFieldLookups(t *testing.T) {
	m, _ := Parse(reflect.TypeOf(User{}))

	if _, ok := m.Field("does_not_exist"); ok {
		t.Error("Field returned ok for unknown field")
	}
	if _, ok := m.Relation("does_not_exist"); ok {
		t.Error("Relation returned ok for unknown relation")
	}
	if _, ok := m.ColumnFor("does_not_exist"); ok {
		t.Error("ColumnFor returned ok for unknown field")
	}
}

func TestGetCaches(t *testing.T) {
	m1, err := Get(reflect.TypeOf(Post{}))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	m2, _ := Get(reflect.TypeOf(Post{}))
	if m1 != m2 {
		t.Error("Get did not return the cached *Model instance")
	}
	// Pointer type must resolve to the same cached entry as the value type.
	m3, _ := Get(reflect.TypeOf(&Post{}))
	if m1 != m3 {
		t.Error("Get(*Post) and Get(Post) returned different cached entries")
	}
}

func TestOf(t *testing.T) {
	m, err := Of[Role]()
	if err != nil {
		t.Fatalf("Of[Role]: %v", err)
	}
	if m.Table != "roles" {
		t.Errorf("Table = %q, want roles", m.Table)
	}
	// Of and Get share the cache.
	mg, _ := Get(reflect.TypeOf(Role{}))
	if m != mg {
		t.Error("Of and Get returned different cached entries")
	}
}

func TestNullableDefaultNullTag(t *testing.T) {
	type WithDefaultNull struct {
		Base
		Note string `json:"note" gorm:"default:null"`
	}
	m, _ := Parse(reflect.TypeOf(WithDefaultNull{}))
	f, ok := m.Field("note")
	if !ok {
		t.Fatal("note field missing")
	}
	if !f.Nullable {
		t.Error("gorm:\"default:null\" field should be Nullable")
	}
}

func TestBytesKind(t *testing.T) {
	type WithBytes struct {
		Base
		Payload []byte `json:"payload"`
	}
	m, _ := Parse(reflect.TypeOf(WithBytes{}))
	f, ok := m.Field("payload")
	if !ok {
		t.Fatal("payload field missing")
	}
	if f.Kind != KindBytes {
		t.Errorf("Kind = %d, want KindBytes", f.Kind)
	}
}

func TestJSONNameFallback(t *testing.T) {
	type NoJSONTag struct {
		Base
		FullName string // no json tag -> lowercased field name "fullname"
	}
	m, _ := Parse(reflect.TypeOf(NoJSONTag{}))
	if _, ok := m.Field("fullname"); !ok {
		t.Error("field without json tag should fall back to lowercased Go name")
	}
}
