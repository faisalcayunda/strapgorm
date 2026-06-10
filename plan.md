# Plan: StrapGORM — Port StrapAlchemy ke Golang

> Query builder gaya Strapi untuk Go, terinspirasi dari [strapalchemy](https://github.com/faisalcayunda/strapalchemy) (Python/SQLAlchemy).

**Status**: Draft v0.1
**Target rilis pertama**: `v0.1.0` (MVP) dalam ±4 minggu
**Lisensi**: MIT

---

## 1. Latar Belakang & Tujuan

StrapAlchemy menyediakan sintaks query gaya Strapi (filter `$eq`, `$contains`, dst., sorting `field:asc`, pagination, search, populate) di atas SQLAlchemy. Versi Go ditujukan untuk developer backend Go yang membangun REST API dan ingin parsing query string langsung menjadi query database yang aman.

**Tujuan:**

1. Paritas fitur dengan strapalchemy v0.2.5 (filter, sort, pagination, search, field selection, populate).
2. API idiomatik Go: satu API berbasis `context.Context` (tanpa dualisme sync/async).
3. Aman secara default: whitelist field, parameterized query, anti SQL injection.
4. Siap pakai di handler HTTP: parser query string gaya Strapi bawaan.

**Non-goals (v0.x):**

- Bukan ORM baru — hanya layer builder di atas ORM yang ada.
- Tidak mendukung semua database; fokus PostgreSQL, MySQL, SQLite.
- Tidak ada caching hasil query (cukup caching metadata schema).
- Gunakan bahasa inggris untuk menulis keseluruhan dokumen

---

## 2. Keputusan Desain

### 2.1 Target ORM

| Opsi | Kelebihan | Kekurangan | Keputusan |
|---|---|---|---|
| **GORM** | Paling populer, `Preload`/`Select`/`Scopes` cocok untuk pola builder | Reflection-heavy | ✅ **Pilihan utama (v0.1)** |
| Bun | Dekat ke gaya SQLAlchemy Core 2.0 | Komunitas lebih kecil | Adapter menyusul (v0.3+) |
| database/sql + squirrel | Paling fleksibel | Effort terbesar, tanpa relasi otomatis | Tidak (dulu) |

Desain internal memakai interface `Adapter` sejak awal agar tidak lock-in ke GORM.

### 2.2 Perbedaan Fundamental dari Versi Python

| Aspek | Python (strapalchemy) | Go (strapgorm) |
|---|---|---|
| Sync vs Async | Dua kelas (`Paginator` / `SyncPaginator`) | Satu API + `context.Context` |
| Serialization | `ModelSerializer` | Struct tag `json` + helper field selection |
| Searchable fields | `__searchable__` dict di model | Interface `Searchable() []string` |
| Introspeksi model | SQLAlchemy mapper | Reflection + cache metadata (`sync.Map`) |
| Tipe generik | Duck typing | Generics: `Builder[T any]` |
| Input utama | Dict Python | Query string URL → parser bawaan |

### 2.3 Nama & Module Path

- Nama paket: `strapgorm` (alternatif: `straporm`, `strapql`)
- Module: `github.com/faisalcayunda/strapgorm`

---

## 3. Arsitektur & Struktur Repo

```
strapgorm/
├── go.mod
├── strapgorm.go        # Facade: QueryBuilder[T] yang merangkai semua komponen
├── schema/             # Introspeksi model: kolom, relasi, tipe; di-cache per model
│   └── schema.go
├── parser/             # Query string gaya Strapi → struct QueryParams
│   └── parser.go       # filters[name][$contains]=John&sort=name:asc&pagination[page]=1
├── filter/             # FilterBuilder + operator registry (extensible)
│   ├── filter.go
│   └── operators.go
├── sort/               # SortBuilder (multi-field, relasi via dot notation)
├── paginate/           # Paginator: page-based & offset-based + meta
├── search/             # SearchEngine: ILIKE default, ParadeDB BM25 opsional
├── fields/             # FieldSelector (Select dengan whitelist)
├── populate/           # PopulationBuilder (Preload, nested: user.role)
├── internal/joins/     # Registry alias JOIN (deduplikasi join filter+sort)
├── examples/
│   ├── gin/
│   ├── fiber/
│   └── echo/
└── .github/workflows/  # CI: lint, test, coverage, release tag
```

### 3.1 API Publik (Target)

```go
// Di handler HTTP
params, err := parser.FromURL(r.URL.Query())

users, meta, err := strapgorm.New[User](db).
    Filters(params.Filters).
    Sort(params.Sort).
    Search(params.Search).
    Fields(params.Fields).
    Populate(params.Populate).
    Paginate(ctx, params.Pagination)

// meta: { page, page_size, page_count, total, has_next, has_previous }
```

```go
// Model dengan searchable fields
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

---

## 4. Cakupan Fitur (Paritas v0.2.5)

### 4.1 Operator Filter (17 operator)

| Operator | Deskripsi | SQL (parameterized) |
|---|---|---|
| `$eq` | Sama dengan | `col = ?` |
| `$ne` | Tidak sama | `col <> ?` |
| `$lt` / `$lte` | Kurang dari (atau sama) | `col < ?` / `col <= ?` |
| `$gt` / `$gte` | Lebih dari (atau sama) | `col > ?` / `col >= ?` |
| `$in` / `$notIn` | Dalam / di luar list | `col IN (?)` / `col NOT IN (?)` |
| `$contains` | Mengandung | `col LIKE '%' \|\| ? \|\| '%'` |
| `$containsi` | Mengandung (case-insensitive) | `col ILIKE ...` / `LOWER(col) LIKE ...` |
| `$startsWith` / `$endsWith` | Diawali / diakhiri | `LIKE ? \|\| '%'` / `LIKE '%' \|\| ?` |
| `$null` / `$notNull` | NULL / NOT NULL | `col IS (NOT) NULL` |
| `$between` | Di antara dua nilai | `col BETWEEN ? AND ?` |
| `$or` / `$and` | Logika OR / AND (nested) | grouping `(...)` |

Operator disimpan dalam **registry** sehingga user bisa mendaftarkan operator custom.

### 4.2 Fitur Lain

- **Nested relationship filtering**: `organization.slug` → JOIN otomatis + dedup alias.
- **Sorting**: `name:asc`, multi-field, termasuk field relasi.
- **Pagination**: mode `{page, page_size}` dan `{start, limit}`; count via query terpisah (strip ORDER BY/LIMIT).
- **Search**: ILIKE multi-kolom OR; deteksi dialek Postgres + ParadeDB → BM25.
- **Field selection**: `["id", "name", "organization.slug"]` → `Select()` tervalidasi.
- **Populate**: `Preload` nested; opsi JOIN untuk relasi to-one.

---

## 5. Fase Pengerjaan

### Fase 1 — Fondasi (Minggu 1)

- [ ] Init module, struktur folder, lisensi MIT
- [ ] CI GitHub Actions: `golangci-lint`, `go test -race -cover`
- [ ] Package `schema`: introspeksi model GORM (JSON name ↔ kolom DB ↔ relasi), cache `sync.Map`
- [ ] Whitelist field berbasis schema (fondasi keamanan semua komponen)

### Fase 2 — Filtering (Minggu 1–2)

- [ ] Operator registry + 15 operator skalar
- [ ] `$or` / `$and` nested dengan grouping benar
- [ ] Konversi tipe nilai (string → int/bool/time) berdasarkan metadata kolom; multi-layout `time.Parse` (padanan python-dateutil)
- [ ] Nested filtering dot notation → JOIN otomatis
- [ ] Table-driven test per operator

### Fase 3 — Sorting & Pagination (Minggu 2)

- [ ] `SortBuilder`: parsing `field:asc|desc`, multi-field, field relasi
- [ ] `Paginator`: dua mode + struct `Meta` lengkap
- [ ] Optimasi count query
- [ ] Registry alias JOIN bersama (hindari join ganda filter+sort)

### Fase 4 — Search, Fields, Populate (Minggu 3)

- [ ] `SearchEngine`: ILIKE fallback, interface `Searchable()`
- [ ] Driver ParadeDB BM25 (build tag / deteksi runtime, opsional)
- [ ] `FieldSelector` dengan validasi whitelist
- [ ] `PopulationBuilder`: Preload nested

### Fase 5 — Parser & Integrasi (Minggu 3–4)

- [ ] `parser.FromURL`: `filters[x][$op]=v`, `sort=`, `pagination[page]=`, `fields=`, `populate=`
- [ ] Facade `strapgorm.New[T](db)` chainable
- [ ] Contoh lengkap Gin, Fiber, Echo
- [ ] Fuzz test parser (input tidak terpercaya dari user)

### Fase 6 — QA, Dokumentasi, Rilis (Minggu 4)

- [ ] Integration test: SQLite in-memory + Postgres via testcontainers
- [ ] Benchmark vs raw GORM (target overhead < 5–10%)
- [ ] README: tabel operator, quick start, contoh framework
- [ ] Godoc lengkap, CHANGELOG.md
- [ ] Tag `v0.1.0` → otomatis terindeks pkg.go.dev

---

## 6. Risiko & Mitigasi

| Risiko | Dampak | Mitigasi |
|---|---|---|
| SQL injection via nama field dari query string | Kritis | Whitelist ketat dari schema; tolak field tak dikenal dengan error eksplisit |
| Reflection lambat / rapuh | Sedang | Introspeksi sekali per model, cache `sync.Map`; benchmark di CI |
| Semua nilai query string bertipe string | Sedang | Konversi tipe berdasarkan metadata kolom + error jelas saat gagal parse |
| JOIN ganda (filter + sort pada relasi sama) | Sedang | Registry alias join per query |
| Lock-in GORM | Rendah | Interface `Adapter` sejak v0.1 walau implementasi tunggal |
| Perbedaan dialek (ILIKE hanya Postgres) | Rendah | Abstraksi dialek: `LOWER(col) LIKE` untuk MySQL/SQLite |

---

## 7. Definisi MVP & Prioritas

**MVP (v0.1.0)** — cukup Fase 1–3 + parser dasar: filter, sort, pagination menutup ±80% use case (mengikuti evolusi changelog versi Python: search/populate datang belakangan).

**Roadmap setelahnya:**

- `v0.2.0` — search, field selection, populate, contoh framework
- `v0.3.0` — adapter Bun, operator custom publik, dokumentasi situs
- `v1.0.0` — API stabil setelah feedback komunitas

---

## 8. Definition of Done

- Coverage unit test ≥ 80% pada package inti (`filter`, `parser`, `paginate`)
- Semua operator lulus table-driven test di 3 database (SQLite, Postgres, MySQL)
- Fuzz test parser berjalan tanpa panic
- Lint bersih (`golangci-lint`), `go vet` lolos, CI hijau
- README + godoc memungkinkan adopsi tanpa membaca source code