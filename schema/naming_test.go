package schema

import "testing"

func TestToSnakeCase(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"ID", "id"},
		{"Name", "name"},
		{"UserID", "user_id"},
		{"OrganizationID", "organization_id"},
		{"FirstName", "first_name"},
		{"HTTPServer", "http_server"},
		{"APIKey", "api_key"},
		{"OAuth2Token", "o_auth2_token"},
		{"already_snake", "already_snake"},
		{"A", "a"},
		{"ABC", "abc"},
		{"createdAt", "created_at"},
		{"HTML", "html"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := ToSnakeCase(tt.in); got != tt.want {
				t.Errorf("ToSnakeCase(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestPluralize(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"user", "users"},
		{"category", "categories"},
		{"company", "companies"},
		{"box", "boxes"},
		{"class", "classes"},
		{"quiz", "quizzes"},
		{"branch", "branches"},
		{"dish", "dishes"},
		{"day", "days"}, // vowel + y stays as "...ys"
		{"key", "keys"}, // vowel + y
		{"audit_log", "audit_logs"},
		{"user_role", "user_roles"},
		{"organization", "organizations"},
		{"address", "addresses"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := Pluralize(tt.in); got != tt.want {
				t.Errorf("Pluralize(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestTableName(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"User", "users"},
		{"Organization", "organizations"},
		{"Category", "categories"},
		{"AuditLog", "audit_logs"},
		{"Address", "addresses"},
		{"Company", "companies"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := TableName(tt.in); got != tt.want {
				t.Errorf("TableName(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
