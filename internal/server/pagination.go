package server

import (
	"net/http"
	"strconv"
	"strings"
)

const (
	defaultPageSize = 25
	maxPageSize     = 100
)

type pageInfo struct {
	Total   int `json:"total"`
	Page    int `json:"page"`
	PerPage int `json:"perPage"`
	Pages   int `json:"pages"`
}

type pageResult[T any] struct {
	Items []T `json:"items"`
	pageInfo
}

func pageRequest(r *http.Request) (page, perPage int) {
	return pageFromRequest(r, "page", "per_page")
}

func pageFromRequest(r *http.Request, pageKey, perPageKey string) (page, perPage int) {
	query := r.URL.Query()
	page = 1
	perPage = defaultPageSize
	if value, err := strconv.Atoi(query.Get(pageKey)); err == nil && value > 0 {
		page = value
	}
	if value, err := strconv.Atoi(query.Get(perPageKey)); err == nil && value > 0 {
		perPage = value
	}
	if perPage > maxPageSize {
		perPage = maxPageSize
	}
	return page, perPage
}

func pagedRequest(r *http.Request) bool {
	query := r.URL.Query()
	return query.Has("page") || query.Has("per_page") || query.Has("q")
}

func searchQuery(r *http.Request) string {
	return strings.TrimSpace(r.URL.Query().Get("q"))
}

func regionQuery(r *http.Request) string {
	return strings.TrimSpace(r.URL.Query().Get("region"))
}

func paginate[T any](items []T, page, perPage int) ([]T, pageInfo) {
	total := len(items)
	pages := 0
	if total > 0 {
		pages = (total + perPage - 1) / perPage
		if page > pages {
			page = pages
		}
	}
	if page < 1 {
		page = 1
	}
	start := (page - 1) * perPage
	if start > total {
		start = total
	}
	end := start + perPage
	if end > total {
		end = total
	}
	return items[start:end], pageInfo{Total: total, Page: page, PerPage: perPage, Pages: pages}
}

func pageSlice[T any](items []T, page, perPage int) []T {
	start := (page - 1) * perPage
	if start > len(items) {
		start = len(items)
	}
	end := start + perPage
	if end > len(items) {
		end = len(items)
	}
	return items[start:end]
}

func pageCount(total, perPage int) int {
	if total == 0 {
		return 0
	}
	return (total + perPage - 1) / perPage
}

func paginateResult[T any](items []T, page, perPage int) pageResult[T] {
	items, info := paginate(items, page, perPage)
	if items == nil {
		items = []T{}
	}
	return pageResult[T]{Items: items, pageInfo: info}
}

func matchesSearch(query string, values ...string) bool {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return true
	}
	for _, value := range values {
		if strings.Contains(strings.ToLower(value), query) {
			return true
		}
	}
	return false
}
