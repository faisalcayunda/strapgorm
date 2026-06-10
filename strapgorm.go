package strapgorm

import (
	"context"
	"fmt"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/faisalcayunda/strapgorm/dialect"
	"github.com/faisalcayunda/strapgorm/fields"
	"github.com/faisalcayunda/strapgorm/filter"
	"github.com/faisalcayunda/strapgorm/internal/gormschema"
	"github.com/faisalcayunda/strapgorm/internal/joins"
	"github.com/faisalcayunda/strapgorm/paginate"
	"github.com/faisalcayunda/strapgorm/parser"
	"github.com/faisalcayunda/strapgorm/populate"
	"github.com/faisalcayunda/strapgorm/schema"
	"github.com/faisalcayunda/strapgorm/search"
	sortpkg "github.com/faisalcayunda/strapgorm/sort"
)

// Builder is the chainable facade that assembles a Strapi-style query for the
// model type T and executes it through a *gorm.DB.
//
// A Builder is created with New and configured with the chainable Filters, Sort,
// Search, Fields, and Populate methods (or all at once from a decoded query
// string with FromParams). Calling Paginate (or Find/Count) compiles the
// accumulated state into safe, parameterized SQL, applies it to a *gorm.DB
// session, and runs the query.
//
// Every untrusted token (field name, relation name, operator) is resolved
// against the schema whitelist for T before it reaches SQL; an unknown token
// causes the terminal method to return a wrapped error and never produces SQL.
// Operand values are always bound as parameters. The zero value of Builder is
// not usable; always start from New.
//
// A Builder is not safe for concurrent use. Configuration is cheap and does not
// touch the database until a terminal method is called; build one per request.
type Builder[T any] struct {
	db *gorm.DB

	model   *schema.Model
	dialect dialect.Dialect
	ops     *filter.Registry

	rawFilters  map[string]any
	rawSort     []string
	rawSearch   string
	rawFields   []string
	rawPopulate []string

	// err latches the first construction-time error (a failed model resolution
	// in New) so terminal methods can report it instead of panicking on a nil
	// model.
	err error
}

// New returns a Builder bound to db for the model type T.
//
// The model metadata for T is resolved (and cached) immediately through the
// GORM-backed schema adapter, so the table, column, relation, and join-table
// names match the live *gorm.DB mapping (including any custom NamingStrategy).
// If schema resolution fails, the error is latched and surfaced from the first
// terminal call (Paginate, Find, Count, or ToSQL). The SQL dialect is inferred
// from the *gorm.DB driver name (Postgres, MySQL, or SQLite), and the default
// operator registry (the 17 built-in Strapi operators) is installed. Use the
// chainable methods or FromParams to configure the query, then call a terminal
// method. New never returns nil.
func New[T any](db *gorm.DB) *Builder[T] {
	b := &Builder[T]{
		db:  db,
		ops: filter.Default(),
	}

	if db != nil && db.Dialector != nil {
		b.dialect = dialect.ByName(db.Name())
	} else {
		b.dialect = dialect.Postgres
	}

	model, err := gormschema.From(db, new(T))
	if err != nil {
		b.err = fmt.Errorf("strapgorm: resolving schema for %T: %w", *new(T), err)
		return b
	}
	b.model = model

	return b
}

// WithDialect overrides the SQL dialect inferred from the *gorm.DB driver.
//
// It is rarely needed: New already selects the dialect from the driver name. It
// exists for callers driving a generic *gorm.DB whose driver name does not map
// cleanly to a supported dialect. A nil dialect is ignored.
func (b *Builder[T]) WithDialect(d dialect.Dialect) *Builder[T] {
	if d != nil {
		b.dialect = d
	}
	return b
}

// WithOperators replaces the operator registry used for filter compilation.
//
// Pass a registry derived from filter.Default() with custom operators
// registered to extend the built-in Strapi operator set. A nil registry is
// ignored.
func (b *Builder[T]) WithOperators(reg *filter.Registry) *Builder[T] {
	if reg != nil {
		b.ops = reg
	}
	return b
}

// Operator registers a custom filter operator under name on this Builder's
// operator registry, adding to (or overriding) the built-in Strapi operators.
//
// name should include the leading "$" to match the query-string convention (for
// example "$icontains"). The operator becomes usable in filter objects compiled
// by the subsequent terminal call. It returns the Builder for chaining so an
// operator can be registered inline before configuring filters.
func (b *Builder[T]) Operator(name string, fn filter.OpFunc) *Builder[T] {
	b.ops.Register(name, fn)
	return b
}

// Filters sets the decoded Strapi filter object (parser.QueryParams.Filters).
//
// The object is compiled into a parameterized WHERE clause when a terminal
// method runs. Passing nil clears any previously set filters.
func (b *Builder[T]) Filters(raw map[string]any) *Builder[T] {
	b.rawFilters = raw
	return b
}

// Sort sets the ordered list of Strapi sort directives ("field" or
// "field:direction", including dot-notation relation fields).
func (b *Builder[T]) Sort(raw []string) *Builder[T] {
	b.rawSort = raw
	return b
}

// Search sets the free-text search term matched case-insensitively against the
// model's Searchable() fields. An empty term disables search.
func (b *Builder[T]) Search(term string) *Builder[T] {
	b.rawSearch = term
	return b
}

// Fields sets the requested SELECT projection (a list of field json-names). An
// empty list selects all columns.
func (b *Builder[T]) Fields(raw []string) *Builder[T] {
	b.rawFields = raw
	return b
}

// Populate sets the relations to eager-load via GORM Preload. The single
// element "*" requests populating all of the model's relations.
func (b *Builder[T]) Populate(raw []string) *Builder[T] {
	b.rawPopulate = raw
	return b
}

// FromParams configures the builder from a decoded query string in one call,
// equivalent to chaining Filters, Sort, Search, Fields, and Populate with the
// matching QueryParams fields. The pagination component is not consumed here;
// pass qp.Pagination to Paginate.
func (b *Builder[T]) FromParams(qp parser.QueryParams) *Builder[T] {
	return b.
		Filters(qp.Filters).
		Sort(qp.Sort).
		Search(qp.Search).
		Fields(qp.Fields).
		Populate(qp.Populate)
}

// Paginate executes the configured query for page p and returns the matching
// rows together with the response metadata.
//
// The accumulated filter, search, sort, field-selection, and populate state is
// compiled into safe SQL and applied to a *gorm.DB session scoped to ctx. When
// p.WithCount is set a separate COUNT query is run with the same WHERE/JOIN
// predicates but without ORDER BY, LIMIT, or OFFSET, and the result drives
// Meta.Total, Meta.PageCount, Meta.HasNext, and Meta.HasPrevious. The page
// itself is fetched with the LIMIT and OFFSET derived from p.
//
// Any configuration error (an invalid model, an unknown field/relation/operator,
// or an un-coercible operand) is returned here; on error the returned slice is
// nil and the Meta is the zero value.
func (b *Builder[T]) Paginate(ctx context.Context, p paginate.Params) ([]T, paginate.Meta, error) {
	p = p.Normalize()

	plan, err := b.plan(ctx)
	if err != nil {
		return nil, paginate.Meta{}, err
	}

	var total int64
	if p.WithCount {
		countTx := plan.session.Session(&gorm.Session{})
		if err := countTx.Count(&total).Error; err != nil {
			return nil, paginate.Meta{}, fmt.Errorf("strapgorm: counting rows: %w", err)
		}
	}

	session := plan.applyProjection(plan.session)
	session = plan.applyOrders(session)

	limit, offset := p.LimitOffset()
	session = session.Limit(limit).Offset(offset)

	var rows []T
	if err := session.Find(&rows).Error; err != nil {
		return nil, paginate.Meta{}, fmt.Errorf("strapgorm: executing query: %w", err)
	}

	// Without an explicit count, the metadata reports the rows on this page as the
	// total so callers still receive a coherent (if page-local) Meta.
	if !p.WithCount {
		total = int64(len(rows))
	}

	return rows, paginate.BuildMeta(p, total), nil
}

// Find executes the configured query without pagination and returns every
// matching row. It applies the same filter, search, sort, field-selection, and
// populate state as Paginate but imposes no LIMIT/OFFSET; use it only when the
// result set is known to be bounded.
func (b *Builder[T]) Find(ctx context.Context) ([]T, error) {
	plan, err := b.plan(ctx)
	if err != nil {
		return nil, err
	}

	session := plan.applyProjection(plan.session)
	session = plan.applyOrders(session)

	var rows []T
	if err := session.Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("strapgorm: executing query: %w", err)
	}
	return rows, nil
}

// Count executes only the COUNT form of the configured query (filter and search
// predicates plus their JOINs, without ORDER BY/LIMIT/OFFSET) and returns the
// number of matching rows.
func (b *Builder[T]) Count(ctx context.Context) (int64, error) {
	plan, err := b.plan(ctx)
	if err != nil {
		return 0, err
	}

	var total int64
	if err := plan.session.Count(&total).Error; err != nil {
		return 0, fmt.Errorf("strapgorm: counting rows: %w", err)
	}
	return total, nil
}

// ToSQL renders the SQL the configured query would execute for page p, without
// running it against the database. It is intended for debugging and tests.
//
// The statement is built under a DryRun session: filters, search, sort, field
// selection, populate, and the LIMIT/OFFSET derived from p are all applied, then
// the rendered, dialect-translated SQL and the bound parameters (in placeholder
// order) are returned. No query is executed and no rows are scanned. Any
// configuration error (unknown field/relation/operator, schema resolution
// failure) is returned here.
//
// Preloads do not appear in the returned SQL: GORM issues each Preload as a
// separate query at scan time, so a DryRun Find renders only the root statement.
func (b *Builder[T]) ToSQL(ctx context.Context, p paginate.Params) (sql string, vars []any, err error) {
	p = p.Normalize()

	// Build on a DryRun session so the pipeline renders SQL without executing it.
	// planOn re-applies WithContext(ctx) to this base, which is idempotent.
	dry := b.db.Session(&gorm.Session{DryRun: true})

	plan, err := b.planOn(ctx, dry)
	if err != nil {
		return "", nil, err
	}

	session := plan.applyProjection(plan.session)
	session = plan.applyOrders(session)

	limit, offset := p.LimitOffset()
	session = session.Limit(limit).Offset(offset)

	var rows []T
	session = session.Find(&rows)
	if session.Statement == nil {
		return "", nil, fmt.Errorf("strapgorm: ToSQL produced no statement")
	}

	stmt := session.Statement
	return stmt.SQL.String(), stmt.Vars, nil
}

// compiledPlan is the result of resolving a Builder's configuration: a *gorm.DB
// session carrying the WHERE predicate, JOINs, and Preloads, plus the resolved
// (schema-derived) projection columns and ORDER BY expressions that the count
// and page queries apply selectively.
type compiledPlan struct {
	session *gorm.DB
	columns []string
	orders  []string
}

// applyProjection adds the resolved column projection to session, or returns it
// unchanged when no explicit projection was requested.
func (pl compiledPlan) applyProjection(session *gorm.DB) *gorm.DB {
	if len(pl.columns) == 0 {
		return session
	}
	return session.Select(pl.columns)
}

// applyOrders adds every resolved ORDER BY expression to session in order.
func (pl compiledPlan) applyOrders(session *gorm.DB) *gorm.DB {
	for _, order := range pl.orders {
		session = session.Order(order)
	}
	return session
}

// plan resolves the accumulated configuration into a compiledPlan rooted at the
// Builder's *gorm.DB, scoped to ctx. It is the default entry point used by the
// terminal Find, Count, and Paginate methods.
func (b *Builder[T]) plan(ctx context.Context) (compiledPlan, error) {
	return b.planOn(ctx, b.db)
}

// planOn resolves the accumulated configuration into a compiledPlan rooted at
// base (scoped to ctx). It validates every untrusted token against the schema
// whitelist (returning a wrapped error on the first unknown field/relation/
// operator) and never partially applies an invalid query: a resolution failure
// yields an error and no session is used. Taking base as a parameter lets ToSQL
// build the same plan on a DryRun session without duplicating the pipeline.
func (b *Builder[T]) planOn(ctx context.Context, base *gorm.DB) (compiledPlan, error) {
	if b.err != nil {
		return compiledPlan{}, b.err
	}
	if b.model == nil {
		model, err := gormschema.From(b.db, new(T))
		if err != nil {
			return compiledPlan{}, fmt.Errorf("strapgorm: introspecting model: %w", err)
		}
		b.model = model
	}

	jr := joins.NewRegistry(b.model.Table)

	whereParts := make([]string, 0, 2)
	whereArgs := make([]any, 0, 4)

	// Filters -> WHERE (may register relation JOINs).
	if len(b.rawFilters) > 0 {
		cl, err := filter.Build(b.model, b.rawFilters, b.dialect, b.ops, jr)
		if err != nil {
			return compiledPlan{}, fmt.Errorf("strapgorm: building filters: %w", err)
		}
		if cl.SQL != "" {
			whereParts = append(whereParts, cl.SQL)
			whereArgs = append(whereArgs, cl.Args...)
		}
	}

	// Search -> WHERE.
	if b.rawSearch != "" {
		cl, err := search.Build(b.model, jr.RootTable(), b.rawSearch, b.dialect)
		if err != nil {
			return compiledPlan{}, fmt.Errorf("strapgorm: building search: %w", err)
		}
		if cl.SQL != "" {
			whereParts = append(whereParts, cl.SQL)
			whereArgs = append(whereArgs, cl.Args...)
		}
	}

	// Sort -> ORDER BY (reuses jr so a relation referenced by both filtering and
	// sorting contributes exactly one JOIN).
	var orders []string
	if len(b.rawSort) > 0 {
		built, err := sortpkg.Build(b.model, b.rawSort, jr)
		if err != nil {
			return compiledPlan{}, fmt.Errorf("strapgorm: building sort: %w", err)
		}
		orders = built
	}

	// Fields -> SELECT projection (qualified by the root table).
	var columns []string
	if len(b.rawFields) > 0 {
		cols, err := fields.Build(b.model, b.rawFields)
		if err != nil {
			return compiledPlan{}, fmt.Errorf("strapgorm: building field selection: %w", err)
		}
		columns = qualifyColumns(jr.RootTable(), cols)
	}

	// Populate -> validate relation paths (against the schema whitelist) into the
	// GORM association specs to preload, before any session is configured.
	specs, err := populate.Plan(b.model, b.rawPopulate)
	if err != nil {
		return compiledPlan{}, err
	}

	session := base.WithContext(ctx).Model(new(T))

	for _, j := range jr.Joins() {
		session = session.Joins(j.SQL)
	}

	if len(whereParts) > 0 {
		session = session.Where(joinAnd(whereParts), whereArgs...)
	}

	for _, spec := range specs {
		if spec.All {
			// The wildcard "*" maps to GORM's preload-every-association sentinel.
			session = session.Preload(clause.Associations)
			continue
		}
		session = session.Preload(spec.Association)
	}

	return compiledPlan{session: session, columns: columns, orders: orders}, nil
}

// qualifyColumns prefixes each bare column with the root table so the projection
// stays unambiguous once relation JOINs are present. The columns originate from
// schema metadata, never from raw user input.
func qualifyColumns(table string, cols []string) []string {
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = table + "." + c
	}
	return out
}

// joinAnd combines WHERE fragments with " AND ", parenthesizing each so that a
// search OR-group is not flattened into a sibling filter condition. A single
// fragment is returned unwrapped.
func joinAnd(parts []string) string {
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	default:
		wrapped := make([]string, len(parts))
		for i, p := range parts {
			wrapped[i] = "(" + p + ")"
		}
		return strings.Join(wrapped, " AND ")
	}
}
