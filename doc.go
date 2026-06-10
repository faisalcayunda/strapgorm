// Package strapgorm is a Strapi-style query builder for Go on top of GORM.
//
// strapgorm parses Strapi-flavored query strings such as
//
//	filters[name][$contains]=John&sort=name:asc&pagination[page]=1&fields=id,name&populate=organization
//
// into safe, parameterized SQL that is executed through a *gorm.DB. It is not a
// new ORM; it is a builder layer that sits on top of GORM and mirrors the
// query semantics of strapalchemy (the Python/SQLAlchemy reference
// implementation), targeting feature parity with strapalchemy v0.2.5.
//
// # Security model
//
// Field names, relation names, and operators that originate from a query string
// are treated as untrusted input. Every such token is resolved against schema
// metadata derived by reflection (the whitelist). Unknown fields, relations, or
// operators produce an explicit error and are never interpolated into SQL.
// Column and table identifiers only ever come from schema metadata, and values
// are always bound as parameters, never concatenated into the query text.
//
// # Package layout
//
// The functionality is split into focused packages:
//
//   - schema: reflection-based model introspection (fields, columns, relations)
//     with a process-wide cache.
//   - dialect: per-database SQL fragment helpers (Postgres, MySQL, SQLite).
//   - paginate: page- and offset-based pagination parameters and metadata.
//   - filter, sort, fields, search, parser: query-string parsing and builders.
//   - internal/joins: deduplicated JOIN alias registry.
//   - populate: GORM Preload wiring (depends on GORM).
//
// The GORM-dependent facade (this root package), the populate package, and the
// examples require the GORM module to compile; the remaining packages are
// dependency-light and build without GORM.
package strapgorm
