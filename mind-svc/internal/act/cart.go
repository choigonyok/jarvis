package act

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
)

// Jobs is where a browser job is made: the ledger in the stack, run by the
// agent's scheduler with its browser.
type Jobs interface {
	Create(ctx context.Context, title, goal, instructions string, sites []string) (int64, error)
	Cancel(ctx context.Context, id int64) error
}

// CartActuator does cart.add: a job that puts an item in the shop's cart
// and stops there. Paying is never part of it - the browser's payment guard
// stops any page that tries, and the operator orders by hand.
type CartActuator struct{ Jobs Jobs }

type cart struct {
	Item   string `json:"item"`
	Class  string `json:"class"`
	PlanID string `json:"planId"`
	NoteID string `json:"noteId"`
	Site   string `json:"site"`
}

func (CartActuator) Kinds() []string { return []string{"cart.add"} }

func (CartActuator) Preview(_ context.Context, _ string, payload json.RawMessage) (string, error) {
	var c cart
	if err := json.Unmarshal(payload, &c); err != nil {
		return "", err
	}
	if strings.TrimSpace(c.Item) == "" {
		return "", errors.New("담을 상품이 필요합니다")
	}
	return fmt.Sprintf("%s 장바구니에 '%s'을(를) 담기만 하는 작업을 만듭니다. 결제는 하지 않습니다", siteName(c.Site), c.Item), nil
}

func (a CartActuator) Execute(ctx context.Context, _ string, payload json.RawMessage) (json.RawMessage, json.RawMessage, error) {
	var c cart
	if err := json.Unmarshal(payload, &c); err != nil {
		return nil, nil, err
	}
	site := c.Site
	if site == "" {
		site = "coupang.com"
	}
	id, err := a.Jobs.Create(ctx, "장바구니: "+c.Item,
		fmt.Sprintf("%s 장바구니에 '%s'을(를) 담는다", siteName(site), c.Item),
		fmt.Sprintf("%s에서 '%s'(또는 지난번에 산 것과 같은 상품)을 찾아 장바구니에 1개 담고 끝낸다. 주문·결제 버튼은 누르지 않는다. 같은 상품이 이미 장바구니에 있으면 그대로 두고 끝낸다.", siteName(site), c.Item),
		[]string{site})
	if err != nil {
		return nil, nil, err
	}
	return mustJSON(map[string]int64{"jobId": id}), mustJSON(map[string]int64{"jobId": id}), nil
}

func (a CartActuator) Undo(ctx context.Context, _ string, undo json.RawMessage) (json.RawMessage, error) {
	var u struct {
		JobID int64 `json:"jobId"`
	}
	if err := json.Unmarshal(undo, &u); err != nil {
		return nil, err
	}
	return json.RawMessage(`{}`), a.Jobs.Cancel(ctx, u.JobID)
}

func siteName(site string) string {
	if strings.Contains(site, "coupang") || site == "" {
		return "쿠팡"
	}
	return site
}

// MemoryJobs keeps jobs in this process.
type MemoryJobs struct {
	mu   sync.Mutex
	seq  int64
	Made map[int64]string
}

func (m *MemoryJobs) Create(_ context.Context, title, _, _ string, _ []string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Made == nil {
		m.Made = map[int64]string{}
	}
	m.seq++
	m.Made[m.seq] = title
	return m.seq, nil
}

func (m *MemoryJobs) Cancel(_ context.Context, id int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.Made, id)
	return nil
}

// Ledger is the jobs mind keeps.
type Ledger interface {
	CreateJob(ctx context.Context, title, goal, instructions string, sites []string) (int64, error)
	CancelJob(ctx context.Context, id int64) error
}

// JobsBook makes jobs in that ledger.
type JobsBook struct{ Ledger Ledger }

func (j JobsBook) Create(ctx context.Context, title, goal, instructions string, sites []string) (int64, error) {
	return j.Ledger.CreateJob(ctx, title, goal, instructions, sites)
}

func (j JobsBook) Cancel(ctx context.Context, id int64) error { return j.Ledger.CancelJob(ctx, id) }
