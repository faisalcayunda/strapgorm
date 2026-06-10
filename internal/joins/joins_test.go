package joins

import (
	"reflect"
	"testing"
)

func TestRootTable(t *testing.T) {
	r := NewRegistry("users")
	if got := r.RootTable(); got != "users" {
		t.Errorf("RootTable() = %q, want %q", got, "users")
	}
}

func TestEnsureBuildsOncePerPath(t *testing.T) {
	r := NewRegistry("users")
	calls := 0
	build := func() Join {
		calls++
		return Join{
			SQL:   "LEFT JOIN organizations ON organizations.id = users.organization_id",
			Alias: "organizations",
		}
	}

	alias1 := r.Ensure("organization", build)
	alias2 := r.Ensure("organization", build)
	alias3 := r.Ensure("organization", build)

	if calls != 1 {
		t.Errorf("build called %d times, want 1", calls)
	}
	if alias1 != "organizations" || alias2 != "organizations" || alias3 != "organizations" {
		t.Errorf("aliases = %q/%q/%q, want all %q", alias1, alias2, alias3, "organizations")
	}
	if got := len(r.Joins()); got != 1 {
		t.Errorf("len(Joins()) = %d, want 1", got)
	}
}

func TestEnsurePreservesInsertionOrder(t *testing.T) {
	r := NewRegistry("users")

	r.Ensure("organization", func() Join {
		return Join{SQL: "JOIN organizations ...", Alias: "organizations"}
	})
	r.Ensure("role", func() Join {
		return Join{SQL: "JOIN roles ...", Alias: "roles"}
	})
	// Re-reference the first path; must not reorder or duplicate.
	r.Ensure("organization", func() Join {
		t.Fatal("build should not be called for an already-ensured path")
		return Join{}
	})
	r.Ensure("team", func() Join {
		return Join{SQL: "JOIN teams ...", Alias: "teams"}
	})

	got := r.Joins()
	wantAliases := []string{"organizations", "roles", "teams"}
	gotAliases := make([]string, len(got))
	for i, j := range got {
		gotAliases[i] = j.Alias
	}
	if !reflect.DeepEqual(gotAliases, wantAliases) {
		t.Errorf("alias order = %v, want %v", gotAliases, wantAliases)
	}
}

func TestEnsureDistinctPathsSameAlias(t *testing.T) {
	// Two different paths may legitimately build distinct joins even if their
	// aliases collide in naming; the registry keys on path, not alias.
	r := NewRegistry("users")
	r.Ensure("a", func() Join { return Join{SQL: "JOIN x AS t1", Alias: "t1"} })
	r.Ensure("b", func() Join { return Join{SQL: "JOIN y AS t2", Alias: "t2"} })
	if got := len(r.Joins()); got != 2 {
		t.Errorf("len(Joins()) = %d, want 2", got)
	}
}

func TestJoinsReturnsFreshSlice(t *testing.T) {
	r := NewRegistry("users")
	r.Ensure("organization", func() Join {
		return Join{SQL: "JOIN organizations ...", Alias: "organizations"}
	})
	first := r.Joins()
	first[0].Alias = "mutated"
	second := r.Joins()
	if second[0].Alias != "organizations" {
		t.Errorf("registry state mutated through returned slice: got %q", second[0].Alias)
	}
}

func TestJoinsEmpty(t *testing.T) {
	r := NewRegistry("users")
	if got := r.Joins(); len(got) != 0 {
		t.Errorf("len(Joins()) = %d, want 0", len(got))
	}
}
