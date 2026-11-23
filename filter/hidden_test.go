package filter

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/faisalcayunda/strapgorm/dialect"
	"github.com/faisalcayunda/strapgorm/internal/joins"
	"github.com/faisalcayunda/strapgorm/schema"
)

type hiddenSecret struct {
	ID    uint   `json:"id" gorm:"primaryKey"`
	Token string `json:"-"`
	Label string `json:"label"`
}

type hiddenChild struct {
	ID     uint   `json:"id" gorm:"primaryKey"`
	Name   string `json:"name"`
	LastIP string `json:"last_ip" strapgorm:"hidden"`
	Hash   string `json:"-"` // untagged json:"-" keeps today's behavior
}

type hiddenBase struct {
	Subject *string `json:"-" strapgorm:"hidden"`
}

type hiddenRow struct {
	hiddenBase
	ID       uint          `json:"id" gorm:"primaryKey"`
	Name     string        `json:"name"`
	IP       string        `json:"ip,omitempty" strapgorm:"hidden"`
	Password string        `json:"-"` // untagged json:"-" keeps today's behavior
	Child    *hiddenChild  `json:"child"`
	Vault    *hiddenSecret `json:"-" strapgorm:"hidden"`
}

func buildHidden(t *testing.T, raw map[string]any) (Clause, error) {
	t.Helper()
	m, err := schema.Get(reflect.TypeOf(hiddenRow{}))
	if err != nil {
		t.Fatalf("schema.Get(hiddenRow): %v", err)
	}
	return Build(m, raw, dialect.Postgres, Default(), joins.NewRegistry(m.Table))
}

func eq(v string) map[string]any { return map[string]any{"$eq": v} }

// A field or relation tagged strapgorm:"hidden" is refused wherever it hides: a
// top-level key, an $and/$or group, an embedded struct, a relation reached by
// dotted path or by nested object, or a hidden relation. Visible fields pass.
func TestBuildHidden(t *testing.T) {
	tests := []struct {
		name    string
		raw     map[string]any
		blocked string // the key named in the error; "" means the filter is allowed
	}{
		{"hidden field", map[string]any{"ip": eq("10.0.0.1")}, "ip"},
		{"hidden field, other operator", map[string]any{"ip": map[string]any{"$startsWith": "10."}}, "ip"},
		{"hidden field in $or", map[string]any{"$or": []any{
			map[string]any{"name": eq("a")},
			map[string]any{"ip": eq("10.0.0.1")},
		}}, "ip"},
		{"hidden field in $and inside $or", map[string]any{"$or": []any{
			map[string]any{"$and": []any{map[string]any{"ip": map[string]any{"$null": "true"}}}},
		}}, "ip"},
		{"hidden field promoted from an embedded struct", map[string]any{"subject": eq("x")}, "subject"},
		{"hidden field of a relation, dotted", map[string]any{"child.last_ip": eq("1")}, "last_ip"},
		{"hidden field of a relation, nested object", map[string]any{"child": map[string]any{"last_ip": eq("1")}}, "last_ip"},
		{"hidden relation", map[string]any{"vault": map[string]any{"label": eq("x")}}, "vault"},
		{"hidden relation, dotted", map[string]any{"vault.label": eq("x")}, "vault.label"},

		{"visible field", map[string]any{"name": eq("a")}, ""},
		{"visible relation field, dotted", map[string]any{"child.name": eq("a")}, ""},
		{"visible relation field, nested object", map[string]any{"child": map[string]any{"name": eq("a")}}, ""},
		{"untagged json:\"-\" field keeps today's behavior", map[string]any{"password": eq("x")}, ""},
		{"untagged json:\"-\" field of a relation keeps today's behavior", map[string]any{"child.hash": eq("x")}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := buildHidden(t, tt.raw)
			if tt.blocked == "" {
				if err != nil {
					t.Fatalf("Build: %v, want the filter allowed", err)
				}
				return
			}
			if !errors.Is(err, schema.ErrUnknownField) {
				t.Fatalf("err = %v, want ErrUnknownField for %q", err, tt.blocked)
			}
			if want := `: "` + tt.blocked + `"`; !strings.HasSuffix(err.Error(), want) {
				t.Errorf("err = %q, want it to end with %q", err, want)
			}
		})
	}
}

// A hidden field must answer with the same error, word for word, as a field the
// model does not have, so a caller cannot tell the two apart.
func TestBuildHiddenMatchesUnknown(t *testing.T) {
	pairs := []struct {
		name            string
		hidden, unknown map[string]any
	}{
		{"root field", map[string]any{"ip": eq("1")}, map[string]any{"nope": eq("1")}},
		{"relation field, dotted", map[string]any{"child.last_ip": eq("1")}, map[string]any{"child.nope": eq("1")}},
		{"relation field, nested object",
			map[string]any{"child": map[string]any{"last_ip": eq("1")}},
			map[string]any{"child": map[string]any{"nope": eq("1")}}},
		{"relation", map[string]any{"vault.label": eq("1")}, map[string]any{"nope.label": eq("1")}},
	}
	for _, p := range pairs {
		t.Run(p.name, func(t *testing.T) {
			_, hiddenErr := buildHidden(t, p.hidden)
			_, unknownErr := buildHidden(t, p.unknown)
			if hiddenErr == nil || unknownErr == nil {
				t.Fatalf("hidden err = %v, unknown err = %v, want both set", hiddenErr, unknownErr)
			}
			if !errors.Is(hiddenErr, schema.ErrUnknownField) || !errors.Is(unknownErr, schema.ErrUnknownField) {
				t.Fatalf("hidden err = %v, unknown err = %v, want both ErrUnknownField", hiddenErr, unknownErr)
			}
			// Mask the one differing token; everything else must match.
			norm := func(err error) string {
				s := err.Error()
				for _, tok := range []string{"last_ip", "ip", "vault", "nope"} {
					s = strings.ReplaceAll(s, `"`+tok, `"X`)
				}
				return s
			}
			if norm(hiddenErr) != norm(unknownErr) {
				t.Errorf("hidden err = %q, unknown err = %q, want the same wording", hiddenErr, unknownErr)
			}
		})
	}
}
