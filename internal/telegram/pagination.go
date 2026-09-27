package telegram

import "fmt"

const defaultPageSize = 8

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
