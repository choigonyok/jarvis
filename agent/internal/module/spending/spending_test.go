package spending

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const bookJSON = `{
  "month":"2026-10","daysInMonth":31,"elapsedDays":6,"totalKrw":310000,"count":4,
  "prev":{"month":"2026-09","totalKrw":1500000,"sameDayKrw":250000},
  "budgetKrw":1550000,
  "categories":[{"name":"식비","totalKrw":200000,"count":2,"budgetKrw":400000,"prevKrw":500000},
                {"name":"구독","totalKrw":29000,"count":1,"budgetKrw":null,"prevKrw":29000}],
  "topMerchants":[{"name":"배달의민족","totalKrw":150000,"count":2}],
  "transactions":[
    {"merchant":"ANTHROPIC","category":"구독","amountKrw":29000,"currency":"USD","foreignAmount":20,"estimated":true,
     "installment":0,"approvedAt":"2026-10-05T03:00:00+09:00","memo":"","status":"ok","signedKrw":29000},
    {"merchant":"배달의민족","category":"식비","amountKrw":45900,"currency":"KRW","foreignAmount":null,"estimated":false,
     "installment":0,"approvedAt":"2026-10-04T19:02:00+09:00","memo":"","status":"cancelled","signedKrw":0}
  ],
  "recurring":[{"merchant":"ANTHROPIC","amountKrw":29000,"nextAt":"2026-11-05","months":3}],
  "unparsed":[{"chatName":"신한카드"}],
  "collector":{"latestMessageAt":null,"lastAlertAt":null,"stale":true,"lastError":""}
}`

func TestGetSpending(t *testing.T) {
	svc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/spending" || r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(bookJSON))
	}))
	defer svc.Close()

	mcpSrv := httptest.NewServer(New(NewStore(svc.URL, "tok")).Handler())
	defer mcpSrv.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "t", Version: "1"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: mcpSrv.URL}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	tools, err := session.ListTools(context.Background(), nil)
	if err != nil || len(tools.Tools) != len(Tools) {
		t.Fatalf("도구 목록이 자동 허용 목록과 다릅니다: %v %v", tools, err)
	}

	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "get_spending", Arguments: map[string]any{}})
	if err != nil || res.IsError {
		t.Fatalf("%v %+v", err, res)
	}
	text := res.Content[0].(*mcp.TextContent).Text
	for _, want := range []string{
		"2026-10 지출 ₩310,000 (4건, 31일 중 6일 경과)",
		"같은 날까지 ₩250,000 → ₩60,000 더 씀",
		"오늘까지 적정 ₩300,000 → ₩10,000 더 씀",
		"주의: 카톡 메시지가 한동안",
		"읽지 못한 카드 알림 1건",
		"식비 ₩200,000 (2건, 지난달 ₩500,000) · 예산 ₩400,000 중 ₩200,000 남음",
		"ANTHROPIC · 구독 · ₩29,000 ($20.00, 원화는 추정)",
		"취소됨(합계 제외)",
		"다음 예상 2026-11-05",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("%q 가 없습니다:\n%s", want, text)
		}
	}
}
