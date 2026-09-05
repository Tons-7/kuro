package store

import (
	"net/url"
	"strconv"
)

// Total supports numbered pages, HasMore supports infinite scroll; a UI can
// use either.
type Page[T any] struct {
	Items   []T  `json:"items"`
	Total   int  `json:"total"`
	Page    int  `json:"page"`
	PerPage int  `json:"perPage"`
	HasMore bool `json:"hasMore"`
}

type Paging struct {
	Page    int
	PerPage int
}

func (p Paging) Offset() int { return (p.Page - 1) * p.PerPage }

// perPage is clamped: unbounded, one request could pull the whole corpus.
func ParsePaging(q url.Values, defaultPerPage, maxPerPage int) Paging {
	page, _ := strconv.Atoi(q.Get("page"))
	if page < 1 {
		page = 1
	}

	perPage, _ := strconv.Atoi(q.Get("perPage"))
	if perPage <= 0 {
		perPage = defaultPerPage
	}
	if perPage > maxPerPage {
		perPage = maxPerPage
	}

	// SQLite walks every skipped row, so a deep offset is capped.
	if page > 10000 {
		page = 10000
	}
	return Paging{Page: page, PerPage: perPage}
}

// Callers query perPage+1 so HasMore needs no second query.
func NewPage[T any](items []T, p Paging, total int) Page[T] {
	hasMore := len(items) > p.PerPage
	if hasMore {
		items = items[:p.PerPage]
	}
	if items == nil {
		items = []T{}
	}
	return Page[T]{
		Items: items, Total: total,
		Page: p.Page, PerPage: p.PerPage,
		HasMore: hasMore,
	}
}
