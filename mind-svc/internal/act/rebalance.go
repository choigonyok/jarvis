package act

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Orders is where orders are placed: assets-svc in the stack, the one
// service that holds the brokerage keys.
type Orders interface {
	// Check is the current rebalance plan as orders, checked but not sent.
	Check(ctx context.Context) ([]Order, error)
	// Place sends exactly these orders and says how each went.
	Place(ctx context.Context, orders []Order, planID string) ([]Order, error)
}

// Order is one order as assets-svc takes and answers it (assets-svc
// src/orders.ts): what to trade, and - in an answer - how it went.
type Order struct {
	Venue     string   `json:"venue"`
	Symbol    string   `json:"symbol"`
	Side      string   `json:"side"`
	Units     *float64 `json:"units"`
	AmountKrw float64  `json:"amountKrw"`
	Status    string   `json:"status,omitempty"`
	Message   string   `json:"message,omitempty"`
	Name      string   `json:"name,omitempty"`
}

// Line is the order in a sentence: "KO 1주 매수", "BTC ₩52,000 매도".
func (o Order) Line() string {
	name := o.Name
	if name == "" {
		name = o.Symbol
	}
	verb := "매수"
	if o.Side == "sell" {
		verb = "매도"
	}
	if o.Units == nil || o.Venue == "upbit" {
		return fmt.Sprintf("%s ₩%s %s", name, comma(int64(o.AmountKrw)), verb)
	}
	unit := "주"
	if o.Venue == "gold" {
		unit = "g"
	}
	return fmt.Sprintf("%s %g%s %s", name, *o.Units, unit, verb)
}

func comma(v int64) string {
	s := fmt.Sprint(v)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// RebalanceActuator does assets.rebalance: the orders a rebalance card
// showed, sent when it is approved - those and no others, so what was
// approved is what goes, whatever the prices did meanwhile. Asked every time
// (the gate's Fixed list): no order is placed by itself. An order cannot be
// taken back; selling what was bought is a new decision.
type RebalanceActuator struct{ Orders Orders }

// RebalancePlan is the payload: the orders as checked when the card was raised.
type RebalancePlan struct {
	PlanID string  `json:"planId"`
	Orders []Order `json:"orders"`
}

func (RebalanceActuator) Kinds() []string { return []string{"assets.rebalance"} }

func (RebalanceActuator) Preview(_ context.Context, _ string, payload json.RawMessage) (string, error) {
	var p RebalancePlan
	if err := json.Unmarshal(payload, &p); err != nil {
		return "", err
	}
	if len(p.Orders) == 0 {
		return "", errors.New("낼 주문이 없습니다")
	}
	lines := make([]string, 0, len(p.Orders))
	for _, o := range p.Orders {
		lines = append(lines, o.Line())
	}
	return "승인하면 이 주문을 냅니다(파는 것부터): " + strings.Join(lines, " · "), nil
}

func (a RebalanceActuator) Execute(ctx context.Context, _ string, payload json.RawMessage) (json.RawMessage, json.RawMessage, error) {
	if a.Orders == nil {
		return nil, nil, errors.New("자산 서비스가 연결되지 않았습니다")
	}
	var p RebalancePlan
	if err := json.Unmarshal(payload, &p); err != nil {
		return nil, nil, err
	}
	if len(p.Orders) == 0 {
		return nil, nil, errors.New("낼 주문이 없습니다")
	}
	got, err := a.Orders.Place(ctx, p.Orders, p.PlanID)
	if err != nil {
		return nil, nil, err
	}
	placed := 0
	for _, o := range got {
		if o.Status == "placed" {
			placed++
		}
	}
	result := mustJSON(map[string]any{"orders": got, "placed": placed})
	if placed == 0 {
		why := "증권사가 받지 않았습니다"
		for _, o := range got {
			if o.Message != "" {
				why = o.Message
				break
			}
		}
		return result, nil, errors.New("주문을 하나도 내지 못했습니다: " + why)
	}
	return result, json.RawMessage(`{"op":"none"}`), nil
}

func (RebalanceActuator) Undo(context.Context, string, json.RawMessage) (json.RawMessage, error) {
	return nil, errors.New("낸 주문은 되돌릴 수 없어요. 되팔거나 되사는 것은 새 주문이에요")
}

// AssetsOrders is assets-svc (POST /orders/rebalance with dryRun, POST /orders).
type AssetsOrders struct {
	URL   string
	Token string
	HTTP  *http.Client
}

func (a AssetsOrders) Check(ctx context.Context) ([]Order, error) {
	var out struct {
		PlanID  string  `json:"planId"`
		Results []Order `json:"results"`
	}
	if err := a.post(ctx, "/orders/rebalance", map[string]any{"dryRun": true}, &out); err != nil {
		return nil, err
	}
	var ready []Order
	for _, o := range out.Results {
		// Only what can go: refused lines and KRX gold (by hand) are not asked about.
		if o.Status == "preview" {
			o.Status, o.Message = "", ""
			ready = append(ready, o)
		}
	}
	return ready, nil
}

func (a AssetsOrders) Place(ctx context.Context, orders []Order, planID string) ([]Order, error) {
	var out struct {
		Results []Order `json:"results"`
	}
	err := a.post(ctx, "/orders", map[string]any{"orders": orders, "reason": "rebalance", "planId": planID}, &out)
	return out.Results, err
}

func (a AssetsOrders) post(ctx context.Context, path string, in, out any) error {
	body, _ := json.Marshal(in)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(a.URL, "/")+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if a.Token != "" {
		req.Header.Set("Authorization", "Bearer "+a.Token)
	}
	client := a.HTTP
	if client == nil {
		// Placing reads the brokerages afresh first: a few seconds each.
		client = &http.Client{Timeout: 60 * time.Second}
	}
	res, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("자산 서비스: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(res.Body).Decode(&e)
		if e.Error == "" {
			e.Error = res.Status
		}
		return fmt.Errorf("자산 서비스: %s", e.Error)
	}
	return json.NewDecoder(res.Body).Decode(out)
}

// MemoryOrders checks and places orders in this process (tests, the
// standalone mind).
type MemoryOrders struct {
	mu     sync.Mutex
	Plan   []Order
	Placed []Order
}

func (m *MemoryOrders) Check(context.Context) ([]Order, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Order(nil), m.Plan...), nil
}

func (m *MemoryOrders) Place(_ context.Context, orders []Order, _ string) ([]Order, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Order, len(orders))
	for i, o := range orders {
		o.Status = "placed"
		out[i] = o
	}
	m.Placed = append(m.Placed, out...)
	return out, nil
}
