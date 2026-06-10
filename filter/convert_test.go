package filter

import (
	"reflect"
	"testing"
	"time"

	"github.com/faisalcayunda/strapgorm/schema"
)

func TestToIntBranches(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want int64
		ok   bool
	}{
		{"int", 5, 5, true},
		{"int8", int8(5), 5, true},
		{"int16", int16(5), 5, true},
		{"int32", int32(5), 5, true},
		{"int64", int64(5), 5, true},
		{"float32", float32(5), 5, true},
		{"float64", float64(5.9), 5, true},
		{"string", "42", 42, true},
		{"bad string", "x", 0, false},
		{"unsupported", []byte("x"), 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := toInt(tt.in)
			if tt.ok && (err != nil || got != tt.want) {
				t.Errorf("toInt(%v) = %d, %v", tt.in, got, err)
			}
			if !tt.ok && err == nil {
				t.Errorf("toInt(%v): expected error", tt.in)
			}
		})
	}
}

func TestToUintBranches(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want uint64
		ok   bool
	}{
		{"uint", uint(5), 5, true},
		{"uint8", uint8(5), 5, true},
		{"uint16", uint16(5), 5, true},
		{"uint32", uint32(5), 5, true},
		{"uint64", uint64(5), 5, true},
		{"int", 5, 5, true},
		{"int negative", -1, 0, false},
		{"int64", int64(5), 5, true},
		{"int64 negative", int64(-1), 0, false},
		{"float64", float64(5), 5, true},
		{"float64 negative", float64(-1), 0, false},
		{"string", "9", 9, true},
		{"bad string", "x", 0, false},
		{"unsupported", []byte("x"), 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := toUint(tt.in)
			if tt.ok && (err != nil || got != tt.want) {
				t.Errorf("toUint(%v) = %d, %v", tt.in, got, err)
			}
			if !tt.ok && err == nil {
				t.Errorf("toUint(%v): expected error", tt.in)
			}
		})
	}
}

func TestToFloatBranches(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want float64
		ok   bool
	}{
		{"float32", float32(1.5), 1.5, true},
		{"float64", float64(1.5), 1.5, true},
		{"int", 3, 3, true},
		{"int64", int64(3), 3, true},
		{"string", "2.5", 2.5, true},
		{"bad string", "x", 0, false},
		{"unsupported", true, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := toFloat(tt.in)
			if tt.ok && (err != nil || got != tt.want) {
				t.Errorf("toFloat(%v) = %v, %v", tt.in, got, err)
			}
			if !tt.ok && err == nil {
				t.Errorf("toFloat(%v): expected error", tt.in)
			}
		})
	}
}

func TestToBoolBranches(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want bool
		ok   bool
	}{
		{"bool true", true, true, true},
		{"bool false", false, false, true},
		{"string true", "TRUE", true, true},
		{"string 1", "1", true, true},
		{"string false", "false", false, true},
		{"string 0", "0", false, true},
		{"bad string", "maybe", false, false},
		{"int nonzero", 2, true, true},
		{"int zero", 0, false, true},
		{"int64 nonzero", int64(3), true, true},
		{"float nonzero", float64(1), true, true},
		{"unsupported", []byte("x"), false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := toBool(tt.in)
			if tt.ok && (err != nil || got != tt.want) {
				t.Errorf("toBool(%v) = %v, %v", tt.in, got, err)
			}
			if !tt.ok && err == nil {
				t.Errorf("toBool(%v): expected error", tt.in)
			}
		})
	}
}

func TestToTimeBranches(t *testing.T) {
	now := time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)

	if got, err := toTime(now); err != nil || !got.(time.Time).Equal(now) {
		t.Errorf("toTime(time.Time) = %v, %v", got, err)
	}
	if got, err := toTime(int(1700000000)); err != nil {
		t.Errorf("toTime(int) error: %v", err)
	} else if !got.(time.Time).Equal(time.Unix(1700000000, 0).UTC()) {
		t.Errorf("toTime(int) = %v", got)
	}
	if got, err := toTime(int64(1700000000)); err != nil {
		t.Errorf("toTime(int64) error: %v", err)
	} else if !got.(time.Time).Equal(time.Unix(1700000000, 0).UTC()) {
		t.Errorf("toTime(int64) = %v", got)
	}
	if got, err := toTime(float64(1700000000)); err != nil {
		t.Errorf("toTime(float64) error: %v", err)
	} else if !got.(time.Time).Equal(time.Unix(1700000000, 0).UTC()) {
		t.Errorf("toTime(float64) = %v", got)
	}
	if _, err := toTime([]byte("x")); err == nil {
		t.Error("toTime([]byte) expected error")
	}
	if _, err := toTime("not-a-time"); err == nil {
		t.Error("toTime(bad string) expected error")
	}
}

func TestToStringBranches(t *testing.T) {
	if got, err := toString("x"); err != nil || got != "x" {
		t.Errorf("toString(string) = %q, %v", got, err)
	}
	if got, err := toString(42); err != nil || got != "42" {
		t.Errorf("toString(int) = %q, %v", got, err)
	}
	// fmt.Stringer branch.
	if got, err := toString(stringer{}); err != nil || got != "STR" {
		t.Errorf("toString(Stringer) = %q, %v", got, err)
	}
	if _, err := toString(nil); err == nil {
		t.Error("toString(nil) expected error")
	}
}

type stringer struct{}

func (stringer) String() string { return "STR" }

func TestToListBranches(t *testing.T) {
	if got, _ := toList([]any{1, 2}); !reflect.DeepEqual(got, []any{1, 2}) {
		t.Errorf("toList([]any) = %#v", got)
	}
	if got, _ := toList([]string{"a", "b"}); !reflect.DeepEqual(got, []any{"a", "b"}) {
		t.Errorf("toList([]string) = %#v", got)
	}
	if got, _ := toList(" a , b ,c"); !reflect.DeepEqual(got, []any{"a", "b", "c"}) {
		t.Errorf("toList(csv) = %#v", got)
	}
	if got, _ := toList(7); !reflect.DeepEqual(got, []any{7}) {
		t.Errorf("toList(scalar) = %#v", got)
	}
}

func TestSingularBranches(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"users", "user"},
		{"companies", "company"},
		{"boxes", "box"},
		{"buses", "bus"},
		{"quizzes", "quizz"},
		{"matches", "match"},
		{"dishes", "dish"},
		{"data", "data"},
	}
	for _, tt := range tests {
		if got := singular(tt.in); got != tt.want {
			t.Errorf("singular(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestCoerceScalarKinds(t *testing.T) {
	// KindOther and KindBytes fall through to string coercion.
	for _, k := range []schema.Kind{schema.KindOther, schema.KindBytes} {
		got, err := coerceScalar(schema.Field{Kind: k}, 5)
		if err != nil || got != "5" {
			t.Errorf("coerceScalar(kind %v) = %v, %v", k, got, err)
		}
	}
	// An out-of-range kind hits the default passthrough.
	if got, err := coerceScalar(schema.Field{Kind: schema.Kind(0xFE)}, 5); err != nil || got != 5 {
		t.Errorf("coerceScalar(unknown) = %v, %v", got, err)
	}
}
