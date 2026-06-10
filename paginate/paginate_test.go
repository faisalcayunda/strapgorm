package paginate

import "testing"

func TestNormalizePageMode(t *testing.T) {
	tests := []struct {
		name         string
		in           Params
		wantPage     int
		wantPageSize int
	}{
		{"defaults from zero value", Params{Mode: ModePage}, 1, DefaultPageSize},
		{"negative page clamped to 1", Params{Mode: ModePage, Page: -3, PageSize: 10}, 1, 10},
		{"zero page clamped to 1", Params{Mode: ModePage, Page: 0, PageSize: 10}, 1, 10},
		{"pagesize zero -> default", Params{Mode: ModePage, Page: 2, PageSize: 0}, 2, DefaultPageSize},
		{"pagesize over cap clamped", Params{Mode: ModePage, Page: 2, PageSize: 5000}, 2, MaxPageSize},
		{"pagesize negative -> default", Params{Mode: ModePage, Page: 2, PageSize: -1}, 2, DefaultPageSize},
		{"valid values preserved", Params{Mode: ModePage, Page: 4, PageSize: 50}, 4, 50},
		{"max boundary preserved", Params{Mode: ModePage, Page: 1, PageSize: MaxPageSize}, 1, MaxPageSize},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.in.Normalize()
			if got.Mode != ModePage {
				t.Errorf("Mode = %d, want ModePage", got.Mode)
			}
			if got.Page != tt.wantPage {
				t.Errorf("Page = %d, want %d", got.Page, tt.wantPage)
			}
			if got.PageSize != tt.wantPageSize {
				t.Errorf("PageSize = %d, want %d", got.PageSize, tt.wantPageSize)
			}
		})
	}
}

func TestNormalizeOffsetMode(t *testing.T) {
	tests := []struct {
		name      string
		in        Params
		wantStart int
		wantLimit int
	}{
		{"defaults", Params{Mode: ModeOffset}, 0, DefaultPageSize},
		{"negative start clamped", Params{Mode: ModeOffset, Start: -5, Limit: 10}, 0, 10},
		{"limit zero -> default", Params{Mode: ModeOffset, Start: 20, Limit: 0}, 20, DefaultPageSize},
		{"limit over cap clamped", Params{Mode: ModeOffset, Start: 20, Limit: 9999}, 20, MaxPageSize},
		{"valid preserved", Params{Mode: ModeOffset, Start: 30, Limit: 15}, 30, 15},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.in.Normalize()
			if got.Mode != ModeOffset {
				t.Errorf("Mode = %d, want ModeOffset", got.Mode)
			}
			if got.Start != tt.wantStart {
				t.Errorf("Start = %d, want %d", got.Start, tt.wantStart)
			}
			if got.Limit != tt.wantLimit {
				t.Errorf("Limit = %d, want %d", got.Limit, tt.wantLimit)
			}
		})
	}
}

func TestNormalizePreservesWithCount(t *testing.T) {
	got := Params{Mode: ModePage, WithCount: true}.Normalize()
	if !got.WithCount {
		t.Error("WithCount not preserved")
	}
}

func TestLimitOffset(t *testing.T) {
	tests := []struct {
		name       string
		in         Params
		wantLimit  int
		wantOffset int
	}{
		{"page 1", Params{Mode: ModePage, Page: 1, PageSize: 25}, 25, 0},
		{"page 2", Params{Mode: ModePage, Page: 2, PageSize: 25}, 25, 25},
		{"page 3 size 10", Params{Mode: ModePage, Page: 3, PageSize: 10}, 10, 20},
		{"page zero normalized", Params{Mode: ModePage, Page: 0, PageSize: 10}, 10, 0},
		{"offset basic", Params{Mode: ModeOffset, Start: 40, Limit: 20}, 20, 40},
		{"offset negative start", Params{Mode: ModeOffset, Start: -10, Limit: 20}, 20, 0},
		{"offset default limit", Params{Mode: ModeOffset, Start: 5}, DefaultPageSize, 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			limit, offset := tt.in.LimitOffset()
			if limit != tt.wantLimit {
				t.Errorf("limit = %d, want %d", limit, tt.wantLimit)
			}
			if offset != tt.wantOffset {
				t.Errorf("offset = %d, want %d", offset, tt.wantOffset)
			}
		})
	}
}

func TestBuildMetaPageMode(t *testing.T) {
	tests := []struct {
		name  string
		in    Params
		total int64
		want  Meta
	}{
		{
			name:  "first page of many",
			in:    Params{Mode: ModePage, Page: 1, PageSize: 10},
			total: 95,
			want:  Meta{Page: 1, PageSize: 10, PageCount: 10, Total: 95, HasNext: true, HasPrevious: false},
		},
		{
			name:  "middle page",
			in:    Params{Mode: ModePage, Page: 5, PageSize: 10},
			total: 95,
			want:  Meta{Page: 5, PageSize: 10, PageCount: 10, Total: 95, HasNext: true, HasPrevious: true},
		},
		{
			name:  "last page exact multiple",
			in:    Params{Mode: ModePage, Page: 10, PageSize: 10},
			total: 100,
			want:  Meta{Page: 10, PageSize: 10, PageCount: 10, Total: 100, HasNext: false, HasPrevious: true},
		},
		{
			name:  "last partial page",
			in:    Params{Mode: ModePage, Page: 10, PageSize: 10},
			total: 95,
			want:  Meta{Page: 10, PageSize: 10, PageCount: 10, Total: 95, HasNext: false, HasPrevious: true},
		},
		{
			name:  "empty result set",
			in:    Params{Mode: ModePage, Page: 1, PageSize: 10},
			total: 0,
			want:  Meta{Page: 1, PageSize: 10, PageCount: 0, Total: 0, HasNext: false, HasPrevious: false},
		},
		{
			name:  "single full page",
			in:    Params{Mode: ModePage, Page: 1, PageSize: 25},
			total: 25,
			want:  Meta{Page: 1, PageSize: 25, PageCount: 1, Total: 25, HasNext: false, HasPrevious: false},
		},
		{
			name:  "negative total treated as zero",
			in:    Params{Mode: ModePage, Page: 1, PageSize: 10},
			total: -5,
			want:  Meta{Page: 1, PageSize: 10, PageCount: 0, Total: 0, HasNext: false, HasPrevious: false},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := BuildMeta(tt.in, tt.total); got != tt.want {
				t.Errorf("BuildMeta() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestBuildMetaOffsetMode(t *testing.T) {
	tests := []struct {
		name  string
		in    Params
		total int64
		want  Meta
	}{
		{
			name:  "offset 0 first page",
			in:    Params{Mode: ModeOffset, Start: 0, Limit: 10},
			total: 95,
			want:  Meta{Page: 1, PageSize: 10, PageCount: 10, Total: 95, HasNext: true, HasPrevious: false},
		},
		{
			name:  "offset 10 second page",
			in:    Params{Mode: ModeOffset, Start: 10, Limit: 10},
			total: 95,
			want:  Meta{Page: 2, PageSize: 10, PageCount: 10, Total: 95, HasNext: true, HasPrevious: true},
		},
		{
			name:  "offset on last page",
			in:    Params{Mode: ModeOffset, Start: 90, Limit: 10},
			total: 95,
			want:  Meta{Page: 10, PageSize: 10, PageCount: 10, Total: 95, HasNext: false, HasPrevious: true},
		},
		{
			name:  "offset not page-aligned",
			in:    Params{Mode: ModeOffset, Start: 5, Limit: 10},
			total: 95,
			want:  Meta{Page: 1, PageSize: 10, PageCount: 10, Total: 95, HasNext: true, HasPrevious: false},
		},
		{
			name:  "offset empty result",
			in:    Params{Mode: ModeOffset, Start: 0, Limit: 10},
			total: 0,
			want:  Meta{Page: 1, PageSize: 10, PageCount: 0, Total: 0, HasNext: false, HasPrevious: false},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := BuildMeta(tt.in, tt.total); got != tt.want {
				t.Errorf("BuildMeta() = %+v, want %+v", got, tt.want)
			}
		})
	}
}
