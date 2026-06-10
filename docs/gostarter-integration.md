# Integrating strapgorm into a gostarter project

[gostarter](https://github.com/faisalcayunda/gostarter) generates zero-lock-in Go
services using a **port/adapter (hexagonal)** layout. This note shows how
strapgorm slots into that layout as the query engine behind a domain's list
endpoint, and why it composes cleanly with gostarter's database layer.

## The shape of a gostarter domain

A gostarter domain (for example `internal/product`) typically looks like:

```text
internal/product/
├── internal/
│   ├── core/            # domain model + the Repository PORT (interface) + Service
│   │   ├── product.go   # type Product, type Repository interface, NewService(repo)
│   │   └── service.go
│   └── adapter/         # ADAPTERS that satisfy the ports
│       └── repository.go  # gormRepo implements core.Repository  <-- strapgorm lives here
└── product.go           # composition root / wiring
```

- **Port:** `core.Repository` — an interface the service depends on.
- **Adapter:** a struct that implements `Repository` using a concrete technology.
- **DI:** `NewService(repo)` receives the port; the wiring layer injects the
  adapter.

strapgorm belongs in the **adapter**, never in `core`. The core stays free of
GORM and strapgorm; it only knows the `Repository` interface and the decoded
`parser.QueryParams`.

## Driver compatibility (no conflict)

gostarter opens its primary pool with:

| Database | gostarter driver | GORM driver | Same driver family? |
|---|---|---|---|
| PostgreSQL | `jackc/pgx/v5` + `pgxpool` | `gorm.io/driver/postgres` (pgx/v5-backed) | ✅ yes |
| MySQL | `database/sql` + `go-sql-driver/mysql` | `gorm.io/driver/mysql` (wraps go-sql-driver) | ✅ yes |

Because the GORM drivers wrap the exact drivers gostarter already uses, you have
two equally valid options:

1. **Open an independent `*gorm.DB`** from the same `DATABASE_URL` / DSN that
   gostarter exposes. Simplest; GORM manages its own pool against the same
   server. This is what `examples/gostarter/repository.go` shows
   (`OpenPostgres` / `OpenMySQL`).
2. **Share one `*sql.DB`** by handing GORM an existing connection via the
   driver's `Conn` option (`postgres.New(postgres.Config{Conn: sqlDB})` /
   `mysql.New(mysql.Config{Conn: sqlDB})`). Use this when you want a single
   connection pool for the whole process.

Either way there is **no driver conflict** and **no second driver dependency**
beyond the GORM wrapper.

## The adapter

```go
package adapter

import (
	"context"

	"gorm.io/gorm"

	strapgorm "github.com/faisalcayunda/strapgorm"
	"github.com/faisalcayunda/strapgorm/paginate"
	"github.com/faisalcayunda/strapgorm/parser"

	"yourapp/internal/product/internal/core"
)

type gormProductRepo struct{ db *gorm.DB }

// NewGormProductRepository returns a core.Repository backed by GORM + strapgorm.
func NewGormProductRepository(db *gorm.DB) core.Repository {
	return &gormProductRepo{db: db}
}

func (r *gormProductRepo) List(
	ctx context.Context,
	qp parser.QueryParams,
) ([]core.Product, paginate.Meta, error) {
	return strapgorm.New[core.Product](r.db).
		FromParams(qp).
		Paginate(ctx, qp.Pagination)
}
```

The HTTP handler decodes the query string once with `parser.FromURL` and passes
the typed `QueryParams` down through the service to `Repository.List`, keeping the
transport layer thin and the core technology-agnostic.

## Wiring

```go
// composition root
db, err := gorm.Open(postgres.Open(os.Getenv("DATABASE_URL")), &gorm.Config{})
// ...
repo := adapter.NewGormProductRepository(db)
svc := core.NewService(repo)
```

A complete, compilable adapter (with the `Product` model, the `Repository` port,
`NewGormRepository`, and the `OpenPostgres` / `OpenMySQL` helpers) lives in
[`examples/gostarter/repository.go`](../examples/gostarter/repository.go).

## Security reminder

All query-string tokens are resolved against `core.Product`'s schema whitelist
inside strapgorm. An unknown field, relation, or operator returns a wrapped error
from `List` (surface it as `400 Bad Request`) and never reaches SQL; values are
always bound as parameters. The core model's struct tags are the single source of
truth for what the API will accept.
