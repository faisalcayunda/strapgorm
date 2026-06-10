package gormschema

import (
	"database/sql"
	"reflect"
	"strings"
	"time"

	"github.com/faisalcayunda/strapgorm/schema"
)

// deref removes pointer indirection from t, returning nil for a nil input type.
func deref(t reflect.Type) reflect.Type {
	if t == nil {
		return nil
	}
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	return t
}

// snake converts a Go identifier to snake_case using the shared schema helper,
// keeping relation Name derivation consistent with the reflection adapter.
func snake(s string) string { return schema.ToSnakeCase(s) }

// lower lowercases s.
func lower(s string) string { return strings.ToLower(s) }

// trimSpace trims leading and trailing whitespace from s.
func trimSpace(s string) string { return strings.TrimSpace(s) }

// indexByte returns the index of the first occurrence of b in s, or -1.
func indexByte(s string, b byte) int { return strings.IndexByte(s, b) }

// isSQLNull reports whether t is one of the sql.Null* wrapper types, which are
// treated as nullable scalar columns.
func isSQLNull(t reflect.Type) bool {
	switch t {
	case nullTimeType, nullStringType, nullBoolType,
		nullInt64Type, nullInt32Type, nullInt16Type, nullFloat64Type:
		return true
	}
	return false
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
