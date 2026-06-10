package parser

import (
	"errors"
	"net/url"
	"reflect"
	"testing"

	"github.com/faisalcayunda/strapgorm/paginate"
)

// mustParseQuery decodes rawQuery via url.ParseQuery and fails the test on error.
func mustParseQuery(t *testing.T, rawQuery string) url.Values {
	t.Helper()
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		t.Fatalf("url.ParseQuery(%q) returned error: %v", rawQuery, err)
	}
	return values
}

// --- Filters -------------------------------------------------------------

func TestFromURL_Filters(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  map[string]any
	}{
		{
			name:  "single operator",
			query: "filters[name][$contains]=John",
			want: map[string]any{
				"name": map[string]any{"$contains": "John"},
			},
		},
		{
			name:  "scalar leaf without operator",
			query: "filters[name]=John",
			want: map[string]any{
				"name": "John",
			},
		},
		{
			name:  "multiple fields and operators",
			query: "filters[name][$contains]=John&filters[age][$gte]=18",
			want: map[string]any{
				"name": map[string]any{"$contains": "John"},
				"age":  map[string]any{"$gte": "18"},
			},
		},
		{
			name:  "nested relation filter",
			query: "filters[organization][slug][$eq]=acme",
			want: map[string]any{
				"organization": map[string]any{
					"slug": map[string]any{"$eq": "acme"},
				},
			},
		},
		{
			name:  "logical $or array of objects",
			query: "filters[$or][0][name][$eq]=a&filters[$or][1][name][$eq]=b",
			want: map[string]any{
				"$or": []any{
					map[string]any{"name": map[string]any{"$eq": "a"}},
					map[string]any{"name": map[string]any{"$eq": "b"}},
				},
			},
		},
		{
			name:  "logical $and array of objects",
			query: "filters[$and][0][age][$gte]=18&filters[$and][1][age][$lte]=65",
			want: map[string]any{
				"$and": []any{
					map[string]any{"age": map[string]any{"$gte": "18"}},
					map[string]any{"age": map[string]any{"$lte": "65"}},
				},
			},
		},
		{
			name:  "or array preserves index order regardless of key order",
			query: "filters[$or][1][name][$eq]=b&filters[$or][0][name][$eq]=a",
			want: map[string]any{
				"$or": []any{
					map[string]any{"name": map[string]any{"$eq": "a"}},
					map[string]any{"name": map[string]any{"$eq": "b"}},
				},
			},
		},
		{
			name:  "between two-element array",
			query: "filters[age][$between][0]=18&filters[age][$between][1]=65",
			want: map[string]any{
				"age": map[string]any{
					"$between": []any{"18", "65"},
				},
			},
		},
		{
			name:  "in list array",
			query: "filters[id][$in][0]=1&filters[id][$in][1]=2&filters[id][$in][2]=3",
			want: map[string]any{
				"id": map[string]any{
					"$in": []any{"1", "2", "3"},
				},
			},
		},
		{
			name:  "null operator with bare value",
			query: "filters[deletedAt][$null]=true",
			want: map[string]any{
				"deletedAt": map[string]any{"$null": "true"},
			},
		},
		{
			name:  "nested $or under a field",
			query: "filters[$or][0][name][$eq]=a&filters[$or][1][organization][slug][$eq]=acme",
			want: map[string]any{
				"$or": []any{
					map[string]any{"name": map[string]any{"$eq": "a"}},
					map[string]any{"organization": map[string]any{"slug": map[string]any{"$eq": "acme"}}},
				},
			},
		},
		{
			name:  "no filters yields nil",
			query: "sort=name:asc",
			want:  nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := FromURL(mustParseQuery(t, tc.query))
			if err != nil {
				t.Fatalf("FromURL(%q) returned error: %v", tc.query, err)
			}
			if !reflect.DeepEqual(got.Filters, tc.want) {
				t.Errorf("Filters mismatch for %q\n got: %#v\nwant: %#v", tc.query, got.Filters, tc.want)
			}
		})
	}
}

func TestFromURL_FiltersMalformed(t *testing.T) {
	tests := []struct {
		name  string
		query string
	}{
		{name: "empty bracket", query: "filters[]=x"},
		{name: "empty nested bracket", query: "filters[name][]=x"},
		{name: "scalar then nested conflict", query: "filters[a]=x&filters[a][b]=y"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := FromURL(mustParseQuery(t, tc.query))
			if err == nil {
				t.Fatalf("FromURL(%q) expected error, got nil", tc.query)
			}
			if !errors.Is(err, ErrMalformedQuery) {
				t.Errorf("FromURL(%q) error = %v, want wrapped ErrMalformedQuery", tc.query, err)
			}
		})
	}
}

// --- Sort ----------------------------------------------------------------

func TestFromURL_Sort(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  []string
	}{
		{name: "single field colon direction", query: "sort=name:asc", want: []string{"name:asc"}},
		{name: "comma separated", query: "sort=name:asc,age:desc", want: []string{"name:asc", "age:desc"}},
		{name: "indexed form", query: "sort[0]=name:asc&sort[1]=createdAt:desc", want: []string{"name:asc", "createdAt:desc"}},
		{name: "indexed reversed order", query: "sort[1]=b:asc&sort[0]=a:asc", want: []string{"a:asc", "b:asc"}},
		{name: "bare field no direction", query: "sort=name", want: []string{"name"}},
		{name: "relation dot notation", query: "sort=organization.slug:asc", want: []string{"organization.slug:asc"}},
		{name: "blanks dropped", query: "sort=name:asc,,age:desc", want: []string{"name:asc", "age:desc"}},
		{name: "absent yields nil", query: "fields=id", want: nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := FromURL(mustParseQuery(t, tc.query))
			if err != nil {
				t.Fatalf("FromURL(%q) returned error: %v", tc.query, err)
			}
			if !reflect.DeepEqual(got.Sort, tc.want) {
				t.Errorf("Sort mismatch for %q\n got: %#v\nwant: %#v", tc.query, got.Sort, tc.want)
			}
		})
	}
}

// --- Fields --------------------------------------------------------------

func TestFromURL_Fields(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  []string
	}{
		{name: "comma form", query: "fields=id,name", want: []string{"id", "name"}},
		{name: "indexed form", query: "fields[0]=id&fields[1]=name", want: []string{"id", "name"}},
		{name: "single", query: "fields=id", want: []string{"id"}},
		{name: "relation qualified kept verbatim", query: "fields=id,organization.slug", want: []string{"id", "organization.slug"}},
		{name: "absent yields nil", query: "sort=id", want: nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := FromURL(mustParseQuery(t, tc.query))
			if err != nil {
				t.Fatalf("FromURL(%q) returned error: %v", tc.query, err)
			}
			if !reflect.DeepEqual(got.Fields, tc.want) {
				t.Errorf("Fields mismatch for %q\n got: %#v\nwant: %#v", tc.query, got.Fields, tc.want)
			}
		})
	}
}

// --- Populate ------------------------------------------------------------

func TestFromURL_Populate(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  []string
	}{
		{name: "comma form", query: "populate=organization,role", want: []string{"organization", "role"}},
		{name: "indexed form", query: "populate[0]=organization", want: []string{"organization"}},
		{name: "wildcard", query: "populate=*", want: []string{"*"}},
		{name: "wildcard wins over others", query: "populate=organization,*,role", want: []string{"*"}},
		{name: "absent yields nil", query: "sort=id", want: nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := FromURL(mustParseQuery(t, tc.query))
			if err != nil {
				t.Fatalf("FromURL(%q) returned error: %v", tc.query, err)
			}
			if !reflect.DeepEqual(got.Populate, tc.want) {
				t.Errorf("Populate mismatch for %q\n got: %#v\nwant: %#v", tc.query, got.Populate, tc.want)
			}
		})
	}
}

// --- Search --------------------------------------------------------------

func TestFromURL_Search(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  string
	}{
		{name: "search key", query: "search=foo", want: "foo"},
		{name: "underscore q key", query: "_q=bar", want: "bar"},
		{name: "_q takes precedence", query: "search=foo&_q=bar", want: "bar"},
		{name: "trimmed", query: "search=%20foo%20", want: "foo"},
		{name: "absent yields empty", query: "sort=id", want: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := FromURL(mustParseQuery(t, tc.query))
			if err != nil {
				t.Fatalf("FromURL(%q) returned error: %v", tc.query, err)
			}
			if got.Search != tc.want {
				t.Errorf("Search mismatch for %q: got %q want %q", tc.query, got.Search, tc.want)
			}
		})
	}
}

// --- Pagination ----------------------------------------------------------

func TestFromURL_Pagination(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  paginate.Params
	}{
		{
			name:  "page mode bracketed",
			query: "pagination[page]=2&pagination[pageSize]=10",
			want:  paginate.Params{Mode: paginate.ModePage, Page: 2, PageSize: 10}.Normalize(),
		},
		{
			name:  "offset mode bracketed",
			query: "pagination[start]=20&pagination[limit]=10",
			want:  paginate.Params{Mode: paginate.ModeOffset, Start: 20, Limit: 10}.Normalize(),
		},
		{
			name:  "withCount true",
			query: "pagination[page]=1&pagination[withCount]=true",
			want:  paginate.Params{Mode: paginate.ModePage, Page: 1, WithCount: true}.Normalize(),
		},
		{
			name:  "withCount false",
			query: "pagination[page]=1&pagination[withCount]=false",
			want:  paginate.Params{Mode: paginate.ModePage, Page: 1, WithCount: false}.Normalize(),
		},
		{
			name:  "top-level page aliases",
			query: "page=3&pageSize=15",
			want:  paginate.Params{Mode: paginate.ModePage, Page: 3, PageSize: 15}.Normalize(),
		},
		{
			name:  "default page mode when absent",
			query: "sort=id",
			want:  paginate.Params{Mode: paginate.ModePage}.Normalize(),
		},
		{
			name:  "offset mode only limit present",
			query: "pagination[limit]=50",
			want:  paginate.Params{Mode: paginate.ModeOffset, Limit: 50}.Normalize(),
		},
		{
			name:  "page size clamped above max",
			query: "pagination[page]=1&pagination[pageSize]=9999",
			want:  paginate.Params{Mode: paginate.ModePage, Page: 1, PageSize: paginate.MaxPageSize}.Normalize(),
		},
		{
			name:  "invalid numbers fall back to defaults",
			query: "pagination[page]=abc&pagination[pageSize]=xyz",
			want:  paginate.Params{Mode: paginate.ModePage}.Normalize(),
		},
		{
			name:  "bracketed page overrides bare alias",
			query: "pagination[page]=2&page=9",
			want:  paginate.Params{Mode: paginate.ModePage, Page: 2}.Normalize(),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := FromURL(mustParseQuery(t, tc.query))
			if err != nil {
				t.Fatalf("FromURL(%q) returned error: %v", tc.query, err)
			}
			if !reflect.DeepEqual(got.Pagination, tc.want) {
				t.Errorf("Pagination mismatch for %q\n got: %#v\nwant: %#v", tc.query, got.Pagination, tc.want)
			}
		})
	}
}

// --- Combined / end-to-end ----------------------------------------------

func TestFromURL_Combined(t *testing.T) {
	query := "filters[name][$contains]=John&filters[age][$gte]=18" +
		"&sort=name:asc,createdAt:desc" +
		"&pagination[page]=2&pagination[pageSize]=10&pagination[withCount]=true" +
		"&fields=id,name" +
		"&populate=organization,role" +
		"&_q=alice"

	got, err := FromURL(mustParseQuery(t, query))
	if err != nil {
		t.Fatalf("FromURL returned error: %v", err)
	}

	wantFilters := map[string]any{
		"name": map[string]any{"$contains": "John"},
		"age":  map[string]any{"$gte": "18"},
	}
	if !reflect.DeepEqual(got.Filters, wantFilters) {
		t.Errorf("Filters mismatch\n got: %#v\nwant: %#v", got.Filters, wantFilters)
	}
	if want := []string{"name:asc", "createdAt:desc"}; !reflect.DeepEqual(got.Sort, want) {
		t.Errorf("Sort mismatch: got %#v want %#v", got.Sort, want)
	}
	if want := []string{"id", "name"}; !reflect.DeepEqual(got.Fields, want) {
		t.Errorf("Fields mismatch: got %#v want %#v", got.Fields, want)
	}
	if want := []string{"organization", "role"}; !reflect.DeepEqual(got.Populate, want) {
		t.Errorf("Populate mismatch: got %#v want %#v", got.Populate, want)
	}
	if got.Search != "alice" {
		t.Errorf("Search mismatch: got %q want %q", got.Search, "alice")
	}
	wantPagination := paginate.Params{
		Mode: paginate.ModePage, Page: 2, PageSize: 10, WithCount: true,
	}.Normalize()
	if !reflect.DeepEqual(got.Pagination, wantPagination) {
		t.Errorf("Pagination mismatch\n got: %#v\nwant: %#v", got.Pagination, wantPagination)
	}
}

func TestFromURL_UnknownKeysIgnored(t *testing.T) {
	got, err := FromURL(mustParseQuery(t, "totallyUnknown=1&another[thing]=2&sort=name"))
	if err != nil {
		t.Fatalf("FromURL returned error: %v", err)
	}
	if want := []string{"name"}; !reflect.DeepEqual(got.Sort, want) {
		t.Errorf("Sort mismatch: got %#v want %#v", got.Sort, want)
	}
	if got.Filters != nil {
		t.Errorf("Filters should be nil, got %#v", got.Filters)
	}
}

func TestFromValues_EquivalentToFromURL(t *testing.T) {
	query := "filters[name][$eq]=x&sort=a:asc&page=2"
	values := mustParseQuery(t, query)

	fromURL, err := FromURL(values)
	if err != nil {
		t.Fatalf("FromURL error: %v", err)
	}
	fromValues, err := FromValues(map[string][]string(values))
	if err != nil {
		t.Fatalf("FromValues error: %v", err)
	}
	if !reflect.DeepEqual(fromURL, fromValues) {
		t.Errorf("FromURL and FromValues diverged\n url:    %#v\n values: %#v", fromURL, fromValues)
	}
}

func TestFromURL_EmptyInput(t *testing.T) {
	got, err := FromURL(url.Values{})
	if err != nil {
		t.Fatalf("FromURL(empty) returned error: %v", err)
	}
	if got.Filters != nil || got.Sort != nil || got.Fields != nil || got.Populate != nil || got.Search != "" {
		t.Errorf("empty input should yield zero concerns, got %#v", got)
	}
	want := paginate.Params{Mode: paginate.ModePage}.Normalize()
	if !reflect.DeepEqual(got.Pagination, want) {
		t.Errorf("Pagination for empty input: got %#v want %#v", got.Pagination, want)
	}
}

// --- Unit-level helper coverage -----------------------------------------

func TestSplitKey(t *testing.T) {
	tests := []struct {
		raw      string
		wantTop  string
		wantPath []string
	}{
		{raw: "sort", wantTop: "sort", wantPath: nil},
		{raw: "filters[name]", wantTop: "filters", wantPath: []string{"name"}},
		{raw: "filters[name][$contains]", wantTop: "filters", wantPath: []string{"name", "$contains"}},
		{raw: "filters[$or][0][name][$eq]", wantTop: "filters", wantPath: []string{"$or", "0", "name", "$eq"}},
		{raw: "pagination[page]", wantTop: "pagination", wantPath: []string{"page"}},
	}
	for _, tc := range tests {
		t.Run(tc.raw, func(t *testing.T) {
			top, path := splitKey(tc.raw)
			if top != tc.wantTop {
				t.Errorf("top = %q want %q", top, tc.wantTop)
			}
			if !reflect.DeepEqual(path, tc.wantPath) {
				t.Errorf("path = %#v want %#v", path, tc.wantPath)
			}
		})
	}
}

func TestAsOrderedSlice(t *testing.T) {
	tests := []struct {
		name   string
		in     map[string]any
		wantOK bool
		want   []any
	}{
		{name: "contiguous", in: map[string]any{"0": "a", "1": "b"}, wantOK: true, want: []any{"a", "b"}},
		{name: "single", in: map[string]any{"0": "a"}, wantOK: true, want: []any{"a"}},
		{name: "gap", in: map[string]any{"0": "a", "2": "c"}, wantOK: false},
		{name: "non-numeric key", in: map[string]any{"0": "a", "x": "b"}, wantOK: false},
		{name: "empty", in: map[string]any{}, wantOK: false},
		{name: "named object", in: map[string]any{"name": "a"}, wantOK: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := asOrderedSlice(tc.in)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v want %v", ok, tc.wantOK)
			}
			if ok && !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %#v want %#v", got, tc.want)
			}
		})
	}
}

func TestParseBool(t *testing.T) {
	truthy := []string{"true", "1", "yes", "on", "TRUE", "Yes"}
	for _, s := range truthy {
		if !parseBool(s) {
			t.Errorf("parseBool(%q) = false, want true", s)
		}
	}
	falsy := []string{"false", "0", "no", "off", "", "maybe"}
	for _, s := range falsy {
		if parseBool(s) {
			t.Errorf("parseBool(%q) = true, want false", s)
		}
	}
}

// --- Fuzz ----------------------------------------------------------------

// FuzzFromURL feeds arbitrary strings through url.ParseQuery into FromURL and
// asserts that no input causes a panic. Decoding errors are acceptable (and
// expected for malformed bracket nesting); only a panic or an inconsistent
// success/result is a failure.
func FuzzFromURL(f *testing.F) {
	seeds := []string{
		"filters[name][$contains]=John",
		"filters[$or][0][name][$eq]=a&filters[$or][1][name][$eq]=b",
		"sort=name:asc,age:desc",
		"pagination[page]=2&pagination[pageSize]=10",
		"pagination[start]=20&pagination[limit]=10&pagination[withCount]=true",
		"fields=id,name&populate=*",
		"_q=hello&search=world",
		"filters[]=x",
		"filters[a][",
		"filters[a]=x&filters[a][b]=y",
		"][[[]]][=weird&&==",
		"filters[age][$between][0]=18&filters[age][$between][1]=65",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, raw string) {
		values, err := url.ParseQuery(raw)
		if err != nil {
			// Not a valid query string; nothing to decode.
			return
		}
		// Must not panic. The error (if any) is acceptable.
		got, err := FromURL(values)
		if err != nil {
			if !errors.Is(err, ErrMalformedQuery) {
				t.Errorf("FromURL(%q) returned non-sentinel error: %v", raw, err)
			}
			return
		}
		// On success, Pagination is always normalized into a valid range.
		if got.Pagination.PageSize < 0 || got.Pagination.Limit < 0 {
			t.Errorf("FromURL(%q) produced negative pagination bound: %#v", raw, got.Pagination)
		}
	})
}
