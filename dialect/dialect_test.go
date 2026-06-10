package dialect

import "testing"

func TestNames(t *testing.T) {
	tests := []struct {
		d    Dialect
		want string
	}{
		{Postgres, "postgres"},
		{MySQL, "mysql"},
		{SQLite, "sqlite"},
	}
	for _, tt := range tests {
		if got := tt.d.Name(); got != tt.want {
			t.Errorf("Name() = %q, want %q", got, tt.want)
		}
	}
}

func TestQuote(t *testing.T) {
	tests := []struct {
		name  string
		d     Dialect
		ident string
		want  string
	}{
		{"postgres simple", Postgres, "name", `"name"`},
		{"postgres embedded quote", Postgres, `na"me`, `"na""me"`},
		{"sqlite simple", SQLite, "name", `"name"`},
		{"sqlite embedded quote", SQLite, `na"me`, `"na""me"`},
		{"mysql simple", MySQL, "name", "`name`"},
		{"mysql embedded backtick", MySQL, "na`me", "`na``me`"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.d.Quote(tt.ident); got != tt.want {
				t.Errorf("Quote(%q) = %q, want %q", tt.ident, got, tt.want)
			}
		})
	}
}

func TestLike(t *testing.T) {
	tests := []struct {
		name string
		d    Dialect
		col  string
		want string
	}{
		{"postgres", Postgres, "users.name", "users.name LIKE ?"},
		{"mysql", MySQL, "users.name", "users.name LIKE ?"},
		{"sqlite", SQLite, "users.name", "users.name LIKE ?"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.d.Like(tt.col); got != tt.want {
				t.Errorf("Like(%q) = %q, want %q", tt.col, got, tt.want)
			}
		})
	}
}

func TestILike(t *testing.T) {
	tests := []struct {
		name string
		d    Dialect
		col  string
		want string
	}{
		{"postgres uses native ILIKE", Postgres, "users.name", "users.name ILIKE ?"},
		{"mysql falls back to LOWER", MySQL, "users.name", "LOWER(users.name) LIKE LOWER(?)"},
		{"sqlite falls back to LOWER", SQLite, "users.name", "LOWER(users.name) LIKE LOWER(?)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.d.ILike(tt.col); got != tt.want {
				t.Errorf("ILike(%q) = %q, want %q", tt.col, got, tt.want)
			}
		})
	}
}

func TestByName(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"postgres", "postgres"},
		{"postgresql", "postgres"},
		{"pg", "postgres"},
		{"PostgreSQL", "postgres"},
		{"  PG  ", "postgres"},
		{"mysql", "mysql"},
		{"MySQL", "mysql"},
		{"sqlite", "sqlite"},
		{"sqlite3", "sqlite"},
		{"SQLite3", "sqlite"},
		{"", "postgres"},
		{"oracle", "postgres"},
		{"unknown", "postgres"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := ByName(tt.in).Name(); got != tt.want {
				t.Errorf("ByName(%q).Name() = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
