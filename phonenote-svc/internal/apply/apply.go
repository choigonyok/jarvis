// Package apply carries what a notification said into the data it is about.
//
// Today that is the 가계부: a card alert says "배달의민족 ₩23,000" and no
// more; the delivery app's own notification says what was ordered. The
// payment it belongs to - near in time, the same amount, or a delivery
// app's charge when no amount was read - gets the items (the menu) and the
// shop. A payment that already has items (an order the browser read, or a
// person's own) is left as it is.
//
// The card alert often comes after the app's notification, so a reading with
// no payment yet is tried again for a while before it is let go.
package apply

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/choigonyok/jarvis/phonenote-svc/internal/note"
)

// Wait is how long a reading waits for its payment to show up.
const Wait = 6 * time.Hour

// Near is how far apart a notification and its payment may be.
const Near = 3 * time.Hour

// Tx is a payment as spending-svc lists it.
type Tx struct {
	ID         int64     `json:"id"`
	AmountKrw  int64     `json:"amountKrw"`
	Merchant   string    `json:"merchant"`
	Memo       string    `json:"memo"`
	ApprovedAt time.Time `json:"approvedAt"`
	Status     string    `json:"status"`
	Items      []struct {
		Name string `json:"name"`
	} `json:"items"`
}

// Spending is the 가계부 as this needs it.
type Spending interface {
	Month(ctx context.Context, month string) ([]Tx, error)
	SaveItems(ctx context.Context, id int64, items []note.Item, comment string) error
	SetMemo(ctx context.Context, id int64, memo string) error
}

// Delivery apps and what their charges are called on a card alert.
var deliveryMerchants = map[string][]string{
	"com.jawebs.baedal":       {"배달의민족", "우아한형제들", "배민"},
	"com.coupang.coupangeats": {"쿠팡이츠", "쿠팡이츠서비스", "쿠팡(쿠페이)-쿠팡이츠"},
	"com.coupang.mobile.eats": {"쿠팡이츠", "쿠팡이츠서비스"},
	"com.yogiyo.yogiyoapp":    {"요기요", "위대한상상"},
	"kr.co.ddangyo":           {"땡겨요"},
}

type Applier struct {
	Spending Spending
	Loc      *time.Location
	Now      func() time.Time
}

// Apply does what a reading calls for. done false: try again later (the
// payment is not in yet); applied says what changed, empty when nothing did.
func (a Applier) Apply(ctx context.Context, n note.Note) (applied string, done bool, err error) {
	r := n.Reading
	if r == nil || !r.Meaningful || a.Spending == nil {
		return "", true, nil
	}
	if r.Kind != note.DeliveryOrder && r.Kind != note.Purchase {
		return "", true, nil
	}
	if r.Stage == "cancelled" || (len(r.Items) == 0 && r.Merchant == "") {
		return "", true, nil
	}
	tx, err := a.payment(ctx, n)
	if err != nil {
		return "", false, err
	}
	if tx == nil {
		return "", a.now().Sub(n.At) > Wait, nil
	}
	var did []string
	if len(r.Items) > 0 && len(tx.Items) == 0 {
		if err := a.Spending.SaveItems(ctx, tx.ID, r.Items, "폰 알림("+appName(n.App)+")에서 읽음"); err != nil {
			return "", false, err
		}
		did = append(did, fmt.Sprintf("상품 %d개", len(r.Items)))
	}
	// The shop, when the payment only knows the app: "배달의민족" says little.
	if r.Merchant != "" && tx.Memo == "" && !strings.Contains(tx.Merchant, r.Merchant) {
		if err := a.Spending.SetMemo(ctx, tx.ID, r.Merchant); err != nil {
			return "", false, err
		}
		did = append(did, "가게 "+r.Merchant)
	}
	if len(did) == 0 {
		return "", true, nil
	}
	return fmt.Sprintf("가계부 #%d 에 %s", tx.ID, strings.Join(did, " · ")), true, nil
}

func (a Applier) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

// payment is the charge a notification is about: the same amount within
// Near, or - with no amount read - the delivery app's nearest charge.
func (a Applier) payment(ctx context.Context, n note.Note) (*Tx, error) {
	loc := a.Loc
	if loc == nil {
		loc = time.Local
	}
	at := n.At.In(loc)
	months := []string{at.Format("2006-01")}
	if edge := at.Add(-Near); edge.Month() != at.Month() {
		months = append(months, edge.Format("2006-01"))
	}
	var best *Tx
	var bestGap time.Duration
	names := deliveryMerchants[n.App]
	for _, m := range months {
		txs, err := a.Spending.Month(ctx, m)
		if err != nil {
			return nil, err
		}
		for i := range txs {
			t := txs[i]
			gap := t.ApprovedAt.Sub(n.At)
			if gap < 0 {
				gap = -gap
			}
			if t.Status != "ok" || gap > Near {
				continue
			}
			match := false
			if amt := n.Reading.AmountKrw; amt > 0 {
				match = abs(t.AmountKrw-amt) <= 10
			} else {
				for _, name := range names {
					match = match || strings.Contains(t.Merchant, name)
				}
			}
			if match && (best == nil || gap < bestGap) {
				best, bestGap = &t, gap
			}
		}
	}
	return best, nil
}

func abs(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

func appName(app string) string {
	if names, ok := deliveryMerchants[app]; ok {
		return names[0]
	}
	return app
}

// HTTP is spending-svc.
type HTTP struct {
	URL   string
	Token string
	HTTP  *http.Client
}

func (h HTTP) Month(ctx context.Context, month string) ([]Tx, error) {
	var out struct {
		Transactions []Tx `json:"transactions"`
	}
	err := h.do(ctx, http.MethodGet, "/spending?month="+url.QueryEscape(month), nil, &out)
	return out.Transactions, err
}

func (h HTTP) SaveItems(ctx context.Context, id int64, items []note.Item, comment string) error {
	type item struct {
		Name     string `json:"name"`
		Quantity int    `json:"quantity"`
		PriceKrw int64  `json:"priceKrw"`
		Category string `json:"category"`
	}
	body := struct {
		Items []item `json:"items"`
		Note  string `json:"note"`
	}{Note: comment}
	for _, it := range items {
		cat := it.Category
		if cat == "" {
			cat = "식비"
		}
		body.Items = append(body.Items, item{Name: it.Name, Quantity: max(it.Quantity, 1), PriceKrw: it.PriceKrw, Category: cat})
	}
	return h.do(ctx, http.MethodPost, fmt.Sprintf("/transactions/%d/items", id), body, nil)
}

func (h HTTP) SetMemo(ctx context.Context, id int64, memo string) error {
	return h.do(ctx, http.MethodPatch, fmt.Sprintf("/transactions/%d", id), map[string]string{"memo": memo}, nil)
}

func (h HTTP) do(ctx context.Context, method, path string, in, out any) error {
	var body *bytes.Reader
	if in != nil {
		b, _ := json.Marshal(in)
		body = bytes.NewReader(b)
	} else {
		body = bytes.NewReader(nil)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(h.URL, "/")+path, body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if h.Token != "" {
		req.Header.Set("Authorization", "Bearer "+h.Token)
	}
	client := h.HTTP
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	res, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("가계부 서비스: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(res.Body).Decode(&e)
		return fmt.Errorf("가계부 서비스: %s", strings.TrimSpace(res.Status+" "+e.Error))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(res.Body).Decode(out)
}
