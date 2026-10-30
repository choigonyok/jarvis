package spending

import (
	"context"
	"net/http"
)

// KindCategory is mind's action that files a charge. Asking what a charge was
// for is mind's now (its slot merchant.category): this name is only how the
// card's push is muted with the rest of 가계부.
const KindCategory = "spending.category"

// Categories is the book's list of categories, for a card elsewhere that
// files something into it (assets/ask.go).
func (m *Module) Categories(ctx context.Context) ([]string, error) {
	var payload struct {
		Categories []string `json:"categories"`
	}
	err := m.store.call(ctx, http.MethodGet, "/unclear", nil, &payload)
	return payload.Categories, err
}
