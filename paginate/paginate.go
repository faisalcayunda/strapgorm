// Package paginate models the two pagination strategies strapgorm supports —
// page-based ({page, page_size}) and offset-based ({start, limit}) — and
// computes the response metadata returned alongside paginated results.
//
// The package is dependency-light and performs no database access; it only
// normalizes user-supplied parameters into safe bounds and derives the SQL
// LIMIT/OFFSET and the response Meta.
package paginate

// Mode selects the pagination strategy.
type Mode uint8

const (
	// ModePage selects page-based pagination driven by Page and PageSize.
	ModePage Mode = iota
	// ModeOffset selects offset-based pagination driven by Start and Limit.
	ModeOffset
)

const (
	// DefaultPageSize is the page size applied when none is supplied or the
	// supplied value is out of range.
	DefaultPageSize = 25
	// MaxPageSize caps the page size (and the offset-mode limit) to protect
	// the database from unbounded result sets requested via the query string.
	MaxPageSize = 100
)

// Params holds the raw pagination request before normalization.
//
// For ModePage, Page (1-based) and PageSize are authoritative. For ModeOffset,
// Start (0-based row offset) and Limit are authoritative. WithCount requests an
// accompanying total-row count so that BuildMeta can populate Total, PageCount,
// HasNext, and HasPrevious.
type Params struct {
	// Mode selects which fields are authoritative.
	Mode Mode
	// Page is the 1-based page number (ModePage).
	Page int
	// PageSize is the number of rows per page (ModePage).
	PageSize int
	// Start is the 0-based row offset (ModeOffset).
	Start int
	// Limit is the maximum number of rows to return (ModeOffset).
	Limit int
	// WithCount requests a total-row count for the metadata.
	WithCount bool
}

// Normalize returns a copy of p with every field clamped into a safe range.
//
// For both modes the effective page size is forced into [1, MaxPageSize],
// defaulting to DefaultPageSize when the requested value is non-positive or
// above the cap. For ModePage, Page is forced to at least 1. For ModeOffset,
// Start is forced to at least 0 and Limit is clamped like the page size.
//
// Mode and WithCount are preserved unchanged.
func (p Params) Normalize() Params {
	out := p
	switch p.Mode {
	case ModeOffset:
		if out.Start < 0 {
			out.Start = 0
		}
		out.Limit = clampPageSize(out.Limit)
	default:
		out.Mode = ModePage
		if out.Page < 1 {
			out.Page = 1
		}
		out.PageSize = clampPageSize(out.PageSize)
	}
	return out
}

// clampPageSize forces n into [1, MaxPageSize], defaulting to DefaultPageSize
// when n is non-positive.
func clampPageSize(n int) int {
	switch {
	case n <= 0:
		return DefaultPageSize
	case n > MaxPageSize:
		return MaxPageSize
	default:
		return n
	}
}

// LimitOffset normalizes p and returns the SQL LIMIT and OFFSET to apply.
//
// For ModePage the offset is (Page-1)*PageSize and the limit is PageSize. For
// ModeOffset the offset is Start and the limit is Limit.
func (p Params) LimitOffset() (limit, offset int) {
	n := p.Normalize()
	if n.Mode == ModeOffset {
		return n.Limit, n.Start
	}
	return n.PageSize, (n.Page - 1) * n.PageSize
}

// Meta is the pagination metadata returned alongside a page of results.
//
// PageCount, Total, HasNext, and HasPrevious are only meaningful when the
// total-row count was computed (Params.WithCount); see BuildMeta.
type Meta struct {
	// Page is the effective 1-based page number. For ModeOffset it is derived
	// from Start and the effective page size.
	Page int `json:"page"`
	// PageSize is the effective number of rows per page.
	PageSize int `json:"page_size"`
	// PageCount is the total number of pages (ceil(Total/PageSize)).
	PageCount int `json:"page_count"`
	// Total is the total number of matching rows.
	Total int64 `json:"total"`
	// HasNext reports whether a subsequent page exists.
	HasNext bool `json:"has_next"`
	// HasPrevious reports whether a preceding page exists.
	HasPrevious bool `json:"has_previous"`
}

// BuildMeta normalizes p and computes the response Meta for a result set whose
// total matching-row count is total.
//
// PageCount is ceil(total/pageSize). For ModePage, the page number is taken
// directly from the normalized Page. For ModeOffset, the page number is derived
// from the offset as floor(Start/pageSize)+1 so the metadata still exposes a
// page-oriented view. HasNext and HasPrevious are computed from the effective
// page position relative to PageCount.
func BuildMeta(p Params, total int64) Meta {
	n := p.Normalize()

	var pageSize, page int
	if n.Mode == ModeOffset {
		pageSize = n.Limit
		page = n.Start/pageSize + 1
	} else {
		pageSize = n.PageSize
		page = n.Page
	}

	if total < 0 {
		total = 0
	}

	pageCount := 0
	if pageSize > 0 {
		pageCount = int((total + int64(pageSize) - 1) / int64(pageSize))
	}

	return Meta{
		Page:        page,
		PageSize:    pageSize,
		PageCount:   pageCount,
		Total:       total,
		HasNext:     int64(page)*int64(pageSize) < total,
		HasPrevious: page > 1 && total > 0,
	}
}
