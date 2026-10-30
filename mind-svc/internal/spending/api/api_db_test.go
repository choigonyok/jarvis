package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/spending/ingest"
	"github.com/choigonyok/jarvis/mind-svc/internal/spending/store"
)

// The book's routes as mind puts them on its API: the screen writes a charge
// in, reads the month, corrects it, and the book says what it thinks of it.
func TestTheBookOnMindsAPI(t *testing.T) {
	dsn := os.Getenv("JARVIS_TEST_DSN")
	if dsn == "" {
		t.Skip("JARVIS_TEST_DSN 이 없어 건너뜁니다 (scripts/dbtest)")
	}
	ctx := context.Background()
	st, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	in := ingest.New(nil, st, time.Hour, log)
	s := New(st, in, log)
	mux := http.NewServeMux()
	route := func(pattern string, h http.HandlerFunc) { mux.HandleFunc(pattern, h) }
	s.Mount(route, route)
	do := func(method, path, body string, out any) int {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if out != nil {
			if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
				t.Fatalf("%s %s: %s", method, path, rec.Body.String())
			}
		}
		return rec.Code
	}
	today := time.Now().Format("2006-01-02")
	var added struct {
		ID int64 `json:"id"`
	}
	if code := do(http.MethodPost, Prefix+"/transactions", `{"date":"`+today+`","merchant":"현금 식당","amountKrw":9000,"category":"식비"}`, &added); code != http.StatusCreated || added.ID == 0 {
		t.Fatalf("%d %+v", code, added)
	}
	var month Response
	if code := do(http.MethodGet, Prefix+"/spending", "", &month); code != http.StatusOK {
		t.Fatalf("%d", code)
	}
	found := false
	for _, tx := range month.Transactions {
		found = found || (tx.ID == added.ID && tx.Status == "ok" && tx.SignedKrw == 9000)
	}
	if !found {
		t.Fatalf("%+v", month.Transactions)
	}
	if code := do(http.MethodPatch, Prefix+"/transactions/"+jsonNumber(added.ID), `{"memo":"점심"}`, nil); code != http.StatusOK {
		t.Fatalf("%d", code)
	}
	if code := do(http.MethodPatch, Prefix+"/transactions/"+jsonNumber(added.ID), `{"category":"없는 분류"}`, nil); code != http.StatusBadRequest {
		t.Fatalf("없는 분류: %d", code)
	}
	if code := do(http.MethodDelete, Prefix+"/transactions/"+jsonNumber(added.ID), "", nil); code != http.StatusOK {
		t.Fatalf("%d", code)
	}
	if code := do(http.MethodDelete, Prefix+"/transactions/"+jsonNumber(added.ID), "", nil); code != http.StatusNotFound {
		t.Fatalf("지운 것: %d", code)
	}
	req := httptest.NewRequest(http.MethodGet, Prefix+"/health", nil)
	rec := httptest.NewRecorder()
	s.Health()(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
}

func jsonNumber(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}

// mind's card round reads the same list, and files through the book.
func TestTheBookForTheCards(t *testing.T) {
	dsn := os.Getenv("JARVIS_TEST_DSN")
	if dsn == "" {
		t.Skip("JARVIS_TEST_DSN 이 없어 건너뜁니다 (scripts/dbtest)")
	}
	ctx := context.Background()
	st, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := New(st, ingest.New(nil, st, time.Hour, log), log)
	id, err := st.AddManual(ctx, store.Manual{ApprovedAt: time.Now(), Merchant: "처음 보는 상호", AmountKrw: 1000})
	if err != nil {
		t.Fatal(err)
	}
	var list struct {
		Entries []struct {
			Kind string `json:"kind"`
			ID   int64  `json:"id"`
		} `json:"entries"`
		Categories []string `json:"categories"`
	}
	if err := s.Unclear(ctx, &list); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range list.Entries {
		found = found || (e.ID == id && e.Kind == "tx")
	}
	if !found || len(list.Categories) == 0 {
		t.Fatalf("%+v", list)
	}
	if err := s.FileCharge(ctx, id, "식비", false); err != nil {
		t.Fatal(err)
	}
	if err := st.Delete(ctx, id); err != nil {
		t.Fatal(err)
	}
}
