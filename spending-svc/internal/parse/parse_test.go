package parse

import (
	"testing"
	"time"
)

var kst = time.FixedZone("KST", 9*3600)

func sent(s string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04", s, kst)
	if err != nil {
		panic(err)
	}
	return t
}

// The formats below are the shapes card alerts come in - labelled 알림톡,
// bulleted, one-fact-per-line, and the run-on SMS line. The ones marked 실제
// were copied from alerts this account received; the rest are written from
// the formats as published. A real alert that fails belongs here as a case.
func TestParsesPayments(t *testing.T) {
	cases := []struct {
		name, chat, text, sent string
		want                   Alert
	}{
		{
			name: "신한 알림톡(라벨형)",
			chat: "신한카드",
			text: "[신한카드] 승인\n홍*동님\n카드 : 신한카드(1234)\n금액 : 12,000원 일시불\n일시 : 10/06 12:34\n가맹점 : 스타벅스 강남역점\n누적금액 : 345,678원",
			sent: "2026-10-06 12:34",
			want: Alert{Issuer: "신한카드", CardTail: "1234", Kind: KindApproval, AmountKrw: 12000, Currency: "KRW",
				Merchant: "스타벅스 강남역점", ApprovedAt: sent("2026-10-06 12:34")},
		},
		{
			// 이 계정에 실제로 온 첫 결제 알림(2026-10-06).
			name: "하나 글머리표형",
			chat: "하나카드",
			text: "[하나카드] 승인 안내\n\n◆ 사용금액 : 3,250원 \n    - 손님명 : 홍*동님 \n◆ 이용현황\n    - 이용카드 : 하나 5*2*                    \n    - 거래종류 : 신용\n    - 거래구분 : 일시불\n    - 가맹점명 : 쿠팡(쿠페이)\n    - 거래시간 : 10/06 22:51 \n◆ 누적금액 : 412,800원",
			sent: "2026-10-06 22:51",
			want: Alert{Issuer: "하나카드", CardTail: "5*2*", Kind: KindApproval, AmountKrw: 3250, Currency: "KRW",
				Merchant: "쿠팡(쿠페이)", ApprovedAt: sent("2026-10-06 22:51")},
		},
		{
			// 이 계정에 실제로 온 신한카드 알림(2026-10-06).
			name: "신한 실제",
			chat: "신한카드",
			text: "[신한카드(7012)승인] 홍*동\n- 승인금액: 7,600원(일시불)\n- 승인일시: 10/06 22:55\n- 가맹점명: 쿠팡\n- 누적금액: 523,410원\n\n[신한카드 1544-7000]\n",
			sent: "2026-10-06 22:55",
			want: Alert{Issuer: "신한카드", CardTail: "7012", Kind: KindApproval, AmountKrw: 7600, Currency: "KRW",
				Merchant: "쿠팡", ApprovedAt: sent("2026-10-06 22:55")},
		},
		{
			name: "KB 줄바꿈형",
			chat: "KB국민카드",
			text: "KB국민카드1234승인\n홍*동님\n45,900원 일시불\n10/05 19:02\n배달의민족\n누적1,234,000원",
			sent: "2026-10-05 19:02",
			want: Alert{Issuer: "KB국민카드", CardTail: "1234", Kind: KindApproval, AmountKrw: 45900, Currency: "KRW",
				Merchant: "배달의민족", ApprovedAt: sent("2026-10-05 19:02")},
		},
		{
			name: "현대 할부",
			chat: "현대카드",
			text: "현대카드 M 승인\n홍*동\n1,290,000원 6개월\n10/04 15:10\n애플스토어 가로수길\n누적 2,000,000원",
			sent: "2026-10-04 15:11",
			want: Alert{Issuer: "현대카드", Kind: KindApproval, AmountKrw: 1290000, Currency: "KRW", Installment: 6,
				Merchant: "애플스토어 가로수길", ApprovedAt: sent("2026-10-04 15:10")},
		},
		{
			name: "SMS 한 줄",
			chat: "신한카드",
			text: "신한카드(1234)승인 홍*동 8,500원(일시불)10/06 08:15 GS25 역삼점 누적353,678원",
			sent: "2026-10-06 08:15",
			want: Alert{Issuer: "신한카드", CardTail: "1234", Kind: KindApproval, AmountKrw: 8500, Currency: "KRW",
				Merchant: "GS25 역삼점", ApprovedAt: sent("2026-10-06 08:15")},
		},
		{
			name: "취소",
			chat: "삼성카드",
			text: "삼성카드 승인취소\n홍*동님\n12,000원\n10/06 13:00\n스타벅스 강남역점",
			sent: "2026-10-06 13:00",
			want: Alert{Issuer: "삼성카드", Kind: KindCancel, AmountKrw: 12000, Currency: "KRW",
				Merchant: "스타벅스 강남역점", ApprovedAt: sent("2026-10-06 13:00")},
		},
		{
			name: "해외 달러",
			chat: "신한카드",
			text: "[신한카드] 해외승인\n홍*동님\n카드 : 신한카드(1234)\n금액 : USD 20.00\n일시 : 10/06 03:12\n가맹점 : ANTHROPIC",
			sent: "2026-10-06 03:12",
			want: Alert{Issuer: "신한카드", CardTail: "1234", Kind: KindApproval, Currency: "USD", ForeignAmount: 20,
				Merchant: "ANTHROPIC", ApprovedAt: sent("2026-10-06 03:12")},
		},
		{
			name: "연말을 넘긴 알림",
			chat: "하나카드",
			text: "하나카드 승인\n홍*동님\n30,000원 일시불\n12/31 23:58\n교보문고",
			sent: "2027-01-01 00:01",
			want: Alert{Issuer: "하나카드", Kind: KindApproval, AmountKrw: 30000, Currency: "KRW",
				Merchant: "교보문고", ApprovedAt: sent("2026-12-31 23:58")},
		},
		{
			name: "채널 이름에 카드가 없으면 본문의 대괄호",
			chat: "신한 SOL",
			text: "[신한카드] 승인\n홍*동님\n금액 : 3,000원\n일시 : 10/06 09:00\n사용처 : 티머니",
			sent: "2026-10-06 09:00",
			want: Alert{Issuer: "신한카드", Kind: KindApproval, AmountKrw: 3000, Currency: "KRW",
				Merchant: "티머니", ApprovedAt: sent("2026-10-06 09:00")},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, out := Parse(c.chat, c.text, sent(c.sent))
			if out != Parsed {
				t.Fatalf("outcome = %v, alert %+v", out, got)
			}
			if got != c.want {
				t.Fatalf("\n got %+v\nwant %+v", got, c.want)
			}
		})
	}
}

func TestIgnoresWhatIsNotAPayment(t *testing.T) {
	for name, text := range map[string]string{
		// The real message this account's 신한카드 channel has sent so far.
		"공지":  "[신한카드] SOL페이 업데이트 안내\n\n안녕하세요.\n신한카드에서 SOL페이 보안 강화를 위한 앱 업데이트 안내를 알려드립니다.\n금융 보안 강화를 위해 6.0.0 미만 버전의 SOL페이는 26년 10월 12일부터 이용이 불가합니다.",
		"광고":  "(광고)[신한카드] 이번 주말 결제 시 최대 10,000원 할인! 10/10 00:00부터",
		"명세서": "[신한카드] 10월 결제예정금액 안내\n결제예정 345,678원\n결제일 10/14",
	} {
		if _, out := Parse("신한카드", text, sent("2026-10-06 10:00")); out != NotPayment {
			t.Errorf("%s: outcome %v, want NotPayment", name, out)
		}
	}
}

// Something that is plainly a charge but whose merchant cannot be found must
// not be dropped and must not be guessed.
func TestUnreadableIsNotDropped(t *testing.T) {
	_, out := Parse("신한카드", "[신한카드] 승인\n홍*동님\n12,000원\n10/06 12:34", sent("2026-10-06 12:34"))
	if out != Unreadable {
		t.Fatalf("outcome %v, want Unreadable", out)
	}
	_, out = Parse("신한카드", "[신한카드] 해외승인\n금액 : JPY 1,200\n일시 : 10/06 12:34\n가맹점 : LAWSON", sent("2026-10-06 12:34"))
	if out != Unreadable {
		t.Fatalf("엔화: outcome %v, want Unreadable", out)
	}
}
