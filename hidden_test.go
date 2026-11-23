package strapgorm

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/faisalcayunda/strapgorm/schema"
)

// Account carries the three flavours of hidden column through the GORM-backed
// root schema: a tagged field, an untagged json:"-" field, and a tagged relation.
type Account struct {
	auditBase
	ID       uint     `json:"id" gorm:"primaryKey"`
	Name     string   `json:"name"`
	LastIP   string   `json:"last_ip,omitempty" gorm:"column:last_login_ip" strapgorm:"hidden"`
	Password string   `json:"-"`
	OwnerID  uint     `json:"owner_id"`
	Owner    *Profile `json:"owner" strapgorm:"hidden"`
	PeerID   uint     `json:"peer_id"`
	Peer     *Profile `json:"peer"`
}

// auditBase is embedded: its tagged field is promoted onto Account.
type auditBase struct {
	Subject string `json:"-" strapgorm:"hidden"`
}

func (Account) Searchable() []string { return []string{"name", "last_ip"} }

type Profile struct {
	ID    uint   `json:"id" gorm:"primaryKey"`
	Label string `json:"label"`
	Email string `json:"email" strapgorm:"hidden"`
}

func TestHiddenFilterAndSortMatchUnknown(t *testing.T) {
	db := newDryRunDB(t)
	ctx := context.Background()

	run := func(filters map[string]any, sort []string) error {
		b := New[Account](db)
		if filters != nil {
			b = b.Filters(filters)
		}
		if sort != nil {
			b = b.Sort(sort)
		}
		_, _, err := b.ToSQL(ctx, defaultPage())
		return err
	}
	eq := map[string]any{"$eq": "x"}

	cases := []struct {
		name            string
		hidden, unknown func() error
	}{
		{"filter embedded field",
			func() error { return run(map[string]any{"subject": eq}, nil) },
			func() error { return run(map[string]any{"nope": eq}, nil) }},
		{"filter field",
			func() error { return run(map[string]any{"last_ip": eq}, nil) },
			func() error { return run(map[string]any{"nope": eq}, nil) }},
		{"sort field",
			func() error { return run(nil, []string{"last_ip"}) },
			func() error { return run(nil, []string{"nope"}) }},
		{"filter relation field",
			func() error { return run(map[string]any{"peer.email": eq}, nil) },
			func() error { return run(map[string]any{"peer.nope": eq}, nil) }},
		{"sort relation field",
			func() error { return run(nil, []string{"peer.email"}) },
			func() error { return run(nil, []string{"peer.nope"}) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hiddenErr, unknownErr := tc.hidden(), tc.unknown()
			if !errors.Is(hiddenErr, schema.ErrUnknownField) {
				t.Fatalf("hidden err = %v, want ErrUnknownField", hiddenErr)
			}
			norm := func(err error) string {
				return strings.NewReplacer(`"last_ip"`, `"X"`, `"subject"`, `"X"`, `"email"`, `"X"`, `"nope"`, `"X"`).Replace(err.Error())
			}
			if norm(hiddenErr) != norm(unknownErr) {
				t.Errorf("hidden err = %q, unknown err = %q, want the same wording", hiddenErr, unknownErr)
			}
		})
	}

	// A hidden relation answers exactly like a relation that does not exist.
	for name, pair := range map[string][2]error{
		"filter": {run(map[string]any{"owner.label": eq}, nil), run(map[string]any{"nope.label": eq}, nil)},
		"sort":   {run(nil, []string{"owner.label"}), run(nil, []string{"nope.label"})},
	} {
		hiddenErr, unknownErr := pair[0], pair[1]
		if hiddenErr == nil || unknownErr == nil {
			t.Fatalf("hidden relation in %s: hidden err = %v, unknown err = %v, want both set", name, hiddenErr, unknownErr)
		}
		for _, sentinel := range []error{schema.ErrUnknownField, schema.ErrUnknownRelation} {
			if errors.Is(hiddenErr, sentinel) != errors.Is(unknownErr, sentinel) {
				t.Errorf("hidden relation in %s: hidden err = %v, unknown err = %v, want the same error kind", name, hiddenErr, unknownErr)
			}
		}
		norm := func(err error) string { return strings.NewReplacer(`"owner`, `"X`, `"nope`, `"X`).Replace(err.Error()) }
		if norm(hiddenErr) != norm(unknownErr) {
			t.Errorf("hidden relation in %s: hidden err = %q, unknown err = %q, want the same wording", name, hiddenErr, unknownErr)
		}
	}
}

// Untagged json:"-" keeps today's behavior: still filterable and sortable. Hidden
// fields stay available to fields, search and populate.
func TestHiddenTagLeavesOtherPathsAlone(t *testing.T) {
	db := newDryRunDB(t)
	sql, _, err := New[Account](db).
		Filters(map[string]any{"password": map[string]any{"$eq": "x"}, "peer.label": map[string]any{"$eq": "y"}}).
		Sort([]string{"password:desc", "peer.label"}).
		Search("abc").
		Fields([]string{"name", "last_ip"}).
		Populate([]string{"owner"}).
		ToSQL(context.Background(), defaultPage())
	if err != nil {
		t.Fatalf("ToSQL: %v", err)
	}
	assertContains(t, sql, "accounts.password = ?")
	assertContains(t, sql, "accounts.password DESC")
	assertContains(t, sql, "LOWER(accounts.last_login_ip) LIKE") // search reaches the hidden column
	assertContains(t, sql, "last_login_ip")                      // fields reaches it too
}
