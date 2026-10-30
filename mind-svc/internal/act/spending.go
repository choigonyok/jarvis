package act

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
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

// Spending is the book's API: what the rules could not place, and filing it.
type Spending struct {
	URL   string
	Token string
	HTTP  *http.Client
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
		return s.do(ctx, http.MethodPatch, fmt.Sprintf("/items/%d", p.ID), map[string]string{"category": category}, nil)
	}
	return s.do(ctx, http.MethodPatch, fmt.Sprintf("/transactions/%d", p.ID), map[string]any{"category": category, "remember": remember}, nil)
}

// Unclear is what the rules left unfiled lately, and the categories there are.
func (s Spending) Unclear(ctx context.Context, into any) error {
	return s.do(ctx, http.MethodGet, "/unclear", nil, into)
}

func (s Spending) do(ctx context.Context, method, path string, in, out any) error {
	var body bytes.Buffer
	if in != nil {
		if err := json.NewEncoder(&body).Encode(in); err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(s.URL, "/")+path, &body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if s.Token != "" {
		req.Header.Set("Authorization", "Bearer "+s.Token)
	}
	client := s.HTTP
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("가계부 %s: 응답 %d", path, resp.StatusCode)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
