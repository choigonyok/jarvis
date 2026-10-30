package act

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

// KindCategory files a charge under a category.
const KindCategory = "spending.category"

// Answers of the remember field.
const (
	RememberYes = "앞으로도"
	RememberNo  = "이번만"
)

// other is the book's catch-all: where a charge goes back to.
const other = "기타"

// Book is the household book, which mind keeps: what the rules could not
// place, and filing it.
type Book interface {
	// Unclear decodes what the rules left unfiled lately, and the categories
	// there are, into into.
	Unclear(ctx context.Context, into any) error
	// FileCharge files a charge; with remember, its merchant goes the same way
	// from now on.
	FileCharge(ctx context.Context, id int64, category string, remember bool) error
	// FileItem files one product of a split order.
	FileItem(ctx context.Context, id int64, category string) error
}

// Spending files charges in the book.
type Spending struct {
	Book Book
}

func (Spending) Kinds() []string { return []string{KindCategory} }

// CategoryPayload is what a category intent carries: the charge, and what the
// assistant thought it was (shown filled in, or kept to be scored).
type CategoryPayload struct {
	Kind     string            `json:"kind"` // tx | item
	ID       int64             `json:"id"`
	Merchant string            `json:"merchant"`
	Name     string            `json:"name,omitempty"`
	Proposed string            `json:"proposed,omitempty"`
	Answers  map[string]string `json:"answers,omitempty"`
}

// Chosen is the category to file under: the answer given, or what was
// proposed when it went without asking.
func (p CategoryPayload) Chosen() string {
	if c := strings.TrimSpace(p.Answers["category"]); c != "" {
		return c
	}
	return p.Proposed
}

func (Spending) Preview(_ context.Context, _ string, payload json.RawMessage) (string, error) {
	var p CategoryPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return "", err
	}
	if p.Proposed == "" {
		return "", nil
	}
	return p.Merchant + " → " + p.Proposed, nil
}

func (s Spending) Execute(ctx context.Context, _ string, payload json.RawMessage) (json.RawMessage, json.RawMessage, error) {
	var p CategoryPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return nil, nil, err
	}
	category := p.Chosen()
	if category == "" {
		return nil, nil, errors.New("고른 분류가 없습니다")
	}
	remember := strings.TrimSpace(p.Answers["remember"]) != RememberNo
	if err := s.file(ctx, p, category, remember); err != nil {
		return nil, nil, err
	}
	corrected := p.Proposed != "" && p.Proposed != category
	return mustJSON(map[string]any{"category": category, "corrected": corrected}), mustJSON(p), nil
}

// Undo takes a charge back to 기타, by hand as far as the book is concerned,
// so it is not asked about again.
func (s Spending) Undo(ctx context.Context, _ string, undo json.RawMessage) (json.RawMessage, error) {
	var p CategoryPayload
	if err := json.Unmarshal(undo, &p); err != nil {
		return nil, err
	}
	return mustJSON(map[string]string{"category": other}), s.file(ctx, p, other, false)
}

// Settle leaves a charge the operator turned down in 기타 for good.
func (s Spending) Settle(ctx context.Context, payload json.RawMessage) error {
	var p CategoryPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return err
	}
	return s.file(ctx, p, other, false)
}

func (s Spending) file(ctx context.Context, p CategoryPayload, category string, remember bool) error {
	if p.Kind == "item" {
		return s.Book.FileItem(ctx, p.ID, category)
	}
	return s.Book.FileCharge(ctx, p.ID, category, remember)
}

// Unclear is what the rules left unfiled lately, and the categories there are.
func (s Spending) Unclear(ctx context.Context, into any) error {
	return s.Book.Unclear(ctx, into)
}
