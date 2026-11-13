package uniple

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
)

// The blocks this console shows but does not write: the 문화 기록장, the
// 맛집 기록장, the 커플 일기장 and the 데이트 가계부. Their columns were
// checked against uniple one by one; how the app fills them in - a rating's
// scale, who paid, a comment's place - is in code the app has not shown, and
// guessing at it on the two's space would leave rows the app reads wrong.

// LogEntry is one thing seen or done together: a film, a book, a show.
type LogEntry struct {
	ID        string   `json:"id"`
	Category  string   `json:"category"`
	Title     string   `json:"title,omitempty"`
	Rating    *float64 `json:"rating,omitempty"`
	Note      string   `json:"note,omitempty"`
	Photos    []string `json:"photos,omitempty"`
	Cover     string   `json:"cover,omitempty"`
	From      string   `json:"from,omitempty"`
	To        string   `json:"to,omitempty"`
	By        string   `json:"by,omitempty"`
	CreatedAt string   `json:"createdAt"`
}

// FoodEntry is a place eaten at.
type FoodEntry struct {
	ID        string   `json:"id"`
	Meal      string   `json:"meal,omitempty"`
	Title     string   `json:"title,omitempty"`
	Rating    *float64 `json:"rating,omitempty"`
	Note      string   `json:"note,omitempty"`
	Tags      []string `json:"tags,omitempty"`
	Photos    []string `json:"photos,omitempty"`
	Cover     string   `json:"cover,omitempty"`
	Day       string   `json:"day,omitempty"`
	By        string   `json:"by,omitempty"`
	CreatedAt string   `json:"createdAt"`
}

// DiaryEntry is a page of the couple's diary, with what the other wrote back.
type DiaryEntry struct {
	ID        string       `json:"id"`
	By        string       `json:"by"`
	At        string       `json:"at"`
	Title     string       `json:"title,omitempty"`
	Body      string       `json:"body"`
	Photos    []string     `json:"photos,omitempty"`
	Comments  int          `json:"comments,omitempty"`
	Replies   []DiaryReply `json:"replies,omitempty"`
	CreatedAt string       `json:"createdAt"`
}

type DiaryReply struct {
	ID        string `json:"id"`
	By        string `json:"by"`
	Body      string `json:"body"`
	CreatedAt string `json:"createdAt"`
}

// BudgetDate is one outing's spending, its total kept by uniple.
type BudgetDate struct {
	ID        string    `json:"id"`
	Title     string    `json:"title,omitempty"`
	From      string    `json:"from"`
	To        string    `json:"to,omitempty"`
	Total     float64   `json:"total"`
	Count     int       `json:"count"`
	Top       string    `json:"top,omitempty"`
	Memo      string    `json:"memo,omitempty"`
	Expenses  []Expense `json:"expenses,omitempty"`
	CreatedAt string    `json:"createdAt"`
}

// Expense is one payment of an outing. PaidBy is me, partner or shared.
type Expense struct {
	ID        string  `json:"id"`
	Amount    float64 `json:"amount"`
	Category  string  `json:"category,omitempty"`
	PaidBy    string  `json:"paidBy,omitempty"`
	Memo      string  `json:"memo,omitempty"`
	CreatedAt string  `json:"createdAt"`
}

// The home reads each with the app's own columns; a block's page reads the rest.
const (
	logHome    = "log_entries(id,category,card_asset,experienced_on,created_at)"
	foodHome   = "food_entries(id,meal_type,title,card_asset,experienced_on,created_at)"
	diaryHome  = "diary_entries(id,author_id,entry_at,title,body,photo_asset_ids,comment_count,created_at)"
	budgetHome = "budget_dates(id,title,start_on,end_on,total_amount,expense_count,top_category,created_at)"

	logColumns    = "id,category,title,rating,note,photo_asset_ids,card_asset,experienced_on,experienced_end,created_by,created_at"
	foodColumns   = "id,meal_type,title,rating,note,tags,photo_asset_ids,card_asset,experienced_on,created_by,created_at"
	diaryColumns  = "id,author_id,entry_at,title,body,photo_asset_ids,comment_count,created_at"
	budgetColumns = "id,title,start_on,end_on,total_amount,expense_count,top_category,memo,created_at"
)

type logRow struct {
	ID             string          `json:"id"`
	Category       *string         `json:"category"`
	Title          *string         `json:"title"`
	Rating         *float64        `json:"rating"`
	Note           *string         `json:"note"`
	PhotoAssetIDs  []string        `json:"photo_asset_ids"`
	CardAsset      *string         `json:"card_asset"`
	ExperiencedOn  *string         `json:"experienced_on"`
	ExperiencedEnd *string         `json:"experienced_end"`
	CreatedBy      *string         `json:"created_by"`
	CreatedAt      string          `json:"created_at"`
	MealType       *string         `json:"meal_type"`
	Tags           json.RawMessage `json:"tags"`
}

func (r logRow) toLog(me string) LogEntry {
	e := LogEntry{ID: r.ID, Category: str(r.Category), Title: str(r.Title), Rating: r.Rating, Note: str(r.Note), Photos: r.PhotoAssetIDs,
		Cover: firstOf(first(r.PhotoAssetIDs), str(r.CardAsset)), From: str(r.ExperiencedOn), To: str(r.ExperiencedEnd), CreatedAt: r.CreatedAt}
	if r.CreatedBy != nil {
		e.By = side(*r.CreatedBy, me)
	}
	return e
}

func (r logRow) toFood(me string) FoodEntry {
	// The food card shows its own cover first, the log card its first photo.
	e := FoodEntry{ID: r.ID, Meal: str(r.MealType), Title: str(r.Title), Rating: r.Rating, Note: str(r.Note), Tags: tags(r.Tags), Photos: r.PhotoAssetIDs,
		Cover: firstOf(str(r.CardAsset), first(r.PhotoAssetIDs)), Day: str(r.ExperiencedOn), CreatedAt: r.CreatedAt}
	if r.CreatedBy != nil {
		e.By = side(*r.CreatedBy, me)
	}
	return e
}

type diaryRow struct {
	ID            string   `json:"id"`
	AuthorID      *string  `json:"author_id"`
	EntryAt       string   `json:"entry_at"`
	Title         *string  `json:"title"`
	Body          *string  `json:"body"`
	PhotoAssetIDs []string `json:"photo_asset_ids"`
	CommentCount  int      `json:"comment_count"`
	CreatedAt     string   `json:"created_at"`
	Comments      []struct {
		ID        string  `json:"id"`
		AuthorID  *string `json:"author_id"`
		Body      *string `json:"body"`
		CreatedAt string  `json:"created_at"`
	} `json:"diary_comments"`
}

func (r diaryRow) toEntry(me string) DiaryEntry {
	e := DiaryEntry{ID: r.ID, By: side(str(r.AuthorID), me), At: r.EntryAt, Title: str(r.Title), Body: str(r.Body), Photos: r.PhotoAssetIDs,
		Comments: r.CommentCount, CreatedAt: r.CreatedAt}
	for _, c := range r.Comments {
		e.Replies = append(e.Replies, DiaryReply{ID: c.ID, By: side(str(c.AuthorID), me), Body: str(c.Body), CreatedAt: c.CreatedAt})
	}
	return e
}

type budgetRow struct {
	ID           string   `json:"id"`
	Title        *string  `json:"title"`
	StartOn      string   `json:"start_on"`
	EndOn        *string  `json:"end_on"`
	TotalAmount  *float64 `json:"total_amount"`
	ExpenseCount int      `json:"expense_count"`
	TopCategory  *string  `json:"top_category"`
	Memo         *string  `json:"memo"`
	CreatedAt    string   `json:"created_at"`
	Expenses     []struct {
		ID        string   `json:"id"`
		Amount    *float64 `json:"amount"`
		Category  *string  `json:"category"`
		PaidBy    *string  `json:"paid_by"`
		Memo      *string  `json:"memo"`
		CreatedAt string   `json:"created_at"`
	} `json:"budget_expenses"`
}

func (r budgetRow) toDate(me string) BudgetDate {
	d := BudgetDate{ID: r.ID, Title: str(r.Title), From: r.StartOn, To: str(r.EndOn), Count: r.ExpenseCount, Top: str(r.TopCategory), Memo: str(r.Memo), CreatedAt: r.CreatedAt}
	if r.TotalAmount != nil {
		d.Total = *r.TotalAmount
	}
	for _, x := range r.Expenses {
		e := Expense{ID: x.ID, Category: str(x.Category), Memo: str(x.Memo), CreatedAt: x.CreatedAt, PaidBy: payer(str(x.PaidBy), me)}
		if x.Amount != nil {
			e.Amount = *x.Amount
		}
		d.Expenses = append(d.Expenses, e)
	}
	return d
}

// payer is who paid, from the viewer's side: the viewer's id is me, shared
// (or none) is both, anyone else the partner.
func payer(paidBy, me string) string {
	switch {
	case paidBy == "" || strings.EqualFold(paidBy, "shared"):
		return "shared"
	case paidBy == me:
		return "me"
	case uuidLike(paidBy):
		return "partner"
	}
	return paidBy
}

// shelfItems reads a read-only block's page. The replies and the expenses
// come embedded; uniple refusing the embedding gives the page without them.
func (c *Client) shelfItems(ctx context.Context, b *Block, me string) error {
	q := url.Values{"block_id": {"eq." + b.ID}}
	switch b.Type {
	case "log", "food":
		table, cols := "log_entries", logColumns
		if b.Type == "food" {
			table, cols = "food_entries", foodColumns
		}
		q.Set("select", cols)
		q.Set("order", "experienced_on.desc.nullslast,created_at.desc")
		var rows []logRow
		if err := c.rest(ctx, http.MethodGet, table+"?"+q.Encode(), nil, &rows); err != nil {
			return err
		}
		b.Log, b.Food = nil, nil
		for _, r := range rows {
			if b.Type == "log" {
				b.Log = append(b.Log, r.toLog(me))
			} else {
				b.Food = append(b.Food, r.toFood(me))
			}
		}
	case "diary":
		q.Set("order", "entry_at.desc,created_at.desc")
		q.Set("diary_comments.order", "created_at.asc")
		q.Set("select", diaryColumns+",diary_comments(id,author_id,body,created_at)")
		var rows []diaryRow
		if err := c.rest(ctx, http.MethodGet, "diary_entries?"+q.Encode(), nil, &rows); err != nil {
			q.Del("diary_comments.order")
			q.Set("select", diaryColumns)
			if err := c.rest(ctx, http.MethodGet, "diary_entries?"+q.Encode(), nil, &rows); err != nil {
				return err
			}
		}
		b.Diary = nil
		for _, r := range rows {
			b.Diary = append(b.Diary, r.toEntry(me))
		}
	case "budget":
		q.Set("order", "start_on.desc,created_at.desc")
		q.Set("budget_expenses.order", "created_at.asc")
		q.Set("select", budgetColumns+",budget_expenses(id,amount,category,paid_by,memo,created_at)")
		var rows []budgetRow
		if err := c.rest(ctx, http.MethodGet, "budget_dates?"+q.Encode(), nil, &rows); err != nil {
			q.Del("budget_expenses.order")
			q.Set("select", budgetColumns)
			if err := c.rest(ctx, http.MethodGet, "budget_dates?"+q.Encode(), nil, &rows); err != nil {
				return err
			}
		}
		b.Budget = nil
		for _, r := range rows {
			b.Budget = append(b.Budget, r.toDate(me))
		}
	}
	return nil
}

// tags reads a list of words however uniple keeps it: an array of strings,
// or of anything with a name.
func tags(raw json.RawMessage) []string {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var words []string
	if json.Unmarshal(raw, &words) == nil {
		return words
	}
	var any []map[string]any
	if json.Unmarshal(raw, &any) == nil {
		for _, a := range any {
			if n, ok := a["name"].(string); ok {
				words = append(words, n)
			}
		}
	}
	return words
}

func first(s []string) string {
	if len(s) == 0 {
		return ""
	}
	return s[0]
}
