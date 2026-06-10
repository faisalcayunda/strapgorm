# strapgorm

[![Go Reference](https://pkg.go.dev/badge/github.com/faisalcayunda/strapgorm.svg)](https://pkg.go.dev/github.com/faisalcayunda/strapgorm)
[![CI](https://github.com/faisalcayunda/strapgorm/actions/workflows/ci.yml/badge.svg)](https://github.com/faisalcayunda/strapgorm/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/faisalcayunda/strapgorm)](https://goreportcard.com/report/github.com/faisalcayunda/strapgorm)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](./LICENSE)

A **Strapi-style query builder for Go on top of [GORM](https://gorm.io)**. It
parses Strapi-flavored query strings — `filters[name][$contains]=John`,
`sort=name:asc`, `pagination[page]=1`, `fields=id,name`, `populate=organization`
— into **safe, parameterized SQL** that runs through a `*gorm.DB`.

strapgorm is a port of [strapalchemy](https://github.com/faisalcayunda/strapalchemy)
(Python/SQLAlchemy), targeting feature parity with strapalchemy **v0.2.5**.

> **It is not a new ORM.** It is a builder layer that sits on top of GORM and
> turns untrusted query strings into whitelisted, parameterized queries.

---

## Table of Contents

- [Why](#why)
- [Install](#install)
- [Quick start](#quick-start)
- [Query-string syntax](#query-string-syntax)
- [Filter operators](#filter-operators)
- [The `Searchable` interface](#the-searchable-interface)
- [Pagination metadata](#pagination-metadata)
- [Using strapgorm in a gostarter project](#using-strapgorm-in-a-gostarter-project)
- [Framework examples](#framework-examples)
- [Build & offline notes](#build--offline-notes)
- [Security model](#security-model)
- [Roadmap](#roadmap)
- [License](#license)

---

## Why

Building a REST list endpoint usually means hand-writing the same plumbing over
and over: parse `?filters[...]`, validate every field name, translate operators
to SQL, wire up sorting, paginate, count, and shape the response. strapgorm does
all of that from the query string with one chainable call, while guaranteeing:

- **Safe by default** — field, relation, and operator names from the query
  string are resolved against a schema **whitelist**; unknown tokens are an
  explicit error and never reach SQL. Values are always bound as parameters.
- **Idiomatic Go** — one `context.Context`-based API (no sync/async dualism),
  generics (`strapgorm.New[T]`), and a `Searchable() []string` interface instead
  of magic config.
- **Drop-in for HTTP handlers** — a built-in parser for the Strapi/`qs` bracket
  query-string format works with Gin, Fiber, Echo, or `net/http`.
- **Multi-database** — PostgreSQL, MySQL, and SQLite, with dialect-aware
  case-insensitive matching (`ILIKE` on Postgres, `LOWER(col) LIKE LOWER(?)`
  elsewhere).

## Install

```sh
go get github.com/faisalcayunda/strapgorm
```

Then fetch GORM and the driver(s) you use:

```sh
go get gorm.io/gorm gorm.io/driver/postgres   # or gorm.io/driver/mysql, github.com/glebarez/sqlite
go mod tidy
```

strapgorm requires **Go 1.25+**.

## Quick start

```go
package main

import (
	"context"
	"net/http"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	strapgorm "github.com/faisalcayunda/strapgorm"
	"github.com/faisalcayunda/strapgorm/parser"
)

// User is your domain model. json tags name fields as they appear in query
// strings and responses; the gorm tags drive the table mapping.
type User struct {
	ID    uint   `json:"id" gorm:"primaryKey"`
	Name  string `json:"name"`
	Email string `json:"email"`
	Bio   string `json:"bio"`
}

// Opt the model into free-text search over these fields.
func (User) Searchable() []string {
	return []string{"name", "email", "bio"}
}

func listUsers(db *gorm.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// 1. Decode the Strapi query string.
		params, err := parser.FromURL(r.URL.Query())
		if err != nil { /* 400 */ return }

		// 2. Build and run the query — filters, sort, search, fields, populate,
		//    and pagination in one chain.
		users, meta, err := strapgorm.New[User](db).
			Filters(params.Filters).
			Sort(params.Sort).
			Search(params.Search).
			Fields(params.Fields).
			Populate(params.Populate).
			Paginate(r.Context(), params.Pagination)
		if err != nil { /* 400 — unknown field/operator, etc. */ return }

		// 3. Respond. meta is { page, page_size, page_count, total,
		//    has_next, has_previous }.
		_ = users
		_ = meta
	}
}
```

The whole configuration step can be collapsed with `FromParams`:

```go
users, meta, err := strapgorm.New[User](db).
	FromParams(params).
	Paginate(r.Context(), params.Pagination)
```

`New[T]` infers the SQL dialect from the `*gorm.DB` driver and resolves the
model whitelist from GORM's own schema (honoring your `NamingStrategy`). Besides
`Paginate`, the builder also offers `Find(ctx)` (no pagination) and
`Count(ctx)`.

## Query-string syntax

strapgorm understands the Strapi / `qs` bracket encoding:

```text
# Filters (field operator maps)
filters[name][$contains]=John
filters[age][$gte]=18
filters[price][$between]=10,99

# Nested relation filtering (dot notation or nested brackets)
filters[organization][slug][$eq]=acme
filters[organization.slug][$eq]=acme

# Logical grouping
filters[$or][0][name][$eq]=a&filters[$or][1][name][$eq]=b

# Sorting (comma list or indexed; multi-field; relation fields)
sort=name:asc,createdAt:desc
sort[0]=name:asc&sort[1]=organization.name:desc

# Pagination — page mode
pagination[page]=2&pagination[pageSize]=10&pagination[withCount]=true
# Pagination — offset mode
pagination[start]=20&pagination[limit]=10

# Field selection (SELECT projection)
fields=id,name
fields[0]=id&fields[1]=name

# Populate relations (or wildcard)
populate=organization,role
populate=*

# Free-text search (Strapi's _q is also accepted)
search=acme
_q=acme
```

## Filter operators

The 17 built-in operators (operators live in an extensible **registry**, so you
can register custom ones via `Builder.Operator` / `filter.Registry.Register`):

| Operator | Description | SQL (parameterized) |
|---|---|---|
| `$eq` | Equals | `col = ?` |
| `$ne` | Not equal | `col <> ?` |
| `$lt` / `$lte` | Less than (or equal) | `col < ?` / `col <= ?` |
| `$gt` / `$gte` | Greater than (or equal) | `col > ?` / `col >= ?` |
| `$in` / `$notIn` | In / not in a list | `col IN (?)` / `col NOT IN (?)` |
| `$contains` | Contains | `col LIKE '%' \|\| ? \|\| '%'` |
| `$containsi` | Contains (case-insensitive) | `col ILIKE ...` / `LOWER(col) LIKE ...` |
| `$startsWith` / `$endsWith` | Starts / ends with | `LIKE ? \|\| '%'` / `LIKE '%' \|\| ?` |
| `$null` / `$notNull` | IS NULL / IS NOT NULL | `col IS (NOT) NULL` |
| `$between` | Between two values | `col BETWEEN ? AND ?` |
| `$or` / `$and` | Logical OR / AND (nested) | grouping `(...)` |

Values are coerced to the target column's Go kind (string → int/uint/float/bool/
time) before binding, with multi-layout time parsing (a `python-dateutil`-like
fallback set).

### Custom operators

```go
users, meta, err := strapgorm.New[User](db).
	Operator("$icontains", func(col string, value any, d dialect.Dialect) (string, []any, error) {
		return d.ILike(col), []any{"%" + value.(string) + "%"}, nil
	}).
	FromParams(params).
	Paginate(ctx, params.Pagination)
```

## The `Searchable` interface

A model declares which fields the free-text `search` parameter matches by
implementing `Searchable`:

```go
type User struct {
	ID    uint   `json:"id" gorm:"primaryKey"`
	Name  string `json:"name"`
	Email string `json:"email"`
	Bio   string `json:"bio"`
}

func (User) Searchable() []string {
	return []string{"name", "email", "bio"}
}
```

`search=acme` then compiles to a case-insensitive OR over those columns:

```sql
(users.name ILIKE ? OR users.email ILIKE ? OR users.bio ILIKE ?)   -- args: %acme% x3
```

If a model has no `Searchable()` and a non-empty `search` term is supplied,
strapgorm returns an explicit error rather than silently matching nothing.

## Pagination metadata

`Paginate` returns `([]T, paginate.Meta, error)`. The `Meta` shape (JSON tags
shown) is:

```jsonc
{
  "page": 2,
  "page_size": 10,
  "page_count": 7,
  "total": 64,
  "has_next": true,
  "has_previous": true
}
```

`page_count`, `total`, `has_next`, and `has_previous` are populated when a count
was requested (`pagination[withCount]=true`). Page size defaults to **25** and is
capped at **100** to protect the database from unbounded result sets. Offset mode
(`pagination[start]` / `pagination[limit]`) is also exposed through the same
page-oriented metadata.

A typical response envelope:

```json
{ "data": [ /* rows */ ], "meta": { /* Meta above */ } }
```

## Using strapgorm in a gostarter project

[gostarter](https://github.com/faisalcayunda/gostarter) is a zero-lock-in Go
project generator. Its data layer uses:

- **Postgres** — `jackc/pgx/v5` + `pgxpool` (a `*pgxpool.Pool`).
- **MySQL** — `database/sql` + `go-sql-driver/mysql` (a `*sql.DB`).

and a **port/adapter (hexagonal) repository pattern**: a domain package exposes a
`Repository` interface in its core package, and a service is built with
`NewService(repo)`.

**Why there is no driver conflict.** `gorm.io/driver/postgres` is **pgx/v5-backed**
and `gorm.io/driver/mysql` wraps **go-sql-driver/mysql** — the *same* drivers
gostarter already uses. Opening a `*gorm.DB` over the same `DATABASE_URL` / DSN
that gostarter exposes is therefore conflict-free; you can run strapgorm's
`*gorm.DB` alongside the application's primary pool, or hand GORM an existing
`*sql.DB`.

A gostarter-style adapter that satisfies the domain `Repository` port:

```go
// internal/product/internal/adapter/repository.go
package adapter

import (
	"context"
	"fmt"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	strapgorm "github.com/faisalcayunda/strapgorm"
	"github.com/faisalcayunda/strapgorm/paginate"
	"github.com/faisalcayunda/strapgorm/parser"
)

type Product struct {
	ID         uint    `gorm:"primaryKey" json:"id"`
	Name       string  `json:"name"`
	SKU        string  `gorm:"uniqueIndex" json:"sku"`
	Price      float64 `json:"price"`
	CategoryID uint    `gorm:"index" json:"category_id"`
	Active     bool    `json:"active"`
}

func (Product) Searchable() []string { return []string{"name", "sku"} }

// Repository is the core port the domain service depends on.
type Repository interface {
	List(ctx context.Context, qp parser.QueryParams) ([]Product, paginate.Meta, error)
}

type gormRepo struct{ db *gorm.DB }

// NewGormRepository builds a Repository for NewService(repo) injection.
func NewGormRepository(db *gorm.DB) Repository { return &gormRepo{db: db} }

func (r *gormRepo) List(ctx context.Context, qp parser.QueryParams) ([]Product, paginate.Meta, error) {
	return strapgorm.New[Product](r.db).
		FromParams(qp).
		Paginate(ctx, qp.Pagination)
}

// Open a *gorm.DB from the SAME DATABASE_URL gostarter exposes (pgx-backed):
//   db, err := gorm.Open(postgres.Open(os.Getenv("DATABASE_URL")), &gorm.Config{})
// For MySQL use mysql.Open(dsn) with the same go-sql-driver DSN.
var _ = postgres.Open
var _ = fmt.Sprintf
```

A complete, compilable version of this adapter (with `OpenPostgres` / `OpenMySQL`
helpers) lives in [`examples/gostarter/repository.go`](./examples/gostarter/repository.go).

## Framework examples

Runnable `GET /users` handlers for each framework are under
[`examples/`](./examples) (a **separate Go module** so the web-framework
dependencies never enter strapgorm's core module graph):

- [`examples/gin/main.go`](./examples/gin/main.go)
- [`examples/fiber/main.go`](./examples/fiber/main.go)
- [`examples/echo/main.go`](./examples/echo/main.go)
- [`examples/gostarter/repository.go`](./examples/gostarter/repository.go) — the port/adapter integration

```sh
cd examples
go mod tidy
DATABASE_URL='postgres://user:pass@localhost:5432/app?sslmode=disable' go run ./gin
```

## Build & offline notes

The **core packages are dependency-light**: `schema`, `dialect`, `paginate`,
`internal/joins`, `filter`, `sort`, `fields`, `search`, `parser`, and `populate`
do **not** import GORM, so they build and `go test` without any network access:

```sh
go test ./schema ./dialect ./paginate ./internal/joins \
        ./filter ./sort ./fields ./search ./parser ./populate
```

The GORM-dependent layer — the root `strapgorm` facade, `internal/gormschema`,
and `examples/` — needs the GORM module and the database drivers. Run
`go mod tidy` once (with network access) to fetch `gorm.io/gorm` and the drivers
declared in `go.mod`; after that the full module builds and tests offline.

## Security model

Field names, relation names, and operators that come from a query string are
**untrusted**. strapgorm enforces:

1. **Whitelist resolution** — every field/relation/operator is resolved against
   schema metadata (derived from your struct via GORM's schema parser, with a
   reflection fallback). An unknown token yields an explicit
   `schema.ErrUnknownField` / `ErrUnknownRelation` / `ErrUnknownOperator` and is
   **never interpolated** into SQL.
2. **Identifiers from metadata only** — column and table names emitted into SQL
   come exclusively from schema metadata, never from raw user strings.
3. **Values always bound** — operand values are always passed as `?` parameters,
   never concatenated into the query text.
4. **Bounded result sets** — page size is clamped to a maximum to prevent
   query-string-driven unbounded scans.

## Roadmap

Following the plan's MVP-first sequencing:

- **v0.1.0** — filtering (17 operators), sorting, pagination, the query-string
  parser, dialect abstraction, the GORM facade. *(this release)*
- **v0.2.0** — search, field selection, and populate refinements; the framework
  examples.
- **v0.3.0** — Bun adapter, public custom-operator surface, documentation site.
- **v1.0.0** — stable API after community feedback.

## License

[MIT](./LICENSE) © 2026 Faisal Nugraha.
