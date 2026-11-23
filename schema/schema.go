// Package schema introspects Go structs into the metadata strapgorm uses to
// translate Strapi-style query strings into safe SQL.
//
// The metadata captured for a model is the security whitelist for that model:
// every field, relation, and column name that strapgorm will accept from an
// untrusted query string is derived here from struct reflection and GORM
// struct tags. Tokens that do not resolve against this metadata are rejected
// with ErrUnknownField, ErrUnknownRelation, or ErrUnknownOperator rather than
// being interpolated into SQL.
//
// # Fidelity
//
// Parse mirrors GORM's default NamingStrategy on a best-effort basis: table
// names are the pluralized snake_case of the struct name and column names are
// the snake_case of the field name unless overridden by a gorm "column" tag.
// This reflection-only view cannot perfectly reproduce every GORM convention
// (notably custom NamingStrategy configurations and association keys inferred
// from the live *gorm.DB). Authoritative runtime fidelity is expected to come
// from a GORM-backed adapter layered on top of this package; the pluralizer
// and relation inference here are documented best-effort fallbacks.
package schema

import (
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"sync"
	"time"
)

// Sentinel errors returned when an untrusted token cannot be resolved against a
// model's whitelist. ErrUnknownOperator is declared here (rather than in the
// filter package) so that filter can reuse it without importing a heavier
// dependency.
var (
	// ErrUnknownField indicates a field name that is not part of the model.
	ErrUnknownField = errors.New("strapgorm/schema: unknown field")
	// ErrUnknownRelation indicates a relation name that is not part of the model.
	ErrUnknownRelation = errors.New("strapgorm/schema: unknown relation")
	// ErrUnknownOperator indicates a filter operator that is not registered.
	ErrUnknownOperator = errors.New("strapgorm/schema: unknown operator")
)

// Kind is a coarse classification of a field's Go type, used to drive
// query-string value coercion without re-running reflection on every value.
type Kind uint8

const (
	// KindString classifies string-kinded fields.
	KindString Kind = iota
	// KindInt classifies signed-integer fields.
	KindInt
	// KindUint classifies unsigned-integer fields.
	KindUint
	// KindFloat classifies floating-point fields.
	KindFloat
	// KindBool classifies boolean fields.
	KindBool
	// KindTime classifies time.Time (and sql.NullTime) fields.
	KindTime
	// KindBytes classifies []byte fields.
	KindBytes
	// KindOther classifies any field that does not fit the categories above.
	KindOther
)

// Field is the resolved metadata for a single scalar column of a model.
type Field struct {
	// JSON is the key used in query strings and JSON payloads: the json struct
	// tag name, or the lowercased Go field name when no json tag is present.
	JSON string
	// Column is the database column name: the gorm "column" tag when present,
	// otherwise the snake_case of the Go field name.
	Column string
	// Kind is the coarse type classification used for value coercion.
	Kind Kind
	// GoType is the field's Go type (with any pointer indirection removed).
	GoType reflect.Type
	// Nullable reports whether the column may hold NULL: true for pointer
	// fields, sql.Null* wrapper types, or a gorm tag carrying "default:null".
	Nullable bool
	// Hidden reports whether the field carries the `strapgorm:"hidden"` tag.
	// filter and sort answer a hidden field with ErrUnknownField, exactly as
	// for a field the model does not have. fields and search still resolve it.
	Hidden bool
}

// RelationType classifies the cardinality and ownership of a relation.
type RelationType uint8

const (
	// BelongsTo is a to-one relation whose foreign key lives on this struct.
	BelongsTo RelationType = iota
	// HasOne is a to-one relation whose foreign key lives on the related struct.
	HasOne
	// HasMany is a to-many relation whose foreign key lives on the related struct.
	HasMany
	// Many2Many is a to-many relation resolved through a join table.
	Many2Many
)

// Relation is the resolved metadata for a single association of a model.
type Relation struct {
	// Name is the dot-notation key used in query strings: the snake_case of the
	// Go field name. The lowercased Go field name is also accepted as an alias
	// (see Model.Relation).
	Name string
	// GoField is the exact Go field name, used as the GORM Preload association
	// name.
	GoField string
	// Type is the relation cardinality/ownership classification.
	Type RelationType
	// Table is the related model's table name.
	Table string
	// FKColumn is the foreign-key column. For BelongsTo it lives on this
	// struct; for HasOne/HasMany it lives on the related table. Best-effort.
	FKColumn string
	// OwnerKey is the referenced key on the owning side (default "id").
	OwnerKey string
	// JoinTable is the join table name for Many2Many relations; empty otherwise.
	JoinTable string
	// RelatedType is the related struct's Go type (pointer indirection removed).
	RelatedType reflect.Type
	// Hidden reports whether the relation carries the `strapgorm:"hidden"` tag.
	// filter and sort answer a hidden relation with the error they give a
	// relation the model does not have. populate still resolves it.
	Hidden bool
}

// Model is the complete introspected metadata for a struct type.
type Model struct {
	// Type is the struct's Go type.
	Type reflect.Type
	// Table is the resolved table name.
	Table string
	// PrimaryKey is the primary-key column (default "id").
	PrimaryKey string
	// Fields maps JSON name to Field.
	Fields map[string]Field
	// Relations maps relation Name (and lowercased Go field alias) to Relation.
	Relations map[string]Relation
	// SearchableFields lists JSON field names eligible for full-text-style
	// search, populated from the Searchable interface when implemented.
	SearchableFields []string
}

// Field returns the Field registered under the given JSON name.
func (m *Model) Field(jsonName string) (Field, bool) {
	f, ok := m.Fields[jsonName]
	return f, ok
}

// Relation returns the Relation registered under the given name (either the
// dot-notation snake_case name or the lowercased Go field alias).
func (m *Model) Relation(name string) (Relation, bool) {
	r, ok := m.Relations[name]
	return r, ok
}

// ColumnFor returns the database column for the given JSON field name.
func (m *Model) ColumnFor(jsonName string) (string, bool) {
	f, ok := m.Fields[jsonName]
	if !ok {
		return "", false
	}
	return f.Column, true
}

// Searchable is implemented by models that declare which fields participate in
// search. When a struct (or a pointer to it) implements Searchable, Parse
// populates Model.SearchableFields from the returned list.
type Searchable interface {
	// Searchable returns the JSON names of the searchable fields.
	Searchable() []string
}

// TagKey is the struct-tag key strapgorm reads for its own options.
const TagKey = "strapgorm"

// optionHidden is the TagKey option that removes a field or relation from the
// filter and sort whitelists.
const optionHidden = "hidden"

// IsHidden reports whether tag carries the hidden option, as in
// `strapgorm:"hidden"`. The value is a comma-separated option list, so
// future options can share the tag.
func IsHidden(tag reflect.StructTag) bool {
	for _, opt := range strings.Split(tag.Get(TagKey), ",") {
		if strings.TrimSpace(opt) == optionHidden {
			return true
		}
	}
	return false
}

var cache sync.Map // map[reflect.Type]*Model

// Of is a generic convenience wrapper around Get for the type parameter T.
func Of[T any]() (*Model, error) {
	var zero T
	return Get(reflect.TypeOf(zero))
}

// Get returns the cached Model for t, parsing and caching it on first use.
// The cache is keyed by t and is safe for concurrent use.
func Get(t reflect.Type) (*Model, error) {
	t = deref(t)
	if cached, ok := cache.Load(t); ok {
		m, ok := cached.(*Model)
		if !ok {
			panic("strapgorm/schema: cache contains unexpected type")
		}
		return m, nil
	}
	m, err := Parse(t)
	if err != nil {
		return nil, err
	}
	actual, _ := cache.LoadOrStore(t, m)
	stored, ok := actual.(*Model)
	if !ok {
		panic("strapgorm/schema: cache contains unexpected type")
	}
	return stored, nil
}

// Parse introspects t and returns its Model.
//
// Parse follows GORM's default conventions on a best-effort, reflection-only
// basis: it skips unexported fields and fields tagged gorm:"-", promotes the
// fields of embedded structs, honors the gorm column, primaryKey, foreignKey,
// references, and many2many tags, derives the table name as the pluralized
// snake_case of the struct name, and infers relations from struct, pointer-to-
// struct, and slice-of-struct fields. See the package documentation for the
// fidelity caveats.
func Parse(t reflect.Type) (*Model, error) {
	t = deref(t)
	if t == nil || t.Kind() != reflect.Struct {
		return nil, errors.New("strapgorm/schema: Parse requires a struct type")
	}

	m := &Model{
		Type:       t,
		Table:      TableName(t.Name()),
		PrimaryKey: "id",
		Fields:     make(map[string]Field),
		Relations:  make(map[string]Relation),
	}

	collectFields(t, m)

	if s := searchableOf(t); s != nil {
		m.SearchableFields = s
	}

	return m, nil
}

// collectFields walks the (possibly embedded) fields of t, recording scalar
// fields and relations into m.
func collectFields(t reflect.Type, m *Model) {
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)

		gormTag := parseGormTag(sf.Tag.Get("gorm"))
		if gormTag.ignored {
			continue
		}

		// Promote embedded struct fields (anonymous structs that are not
		// themselves columns or relations, e.g. gorm.Model).
		if sf.Anonymous {
			et := deref(sf.Type)
			if et != nil && et.Kind() == reflect.Struct && !isScalarStruct(et) {
				collectFields(et, m)
				continue
			}
		}

		if !sf.IsExported() {
			continue
		}

		if rel, ok := relationFor(t, sf, gormTag); ok {
			rel.Hidden = IsHidden(sf.Tag)
			m.Relations[rel.Name] = rel
			if alias := strings.ToLower(sf.Name); alias != rel.Name {
				if _, exists := m.Relations[alias]; !exists {
					m.Relations[alias] = rel
				}
			}
			continue
		}

		f, ok := scalarField(sf, gormTag)
		if !ok {
			continue
		}
		m.Fields[f.JSON] = f

		if gormTag.primaryKey {
			m.PrimaryKey = f.Column
		}
	}
}

// scalarField builds a Field for a scalar struct field, returning false when
// the field is not a recognizable scalar column.
func scalarField(sf reflect.StructField, tag gormTagInfo) (Field, bool) {
	ft := sf.Type
	nullable := false
	if ft.Kind() == reflect.Pointer {
		nullable = true
		ft = ft.Elem()
	}

	kind, ok := classify(ft)
	if !ok {
		return Field{}, false
	}

	if isSQLNull(ft) {
		nullable = true
	}
	if tag.defaultNull {
		nullable = true
	}

	column := tag.column
	if column == "" {
		column = ToSnakeCase(sf.Name)
	}

	return Field{
		JSON:     jsonName(sf),
		Column:   column,
		Kind:     kind,
		GoType:   ft,
		Nullable: nullable,
		Hidden:   IsHidden(sf.Tag),
	}, true
}

// relationFor builds a Relation for sf when it refers to another struct type
// (directly, via pointer, or via a slice), returning false otherwise.
func relationFor(owner reflect.Type, sf reflect.StructField, tag gormTagInfo) (Relation, bool) {
	ft := sf.Type

	// Slice-of-struct (or slice-of-pointer-to-struct) => HasMany / Many2Many.
	if ft.Kind() == reflect.Slice {
		et := deref(ft.Elem())
		if et == nil || et.Kind() != reflect.Struct || isScalarStruct(et) {
			return Relation{}, false
		}
		rel := Relation{
			Name:        ToSnakeCase(sf.Name),
			GoField:     sf.Name,
			Table:       TableName(et.Name()),
			OwnerKey:    "id",
			RelatedType: et,
		}
		if tag.many2many != "" {
			rel.Type = Many2Many
			rel.JoinTable = tag.many2many
		} else {
			rel.Type = HasMany
			rel.FKColumn = relationFK(tag, owner)
		}
		return rel, true
	}

	// Struct or pointer-to-struct => BelongsTo / HasOne.
	et := deref(ft)
	if et == nil || et.Kind() != reflect.Struct || isScalarStruct(et) {
		return Relation{}, false
	}

	rel := Relation{
		Name:        ToSnakeCase(sf.Name),
		GoField:     sf.Name,
		Table:       TableName(et.Name()),
		OwnerKey:    "id",
		RelatedType: et,
	}

	// BelongsTo when this struct carries a <Field>ID / <Field>_id FK column,
	// otherwise HasOne. An explicit foreignKey tag forces BelongsTo semantics.
	fk := tag.foreignKey
	if fk == "" {
		fk = sf.Name + "ID"
	}
	if tag.foreignKey != "" || ownerHasField(owner, fk) {
		rel.Type = BelongsTo
		rel.FKColumn = ToSnakeCase(fk)
	} else {
		rel.Type = HasOne
		rel.FKColumn = relationFK(tag, owner)
	}
	return rel, true
}

// relationFK derives the foreign-key column living on the related table for
// HasOne/HasMany relations: the explicit foreignKey tag if present, otherwise
// the owning struct name + "_id".
func relationFK(tag gormTagInfo, owner reflect.Type) string {
	if tag.foreignKey != "" {
		return ToSnakeCase(tag.foreignKey)
	}
	return ToSnakeCase(owner.Name()) + "_id"
}

// ownerHasField reports whether owner has an exported field whose name matches
// candidate case-insensitively (used to detect a BelongsTo FK such as
// "OrganizationID").
func ownerHasField(owner reflect.Type, candidate string) bool {
	lc := strings.ToLower(candidate)
	for i := 0; i < owner.NumField(); i++ {
		if strings.ToLower(owner.Field(i).Name) == lc {
			return true
		}
	}
	return false
}

// searchableOf returns the searchable field list if t (or *t) implements
// Searchable, otherwise nil.
func searchableOf(t reflect.Type) []string {
	if reflect.PointerTo(t).Implements(searchableType) {
		v := reflect.New(t)
		s, ok := v.Interface().(Searchable)
		if !ok {
			panic("strapgorm/schema: Implements check guarantees Searchable")
		}
		return s.Searchable()
	}
	if t.Implements(searchableType) {
		v := reflect.Zero(t)
		s, ok := v.Interface().(Searchable)
		if !ok {
			panic("strapgorm/schema: Implements check guarantees Searchable")
		}
		return s.Searchable()
	}
	return nil
}

var searchableType = reflect.TypeOf((*Searchable)(nil)).Elem()

// gormTagInfo holds the subset of gorm struct-tag directives Parse honors.
type gormTagInfo struct {
	column      string
	primaryKey  bool
	foreignKey  string
	references  string
	many2many   string
	defaultNull bool
	ignored     bool
}

// parseGormTag parses a gorm struct tag value into a gormTagInfo. Directive
// keys are matched case-insensitively, matching GORM's own behavior.
func parseGormTag(tag string) gormTagInfo {
	var info gormTagInfo
	if tag == "" {
		return info
	}
	for _, part := range strings.Split(tag, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if part == "-" {
			info.ignored = true
			continue
		}
		key, val, hasVal := strings.Cut(part, ":")
		key = strings.TrimSpace(strings.ToLower(key))
		val = strings.TrimSpace(val)
		switch key {
		case "column":
			info.column = val
		case "primarykey", "primary_key":
			info.primaryKey = true
		case "foreignkey":
			info.foreignKey = val
		case "references":
			info.references = val
		case "many2many":
			info.many2many = val
		case "default":
			if hasVal && strings.EqualFold(val, "null") {
				info.defaultNull = true
			}
		}
	}
	return info
}

// jsonName returns the query/JSON key for a field: the json tag name when set,
// otherwise the lowercased Go field name.
func jsonName(sf reflect.StructField) string {
	tag := sf.Tag.Get("json")
	if tag != "" {
		name, _, _ := strings.Cut(tag, ",")
		name = strings.TrimSpace(name)
		if name != "" && name != "-" {
			return name
		}
	}
	return strings.ToLower(sf.Name)
}

// classify maps a (pointer-dereferenced) Go type to a Kind, returning false for
// types that are not treated as scalar columns (e.g. maps, channels, funcs, or
// non-sql.Null structs).
func classify(t reflect.Type) (Kind, bool) {
	if t == timeType || t == nullTimeType {
		return KindTime, true
	}
	switch t {
	case nullStringType:
		return KindString, true
	case nullBoolType:
		return KindBool, true
	case nullInt64Type, nullInt32Type, nullInt16Type:
		return KindInt, true
	case nullFloat64Type:
		return KindFloat, true
	}

	switch t.Kind() {
	case reflect.String:
		return KindString, true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return KindInt, true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return KindUint, true
	case reflect.Float32, reflect.Float64:
		return KindFloat, true
	case reflect.Bool:
		return KindBool, true
	case reflect.Slice:
		if t.Elem().Kind() == reflect.Uint8 {
			return KindBytes, true
		}
		return KindOther, false
	default:
		return KindOther, false
	}
}

// isScalarStruct reports whether a struct type should be treated as a scalar
// column rather than a relation (time.Time and the sql.Null* wrappers).
func isScalarStruct(t reflect.Type) bool {
	switch t {
	case timeType, nullTimeType, nullStringType, nullBoolType,
		nullInt64Type, nullInt32Type, nullInt16Type, nullFloat64Type:
		return true
	}
	return false
}

// isSQLNull reports whether t is one of the sql.Null* wrapper types.
func isSQLNull(t reflect.Type) bool {
	switch t {
	case nullTimeType, nullStringType, nullBoolType,
		nullInt64Type, nullInt32Type, nullInt16Type, nullFloat64Type:
		return true
	}
	return false
}

// deref removes a single level of pointer indirection, returning nil for a nil
// input type.
func deref(t reflect.Type) reflect.Type {
	if t == nil {
		return nil
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t
}

var (
	timeType        = reflect.TypeOf(time.Time{})
	nullTimeType    = reflect.TypeOf(sql.NullTime{})
	nullStringType  = reflect.TypeOf(sql.NullString{})
	nullBoolType    = reflect.TypeOf(sql.NullBool{})
	nullInt64Type   = reflect.TypeOf(sql.NullInt64{})
	nullInt32Type   = reflect.TypeOf(sql.NullInt32{})
	nullInt16Type   = reflect.TypeOf(sql.NullInt16{})
	nullFloat64Type = reflect.TypeOf(sql.NullFloat64{})
)
