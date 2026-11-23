# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- `strapgorm:"hidden"` struct tag: a tagged field or relation is excluded from the
  filter and sort whitelists and answers with the same `ErrUnknownField` /
  `ErrUnknownRelation` as a name the model does not have. `schema.Field.Hidden`,
  `schema.Relation.Hidden` and `schema.IsHidden` expose it. Structs without the
  tag behave as before.
- `examples/` as a standalone Go module (so the web-framework and integration
  dependencies stay out of the core module graph) with runnable `GET /users`
  handlers for Gin, Fiber, and Echo.
- `examples/gostarter/repository.go` — a port/adapter `Repository` integration
  showing how to back a gostarter domain with strapgorm over a `*gorm.DB` opened
  from the same `DATABASE_URL` / DSN, including `OpenPostgres` / `OpenMySQL`
  helpers.
- `README.md`, `.golangci.yml`, and a GitHub Actions CI workflow.

## [0.1.0] - 2026-06-11

The first release. Implements the Strapi-style query MVP (filter, sort,
pagination) plus the search, fields, populate, and parser layers, with a
GORM-backed facade.

### Added

- **schema** — reflection-based model introspection (JSON name ↔ column ↔
  relation) with a process-wide `sync.Map` cache; the per-model security
  whitelist. `Searchable` interface for declaring searchable fields.
- **dialect** — PostgreSQL, MySQL, and SQLite SQL-fragment helpers, including
  case-insensitive matching (`ILIKE` on Postgres, `LOWER(col) LIKE LOWER(?)`
  elsewhere). All placeholders emitted as `?`.
- **filter** — extensible operator registry with the 17 built-in Strapi
  operators (`$eq`, `$ne`, `$lt`, `$lte`, `$gt`, `$gte`, `$in`, `$notIn`,
  `$contains`, `$containsi`, `$startsWith`, `$endsWith`, `$null`, `$notNull`,
  `$between`, `$and`, `$or`); nested `$or`/`$and` grouping; dot-notation relation
  filtering with automatic, deduplicated `LEFT JOIN`s; per-column type coercion
  with multi-layout time parsing.
- **sort** — multi-field `field:asc|desc` ordering, including relation fields via
  dot notation, sharing JOIN aliases with filtering.
- **paginate** — page-based and offset-based pagination with clamped bounds and a
  complete response `Meta` (`page`, `page_size`, `page_count`, `total`,
  `has_next`, `has_previous`).
- **search** — case-insensitive multi-column OR predicate driven by the model's
  `Searchable()` fields.
- **fields** — whitelisted SELECT projection from Strapi `fields` entries, always
  including the primary key.
- **populate** — GORM association planner resolving `populate` entries (and the
  `*` wildcard) against the relation whitelist into Preload specs.
- **parser** — `FromURL` / `FromValues` decode the Strapi/`qs` bracket
  query-string format (filters, sort, pagination, fields, populate, search/`_q`)
  into a typed `QueryParams`, robust against malformed input.
- **internal/joins** — JOIN alias registry that deduplicates joins shared by
  filtering and sorting.
- **internal/gormschema** — GORM-backed schema adapter that maps GORM's own
  parsed schema onto the strapgorm whitelist for runtime-accurate identifiers.
- **strapgorm** (facade) — `New[T](db)` chainable builder: `Filters`, `Sort`,
  `Search`, `Fields`, `Populate`, `Operator`, `FromParams`, terminated by
  `Paginate`, `Find`, or `Count`.

### Security

- Field, relation, and operator tokens from the query string are resolved against
  the schema whitelist; unknown tokens are explicit errors and are never
  interpolated into SQL. Column/table identifiers come only from schema metadata,
  and all values are bound as parameters.

[Unreleased]: https://github.com/faisalcayunda/strapgorm/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/faisalcayunda/strapgorm/releases/tag/v0.1.0
