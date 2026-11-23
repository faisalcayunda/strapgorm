package sort

import (
	"errors"
	"strings"
	"testing"

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

func buildHiddenSort(t *testing.T, raw ...string) ([]string, error) {
	t.Helper()
	m := mustModel(t, hiddenRow{})
	return Build(m, raw, joins.NewRegistry(m.Table))
}

func TestBuildHidden(t *testing.T) {
	tests := []struct {
		name    string
		raw     []string
		wantErr error
		blocked string // the token named in the error
	}{
		{"hidden field", []string{"ip"}, schema.ErrUnknownField, "ip"},
		{"hidden field with direction", []string{"ip:desc"}, schema.ErrUnknownField, "ip"},
		{"hidden field second", []string{"name:asc", "ip:desc"}, schema.ErrUnknownField, "ip"},
		{"hidden field promoted from an embedded struct", []string{"subject"}, schema.ErrUnknownField, "subject"},
		{"hidden field of a relation", []string{"child.last_ip:desc"}, schema.ErrUnknownField, "last_ip"},
		{"hidden relation", []string{"vault.label"}, schema.ErrUnknownRelation, "vault"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := buildHiddenSort(t, tt.raw...)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if want := `"` + tt.blocked + `"`; !strings.Contains(err.Error(), want) {
				t.Errorf("err = %q, want it to name %s", err, want)
			}
			if got != nil {
				t.Errorf("orders = %v, want nil on error", got)
			}
		})
	}
}

func TestBuildHiddenAllowsVisible(t *testing.T) {
	got, err := buildHiddenSort(t, "name:asc", "child.name:desc", "password", "child.hash")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	want := []string{
		"hidden_rows.name ASC",
		"hidden_childs.name DESC",
		"hidden_rows.password ASC",
		"hidden_childs.hash ASC",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("orders = %v, want %v", got, want)
	}
}

// A hidden field answers with the same wording as a field the model does not have.
func TestBuildHiddenMatchesUnknown(t *testing.T) {
	pairs := []struct {
		name            string
		hidden, unknown string
	}{
		{"root field", "ip", "nope"},
		{"relation field", "child.last_ip", "child.nope"},
		{"relation", "vault.label", "nope.label"},
	}
	for _, p := range pairs {
		t.Run(p.name, func(t *testing.T) {
			_, hiddenErr := buildHiddenSort(t, p.hidden)
			_, unknownErr := buildHiddenSort(t, p.unknown)
			if hiddenErr == nil || unknownErr == nil {
				t.Fatalf("hidden err = %v, unknown err = %v, want both set", hiddenErr, unknownErr)
			}
			norm := func(err error) string {
				s := err.Error()
				for _, tok := range []string{"last_ip", "ip", "vault", "nope"} {
					s = strings.ReplaceAll(s, `"`+tok+`"`, `"X"`)
				}
				return s
			}
			if norm(hiddenErr) != norm(unknownErr) {
				t.Errorf("hidden err = %q, unknown err = %q, want the same wording", hiddenErr, unknownErr)
			}
		})
	}
}
