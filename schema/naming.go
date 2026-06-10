package schema

import "strings"

// ToSnakeCase converts a Go identifier to snake_case, matching GORM's default
// NamingStrategy closely enough for column and table derivation.
//
// It lowercases the string while inserting an underscore before each new word
// boundary. Runs of uppercase letters are treated as acronyms: a boundary is
// inserted before the final uppercase letter of an acronym that is followed by
// a lowercase letter (so "HTTPServer" becomes "http_server" and "UserID"
// becomes "user_id"). Digits do not introduce additional underscores beyond an
// existing letter/letter boundary.
func ToSnakeCase(s string) string {
	if s == "" {
		return ""
	}
	runes := []rune(s)
	var b strings.Builder
	b.Grow(len(runes) + 4)

	for i, r := range runes {
		if isUpper(r) {
			if i > 0 {
				prev := runes[i-1]
				next := rune(0)
				if i+1 < len(runes) {
					next = runes[i+1]
				}
				// Boundary when the previous rune is not uppercase
				// (lower/digit -> Upper), or when leaving an acronym into a
				// lowercase word (Upper followed by lower).
				if !isUpper(prev) || (isUpper(prev) && isLower(next)) {
					b.WriteByte('_')
				}
			}
			b.WriteRune(toLower(r))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// TableName returns the GORM-style table name for a struct named structName:
// the pluralized snake_case of the name.
//
// Pluralization is a best-effort fallback (see the package documentation):
// words ending in a consonant + "y" become "...ies"; words ending in "s", "x",
// "z", "ch", or "sh" gain "es"; all other words gain "s".
func TableName(structName string) string {
	return Pluralize(ToSnakeCase(structName))
}

// Pluralize returns a best-effort English plural of the snake_case word.
//
// Only the final word segment (after the last underscore) is pluralized, so
// "audit_log" becomes "audit_logs". The rules are: consonant + "y" -> "ies";
// trailing "s", "x", "z", "ch", or "sh" -> "es"; otherwise -> "s". This is a
// documented fallback; authoritative pluralization comes from the GORM adapter.
func Pluralize(word string) string {
	if word == "" {
		return ""
	}

	prefix := ""
	tail := word
	if idx := strings.LastIndex(word, "_"); idx >= 0 {
		prefix = word[:idx+1]
		tail = word[idx+1:]
	}
	if tail == "" {
		return word + "s"
	}

	lower := tail
	switch {
	case strings.HasSuffix(lower, "y") && len(lower) >= 2 && !isVowel(rune(lower[len(lower)-2])):
		return prefix + lower[:len(lower)-1] + "ies"
	case strings.HasSuffix(lower, "ch"),
		strings.HasSuffix(lower, "sh"),
		strings.HasSuffix(lower, "s"),
		strings.HasSuffix(lower, "x"),
		strings.HasSuffix(lower, "z"):
		return prefix + lower + "es"
	default:
		return prefix + lower + "s"
	}
}

func isUpper(r rune) bool { return r >= 'A' && r <= 'Z' }
func isLower(r rune) bool { return r >= 'a' && r <= 'z' }
func toLower(r rune) rune {
	if isUpper(r) {
		return r + ('a' - 'A')
	}
	return r
}

func isVowel(r rune) bool {
	switch r {
	case 'a', 'e', 'i', 'o', 'u', 'A', 'E', 'I', 'O', 'U':
		return true
	default:
		return false
	}
}
