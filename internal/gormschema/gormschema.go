// Package gormschema adapts GORM's own schema introspection into the
// strapgorm schema.Model whitelist, giving the facade runtime-accurate
// metadata (table names, column names, relation keys, and join tables) that
// honors the live *gorm.DB NamingStrategy and association resolution.
//
// # Why a GORM-backed adapter
//
// The strapgorm/schema package introspects a struct with reflection alone. That
// is sufficient offline and for the common GORM conventions, but it cannot
// reproduce a custom NamingStrategy, association keys inferred from the live
// configuration, or the exact join-table name GORM computes for a many2many
// relation. This package closes that gap: it asks GORM to parse the model and
// then maps GORM's resolved schema onto schema.Model so that every identifier
// strapgorm later emits into SQL is the identifier GORM itself would use.
//
// # Security
//
// The produced schema.Model is the security whitelist consumed by the builder
// layer. Only column and table identifiers taken from GORM's parsed schema are
// recorded; nothing here originates from a query string. Field, relation, and
// operator tokens from untrusted input are validated against this whitelist by
// the filter/sort/fields/search packages, never interpolated.
package gormschema

import (
	"fmt"
	"reflect"
	"sync"

	"github.com/faisalcayunda/strapgorm/schema"

	"gorm.io/gorm"
	gormschema "gorm.io/gorm/schema"
)

// cache memoizes the derived *schema.Model per concrete Go type so that the
// (reflection- and GORM-parse-heavy) mapping runs at most once per model in a
// process. It is keyed by the dereferenced reflect.Type and is safe for
// concurrent use.
var cache sync.Map // map[reflect.Type]*schema.Model

// parseStore is the cache store GORM's schema.Parse requires. GORM uses it to
// memoize parsed *gormschema.Schema values and to break cycles between mutually
// referential models; sharing one store process-wide matches GORM's own usage.
var parseStore sync.Map

// From builds a strapgorm *schema.Model for model using GORM's schema parser so
// that the resulting whitelist matches the live database mapping exactly.
//
// model may be a struct, a pointer to a struct, or a pointer to a zero value
// (for example new(T)). db supplies the NamingStrategy used to resolve table and
// column names; when db or its NamingStrategy is nil, GORM's default
// NamingStrategy is used. The result is cached by the model's concrete type.
//
// An error is returned when model is nil or not a struct type, or when GORM's
// schema parser fails. On success the returned *schema.Model carries the same
// JSON-name/column/relation whitelist as the reflection-only schema.Parse, but
// with GORM-accurate identifiers.
func From(db *gorm.DB, model any) (*schema.Model, error) {
	if model == nil {
		return nil, fmt.Errorf("strapgorm/gormschema: nil model")
	}

	t := deref(reflect.TypeOf(model))
	if t == nil || t.Kind() != reflect.Struct {
		return nil, fmt.Errorf("strapgorm/gormschema: model must be a struct or pointer to struct, got %T", model)
	}

	if cached, ok := cache.Load(t); ok {
		m, ok := cached.(*schema.Model)
		if !ok {
			panic("strapgorm/gormschema: cache contains unexpected type")
		}
		return m, nil
	}

	namer := namerFor(db)
	gs, err := gormschema.Parse(model, &parseStore, namer)
	if err != nil {
		return nil, fmt.Errorf("strapgorm/gormschema: parsing %s: %w", t.Name(), err)
	}

	m := mapSchema(t, gs)

	actual, _ := cache.LoadOrStore(t, m)
	stored, ok := actual.(*schema.Model)
	if !ok {
		panic("strapgorm/gormschema: cache contains unexpected type")
	}
	return stored, nil
}

// namerFor returns the GORM Namer to drive table/column name resolution: the
// live db.NamingStrategy when available, otherwise GORM's default
// NamingStrategy so parsing still succeeds with conventional names.
func namerFor(db *gorm.DB) gormschema.Namer {
	if db != nil && db.NamingStrategy != nil {
		return db.NamingStrategy
	}
	return gormschema.NamingStrategy{}
}

// mapSchema converts a parsed *gormschema.Schema into a strapgorm *schema.Model,
// recording the scalar fields, relations, primary key, and searchable fields.
func mapSchema(t reflect.Type, gs *gormschema.Schema) *schema.Model {
	m := &schema.Model{
		Type:       t,
		Table:      gs.Table,
		PrimaryKey: primaryKeyOf(gs),
		Fields:     make(map[string]schema.Field),
		Relations:  make(map[string]schema.Relation),
	}

	for _, gf := range gs.Fields {
		// Skip association-only struct fields and any field GORM did not map to
		// a database column (DBName == "" means there is no scalar column).
		if gf.DBName == "" {
			continue
		}
		f := mapField(gf)
		m.Fields[f.JSON] = f
	}

	for _, rel := range gs.Relationships.Relations {
		r, ok := mapRelation(rel)
		if !ok {
			continue
		}
		m.Relations[r.Name] = r
		// Accept the lowercased Go field name as an alias too, matching the
		// reflection schema's dual keying so existing relation lookups work.
		if alias := lower(rel.Name); alias != r.Name {
			if _, exists := m.Relations[alias]; !exists {
				m.Relations[alias] = r
			}
		}
	}

	if s := searchableOf(t); s != nil {
		m.SearchableFields = s
	}

	return m
}

// mapField maps a single GORM scalar field onto a schema.Field, deriving the
// JSON key, column, Kind, Go type, and nullability.
func mapField(gf *gormschema.Field) schema.Field {
	ft := deref(gf.FieldType)
	kind := classify(ft)

	return schema.Field{
		JSON:     jsonNameOf(gf),
		Column:   gf.DBName,
		Kind:     kind,
		GoType:   ft,
		Nullable: nullableOf(gf),
	}
}

// mapRelation maps a GORM relationship onto a schema.Relation, returning false
// for relationships strapgorm does not model (none currently; the guard keeps
// the mapping defensive against future GORM relation kinds).
func mapRelation(rel *gormschema.Relationship) (schema.Relation, bool) {
	relType, ok := relationType(rel.Type)
	if !ok {
		return schema.Relation{}, false
	}

	out := schema.Relation{
		Name:        snake(rel.Name),
		GoField:     rel.Name,
		Type:        relType,
		OwnerKey:    "id",
		RelatedType: relatedType(rel),
	}
	if rel.FieldSchema != nil {
		out.Table = rel.FieldSchema.Table
	}
	if rel.JoinTable != nil {
		out.JoinTable = rel.JoinTable.Table
	}

	// References resolves the foreign-key and owner-key columns GORM inferred.
	// For BelongsTo the FK lives on this (owning) struct and the referenced key
	// is on the related side; for HasOne/HasMany the FK lives on the related
	// struct and the referenced key is on this struct. GORM's OwnPrimaryKey flag
	// distinguishes the two directions on each Reference.
	for _, ref := range rel.References {
		if ref.ForeignKey != nil {
			out.FKColumn = ref.ForeignKey.DBName
		}
		if ref.PrimaryKey != nil {
			out.OwnerKey = ref.PrimaryKey.DBName
		}
	}

	return out, true
}

// relationType maps GORM's relationship-type constant onto schema.RelationType.
func relationType(t gormschema.RelationshipType) (schema.RelationType, bool) {
	switch t {
	case gormschema.BelongsTo:
		return schema.BelongsTo, true
	case gormschema.HasOne:
		return schema.HasOne, true
	case gormschema.HasMany:
		return schema.HasMany, true
	case gormschema.Many2Many:
		return schema.Many2Many, true
	default:
		return 0, false
	}
}

// relatedType returns the related model's Go type with pointer indirection
// removed, or nil when GORM did not record a field schema for the relation.
func relatedType(rel *gormschema.Relationship) reflect.Type {
	if rel.FieldSchema == nil {
		return nil
	}
	return deref(rel.FieldSchema.ModelType)
}

// primaryKeyOf returns the prioritized primary-key column, defaulting to "id"
// when GORM did not designate one.
func primaryKeyOf(gs *gormschema.Schema) string {
	if gs.PrioritizedPrimaryField != nil && gs.PrioritizedPrimaryField.DBName != "" {
		return gs.PrioritizedPrimaryField.DBName
	}
	return "id"
}

// jsonNameOf returns the query/JSON key for a GORM field: the json struct-tag
// name when present and usable, otherwise the lowercased Go field name. This
// mirrors the reflection schema package so query-string keys resolve
// identically regardless of which adapter produced the model.
func jsonNameOf(gf *gormschema.Field) string {
	if tag, ok := gf.StructField.Tag.Lookup("json"); ok {
		name := tag
		if comma := indexByte(name, ','); comma >= 0 {
			name = name[:comma]
		}
		name = trimSpace(name)
		if name != "" && name != "-" {
			return name
		}
	}
	return lower(gf.Name)
}

// nullableOf reports whether a GORM field may hold NULL. A pointer field is
// always nullable; otherwise the column is nullable unless GORM marked it
// NotNull (via a not-null tag or primary-key designation).
func nullableOf(gf *gormschema.Field) bool {
	if gf.FieldType != nil && gf.FieldType.Kind() == reflect.Pointer {
		return true
	}
	if isSQLNull(deref(gf.FieldType)) {
		return true
	}
	return !gf.NotNull && !gf.PrimaryKey
}

// classify maps a (pointer-dereferenced) Go type onto a schema.Kind, falling
// back to KindOther for types strapgorm does not coerce specially.
func classify(t reflect.Type) schema.Kind {
	if t == nil {
		return schema.KindOther
	}
	if t == timeType || t == nullTimeType {
		return schema.KindTime
	}
	switch t {
	case nullStringType:
		return schema.KindString
	case nullBoolType:
		return schema.KindBool
	case nullInt64Type, nullInt32Type, nullInt16Type:
		return schema.KindInt
	case nullFloat64Type:
		return schema.KindFloat
	}

	switch t.Kind() {
	case reflect.String:
		return schema.KindString
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return schema.KindInt
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return schema.KindUint
	case reflect.Float32, reflect.Float64:
		return schema.KindFloat
	case reflect.Bool:
		return schema.KindBool
	case reflect.Slice:
		if t.Elem().Kind() == reflect.Uint8 {
			return schema.KindBytes
		}
		return schema.KindOther
	default:
		return schema.KindOther
	}
}

// searchableOf returns the searchable field list when t (or *t) implements
// schema.Searchable, otherwise nil.
func searchableOf(t reflect.Type) []string {
	if reflect.PointerTo(t).Implements(searchableType) {
		v := reflect.New(t)
		s, ok := v.Interface().(schema.Searchable)
		if !ok {
			panic("strapgorm/gormschema: Implements check guarantees Searchable")
		}
		return s.Searchable()
	}
	if t.Implements(searchableType) {
		v := reflect.Zero(t)
		s, ok := v.Interface().(schema.Searchable)
		if !ok {
			panic("strapgorm/gormschema: Implements check guarantees Searchable")
		}
		return s.Searchable()
	}
	return nil
}

var searchableType = reflect.TypeOf((*schema.Searchable)(nil)).Elem()
