package act

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// fakeLedger is the book: what was filed, each as a line.
type fakeLedger struct {
	calls []string
	fail  error
}

func (f *fakeLedger) Unclear(context.Context, any) error { return f.fail }

func (f *fakeLedger) FileCharge(_ context.Context, id int64, category string, remember bool) error {
	f.calls = append(f.calls, fmt.Sprintf("charge %d %s remember=%v", id, category, remember))
	return f.fail
}

func (f *fakeLedger) FileItem(_ context.Context, id int64, category string) error {
	f.calls = append(f.calls, fmt.Sprintf("item %d %s", id, category))
	return f.fail
}

func fakeBook() (Spending, *fakeLedger) {
	f := &fakeLedger{}
	return Spending{Book: f}, f
}

func raw(p CategoryPayload) json.RawMessage {
	b, _ := json.Marshal(p)
	return b
}

func TestFilingACharge(t *testing.T) {
	s, book := fakeBook()
	ctx := context.Background()
	res, undo, err := s.Execute(ctx, KindCategory, raw(CategoryPayload{Kind: "tx", ID: 12, Merchant: "상호", Proposed: "식비",
		Answers: map[string]string{"category": "식비", "remember": RememberYes}}))
	if err != nil {
		t.Fatal(err)
	}
	if len(book.calls) != 1 || book.calls[0] != "charge 12 식비 remember=true" {
		t.Fatalf("%+v", book.calls)
	}
	if !strings.Contains(string(res), `"corrected":false`) || len(undo) == 0 {
		t.Fatalf("%s", res)
	}
}

// Changing the answer on the form is a correction: what was said was wrong.
func TestACorrectionIsSaid(t *testing.T) {
	s, book := fakeBook()
	res, _, err := s.Execute(context.Background(), KindCategory, raw(CategoryPayload{Kind: "tx", ID: 1, Proposed: "식비",
		Answers: map[string]string{"category": "교통", "remember": RememberNo}}))
	if err != nil || !strings.Contains(string(res), `"corrected":true`) || book.calls[0] != "charge 1 교통 remember=false" {
		t.Fatalf("%s %v %+v", res, err, book.calls)
	}
}

// Asked with the form empty, picking anything is no correction of nothing.
func TestABlankFormCorrectsNothing(t *testing.T) {
	s, _ := fakeBook()
	res, _, err := s.Execute(context.Background(), KindCategory, raw(CategoryPayload{Kind: "tx", ID: 1, Answers: map[string]string{"category": "교통"}}))
	if err != nil || !strings.Contains(string(res), `"corrected":false`) {
		t.Fatalf("%s %v", res, err)
	}
}

// Done without asking, it files what was proposed.
func TestDoneWithoutAskingUsesWhatWasProposed(t *testing.T) {
	s, book := fakeBook()
	if _, _, err := s.Execute(context.Background(), KindCategory, raw(CategoryPayload{Kind: "tx", ID: 1, Proposed: "식비"})); err != nil {
		t.Fatal(err)
	}
	if book.calls[0] != "charge 1 식비 remember=true" {
		t.Fatalf("%+v", book.calls)
	}
}

func TestNothingToFile(t *testing.T) {
	s, _ := fakeBook()
	if _, _, err := s.Execute(context.Background(), KindCategory, raw(CategoryPayload{Kind: "tx", ID: 1})); err == nil {
		t.Fatal("고른 분류가 없습니다")
	}
}

func TestAProductAndUndoAndSettle(t *testing.T) {
	s, book := fakeBook()
	ctx := context.Background()
	item := raw(CategoryPayload{Kind: "item", ID: 5, Answers: map[string]string{"category": "식비"}})
	if _, _, err := s.Execute(ctx, KindCategory, item); err != nil {
		t.Fatal(err)
	}
	if book.calls[0] != "item 5 식비" {
		t.Fatalf("%+v", book.calls)
	}
	if _, err := s.Undo(ctx, KindCategory, item); err != nil {
		t.Fatal(err)
	}
	if book.calls[1] != "item 5 기타" {
		t.Fatalf("%+v", book.calls)
	}
	if err := s.Settle(ctx, raw(CategoryPayload{Kind: "tx", ID: 9})); err != nil {
		t.Fatal(err)
	}
	if book.calls[2] != "charge 9 기타 remember=false" {
		t.Fatalf("%+v", book.calls)
	}
}

func TestABookThatRefuses(t *testing.T) {
	s, book := fakeBook()
	book.fail = errors.New("안 됩니다")
	if _, _, err := s.Execute(context.Background(), KindCategory, raw(CategoryPayload{Kind: "tx", ID: 1, Proposed: "식비"})); err == nil {
		t.Fatal("실패를 말해야 합니다")
	}
}
