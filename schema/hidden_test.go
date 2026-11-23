package schema

import (
	"reflect"
	"testing"
)

// --- Hidden fixtures -----------------------------------------------------

type hiddenSecret struct {
	ID    uint   `json:"id" gorm:"primaryKey"`
	Token string `json:"-"`
	Label string `json:"label"`
}

type hiddenChild struct {
	ID     uint   `json:"id" gorm:"primaryKey"`
	Name   string `json:"name"`
	LastIP string `json:"last_ip" strapgorm:"hidden"`
}

type hiddenBase struct {
	Subject *string `json:"-" strapgorm:"hidden"`
}

type hiddenRow struct {
	hiddenBase
	ID       uint          `json:"id" gorm:"primaryKey"`
	Name     string        `json:"name"`
	IP       string        `json:"ip,omitempty" strapgorm:"hidden"`
	Password string        `json:"-"` // json:"-" without the tag keeps today's behavior
	Spaced   string        `json:"spaced" strapgorm:"other, hidden"`
	NotMine  string        `json:"not_mine" strapgorm:"hiddenish"`
	Child    *hiddenChild  `json:"child"`
	Vault    *hiddenSecret `json:"-" strapgorm:"hidden"`
}

func TestHiddenFlagOnFields(t *testing.T) {
	m, err := Parse(reflect.TypeOf(hiddenRow{}))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	tests := []struct {
		json   string
		hidden bool
	}{
		{"ip", true},
		{"subject", true}, // promoted from an embedded struct; json:"-" falls back to the lowercased Go name
		{"spaced", true},  // option list tolerates other options and spaces
		{"password", false},
		{"not_mine", false}, // "hiddenish" is not the hidden option
		{"name", false},
		{"id", false},
	}
	for _, tt := range tests {
		f, ok := m.Field(tt.json)
		if !ok {
			t.Errorf("field %q missing from the model", tt.json)
			continue
		}
		if f.Hidden != tt.hidden {
			t.Errorf("Field(%q).Hidden = %v, want %v", tt.json, f.Hidden, tt.hidden)
		}
	}
}

func TestHiddenFlagOnRelations(t *testing.T) {
	m, err := Parse(reflect.TypeOf(hiddenRow{}))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	vault, ok := m.Relation("vault")
	if !ok || !vault.Hidden {
		t.Errorf("Relation(vault) = %+v, %v, want a hidden relation", vault, ok)
	}
	child, ok := m.Relation("child")
	if !ok || child.Hidden {
		t.Errorf("Relation(child) = %+v, %v, want a visible relation", child, ok)
	}
	related, err := Get(child.RelatedType)
	if err != nil {
		t.Fatalf("Get(child): %v", err)
	}
	if f, _ := related.Field("last_ip"); !f.Hidden {
		t.Error("hiddenChild.last_ip should be hidden")
	}
}

// A hidden field stays in the model: fields and search resolve it through the
// whitelist, so hiding only changes what filter and sort accept.
func TestHiddenFieldStaysResolvable(t *testing.T) {
	m, err := Parse(reflect.TypeOf(hiddenRow{}))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if col, ok := m.ColumnFor("ip"); !ok || col != "ip" {
		t.Errorf("ColumnFor(ip) = %q, %v, want ip, true", col, ok)
	}
}

func TestIsHidden(t *testing.T) {
	tests := []struct {
		tag  reflect.StructTag
		want bool
	}{
		{`strapgorm:"hidden"`, true},
		{`json:"x" strapgorm:"hidden"`, true},
		{`strapgorm:"a,hidden"`, true},
		{`strapgorm:" hidden "`, true},
		{`strapgorm:"hiddenish"`, false},
		{`strapgorm:""`, false},
		{`apiquery:"hidden"`, false},
		{`json:"-"`, false},
		{``, false},
	}
	for _, tt := range tests {
		if got := IsHidden(tt.tag); got != tt.want {
			t.Errorf("IsHidden(%q) = %v, want %v", tt.tag, got, tt.want)
		}
	}
}
