// Package joins provides a small registry that deduplicates SQL JOIN clauses
// within a single query build.
//
// Nested relation filtering and relation-aware sorting can both reference the
// same relation path (for example filtering on "organization.slug" while
// sorting on "organization.name"). Each distinct relation path must contribute
// exactly one JOIN clause; the registry guarantees this and preserves the order
// in which paths were first seen so the generated SQL is deterministic.
//
// The registry is intended to be created once per query build and is not safe
// for concurrent use.
package joins

import "strings"

// Join is a single resolved JOIN clause.
type Join struct {
	// SQL is the complete join clause, for example
	// "LEFT JOIN organizations ON organizations.id = users.organization_id".
	SQL string
	// Alias is the table name or alias that columns reached through this join
	// must be qualified with, for example "organizations".
	Alias string
	// Args holds the parameters bound to any "?" placeholders in SQL, in
	// left-to-right order. It is populated only when a scope predicate (see
	// AddScope) is ANDed into the ON clause; a plain relation join has none.
	Args []any
}

// scopeEntry is one caller-supplied predicate to AND into a relation's JOIN ON
// clause, together with the values bound to its placeholders.
type scopeEntry struct {
	cond string
	args []any
}

// Registry accumulates the JOIN clauses required by a single query, keyed by an
// opaque relation path so that repeated references to the same path reuse one
// clause.
type Registry struct {
	rootTable string
	order     []string
	byPath    map[string]Join
	scopes    map[string][]scopeEntry
}

// NewRegistry returns an empty Registry whose root (un-joined) table is
// rootTable.
func NewRegistry(rootTable string) *Registry {
	return &Registry{
		rootTable: rootTable,
		byPath:    make(map[string]Join),
		scopes:    make(map[string][]scopeEntry),
	}
}

// AddScope registers a predicate to AND into the ON clause of the JOIN built
// for path, scoping the joined rows (for example to the current user). cond is a
// raw SQL boolean using "?" placeholders; args are bound to those placeholders,
// never interpolated. Multiple scopes for the same path AND together in
// registration order. Call before the join is built (via Ensure); a blank cond
// is ignored.
func (r *Registry) AddScope(path, cond string, args []any) {
	cond = strings.TrimSpace(cond)
	if cond == "" {
		return
	}
	r.scopes[path] = append(r.scopes[path], scopeEntry{cond: cond, args: args})
}

// RootTable returns the root table the registry was created for.
func (r *Registry) RootTable() string {
	return r.rootTable
}

// Ensure returns the alias for the JOIN identified by path, building it on
// first use.
//
// The first time a given path is seen, build is invoked, its result is recorded
// (preserving first-seen order), and the join's alias is returned. On any
// subsequent call with the same path the cached alias is returned and build is
// not invoked again. This guarantees one JOIN clause per distinct path.
func (r *Registry) Ensure(path string, build func() Join) string {
	if j, ok := r.byPath[path]; ok {
		return j.Alias
	}
	j := build()
	// AND any registered scope predicates into the freshly built ON clause so a
	// relation referenced by filtering and/or sorting is scoped in exactly one
	// place, regardless of which caller triggered the build.
	for _, sc := range r.scopes[path] {
		j.SQL += " AND (" + sc.cond + ")"
		j.Args = append(j.Args, sc.args...)
	}
	r.byPath[path] = j
	r.order = append(r.order, path)
	return j.Alias
}

// Joins returns the accumulated joins in first-seen (insertion) order.
//
// The returned slice is freshly allocated and may be retained or mutated by the
// caller without affecting the registry.
func (r *Registry) Joins() []Join {
	out := make([]Join, 0, len(r.order))
	for _, p := range r.order {
		out = append(out, r.byPath[p])
	}
	return out
}
