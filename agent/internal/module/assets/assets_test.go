package assets

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// A response in the shape assets-svc sends, nulls included: a venue with no
// key reads as null value, and a window nothing could be priced over has a
// null rate. The tools' output schemas are inferred from the Go types, so
// this is what proves a real response is not rejected on the way out.
const portfolioJSON = `{
  "holdings": [
    {"id":"kis:AAPL","venue":"kis","kind":"stock","symbol":"AAPL","name":"애플","quantity":3,
     "avgPrice":20.5,"price":25.1,"currency":"USD","valueKrw":1050000,"costKrw":857000},
    {"id":"upbit:BTC","venue":"upbit","kind":"coin","symbol":"BTC","name":"비트코인","quantity":0.00512345,
     "avgPrice":110000000,"price":150000000,"currency":"KRW","valueKrw":1002151.5,"costKrw":734911}
  ],
  "cashKrw": 120000,
  "totalKrw": 3722151.5,
  "costKrw": 1591911,
  "profitKrw": 460240.5,
  "returnRate": 0.2891,
  "usdKrw": 1394.2,
  "changes": {
    "day": {"rate": 0.0004, "amountKrw": 800, "missing": []},
    "month": {"rate": 0.05, "amountKrw": 98000, "missing": []},
    "year": {"rate": null, "amountKrw": null, "missing": ["애플"]}
  },
  "series": {"day": [], "month": [], "year": []},
  "at": "2026-10-06T09:00:00.000Z",
  "principal": {
    "since": "2026-05-01", "principalKrw": 4888773, "profitKrw": -1166621.5, "rate": -0.2386,
    "parts": [
      {"venue":"kis","valueKrw":1100000,"principalKrw":2678406,"profitKrw":-1578406,"rate":-0.589},
      {"venue":"gold","valueKrw":null,"principalKrw":0,"profitKrw":null,"rate":null}
    ],
    "flows": []
  },
  "fixed": [{"id":"housing-subscription","label":"주택청약","valueKrw":1000000}],
  "allocation": {
    "rows": [
      {"id":"growth","label":"유망주","valueKrw":1050000,"current":0.4637,"target":0.45,"gapKrw":-31000,"inBand":true,"symbols":["AAPL"]},
      {"id":"coin","label":"메이저코인","valueKrw":1002151,"current":0.4426,"target":0.3,"gapKrw":-322900,"inBand":false,"symbols":["BTC"]},
      {"id":"cash","label":"현금(달러)","valueKrw":120000,"current":0.053,"target":0.12,"gapKrw":151700,"inBand":true,"symbols":[]},
      {"id":"dividend","label":"배당주","valueKrw":0,"current":0,"target":0.08,"gapKrw":181100,"inBand":false,"symbols":[]},
      {"id":"gold","label":"금","valueKrw":0,"current":0,"target":0.05,"gapKrw":113200,"inBand":false,"symbols":[]}
    ],
    "moves": [
      {"from":"coin","to":"dividend","amountKrw":181100,"optional":false},
      {"from":"coin","to":"cash","amountKrw":141800,"optional":true}
    ],
    "totalKrw": 2264151
  },
  "problems": ["금현물: 키 없음"]
}`

func fakeAssets(t *testing.T, token string) *Store {
	t.Helper()
	mux := http.NewServeMux()
	guard := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer "+token {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":"인증이 필요합니다."}`))
				return
			}
			h(w, r)
		}
	}
	mux.HandleFunc("GET /portfolio", guard(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(portfolioJSON))
	}))
	mux.HandleFunc("GET /history", guard(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("days") != "30" {
			t.Errorf("days = %q", r.URL.Query().Get("days"))
		}
		_, _ = w.Write([]byte(`{"snapshots":[
			{"date":"2026-10-01","totalKrw":3600000,"cashKrw":100000,"costKrw":1500000},
			{"date":"2026-10-06","totalKrw":3722151,"cashKrw":120000,"costKrw":1591911}]}`))
	}))
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return NewStore(server.URL, token)
}

func connect(t *testing.T, m *Module) *mcp.ClientSession {
	t.Helper()
	server := httptest.NewServer(m.Handler())
	t.Cleanup(server.Close)
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: server.URL}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func call(t *testing.T, s *mcp.ClientSession, tool string, args map[string]any) (string, bool) {
	t.Helper()
	res, err := s.CallTool(context.Background(), &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", tool, err)
	}
	var out strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			out.WriteString(tc.Text)
		}
	}
	return out.String(), res.IsError
}

// The server has exactly the tools the pre-allowed list names, and no more.
// A write tool added here without a card would skip the gate entirely,
// because every name in Tools is on the auto-allow list.
func TestToolsAreExactlyTheReadOnlyList(t *testing.T) {
	s := connect(t, New(fakeAssets(t, "tok")))
	res, err := s.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	got := map[string]bool{}
	for _, tool := range res.Tools {
		got[QualifiedToolName(tool.Name)] = true
	}
	if len(got) != len(Tools) {
		t.Fatalf("도구 %d개, 목록 %d개: %v", len(got), len(Tools), got)
	}
	for _, name := range Tools {
		if !got[name] {
			t.Errorf("%s 가 서버에 없습니다", name)
		}
	}
}

func TestPortfolioReadsWithNulls(t *testing.T) {
	s := connect(t, New(fakeAssets(t, "tok")))
	text, isErr := call(t, s, "get_portfolio", nil)
	if isErr {
		t.Fatalf("도구 오류: %s", text)
	}
	for _, want := range []string{
		"총자산 ₩3,722,152",
		"원금 대비 −₩1,166,622 (−23.86%",
		"1일 +0.04%, 1개월 +5.00%", // 1년은 rate 가 null 이라 빠진다
		"애플(AAPL) · 한국투자증권 · 3개",
		"$25.10",
		"0.00512345개",
		"주택청약 · 고정 ₩1,000,000",
		"금현물 · 원금 ₩0 · 평가 불가",
		"조회 실패: 금현물: 키 없음",
		"목표 비중 이탈: 메이저코인 44%→목표 30%",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("%q 가 없습니다:\n%s", want, text)
		}
	}
	if strings.Contains(text, "1년") {
		t.Errorf("가격을 못 매긴 기간이 나왔습니다:\n%s", text)
	}
}

func TestAllocationNamesTransfers(t *testing.T) {
	s := connect(t, New(fakeAssets(t, "tok")))
	text, isErr := call(t, s, "get_allocation", nil)
	if isErr {
		t.Fatalf("도구 오류: %s", text)
	}
	for _, want := range []string{
		"메이저코인: 지금 44.3% (₩1,002,151) / 목표 30% · ₩322,900 초과 · 범위 밖 [BTC]",
		"메이저코인 → 배당주 ₩181,100\n",
		"메이저코인 → 현금(달러) ₩141,800 · 여유",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("%q 가 없습니다:\n%s", want, text)
		}
	}
}

func TestHistory(t *testing.T) {
	s := connect(t, New(fakeAssets(t, "tok")))
	text, isErr := call(t, s, "get_history", map[string]any{"days": 30})
	if isErr {
		t.Fatalf("도구 오류: %s", text)
	}
	if !strings.Contains(text, "2026-10-06 · ₩3,722,151") || !strings.Contains(text, "입출금이 섞인") {
		t.Errorf("스냅샷이 제대로 나오지 않습니다:\n%s", text)
	}
}

// A refused or unreachable service reaches the model as a tool error with the
// service's own sentence, so it says why rather than inventing a number.
func TestServiceErrorIsAToolError(t *testing.T) {
	s := connect(t, New(fakeAssets(t, "right").withToken("wrong")))
	text, isErr := call(t, s, "get_portfolio", nil)
	if !isErr || !strings.Contains(text, "인증이 필요합니다") {
		t.Fatalf("isErr=%v text=%q", isErr, text)
	}
	text, isErr = call(t, s, "get_allocation", nil)
	if !isErr {
		t.Fatalf("비중 조회 실패가 오류로 가지 않았습니다: %q", text)
	}
}

func (s *Store) withToken(token string) *Store {
	c := *s
	c.token = token
	return &c
}
