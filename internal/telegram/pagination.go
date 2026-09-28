package telegram

import "fmt"

const defaultPageSize = 8

const (
	paginatorMaxPageButtons = 7
	paginatorWindowPages    = 5
)

func PageBounds(total, page, size int) (start, end, normalizedPage, pages int) {
	if size <= 0 {
		size = defaultPageSize
	}
	pages = (total + size - 1) / size
	if pages < 1 {
		pages = 1
	}
	if page < 0 {
		page = 0
	}
	if page >= pages {
		page = pages - 1
	}
	start = page * size
	end = min(start+size, total)
	return start, end, page, pages
}

func PaginationLabel(page, pages int) string {
	if pages < 1 {
		pages = 1
	}
	return fmt.Sprintf("Page %d/%d", page+1, pages)
}

func PaginatorPages(page, pages int) []int {
	if pages < 1 {
		pages = 1
	}
	if page < 0 {
		page = 0
	}
	if page >= pages {
		page = pages - 1
	}
	if pages <= paginatorMaxPageButtons {
		result := make([]int, pages)
		for index := range result {
			result[index] = index
		}
		return result
	}

	start := page - paginatorWindowPages/2
	if start < 1 {
		start = 1
	}
	maxStart := pages - 1 - paginatorWindowPages
	if start > maxStart {
		start = maxStart
	}
	result := make([]int, 0, paginatorMaxPageButtons)
	result = append(result, 0)
	for index := start; index < start+paginatorWindowPages; index++ {
		result = append(result, index)
	}
	result = append(result, pages-1)
	return result
}
