// Package api serves the spending book. Same bearer scheme as the rest of the
// stack: everything but /health needs the token.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/choigonyok/jarvis/spending-svc/internal/book"
	"github.com/choigonyok/jarvis/spending-svc/internal/category"
	"github.com/choigonyok/jarvis/spending-svc/internal/enrich"
	"github.com/choigonyok/jarvis/spending-svc/internal/ingest"
	"github.com/choigonyok/jarvis/spending-svc/internal/store"
)

type Server struct {
	store  *store.Store
	ingest *ingest.Ingester
	token  string
	log    *slog.Logger
	now    func() time.Time
}

func New(s *store.Store, in *ingest.Ingester, token string, log *slog.Logger) *Server {
	return &Server{store: s, ingest: in, token: token, log: log, now: time.Now}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.health)
	mux.HandleFunc("GET /spending", s.auth(s.month))
	mux.HandleFunc("POST /transactions", s.auth(s.add))
	mux.HandleFunc("PATCH /transactions/{id}", s.auth(s.patch))
	mux.HandleFunc("DELETE /transactions/{id}", s.auth(s.remove))
	mux.HandleFunc("PUT /budgets", s.auth(s.budget))
	mux.HandleFunc("POST /unparsed/{id}/dismiss", s.auth(s.dismiss))
	mux.HandleFunc("POST /unparsed/{id}/resolve", s.auth(s.resolve))
	// 묶음 가맹점 조회. 에이전트가 백그라운드에서 쓴다.
	mux.HandleFunc("GET /enrich/pending", s.auth(s.enrichPending))
	mux.HandleFunc("POST /transactions/{id}/items", s.auth(s.saveItems))
	mux.HandleFunc("POST /transactions/{id}/enrich-report", s.auth(s.enrichReport))
	mux.HandleFunc("POST /enrich/retry", s.auth(s.enrichRetry))
	mux.HandleFunc("PATCH /items/{id}", s.auth(s.patchItem))
	return mux
}

func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.token != "" && r.Header.Get("Authorization") != "Bearer "+s.token {
			writeErr(w, http.StatusUnauthorized, "인증이 필요합니다.")
			return
		}
		next(w, r)
	}
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	if err := s.store.Ping(r.Context()); err != nil {
		writeErr(w, http.StatusServiceUnavailable, "데이터베이스에 연결할 수 없습니다.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// Response is one month, plus what is not about any one month: the
// categories to choose from, what repeats, what could not be read, and
// whether alerts are arriving at all.
type Response struct {
	book.Summary
	CategoryNames []string         `json:"categoryNames"`
	Recurring     []book.Recurring `json:"recurring"`
	Unparsed      []store.Unparsed `json:"unparsed"`
	Collector     ingest.Status    `json:"collector"`
	Budgets       map[string]int64 `json:"budgets"`
	// EnrichBlocked names the sites whose order lookups are waiting on a
	// fresh login.
	EnrichBlocked []Site `json:"enrichBlocked"`
}

type Site struct {
	Source string `json:"source"`
	Label  string `json:"label"`
}

func (s *Server) month(w http.ResponseWriter, r *http.Request) {
	now := s.now()
	month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.Local)
	if q := r.URL.Query().Get("month"); q != "" {
		t, err := time.ParseInLocation("2006-01", q, time.Local)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "month 는 YYYY-MM 입니다.")
			return
		}
		month = t
	}
	res, err := s.build(r.Context(), month, s.now())
	if err != nil {
		s.log.Error("가계부를 읽지 못했습니다", "err", err)
		writeErr(w, http.StatusInternalServerError, "가계부를 읽지 못했습니다.")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) build(ctx context.Context, month, now time.Time) (Response, error) {
	txs, err := s.store.Range(ctx, month, month.AddDate(0, 1, 0))
	if err != nil {
		return Response{}, err
	}
	prev, err := s.store.Range(ctx, month.AddDate(0, -1, 0), month)
	if err != nil {
		return Response{}, err
	}
	// Recurring looks back from today whichever month is open: "what will
	// charge me next" is a question about now.
	recent, err := s.store.Range(ctx, now.AddDate(0, -5, 0), now.Add(time.Hour))
	if err != nil {
		return Response{}, err
	}
	budgets, err := s.store.Budgets(ctx)
	if err != nil {
		return Response{}, err
	}
	unparsed, err := s.store.Unparsed(ctx)
	if err != nil {
		return Response{}, err
	}
	_, blocked, err := s.store.Pending(ctx, 0)
	if err != nil {
		return Response{}, err
	}
	sites := []Site{}
	for _, src := range blocked {
		sites = append(sites, Site{Source: src, Label: enrich.Label(src)})
	}
	return Response{
		Summary:       book.Summarize(month, txs, prev, budgets, now),
		CategoryNames: category.All,
		Recurring:     book.FindRecurring(recent, now),
		Unparsed:      unparsed,
		Collector:     s.ingest.Status(),
		Budgets:       budgets,
		EnrichBlocked: sites,
	}, nil
}

type manualBody struct {
	Date      string `json:"date"` // YYYY-MM-DD
	Time      string `json:"time"` // HH:MM, optional
	Merchant  string `json:"merchant"`
	AmountKrw int64  `json:"amountKrw"`
	Category  string `json:"category"`
	Memo      string `json:"memo"`
}

func (b manualBody) toManual() (store.Manual, error) {
	clock := b.Time
	if clock == "" {
		clock = "12:00"
	}
	at, err := time.ParseInLocation("2006-01-02 15:04", b.Date+" "+clock, time.Local)
	if err != nil {
		return store.Manual{}, errors.New("날짜는 YYYY-MM-DD, 시각은 HH:MM 입니다.")
	}
	merchant := strings.TrimSpace(b.Merchant)
	if merchant == "" || len([]rune(merchant)) > 80 {
		return store.Manual{}, errors.New("사용처를 적어 주세요.")
	}
	if b.AmountKrw <= 0 || b.AmountKrw > 1_000_000_000 {
		return store.Manual{}, errors.New("금액은 0보다 커야 합니다.")
	}
	if b.Category != "" && !category.Valid(b.Category) {
		return store.Manual{}, errors.New("없는 분류입니다.")
	}
	return store.Manual{ApprovedAt: at, Merchant: merchant, AmountKrw: b.AmountKrw,
		Category: b.Category, Memo: limit(b.Memo, 200)}, nil
}

func (s *Server) add(w http.ResponseWriter, r *http.Request) {
	var b manualBody
	if !readJSON(w, r, &b) {
		return
	}
	m, err := b.toManual()
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	id, err := s.store.AddManual(r.Context(), m)
	if err != nil {
		s.log.Error("내역을 추가하지 못했습니다", "err", err)
		writeErr(w, http.StatusInternalServerError, "내역을 추가하지 못했습니다.")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]int64{"id": id})
}

func (s *Server) patch(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var b struct {
		Category  *string `json:"category"`
		Remember  bool    `json:"remember"`
		Memo      *string `json:"memo"`
		Excluded  *bool   `json:"excluded"`
		Merchant  *string `json:"merchant"`
		AmountKrw *int64  `json:"amountKrw"`
	}
	if !readJSON(w, r, &b) {
		return
	}
	if b.Category != nil && !category.Valid(*b.Category) {
		writeErr(w, http.StatusBadRequest, "없는 분류입니다.")
		return
	}
	if b.AmountKrw != nil && *b.AmountKrw < 0 {
		writeErr(w, http.StatusBadRequest, "금액은 0 이상이어야 합니다.")
		return
	}
	if b.Merchant != nil {
		v := strings.TrimSpace(*b.Merchant)
		if v == "" {
			writeErr(w, http.StatusBadRequest, "사용처를 적어 주세요.")
			return
		}
		b.Merchant = &v
	}
	if b.Memo != nil {
		v := limit(*b.Memo, 200)
		b.Memo = &v
	}
	n, err := s.store.Update(r.Context(), id, store.Patch{
		Category: b.Category, Remember: b.Remember, Memo: b.Memo,
		Excluded: b.Excluded, Merchant: b.Merchant, AmountKrw: b.AmountKrw,
	})
	if s.storeErr(w, err, "내역을 고치지 못했습니다.") {
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"updated": n})
}

func (s *Server) remove(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if s.storeErr(w, s.store.Delete(r.Context(), id), "내역을 지우지 못했습니다.") {
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) budget(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Category  string `json:"category"`
		AmountKrw int64  `json:"amountKrw"`
	}
	if !readJSON(w, r, &b) {
		return
	}
	if b.Category != "" && !category.Valid(b.Category) {
		writeErr(w, http.StatusBadRequest, "없는 분류입니다.")
		return
	}
	if err := s.store.SetBudget(r.Context(), b.Category, b.AmountKrw); err != nil {
		s.log.Error("예산을 저장하지 못했습니다", "err", err)
		writeErr(w, http.StatusInternalServerError, "예산을 저장하지 못했습니다.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) dismiss(w http.ResponseWriter, r *http.Request) {
	if s.storeErr(w, s.store.ResolveUnparsed(r.Context(), r.PathValue("id")), "처리하지 못했습니다.") {
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// resolve writes an unreadable alert in by hand. The entry keeps the
// message id, so a replay that can read it later does not add it again.
func (s *Server) resolve(w http.ResponseWriter, r *http.Request) {
	var b manualBody
	if !readJSON(w, r, &b) {
		return
	}
	m, err := b.toManual()
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	m.SourceRef = r.PathValue("id")
	if _, err := s.store.AddManual(r.Context(), m); err != nil {
		s.log.Error("내역을 추가하지 못했습니다", "err", err)
		writeErr(w, http.StatusInternalServerError, "내역을 추가하지 못했습니다.")
		return
	}
	if s.storeErr(w, s.store.ResolveUnparsed(r.Context(), m.SourceRef), "처리하지 못했습니다.") {
		return
	}
	writeJSON(w, http.StatusCreated, map[string]bool{"ok": true})
}

// Pending is the background lookup's work list.
type Pending struct {
	ID          int64     `json:"id"`
	Source      string    `json:"source"`
	SourceLabel string    `json:"sourceLabel"`
	Merchant    string    `json:"merchant"`
	AmountKrw   int64     `json:"amountKrw"`
	ApprovedAt  time.Time `json:"approvedAt"`
	Issuer      string    `json:"issuer"`
	CardTail    string    `json:"cardTail"`
	Note        string    `json:"note"`
}

func (s *Server) enrichPending(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 20 {
		limit = 5
	}
	txs, blocked, err := s.store.Pending(r.Context(), limit)
	if s.storeErr(w, err, "조회 대기열을 읽지 못했습니다.") {
		return
	}
	out := []Pending{}
	for _, t := range txs {
		src := ""
		if t.EnrichSource != nil {
			src = *t.EnrichSource
		}
		out = append(out, Pending{ID: t.ID, Source: src, SourceLabel: enrich.Label(src), Merchant: t.Merchant,
			AmountKrw: t.AmountKrw, ApprovedAt: t.ApprovedAt.In(time.Local), Issuer: t.Issuer, CardTail: t.CardTail,
			Note: t.EnrichNote})
	}
	writeJSON(w, http.StatusOK, map[string]any{"transactions": out, "blocked": blocked})
}

func (s *Server) saveItems(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var b struct {
		Items []struct {
			Name     string `json:"name"`
			Quantity int    `json:"quantity"`
			PriceKrw int64  `json:"priceKrw"`
			Category string `json:"category"`
		} `json:"items"`
		Note string `json:"note"`
	}
	if !readJSON(w, r, &b) {
		return
	}
	if len(b.Items) == 0 || len(b.Items) > 50 {
		writeErr(w, http.StatusBadRequest, "상품은 1~50개입니다.")
		return
	}
	items := make([]enrich.Item, 0, len(b.Items))
	for _, it := range b.Items {
		name := limit(it.Name, 120)
		if name == "" {
			writeErr(w, http.StatusBadRequest, "상품 이름이 비었습니다.")
			return
		}
		if !category.Valid(it.Category) {
			writeErr(w, http.StatusBadRequest, "없는 분류입니다: "+it.Category+". 가능한 분류: "+strings.Join(category.All, ", "))
			return
		}
		items = append(items, enrich.Item{Name: name, Quantity: it.Quantity, ListedKrw: it.PriceKrw, Category: it.Category})
	}
	err := s.store.SaveItems(r.Context(), id, items, limit(b.Note, 200))
	if err != nil && !errors.Is(err, store.ErrNotFound) && !strings.Contains(err.Error(), "주문") &&
		!strings.Contains(err.Error(), "상품") {
		s.log.Error("상품을 저장하지 못했습니다", "err", err)
		writeErr(w, http.StatusInternalServerError, "상품을 저장하지 못했습니다.")
		return
	}
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	if err != nil {
		// Validation: the agent reads this sentence and corrects itself.
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.log.Info("주문 상품을 기록했습니다", "tx", id, "items", len(items))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) enrichReport(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var b struct {
		Outcome string `json:"outcome"`
		Note    string `json:"note"`
	}
	if !readJSON(w, r, &b) {
		return
	}
	err := s.store.Report(r.Context(), id, b.Outcome, limit(b.Note, 200))
	if err != nil && strings.HasPrefix(err.Error(), "outcome") {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if s.storeErr(w, err, "결과를 기록하지 못했습니다.") {
		return
	}
	s.log.Info("주문 조회 결과", "tx", id, "outcome", b.Outcome, "note", b.Note)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) enrichRetry(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Source string `json:"source"`
		ID     int64  `json:"id"`
	}
	if !readJSON(w, r, &b) {
		return
	}
	if b.ID == 0 && b.Source != enrich.Coupang && b.Source != enrich.NaverPay {
		writeErr(w, http.StatusBadRequest, "source 나 id 가 필요합니다.")
		return
	}
	n, err := s.store.Retry(r.Context(), b.Source, b.ID)
	if s.storeErr(w, err, "다시 시도하도록 바꾸지 못했습니다.") {
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"queued": n})
}

func (s *Server) patchItem(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var b struct {
		Category string `json:"category"`
	}
	if !readJSON(w, r, &b) {
		return
	}
	if !category.Valid(b.Category) {
		writeErr(w, http.StatusBadRequest, "없는 분류입니다.")
		return
	}
	if s.storeErr(w, s.store.SetItemCategory(r.Context(), id, b.Category), "분류를 바꾸지 못했습니다.") {
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) storeErr(w http.ResponseWriter, err error, msg string) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, store.ErrNotFound):
		writeErr(w, http.StatusNotFound, err.Error())
	case strings.HasPrefix(err.Error(), "카드 알림"):
		writeErr(w, http.StatusBadRequest, err.Error())
	default:
		s.log.Error(msg, "err", err)
		writeErr(w, http.StatusInternalServerError, msg)
	}
	return true
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "id 가 숫자가 아닙니다.")
		return 0, false
	}
	return id, true
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
	if err != nil {
		writeErr(w, http.StatusRequestEntityTooLarge, "본문이 너무 큽니다.")
		return false
	}
	if err := json.Unmarshal(body, v); err != nil {
		writeErr(w, http.StatusBadRequest, "본문을 읽을 수 없습니다.")
		return false
	}
	return true
}

func limit(s string, n int) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
