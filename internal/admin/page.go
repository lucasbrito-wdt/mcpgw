package admin

const (
	defaultPageLimit = 50
	maxPageLimit     = 200
)

// PageQuery asks for one keyset page. Cursor is the opaque value of the previous page's
// NextCursor; here it is the last id returned (the HTTP layer wraps it, plan gap G14).
type PageQuery struct {
	Limit  int
	Cursor string
}

type Page[T any] struct {
	Items      []T
	NextCursor string
}

// limit is the page size, clamped to 1..maxPageLimit.
func (q PageQuery) limit() int {
	switch {
	case q.Limit <= 0:
		return defaultPageLimit
	case q.Limit > maxPageLimit:
		return maxPageLimit
	}
	return q.Limit
}

// uuidCursor validates a cursor that holds a uuid; "" means first page.
func (q PageQuery) uuidCursor() (string, error) {
	if q.Cursor != "" && !validUUID(q.Cursor) {
		return "", invalid("cursor", "invalid cursor", "cursor inválido")
	}
	return q.Cursor, nil
}

// pageRows turns the limit+1 rows a query fetched into a page; key is the cursor of an item.
func pageRows[T any](rows []T, limit int, key func(T) string) Page[T] {
	if len(rows) <= limit {
		return Page[T]{Items: rows}
	}
	rows = rows[:limit]
	return Page[T]{Items: rows, NextCursor: key(rows[limit-1])}
}
