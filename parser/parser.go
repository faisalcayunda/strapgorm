// Package parser decodes Strapi-flavored query strings into a structured
// QueryParams value that the strapgorm builder packages consume.
//
// Strapi (and the underlying qs library) encodes structured data into a flat
// query string using bracket notation. The parser reverses that encoding:
//
//	filters[name][$contains]=John
//	filters[age][$gte]=18
//	filters[organization][slug][$eq]=acme
//	filters[$or][0][name][$eq]=a&filters[$or][1][name][$eq]=b
//	sort=name:asc,createdAt:desc      (or sort[0]=name:asc&sort[1]=...)
//	pagination[page]=2&pagination[pageSize]=10
//	pagination[start]=20&pagination[limit]=10&pagination[withCount]=true
//	fields=id,name                    (or fields[0]=id&fields[1]=name)
//	populate=organization,role        (or populate[0]=organization, or populate=*)
//	search=foo                        (or _q=foo)
//
// # Filters shape
//
// Filters is decoded into the nested representation the filter package expects:
// a map[string]any whose values are either scalar operands (string), nested
// operator maps (map[string]any keyed by "$eq", "$contains", relation names,
// ...), or, under the logical "$or"/"$and" keys, an ordered []any of condition
// objects. Numeric bracket indices ([0], [1], ...) under a key are reassembled
// into an ordered slice; non-numeric keys form a map. This mirrors the object
// that qs would have produced on the client.
//
// # Security
//
// The parser performs no schema resolution and emits no SQL: it only restructures
// untrusted input into a typed shape. Field names, relation names, and operators
// are preserved verbatim as map keys for the downstream builders to validate
// against the schema whitelist; the parser never interpolates any token into SQL.
// It is robust against malformed input: structurally invalid bracket nesting
// yields a wrapped error, unknown top-level keys are ignored, and no input can
// cause a panic.
package parser

import (
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/faisalcayunda/strapgorm/paginate"
)

// ErrMalformedQuery is the sentinel wrapped by every structural decoding error
// the parser reports (for example unbalanced or improperly nested brackets in a
// filters key). Callers may test for it with errors.Is.
var ErrMalformedQuery = errors.New("strapgorm/parser: malformed query")

// QueryParams is the decoded, builder-ready form of a Strapi query string.
//
// Each field corresponds to one Strapi query concern and is consumed by the
// matching strapgorm package: Filters by filter, Sort by sort, Search by search,
// Fields by fields, Populate by populate, and Pagination by paginate. Every field
// is independent; an absent concern yields a nil/zero value (Pagination is always
// normalized, defaulting to page mode).
type QueryParams struct {
	// Filters is the decoded filter object: a nested map[string]any whose leaves
	// are operator maps and whose logical "$or"/"$and" keys hold ordered []any
	// slices. It is nil when no filters were supplied.
	Filters map[string]any
	// Sort is the ordered list of "field" or "field:direction" directives.
	Sort []string
	// Search is the free-text search term (from "search" or Strapi's "_q").
	Search string
	// Fields is the ordered list of requested field json-names for the SELECT
	// projection.
	Fields []string
	// Populate is the ordered list of relation names to populate; the single
	// element "*" requests populating all relations.
	Populate []string
	// Pagination is the normalized pagination request (always valid).
	Pagination paginate.Params
}

// FromURL decodes the Strapi query parameters carried by values (typically
// r.URL.Query()) into a QueryParams.
//
// It is a thin convenience wrapper that converts the url.Values to a plain
// map[string][]string and delegates to FromValues; see FromValues for the full
// decoding contract and error semantics.
func FromURL(values url.Values) (QueryParams, error) {
	return FromValues(map[string][]string(values))
}

// FromValues decodes the Strapi query parameters carried by values into a
// QueryParams.
//
// Recognized top-level keys are filters, sort, pagination, fields, populate,
// search, and _q, plus the bare-key pagination aliases page and pageSize. Keys
// that use bracket notation (for example "filters[name][$contains]" or "sort[0]")
// are grouped by their leading key before being decoded by the concern-specific
// logic. Unknown top-level keys are ignored.
//
// Pagination is always populated and normalized: when no pagination input is
// present it defaults to page mode with the package defaults. A structurally
// invalid filters key (malformed bracket nesting) yields a wrapped
// ErrMalformedQuery; all other concerns are tolerant and never fail. On error a
// zero QueryParams and the error are returned.
func FromValues(values map[string][]string) (QueryParams, error) {
	var out QueryParams

	// Group raw entries by their leading top-level key so each concern can be
	// decoded independently. Each grouped entry retains its full bracket path
	// (the segments after the leading key) and its values.
	grouped := groupByTopKey(values)

	// --- filters -------------------------------------------------------------
	if entries, ok := grouped["filters"]; ok {
		filters, err := decodeFilters(entries)
		if err != nil {
			return QueryParams{}, err
		}
		out.Filters = filters
	}

	// --- sort ----------------------------------------------------------------
	out.Sort = decodeList(grouped["sort"])

	// --- fields --------------------------------------------------------------
	out.Fields = decodeList(grouped["fields"])

	// --- populate ------------------------------------------------------------
	out.Populate = decodePopulate(grouped["populate"])

	// --- search / _q ---------------------------------------------------------
	out.Search = decodeSearch(values)

	// --- pagination ----------------------------------------------------------
	out.Pagination = decodePagination(grouped["pagination"], values).Normalize()

	return out, nil
}

// entry is a single decoded query parameter belonging to one top-level concern:
// the bracket path beneath the leading key plus the raw values for that key.
//
// For "filters[$or][0][name][$eq]=a" grouped under the "filters" concern, path is
// {"$or", "0", "name", "$eq"} and values is {"a"}. For a bare "sort=name:asc",
// path is empty and values is {"name:asc"}.
type entry struct {
	// path is the ordered list of bracket segments after the leading key.
	path []string
	// values is the list of raw string values associated with the key.
	values []string
}

// groupByTopKey splits each raw query key into its leading top-level key and the
// bracket path beneath it, grouping the resulting entries by top-level key.
//
// A key such as "filters[name][$contains]" yields top key "filters" and path
// {"name", "$contains"}; a bare key such as "sort" yields top key "sort" and an
// empty path. Keys with unbalanced or malformed brackets are still grouped using
// a best-effort split so that the concern-specific decoder can decide whether the
// malformation matters (only filters treats it as an error). The relative order
// of entries within a group follows a deterministic sort of the original keys so
// decoding is reproducible regardless of map iteration order.
func groupByTopKey(values map[string][]string) map[string][]entry {
	// Sort keys for deterministic grouping order (map iteration is random).
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	grouped := make(map[string][]entry, len(values))
	for _, raw := range keys {
		top, path := splitKey(raw)
		grouped[top] = append(grouped[top], entry{path: path, values: values[raw]})
	}
	return grouped
}

// splitKey separates a raw query key into its leading top-level key and the
// ordered list of bracket segments.
//
// "filters[name][$contains]" -> ("filters", {"name", "$contains"}).
// "sort"                      -> ("sort", {}).
// "pagination[page]"          -> ("pagination", {"page"}).
//
// The split is best-effort and never fails: a leading "[" makes the top key
// empty, and any text before the first "[" is the top key. Bracket segments are
// taken between matching "[" and "]"; a missing closing bracket consumes the rest
// of the string as a final segment. Per-segment validity is judged later by the
// concern decoder that needs it.
func splitKey(raw string) (top string, path []string) {
	open := strings.IndexByte(raw, '[')
	if open < 0 {
		return raw, nil
	}
	top = raw[:open]

	rest := raw[open:]
	for len(rest) > 0 {
		if rest[0] != '[' {
			// Stray text between segments (e.g. malformed "a]b["): treat the
			// remainder up to the next '[' as a degenerate segment so nothing is
			// silently dropped; the filters decoder will reject true malformations.
			next := strings.IndexByte(rest, '[')
			if next < 0 {
				path = append(path, rest)
				break
			}
			path = append(path, rest[:next])
			rest = rest[next:]
			continue
		}
		// rest[0] == '['
		close := strings.IndexByte(rest, ']')
		if close < 0 {
			// Unterminated bracket: keep the remaining text (sans '[') as a
			// final segment.
			path = append(path, rest[1:])
			break
		}
		path = append(path, rest[1:close])
		rest = rest[close+1:]
	}
	return top, path
}

// decodeList flattens grouped entries into an ordered, comma-split string list,
// used for the sort and fields concerns.
//
// Both indexed forms (sort[0]=a&sort[1]=b), the bare repeated form
// (fields=id&fields=name), and the comma form (sort=a,b) are supported and may be
// mixed. Indexed entries are ordered by their numeric index; non-indexed entries
// retain their grouped order and follow after indexed ones. Each value is split
// on commas, and blank items (after trimming) are dropped. The result is nil when
// nothing meaningful was supplied.
func decodeList(entries []entry) []string {
	if len(entries) == 0 {
		return nil
	}

	type indexed struct {
		idx int
		val string
	}
	var withIdx []indexed
	var withoutIdx []string

	for _, e := range entries {
		idx, hasIdx := singleNumericIndex(e.path)
		for _, v := range e.values {
			for _, part := range strings.Split(v, ",") {
				part = strings.TrimSpace(part)
				if part == "" {
					continue
				}
				if hasIdx {
					withIdx = append(withIdx, indexed{idx: idx, val: part})
				} else {
					withoutIdx = append(withoutIdx, part)
				}
			}
		}
	}

	sort.SliceStable(withIdx, func(i, j int) bool {
		return withIdx[i].idx < withIdx[j].idx
	})

	out := make([]string, 0, len(withIdx)+len(withoutIdx))
	for _, it := range withIdx {
		out = append(out, it.val)
	}
	out = append(out, withoutIdx...)
	if len(out) == 0 {
		return nil
	}
	return out
}

// decodePopulate decodes the populate concern, which is a list like decodeList
// but with one special case: the wildcard "*" requests populating all relations
// and is preserved as a single-element {"*"} result, taking precedence over any
// other populate entries.
func decodePopulate(entries []entry) []string {
	list := decodeList(entries)
	for _, item := range list {
		if item == "*" {
			return []string{"*"}
		}
	}
	return list
}

// decodeSearch resolves the free-text search term from the raw values, accepting
// both Strapi's "_q" and the conventional "search" key. "_q" takes precedence
// when both are present; the first non-empty value of the chosen key is returned,
// trimmed. The result is "" when neither key carries a value.
func decodeSearch(values map[string][]string) string {
	if v, ok := firstValue(values["_q"]); ok {
		return v
	}
	if v, ok := firstValue(values["search"]); ok {
		return v
	}
	return ""
}

// decodePagination decodes the pagination concern from its bracketed entries
// (pagination[page], pagination[pageSize], pagination[start], pagination[limit],
// pagination[withCount]) with a fallback to the bare top-level page/pageSize keys.
//
// Mode selection follows Strapi: the presence of start or limit selects offset
// mode; otherwise page mode is used. withCount is parsed as a boolean (true/1/yes
// are truthy). Unparseable numbers are left as zero and clamped later by
// paginate.Params.Normalize. The returned Params is not yet normalized.
func decodePagination(entries []entry, values map[string][]string) paginate.Params {
	// Collect the scalar pagination sub-keys from the bracketed entries.
	raw := make(map[string]string, 5)
	for _, e := range entries {
		if len(e.path) != 1 {
			continue
		}
		if v, ok := firstValue(e.values); ok {
			raw[e.path[0]] = v
		}
	}

	// Bare top-level aliases (page=, pageSize=) supplement the bracketed form
	// without overriding an explicit pagination[...] value.
	if _, ok := raw["page"]; !ok {
		if v, ok := firstValue(values["page"]); ok {
			raw["page"] = v
		}
	}
	if _, ok := raw["pageSize"]; !ok {
		if v, ok := firstValue(values["pageSize"]); ok {
			raw["pageSize"] = v
		}
	}

	var p paginate.Params

	_, hasStart := raw["start"]
	_, hasLimit := raw["limit"]
	if hasStart || hasLimit {
		p.Mode = paginate.ModeOffset
		p.Start = atoiOrZero(raw["start"])
		p.Limit = atoiOrZero(raw["limit"])
	} else {
		p.Mode = paginate.ModePage
		p.Page = atoiOrZero(raw["page"])
		p.PageSize = atoiOrZero(raw["pageSize"])
	}

	if v, ok := raw["withCount"]; ok {
		p.WithCount = parseBool(v)
	}

	return p
}

// decodeFilters reassembles the grouped filters entries into the nested object
// the filter package consumes.
//
// Each entry's bracket path is inserted into a shared tree of map[string]any
// nodes keyed by literal segment. After all entries are inserted, the tree is
// normalized: any node whose keys are exactly the contiguous numeric indices
// 0..n-1 is converted into an ordered []any (the representation used for the
// "$or"/"$and" logical arrays), and scalar leaves are stored as their string
// value.
//
// A structurally invalid path (for example an empty segment produced by malformed
// bracket nesting such as "filters[]" or "filters[a][") yields a wrapped
// ErrMalformedQuery. A nil map is returned when there are no usable filter
// entries.
func decodeFilters(entries []entry) (map[string]any, error) {
	root := map[string]any{}

	for _, e := range entries {
		if len(e.path) == 0 {
			// A bare "filters=..." has no structure to decode; Strapi never emits
			// this, so ignore it rather than guessing an interpretation.
			continue
		}
		if err := validatePath(e.path); err != nil {
			return nil, err
		}
		value, ok := firstValue(e.values)
		if !ok {
			value = ""
		}
		if err := insertPath(root, e.path, value); err != nil {
			return nil, err
		}
	}

	if len(root) == 0 {
		return nil, nil
	}

	normalized, ok := normalizeNode(root).(map[string]any)
	if !ok {
		// The root is always a map by construction; defensively guard anyway.
		return nil, nil
	}
	return normalized, nil
}

// validatePath rejects a bracket path that contains an empty segment, which can
// only arise from malformed bracket nesting (for example "filters[]" or a
// trailing "filters[a][" that produced an empty final segment). A wrapped
// ErrMalformedQuery is returned for such paths.
func validatePath(path []string) error {
	for _, seg := range path {
		if seg == "" {
			return fmt.Errorf("%w: empty bracket segment in path %v", ErrMalformedQuery, path)
		}
	}
	return nil
}

// insertPath inserts value at the location addressed by path within the node
// tree rooted at root, creating intermediate map[string]any nodes as needed.
//
// Each intermediate segment must address a nested object; if a prior entry stored
// a scalar at the same location (a structural conflict such as
// "filters[a]=x&filters[a][b]=y"), a wrapped ErrMalformedQuery is returned rather
// than silently overwriting. The final segment stores value as a string leaf.
func insertPath(root map[string]any, path []string, value string) error {
	node := root
	for i := 0; i < len(path)-1; i++ {
		seg := path[i]
		child, exists := node[seg]
		if !exists {
			next := map[string]any{}
			node[seg] = next
			node = next
			continue
		}
		nextMap, ok := child.(map[string]any)
		if !ok {
			return fmt.Errorf("%w: conflicting structure at segment %q in path %v", ErrMalformedQuery, seg, path)
		}
		node = nextMap
	}

	last := path[len(path)-1]
	if existing, exists := node[last]; exists {
		if _, isMap := existing.(map[string]any); isMap {
			return fmt.Errorf("%w: conflicting structure at leaf %q in path %v", ErrMalformedQuery, last, path)
		}
		// A repeated scalar leaf simply takes the latest value; this is benign.
	}
	node[last] = value
	return nil
}

// normalizeNode recursively converts the raw insertion tree into its final shape:
// map nodes whose keys are exactly the contiguous indices 0..n-1 become ordered
// []any slices, all other map nodes are recursed into in place, and scalar leaves
// are returned unchanged. This turns "$or" / "$and" index maps into the ordered
// slices the filter package expects while leaving named object nodes as maps.
func normalizeNode(v any) any {
	m, ok := v.(map[string]any)
	if !ok {
		return v
	}

	// Recurse into children first.
	for k, child := range m {
		m[k] = normalizeNode(child)
	}

	if slice, ok := asOrderedSlice(m); ok {
		return slice
	}
	return m
}

// asOrderedSlice reports whether m's keys are exactly the contiguous numeric
// indices "0".."len-1" and, if so, returns the values ordered by index. A node
// that is empty, has any non-numeric key, has a negative or out-of-range index,
// or has a gap in its indices is not treated as a slice (ok == false), so that
// objects with incidentally-numeric keys are never silently reshaped unless the
// full 0..n-1 sequence is present.
func asOrderedSlice(m map[string]any) (out []any, ok bool) {
	n := len(m)
	if n == 0 {
		return nil, false
	}
	out = make([]any, n)
	seen := make([]bool, n)
	for k, v := range m {
		idx, err := strconv.Atoi(k)
		if err != nil || idx < 0 || idx >= n || seen[idx] {
			return nil, false
		}
		seen[idx] = true
		out[idx] = v
	}
	return out, true
}

// singleNumericIndex reports whether path is a single numeric segment (as in
// "sort[0]" or "fields[2]") and, if so, returns its integer value. Non-numeric or
// multi-segment paths return (0, false).
func singleNumericIndex(path []string) (int, bool) {
	if len(path) != 1 {
		return 0, false
	}
	idx, err := strconv.Atoi(path[0])
	if err != nil || idx < 0 {
		return 0, false
	}
	return idx, true
}

// firstValue returns the first element of vals and true, or ("", false) when vals
// is empty. It does not trim; callers trim where appropriate.
func firstValue(vals []string) (string, bool) {
	if len(vals) == 0 {
		return "", false
	}
	return strings.TrimSpace(vals[0]), true
}

// atoiOrZero parses s as a base-10 integer, returning 0 when s is empty or not a
// valid integer. The downstream paginate.Params.Normalize clamps any zero or
// out-of-range value into a safe bound.
func atoiOrZero(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0
	}
	return n
}

// parseBool interprets a Strapi-style boolean string. "true", "1", "yes", and
// "on" (case-insensitive) are truthy; everything else is false.
func parseBool(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "1", "yes", "on":
		return true
	default:
		return false
	}
}
