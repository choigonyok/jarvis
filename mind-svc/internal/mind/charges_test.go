package mind

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/act"
	"github.com/choigonyok/jarvis/mind-svc/internal/clock"
	"github.com/choigonyok/jarvis/mind-svc/internal/engine"
	"github.com/choigonyok/jarvis/mind-svc/internal/gate"
	"github.com/choigonyok/jarvis/mind-svc/internal/interpret"
	"github.com/choigonyok/jarvis/mind-svc/internal/ledger"
	"github.com/choigonyok/jarvis/mind-svc/internal/llm"
	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/obs"
	"github.com/choigonyok/jarvis/mind-svc/internal/slot"
	"github.com/choigonyok/jarvis/mind-svc/internal/store"
	"io"
	"log/slog"
)

// fakeBook is the book: what is unfiled, and what was filed.
type fakeBook struct {
	mu      sync.Mutex
	entries []slot.Charge
	patches []string
}

func (b *fakeBook) Unclear(_ context.Context, into any) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	raw, err := json.Marshal(map[string]any{"entries": b.entries, "categories": []string{"식비", "교통", "기타"}})
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, into)
}

func (b *fakeBook) FileCharge(_ context.Context, id int64, category string, remember bool) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.patches = append(b.patches, fmt.Sprintf(`/transactions/%d {"category":%q,"remember":%v}`, id, category, remember))
	return nil
}

func (b *fakeBook) FileItem(_ context.Context, id int64, category string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.patches = append(b.patches, fmt.Sprintf(`/items/%d {"category":%q}`, id, category))
	return nil
}

type answerLLM struct{ text string }

func (a answerLLM) Complete(context.Context, string, string) (string, error) { return a.text, nil }
func (a answerLLM) Ready() bool                                              { return a.text != "" }
func (a answerLLM) Model() string                                            { return "t" }
func (a answerLLM) Spent() float64                                           { return 0 }

var _ llm.Client = answerLLM{}

func chargeMind(t *testing.T, answer string, entries ...slot.Charge) (*Mind, *store.Memory, *fakeBook) {
	t.Helper()
	seoul, _ := time.LoadLocation("Asia/Seoul")
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	clk := clock.New()
	clk.Pin(time.Date(2026, 10, 31, 9, 0, 0, 0, seoul))
	st := store.NewMemory()
	book := &fakeBook{entries: entries}
	m := New(Options{Store: st, Log: obs.NewMemoryLog(quiet), LLM: answerLLM{text: answer}, Calendar: act.NewMemoryCalendar(), Notifier: &act.MemoryNotifier{},
		Loc: seoul, Now: clk.Now, Logger: quiet, Interpret: interpret.Config{Owner: "나"}, Policy: gate.DefaultPolicy(), Mode: "eval",
		Spending: &act.Spending{Book: book}})
	return m, st, book
}

func charge(id int64, merchant string) slot.Charge {
	return slot.Charge{Kind: "tx", ID: id, Merchant: merchant, AmountKrw: 9000, ApprovedAt: time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)}
}

func categoryIntents(t *testing.T, st *store.Memory) []model.Intent {
	t.Helper()
	all, err := st.Intents(context.Background(), 100)
	if err != nil {
		t.Fatal(err)
	}
	var out []model.Intent
	for _, in := range all {
		if in.Kind == act.KindCategory {
			out = append(out, in)
		}
	}
	return out
}

func field(in model.Intent, key string) model.Field {
	for _, f := range in.Fields {
		if f.Key == key {
			return f
		}
	}
	return model.Field{}
}

// A new kind asks, however sure the model: nothing is filed unasked before the
// operator has answered a few.
func TestANewKindAsksWithTheAnswerFilledIn(t *testing.T) {
	m, st, book := chargeMind(t, `[{"id":"tx:1","category":"식비","confidence":0.97}]`, charge(1, "써브웨이강남역점"))
	if err := m.fileCharges(context.Background()); err != nil {
		t.Fatal(err)
	}
	list := categoryIntents(t, st)
	if len(list) != 1 {
		t.Fatalf("%+v", list)
	}
	in := list[0]
	if in.Grade != string(engine.AskFilled) || in.Status != model.Pending || in.Decision != model.Ask || !in.Card {
		t.Fatalf("%+v", in)
	}
	if f := field(in, "category"); f.Value != "식비" || !f.Required {
		t.Fatalf("채워서 물어야 합니다: %+v", f)
	}
	if len(book.patches) != 0 {
		t.Fatalf("묻기 전에 쓰면 안 됩니다: %v", book.patches)
	}
}

// What the model cannot place is asked with the form empty, what it is unsure
// of too, and it keeps what the model thought to be scored.
func TestUnsureIsAskedBlank(t *testing.T) {
	m, st, _ := chargeMind(t, `[{"id":"tx:1","category":"식비","confidence":0.3}]`, charge(1, "낯선상호"), charge(2, "모르는곳"))
	if err := m.fileCharges(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, in := range categoryIntents(t, st) {
		if in.Grade != string(engine.AskBlank) || field(in, "category").Value != "" {
			t.Fatalf("%+v", in)
		}
	}
}

func TestABacklogComesAFewAtATime(t *testing.T) {
	var entries []slot.Charge
	for i := int64(1); i <= 7; i++ {
		entries = append(entries, charge(i, "상호"+string(rune('가'+i))))
	}
	m, st, _ := chargeMind(t, "", entries...)
	ctx := context.Background()
	if err := m.fileCharges(ctx); err != nil {
		t.Fatal(err)
	}
	if n := len(categoryIntents(t, st)); n != chargeCards {
		t.Fatalf("%d장", n)
	}
	// Nothing new while they wait, and the same one is not raised twice.
	if err := m.fileCharges(ctx); err != nil {
		t.Fatal(err)
	}
	if n := len(categoryIntents(t, st)); n != chargeCards {
		t.Fatalf("%d장", n)
	}
}

// Answered: the book is written, the merchant is remembered over the model.
func TestAnAnswerIsFiledAndKept(t *testing.T) {
	m, st, book := chargeMind(t, `[{"id":"tx:1","category":"식비","confidence":0.97}]`, charge(1, "쏘카"))
	ctx := context.Background()
	if err := m.fileCharges(ctx); err != nil {
		t.Fatal(err)
	}
	in := categoryIntents(t, st)[0]
	if _, err := m.Gate.ApproveWith(ctx, in.ID, map[string]string{"category": "교통", "remember": act.RememberYes}); err != nil {
		t.Fatal(err)
	}
	if len(book.patches) != 1 || !strings.Contains(book.patches[0], "/transactions/1") || !strings.Contains(book.patches[0], "교통") {
		t.Fatalf("%v", book.patches)
	}
	got, err := st.Class(ctx, slot.MerchantCategory.Name, "쏘카")
	if err != nil || got.Value != "교통" || got.By != model.ClassByOperator {
		t.Fatalf("%+v %v", got, err)
	}
	// Changed on the form: the ledger holds that against the judgement.
	done, _ := st.Intent(ctx, in.ID)
	rows := ledger.Build(ledger.Inputs{Intents: []model.Intent{done}, Now: m.Opts.Now(), Loc: m.Opts.Loc})
	if rows[0].Result != model.ResultCorrected {
		t.Fatalf("%+v", rows[0])
	}
}

// Approved as it stands: what was filled in is the answer, not a question left
// unanswered - the whole point of filling it in.
func TestAFilledFormApprovedAsItStands(t *testing.T) {
	m, st, book := chargeMind(t, `[{"id":"tx:1","category":"식비","confidence":0.97}]`, charge(1, "써브웨이"))
	ctx := context.Background()
	if err := m.fileCharges(ctx); err != nil {
		t.Fatal(err)
	}
	in := categoryIntents(t, st)[0]
	if _, err := m.Gate.ApproveWith(ctx, in.ID, nil); err != nil {
		t.Fatalf("그대로 승인하면 지나가야 합니다: %v", err)
	}
	if len(book.patches) != 1 || !strings.Contains(book.patches[0], "식비") || !strings.Contains(book.patches[0], `"remember":true`) {
		t.Fatalf("%v", book.patches)
	}
	done, _ := st.Intent(ctx, in.ID)
	rows := ledger.Build(ledger.Inputs{Intents: []model.Intent{done}, Now: m.Opts.Now(), Loc: m.Opts.Loc})
	if rows[0].Result != model.ResultApproved {
		t.Fatalf("%+v", rows[0])
	}
}

// An empty form is still a question.
func TestABlankFormMustBeAnswered(t *testing.T) {
	m, st, _ := chargeMind(t, "", charge(1, "상호"))
	ctx := context.Background()
	_ = m.fileCharges(ctx)
	in := categoryIntents(t, st)[0]
	if _, err := m.Gate.ApproveWith(ctx, in.ID, nil); err == nil || !strings.Contains(err.Error(), "분류") {
		t.Fatalf("%v", err)
	}
}

func TestOnlyThisOnceIsNotKept(t *testing.T) {
	m, st, _ := chargeMind(t, "", charge(1, "쏘카"))
	ctx := context.Background()
	_ = m.fileCharges(ctx)
	in := categoryIntents(t, st)[0]
	if _, err := m.Gate.ApproveWith(ctx, in.ID, map[string]string{"category": "교통", "remember": act.RememberNo}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Class(ctx, slot.MerchantCategory.Name, "쏘카"); err == nil {
		t.Fatal("이번만이면 기억하지 않습니다")
	}
}

// Turned down: 기타 for good, as the card says, and not asked again.
func TestTurnedDownStaysOther(t *testing.T) {
	m, st, book := chargeMind(t, "", charge(1, "쏘카"))
	ctx := context.Background()
	_ = m.fileCharges(ctx)
	in := categoryIntents(t, st)[0]
	if _, err := m.Gate.Reject(ctx, in.ID); err != nil {
		t.Fatal(err)
	}
	if len(book.patches) != 1 || !strings.Contains(book.patches[0], "기타") {
		t.Fatalf("%v", book.patches)
	}
	if err := m.fileCharges(ctx); err != nil {
		t.Fatal(err)
	}
	if n := len(categoryIntents(t, st)); n != 1 {
		t.Fatalf("다시 물었습니다: %d", n)
	}
}

// With a record of being right, it is done and told - in the evening, not
// with a buzz - and can be taken back.
func TestARecordEarnsDoingIt(t *testing.T) {
	m, st, book := chargeMind(t, `[{"id":"tx:1","category":"식비","confidence":0.95}]`, charge(1, "써브웨이"))
	ctx := context.Background()
	var rows []model.Outcome
	for i := 0; i < 12; i++ {
		rows = append(rows, model.Outcome{Source: "card", Kind: act.KindCategory, Skill: "slot", Context: "merchant.category", P: 0.95, Result: model.ResultApproved})
	}
	m.Gate.SetBook(engine.NewBook(rows))
	if err := m.fileCharges(ctx); err != nil {
		t.Fatal(err)
	}
	in := categoryIntents(t, st)[0]
	if in.Decision != model.Notify || in.Status != model.Executed || in.Tier != "digest" || len(in.Undo) == 0 {
		t.Fatalf("%+v", in)
	}
	if len(book.patches) != 1 || !strings.Contains(book.patches[0], `"remember":true`) {
		t.Fatalf("%v", book.patches)
	}
	if _, err := m.Gate.Undo(ctx, in.ID); err != nil {
		t.Fatal(err)
	}
	if len(book.patches) != 2 || !strings.Contains(book.patches[1], "기타") {
		t.Fatalf("%v", book.patches)
	}
}

// A merchant the operator placed before is not asked about.
func TestTheOperatorsAnswerIsUsed(t *testing.T) {
	m, st, book := chargeMind(t, "", charge(2, "쏘카"))
	ctx := context.Background()
	_ = st.PutClass(ctx, model.Class{Kind: slot.MerchantCategory.Name, Key: "쏘카", Value: "교통", P: 1, By: model.ClassByOperator})
	if err := m.fileCharges(ctx); err != nil {
		t.Fatal(err)
	}
	if len(book.patches) != 1 || !strings.Contains(book.patches[0], "교통") {
		t.Fatalf("%v", book.patches)
	}
}

// Without a book there is nothing to do, and no loop is started.
func TestNoBookNoCharges(t *testing.T) {
	m, st, _ := chargeMind(t, "")
	m.Opts.Spending = nil
	if err := m.fileCharges(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := len(categoryIntents(t, st)); n != 0 {
		t.Fatalf("%d", n)
	}
}

// The check the design set: what is bought on a cycle comes up as a card with
// what to buy filled in, not a line in the evening.
func TestARestockIsACardWithTheThingFilledIn(t *testing.T) {
	seoul, _ := time.LoadLocation("Asia/Seoul")
	now := time.Date(2026, 10, 31, 7, 0, 0, 0, seoul)
	m, st, _ := testMind(t, now)
	ctx := context.Background()
	for i, days := range []int{63, 42, 21} {
		_ = st.PutOccurrence(ctx, model.Occurrence{ID: "s#" + string(rune('a'+i)), Kind: "purchase.item", At: now.AddDate(0, 0, -days), Title: "생수 2L 12개", Class: "생수"})
	}
	_ = st.PutThingClass(ctx, "생수 2L 12개", "생수")
	if err := m.restock(ctx); err != nil {
		t.Fatal(err)
	}
	all, _ := st.Intents(ctx, 10)
	if len(all) != 1 {
		t.Fatalf("%+v", all)
	}
	in := all[0]
	if in.Kind != "cart.add" || in.Decision != model.Ask || !in.Card || !strings.Contains(in.Title, "담아둘까요") || !strings.Contains(in.Preview, "생수 2L 12개") || !strings.Contains(in.Body, "배송지") {
		t.Fatalf("%+v", in)
	}
}

// One the operator said not to buy again is not offered.
func TestNotBoughtAgainIsNotOffered(t *testing.T) {
	seoul, _ := time.LoadLocation("Asia/Seoul")
	now := time.Date(2026, 10, 31, 7, 0, 0, 0, seoul)
	m, st, _ := testMind(t, now)
	ctx := context.Background()
	for i, days := range []int{63, 42, 21} {
		_ = st.PutOccurrence(ctx, model.Occurrence{ID: "s#" + string(rune('a'+i)), Kind: "purchase.item", At: now.AddDate(0, 0, -days), Title: "생수 2L 12개", Class: "생수"})
	}
	_ = st.PutThingClass(ctx, "생수 2L 12개", "생수")
	_ = st.AddObservations(ctx, []model.Observation{{Source: "review", SubjectType: "물건", Subject: "생수 2L 12개", Dimension: "재구매", Polarity: -1, At: now}})
	if err := m.restock(ctx); err != nil {
		t.Fatal(err)
	}
	all, _ := st.Intents(ctx, 10)
	if len(all) != 1 || all[0].Kind != "info" || !strings.Contains(all[0].Body, "다시 안 산다") {
		t.Fatalf("%+v", all)
	}
}
