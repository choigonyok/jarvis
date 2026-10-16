// Package probes is the list of things the status screen answers, and how
// each one is decided. Adding a check is one entry in All.
package probes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/choigonyok/jarvis/status-svc/internal/check"
)

type Config struct {
	Token       string
	KakaoURL    string
	SpendingURL string
	AssetsURL   string
	CalendarURL string
	ChatURL     string
	WorkoutURL  string
	AgentURL    string
	MindURL     string
	MemoryURL   string
	ContextURL  string
	BrowserAddr string // host:port of the browser MCP bridge
	// HostFile is what scripts/host-check writes on the Mac mini (FileVault,
	// Time Machine, the iMessage mirror, the last backup) - things a
	// container cannot see for itself.
	HostFile string
	DB       *pgxpool.Pool
}

type env struct {
	Config
	client *http.Client
}

var Groups = []string{"수집", "가계부 상품 상세", "외부 계정", "기반"}

func All(cfg Config) []check.Probe {
	e := &env{Config: cfg, client: &http.Client{Timeout: 15 * time.Second}}
	slow := &http.Client{Timeout: 50 * time.Second}

	return []check.Probe{
		// ── 수집 ───────────────────────────────────────────────────────
		{
			ID: "kakao.collector", Group: "수집", Name: "카톡 수집기",
			Rule:  "수집기가 1초마다 카카오톡 DB를 읽고 있으면 정상, 읽다 실패하거나 1분 넘게 읽지 않았으면 이상.",
			Fix:   "kakaotalk 컨테이너 로그를 보세요. 복호화 실패라면 맥의 카카오톡 계정이 바뀌었을 수 있어요.",
			Deps:  []string{"svc.kakaotalk"},
			Every: 30 * time.Second,
			Run:   e.kakaoCollector,
		},
		{
			ID: "kakao.messages", Group: "수집", Name: "카톡 새 메시지",
			Rule:  "마지막 카톡이 12시간 안이면 정상, 12–48시간이면 주의, 48시간을 넘으면 이상으로 추정.",
			Fix:   "맥에서 카카오톡이 켜져 있고 로그인돼 있는지 확인하세요. 꺼져 있던 동안의 메시지는 켜면 다시 들어와요.",
			Deps:  []string{"kakao.collector"},
			Every: 30 * time.Second,
			Run:   e.kakaoMessages,
		},
		{
			ID: "mind.reading", Group: "수집", Name: "대화 해석",
			Rule:  "mind 가 카톡·iMessage·jarvis 대화를 읽고 있으면 정상. 읽히지 않은 메시지가 30분 넘게 밀리면 주의, 2시간을 넘거나 LLM 키가 없으면 이상. 방이 3분 조용해지면(나와의 채팅은 45초) 읽는 게 보통이에요.",
			Fix:   "mind 콘솔(맥미니 127.0.0.1:8102)에서 밀린 방을 보세요. 해석이 실패하고 있다면 오늘 LLM 예산(MIND_DAILY_USD)이나 키를 확인하세요.",
			Deps:  []string{"svc.mind", "kakao.messages"},
			Every: time.Minute,
			Run:   e.mindReading,
		},
		{
			ID: "mind.memory", Group: "수집", Name: "기억으로 보내기",
			Rule:  "해석에서 나온 기억할 사실이 기억 서비스에 들어가고 있으면 정상. 1시간 넘게 기다리는 사실이 있으면 주의, 6시간을 넘으면 이상.",
			Fix:   "기억 서비스가 응답하는지, 오늘 LLM 예산(MEMORY_DAILY_USD)이 남았는지 보세요. 기다리던 사실은 다시 보내져요.",
			Deps:  []string{"svc.memory", "mind.reading"},
			Every: time.Minute,
			Run:   e.mindMemory,
		},
		{
			ID: "context.location", Group: "수집", Name: "위치 수신",
			Rule:  "폰의 OwnTracks 가 보낸 마지막 위치가 36시간 안이면 정상, 36–72시간이면 주의, 72시간을 넘으면 이상. 한 번도 받지 않았으면 측정하지 않아요(설정 전). 크게 움직이지 않는 날은 위치가 드물게 와요.",
			Fix:   "폰에서 OwnTracks 가 위치 권한 '항상 허용'인지, 모드가 Quiet 가 아닌지, 서버 주소·비밀번호가 맞는지 보세요. 배터리가 8% 아래면 일부러 조용히 둬요.",
			Deps:  []string{"svc.context"},
			Every: 5 * time.Minute,
			Run:   e.location,
		},
		{
			ID: "spending.alerts", Group: "수집", Name: "카드 결제 알림",
			Rule:  "가계부가 수집기를 읽을 수 있고 마지막 카드 알림이 3일 안이면 정상, 3–7일이면 주의, 7일을 넘거나 읽기에 실패하면 이상.",
			Fix:   "카드를 안 쓴 날이 이어졌다면 정상이에요. 아니라면 카톡 수집과 spending 컨테이너 로그를 보세요.",
			Deps:  []string{"kakao.messages", "svc.spending"},
			Every: time.Minute,
			Run:   e.spendingAlerts,
		},
		{
			ID: "spending.unparsed", Group: "수집", Name: "읽지 못한 카드 알림",
			Rule:  "카드사 알림 중 금액·가맹점을 읽지 못해 합계에서 빠진 건이 없으면 정상, 있으면 주의.",
			Fix:   "가계부 탭에서 직접 적거나 버리세요.",
			Deps:  []string{"svc.spending"},
			Every: time.Minute,
			Run:   e.spendingUnparsed,
		},

		// ── 가계부 상품 상세 ──────────────────────────────────────────────
		{
			ID: "enrich.coupang", Group: "가계부 상품 상세", Name: "쿠팡 주문 내역",
			Rule:  "에이전트가 대화·작업이 없을 때 30분마다 브라우저 새 탭에서 쿠팡 주문목록을 열어 봐요. 열리면 정상, 로그인 화면으로 넘어가면 이상. 여기서는 그 결과를 읽어요.",
			Fix:   "화면 탭에서 에이전트 브라우저로 coupang.com에 로그인하세요. 로그인이 끝나면 멈춰 있던 상품 상세 조회도 다시 시도해야 해요.",
			Deps:  []string{"browser"},
			Every: 30 * time.Minute, MinGap: time.Minute,
			Run: e.enrich(coupang),
		},
		{
			ID: "enrich.naverpay", Group: "가계부 상품 상세", Name: "네이버페이 주문 내역",
			Rule:  "pay.naver.com은 결제 감시가 모든 요청을 결재 카드로 세우므로 자동으로 열어 보지 않아요. 상품 상세 조회가 로그인을 요구받았다고 기록하면 이상, 아니면 측정하지 않아요.",
			Fix:   "화면 탭에서 에이전트 브라우저로 naver.com에 로그인하세요. '로그인 상태 유지'를 켜야 재시작 뒤에도 남아요.",
			Every: 5 * time.Minute,
			Run:   e.recordOnly(naverpay),
		},

		// ── 외부 계정 ──────────────────────────────────────────────────
		{
			ID: "assets.upbit", Group: "외부 계정", Name: "업비트 잔고",
			Rule:  "자산 서비스가 업비트 잔고를 읽으면 정상, 오류를 돌려주면 이상. 10분마다 확인해요.",
			Fix:   "업비트 API 키의 허용 IP와 만료일을 확인하세요.",
			Deps:  []string{"svc.assets"},
			Every: 10 * time.Minute, MinGap: time.Minute,
			Run: e.portfolioVenue(slow, "업비트:"),
		},
		{
			ID: "assets.kis", Group: "외부 계정", Name: "한국투자증권 잔고",
			Rule:  "자산 서비스가 주식 계좌 잔고를 읽으면 정상, 오류를 돌려주면 이상. 10분마다 확인해요.",
			Fix:   "KIS 앱키 만료(1년)와 계좌번호를 확인하세요. 토큰은 1분에 한 번만 발급되니 서비스 재시작 직후 1분은 실패할 수 있어요.",
			Deps:  []string{"svc.assets"},
			Every: 10 * time.Minute, MinGap: time.Minute,
			Run: e.portfolioVenue(slow, "한국투자증권:"),
		},
		{
			ID: "assets.gold", Group: "외부 계정", Name: "금현물 계좌",
			Rule:  "자산 서비스가 금현물 계좌를 읽으면 정상, 오류를 돌려주면 이상.",
			Fix:   "금현물 계좌용 KIS 앱키(주식 계좌와 별도)를 확인하세요.",
			Deps:  []string{"svc.assets"},
			Every: 10 * time.Minute, MinGap: time.Minute,
			Run: e.portfolioVenue(slow, "금현물:"),
		},
		{
			ID: "assets.flows", Group: "외부 계정", Name: "원금 기록",
			Rule:  "업비트 입출금 내역과 직접 적은 원금 기록을 읽으면 정상, 어느 쪽이든 실패하면 주의(수익률이 틀려져요).",
			Fix:   "업비트 키에 입출금 조회 권한이 있는지 확인하세요.",
			Deps:  []string{"svc.assets"},
			Every: 10 * time.Minute, MinGap: time.Minute,
			Run: e.portfolioVenue(slow, "업비트 입출금 내역:", "원금 기록:"),
		},
		{
			ID: "assets.snapshot", Group: "외부 계정", Name: "자산 일별 기록",
			Rule:  "마지막 자산 스냅샷이 36시간 안이면 정상, 4일 안이면 주의, 그보다 오래되면 이상.",
			Fix:   "스냅샷은 자산 탭이나 에이전트가 자산을 조회할 때 쌓여요.",
			Deps:  []string{"postgres"},
			Every: 5 * time.Minute,
			Run:   e.assetSnapshot,
		},
		{
			ID: "calendar.uniple", Group: "외부 계정", Name: "커플 캘린더",
			Rule:  "캘린더 서비스가 오늘 일정을 읽으면 정상, 실패하면 이상. 5분마다 확인해요.",
			Fix:   "uniple 토큰이 끊겼다면 새 시크릿 창에서 로그인해 refresh_token을 뽑고, 로그아웃하지 말고 창만 닫은 뒤 .env의 UNIPLE_REFRESH_TOKEN을 바꿔 calendar를 재시작하세요.",
			Deps:  []string{"svc.calendar"},
			Every: 5 * time.Minute, MinGap: 30 * time.Second,
			Run: e.calendar,
		},

		// ── 기반 ──────────────────────────────────────────────────────
		{
			ID: "agent", Group: "기반", Name: "에이전트",
			Rule: "에이전트가 응답하면 정상.", Fix: "agent 컨테이너 로그를 보세요.",
			Every: 30 * time.Second,
			Run:   e.httpOK(cfg.AgentURL + "/healthz"),
		},
		{
			ID: "claude", Group: "기반", Name: "Claude Code 구독",
			Rule:  "에이전트가 실행 실패를 저장하지 않아 여기서는 확인하지 않아요. 인증 실패는 대화 화면에 오류로 떠요.",
			Fix:   "대화에서 인증 오류가 나면 claude setup-token으로 CLAUDE_CODE_OAUTH_TOKEN을 새로 받으세요.",
			Deps:  []string{"agent"},
			Every: time.Hour,
			Run: func(context.Context) check.Verdict {
				return check.Verdict{State: check.Unknown, Summary: "확인하지 않는 항목이에요."}
			},
		},
		{
			ID: "browser", Group: "기반", Name: "에이전트 브라우저",
			Rule: "브라우저 컨테이너의 MCP 포트가 열려 있으면 정상.", Fix: "browser 컨테이너를 재시작하세요. 로그인은 프로필 볼륨에 남아 있어요.",
			Every: 30 * time.Second,
			Run:   e.tcp(cfg.BrowserAddr),
		},
		{
			ID: "postgres", Group: "기반", Name: "데이터베이스",
			Rule: "Postgres에 연결되면 정상.", Fix: "postgres 컨테이너와 디스크 용량을 확인하세요.",
			Every: 30 * time.Second,
			Run:   e.postgres,
		},
		e.service("svc.kakaotalk", "카톡 수집 서비스", cfg.KakaoURL, nil),
		e.service("svc.spending", "가계부 서비스", cfg.SpendingURL, []string{"postgres"}),
		e.service("svc.assets", "자산 서비스", cfg.AssetsURL, nil),
		e.service("svc.calendar", "캘린더 서비스", cfg.CalendarURL, []string{"postgres"}),
		e.service("svc.chat", "대화 기록 서비스", cfg.ChatURL, []string{"postgres"}),
		e.service("svc.workout", "운동 기록 서비스", cfg.WorkoutURL, []string{"postgres"}),
		e.service("svc.mind", "인지 루프(mind)", cfg.MindURL, []string{"postgres"}),
		e.service("svc.memory", "기억 서비스", cfg.MemoryURL, []string{"postgres"}),
		e.service("svc.context", "위치·현재 상태", cfg.ContextURL, []string{"postgres"}),
		e.hostFileVault(),
		e.hostTimeMachine(),
		e.hostMirror(),
		e.hostBackup(),
	}
}

// ── 수집 ───────────────────────────────────────────────────────────────

func (e *env) kakaoCollector(ctx context.Context) check.Verdict {
	var st struct {
		Running    bool      `json:"running"`
		LastPollAt time.Time `json:"last_poll_at"`
		LastError  string    `json:"last_error"`
		Total      int64     `json:"total_stored"`
		Latency    int64     `json:"last_latency_ms"`
	}
	if err := e.get(ctx, e.client, e.KakaoURL+"/status", &st); err != nil {
		return fail("수집기 상태를 읽지 못했어요: " + err.Error())
	}
	facts := []string{fmt.Sprintf("저장된 메시지 %d건", st.Total), fmt.Sprintf("마지막 읽기 %dms", st.Latency)}
	v := check.Verdict{ObservedAt: ptr(st.LastPollAt), ObservedLabel: "마지막 읽기", Facts: facts}
	switch {
	case !st.Running:
		v.State, v.Summary = check.Fail, "수집 루프가 멈춰 있어요."
	case st.LastError != "":
		v.State, v.Summary = check.Fail, "카카오톡 DB를 읽다 실패했어요: "+clip(st.LastError, 140)
	case time.Since(st.LastPollAt) > time.Minute:
		v.State, v.Summary = check.Fail, "1분 넘게 DB를 읽지 않았어요."
	default:
		v.State, v.Summary = check.OK, "1초마다 카카오톡 DB를 읽고 있어요."
	}
	return v
}

func (e *env) kakaoMessages(ctx context.Context) check.Verdict {
	var p struct {
		Messages []struct {
			SentAt   int64  `json:"sent_at"`
			ChatName string `json:"chat_name"`
		} `json:"messages"`
	}
	if err := e.get(ctx, e.client, e.KakaoURL+"/messages/recent?limit=1", &p); err != nil {
		return fail("최근 메시지를 읽지 못했어요: " + err.Error())
	}
	if len(p.Messages) == 0 {
		return check.Verdict{State: check.Unknown, Summary: "아직 수집된 메시지가 없어요."}
	}
	at := time.Unix(p.Messages[0].SentAt, 0)
	age := time.Since(at)
	v := check.Verdict{ObservedAt: &at, ObservedLabel: "마지막 메시지",
		Facts: []string{"마지막 메시지가 온 방: " + p.Messages[0].ChatName}}
	switch {
	case age > 48*time.Hour:
		v.State, v.Summary = check.Fail, fmt.Sprintf("%s째 새 카톡이 없어요. 맥의 카카오톡이 꺼졌거나 로그아웃된 것으로 보여요.", span(age))
	case age > 12*time.Hour:
		v.State, v.Summary = check.Warn, fmt.Sprintf("%s째 새 카톡이 없어요. 카카오톡이 꺼졌을 수 있어요.", span(age))
	default:
		v.State, v.Summary = check.OK, "새 메시지가 들어오고 있어요."
	}
	return v
}

type mindStatus struct {
	LLM struct {
		Ready bool    `json:"ready"`
		Spent float64 `json:"spentUsd"`
	} `json:"llm"`
	Rooms         int            `json:"rooms"`
	Unread        int            `json:"unread"`
	OldestUnread  *time.Time     `json:"oldestUnread"`
	LastRead      *time.Time     `json:"lastRead"`
	ReadError     string         `json:"readError"`
	Memory        bool           `json:"memory"`
	Facts         map[string]int `json:"facts"`
	OldestPending *time.Time     `json:"oldestPending"`
	FactError     string         `json:"factError"`
}

func (e *env) mindReading(ctx context.Context) check.Verdict {
	var st mindStatus
	if err := e.get(ctx, e.client, e.MindURL+"/status", &st); err != nil {
		return fail("mind 상태를 읽지 못했어요: " + err.Error())
	}
	facts := []string{fmt.Sprintf("대화방 %d개 · 아직 안 읽은 메시지 %d개", st.Rooms, st.Unread), fmt.Sprintf("오늘 해석 비용 $%.4f", st.LLM.Spent)}
	if st.ReadError != "" {
		facts = append(facts, "최근 해석 실패: "+clip(st.ReadError, 140))
	}
	v := check.Verdict{ObservedAt: st.LastRead, ObservedLabel: "마지막 해석", Facts: facts}
	var behind time.Duration
	if st.OldestUnread != nil {
		behind = time.Since(*st.OldestUnread)
	}
	switch {
	case !st.LLM.Ready:
		v.State, v.Summary = check.Fail, "LLM 키가 없어 대화를 쌓기만 하고 읽지 못해요."
	case behind > 2*time.Hour:
		v.State, v.Summary = check.Fail, fmt.Sprintf("%s째 읽히지 않은 대화가 있어요.", span(behind))
	case behind > 30*time.Minute:
		v.State, v.Summary = check.Warn, fmt.Sprintf("%s째 읽히지 않은 대화가 있어요.", span(behind))
	default:
		v.State, v.Summary = check.OK, "대화를 읽고 있어요."
	}
	return v
}

func (e *env) location(ctx context.Context) check.Verdict {
	var now struct {
		FixAt   *time.Time `json:"fixAt"`
		Summary string     `json:"summary"`
		Visits  []struct {
			Region string `json:"region"`
		} `json:"visits"`
	}
	if err := e.get(ctx, e.client, e.ContextURL+"/now", &now); err != nil {
		return fail("위치 상태를 읽지 못했어요: " + err.Error())
	}
	v := check.Verdict{ObservedAt: now.FixAt, ObservedLabel: "마지막 위치", Facts: []string{now.Summary}}
	if now.FixAt == nil {
		v.State, v.Summary = check.Unknown, "아직 위치를 받은 적이 없어요(OwnTracks 설정 전)."
		return v
	}
	switch age := time.Since(*now.FixAt); {
	case age > 72*time.Hour:
		v.State, v.Summary = check.Fail, fmt.Sprintf("%s째 위치가 오지 않아요.", span(age))
	case age > 36*time.Hour:
		v.State, v.Summary = check.Warn, fmt.Sprintf("%s째 위치가 오지 않아요.", span(age))
	default:
		v.State, v.Summary = check.OK, "위치를 받고 있어요."
	}
	return v
}

func (e *env) mindMemory(ctx context.Context) check.Verdict {
	var st mindStatus
	if err := e.get(ctx, e.client, e.MindURL+"/status", &st); err != nil {
		return fail("mind 상태를 읽지 못했어요: " + err.Error())
	}
	f := st.Facts
	facts := []string{fmt.Sprintf("기억함 %d · 이미 알던 것 %d · 기다림 %d", f["stored"], f["duplicate"], f["pending"])}
	if f["failed"] > 0 {
		// Shown, not alarmed on: a fact given up on stays given up on, and a
		// row that warned forever would push every day.
		facts = append(facts, fmt.Sprintf("여러 번 실패해 포기한 사실 %d개 (mind 콘솔에서 볼 수 있어요)", f["failed"]))
	}
	if st.FactError != "" {
		facts = append(facts, "기다리는 이유: "+clip(st.FactError, 140))
	}
	v := check.Verdict{ObservedAt: st.OldestPending, ObservedLabel: "가장 오래 기다린 사실", Facts: facts}
	var waiting time.Duration
	if st.OldestPending != nil {
		waiting = time.Since(*st.OldestPending)
	}
	switch {
	case !st.Memory:
		v.State, v.Summary = check.Fail, "장기 기억이 연결되어 있지 않아 사실이 장부에만 쌓여요."
	case waiting > 6*time.Hour:
		v.State, v.Summary = check.Fail, fmt.Sprintf("%s째 기억에 넣지 못한 사실이 있어요.", span(waiting))
	case waiting > time.Hour:
		v.State, v.Summary = check.Warn, fmt.Sprintf("%s째 기억에 넣지 못한 사실이 있어요.", span(waiting))
	default:
		v.State, v.Summary = check.OK, "기억할 사실이 기억 서비스에 들어가고 있어요."
		v.ObservedAt = nil
	}
	return v
}

type spendingView struct {
	Collector struct {
		LastPollAt      time.Time  `json:"lastPollAt"`
		LastError       string     `json:"lastError"`
		LatestMessageAt *time.Time `json:"latestMessageAt"`
		LastAlertAt     *time.Time `json:"lastAlertAt"`
	} `json:"collector"`
	Unparsed []struct {
		ChatName string    `json:"chatName"`
		SentAt   time.Time `json:"sentAt"`
	} `json:"unparsed"`
}

func (e *env) spending(ctx context.Context) (spendingView, error) {
	var v spendingView
	err := e.get(ctx, e.client, e.SpendingURL+"/spending", &v)
	return v, err
}

func (e *env) spendingAlerts(ctx context.Context) check.Verdict {
	s, err := e.spending(ctx)
	if err != nil {
		return fail("가계부 서비스를 읽지 못했어요: " + err.Error())
	}
	c := s.Collector
	if c.LastError != "" {
		return fail("가계부가 카톡 수집기를 읽지 못하고 있어요: " + clip(c.LastError, 140))
	}
	if c.LastAlertAt == nil {
		return check.Verdict{State: check.Unknown, Summary: "아직 받은 카드 알림이 없어요."}
	}
	age := time.Since(*c.LastAlertAt)
	v := check.Verdict{ObservedAt: c.LastAlertAt, ObservedLabel: "마지막 카드 알림"}
	switch {
	case age > 7*24*time.Hour:
		v.State, v.Summary = check.Fail, fmt.Sprintf("%s째 카드 알림이 없어요. 결제가 가계부에 빠지고 있을 수 있어요.", span(age))
	case age > 3*24*time.Hour:
		v.State, v.Summary = check.Warn, fmt.Sprintf("%s째 카드 알림이 없어요.", span(age))
	default:
		v.State, v.Summary = check.OK, "카드 알림이 가계부에 들어오고 있어요."
	}
	return v
}

func (e *env) spendingUnparsed(ctx context.Context) check.Verdict {
	s, err := e.spending(ctx)
	if err != nil {
		return fail("가계부 서비스를 읽지 못했어요: " + err.Error())
	}
	if n := len(s.Unparsed); n > 0 {
		latest := s.Unparsed[0].SentAt
		var facts []string
		for i, u := range s.Unparsed {
			if u.SentAt.After(latest) {
				latest = u.SentAt
			}
			if i < 5 {
				facts = append(facts, u.ChatName+" · "+u.SentAt.Format("1월 2일 15:04"))
			}
		}
		return check.Verdict{State: check.Warn,
			Summary:    fmt.Sprintf("%d건이 합계에서 빠져 있어요.", n),
			ObservedAt: &latest, ObservedLabel: "가장 최근", Facts: facts}
	}
	return check.Verdict{State: check.OK, Summary: "모든 카드 알림을 읽었어요."}
}

// ── 가계부 상품 상세 ──────────────────────────────────────────────────────

// site is a marketplace whose order history the agent reads.
type site struct {
	source    string // card_transactions.enrich_source
	orders    string // the order history page
	loginHost string // where a logged-out visit is sent
}

var (
	coupang = site{"coupang", "https://mc.coupang.com/ssr/desktop/order/list", "login.coupang.com"}
	// Never opened by a probe: the browser's payment guard holds every
	// request to pay.naver.com and nid.naver.com for a human decision, GETs
	// included, so a check would raise a "결제를 진행합니다" card each time.
	naverpay = site{"naverpay", "", "nid.naver.com"}
)

// recordOnly judges a site from what the enrichment job recorded, without
// opening anything.
func (e *env) recordOnly(st site) func(context.Context) check.Verdict {
	return func(ctx context.Context) check.Verdict {
		job := e.enrichJob(ctx, st.source)
		v := check.Verdict{Facts: job.facts}
		switch {
		case job.loginRequired > 0:
			v.State = check.Fail
			v.Summary = fmt.Sprintf("주문 내역을 읽다 로그인을 요구받았어요. 상품 상세 %d건이 로그인을 기다리고 있어요.", job.loginRequired)
		case job.stale > 0:
			v.State = check.Warn
			v.Summary = fmt.Sprintf("하루 넘게 밀린 조회가 %d건 있어요.", job.stale)
		case job.lastDone != nil:
			v.State, v.Summary = check.OK, "주문 내역을 읽고 있어요."
			v.ObservedAt, v.ObservedLabel = job.lastDone, "마지막으로 읽은 주문"
		default:
			v.State = check.Unknown
			v.Summary = "결제 감시 때문에 자동으로 열어 보지 않아요. 조회 기록이 생기면 그걸로 판단해요."
		}
		return v
	}
}

// enrich judges by where the order history landed when the agent last opened
// it in its browser (agent /browser-checks). A cookie on disk is not
// evidence: the profile kept every long-lived Coupang cookie while the order
// page sent it to login. The agent opens it, not this service: it is the
// browser's one user and knows when nothing else is driving it.
func (e *env) enrich(st site) func(context.Context) check.Verdict {
	return func(ctx context.Context) check.Verdict {
		job := e.enrichJob(ctx, st.source)
		now := time.Now()
		v := check.Verdict{Facts: job.facts, ObservedAt: &now, ObservedLabel: "브라우저로 확인"}

		var out struct {
			Results map[string]struct {
				URL   string    `json:"url"`
				Busy  bool      `json:"busy"`
				Error string    `json:"error"`
				At    time.Time `json:"at"`
			} `json:"results"`
		}
		if err := e.get(ctx, e.client, e.AgentURL+"/browser-checks", &out); err != nil {
			v.State, v.Summary, v.ObservedAt = check.Unknown, "에이전트에서 확인 결과를 읽지 못했어요: "+clip(err.Error(), 120), nil
			return v
		}
		res, ok := out.Results[st.source]
		if !ok {
			v.State, v.ObservedAt = check.Unknown, nil
			v.Summary = "에이전트가 아직 열어 보지 않았어요. 대화·작업이 없을 때 열어 봐요."
			return v
		}
		v.ObservedAt = &res.At
		if res.Error != "" {
			v.State, v.Summary = check.Unknown, "브라우저로 열어 보지 못했어요: "+clip(res.Error, 120)
			return v
		}
		landing := res
		if landing.Busy {
			// Someone may be typing a password there; a new tab would take
			// the focus away. This site's own login page is itself the
			// answer; another site's only means this check waits.
			if strings.Contains(landing.URL, st.loginHost) {
				v.State = check.Fail
				v.Summary = "브라우저에 로그인 화면이 열려 있어요. 로그인을 마치면 다음 확인 때 정상으로 바뀌어요."
			} else {
				v.State, v.ObservedAt = check.Unknown, nil
				v.Summary = "다른 사이트의 로그인 화면이 열려 있어서 이번에는 열어 보지 않았어요."
			}
			return v
		}
		v.Facts = append([]string{"도착한 주소: " + clip(landing.URL, 100)}, v.Facts...)
		if strings.Contains(landing.URL, st.loginHost) {
			v.State = check.Fail
			v.Summary = "주문목록을 열면 로그인 화면으로 넘어가요."
			if job.loginRequired > 0 {
				v.Summary += fmt.Sprintf(" 상품 상세 %d건이 로그인을 기다리고 있어요.", job.loginRequired)
			}
			return v
		}
		if job.stale > 0 {
			v.State = check.Warn
			v.Summary = fmt.Sprintf("주문목록은 열리지만 하루 넘게 밀린 조회가 %d건 있어요.", job.stale)
			return v
		}
		v.State, v.Summary = check.OK, "주문목록이 열려요."
		return v
	}
}

type enrichJob struct {
	loginRequired, stale int
	lastDone             *time.Time
	facts                []string
}

func (e *env) enrichJob(ctx context.Context, source string) enrichJob {
	var j enrichJob
	if e.DB == nil {
		return j
	}
	var pending int
	err := e.DB.QueryRow(ctx, `
		select count(*) filter (where enrich_status = 'login_required'),
		       count(*) filter (where enrich_status = 'pending' and enrich_next_at < now() - interval '1 day'),
		       count(*) filter (where enrich_status = 'pending'),
		       max(updated_at) filter (where enrich_status = 'done')
		  from card_transactions where enrich_source = $1`, source).
		Scan(&j.loginRequired, &j.stale, &pending, &j.lastDone)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "42703" {
		j.facts = append(j.facts, "상품 상세 조회 기능이 아직 적용되지 않았어요.")
		return j
	}
	if err != nil {
		j.facts = append(j.facts, "조회 기록을 읽지 못했어요: "+clip(err.Error(), 100))
		return j
	}
	j.facts = append(j.facts, fmt.Sprintf("조회 대기 %d건, 로그인 대기 %d건", pending, j.loginRequired))
	if j.lastDone != nil {
		j.facts = append(j.facts, "마지막으로 읽은 주문: "+j.lastDone.Local().Format("1월 2일 15:04"))
	} else {
		j.facts = append(j.facts, "아직 읽은 주문이 없어요.")
	}
	return j
}

// ── 외부 계정 ───────────────────────────────────────────────────────────

// portfolio is fetched once per ten-minute window and shared by the four
// venue probes, so one round of checks is one call to the brokerages.
type portfolioCache struct {
	at       time.Time
	problems []string
	err      error
	asOf     time.Time
}

var pf struct {
	cache portfolioCache
	ch    chan struct{}
}

func init() { pf.ch = make(chan struct{}, 1) }

func (e *env) portfolio(ctx context.Context, client *http.Client) portfolioCache {
	pf.ch <- struct{}{}
	defer func() { <-pf.ch }()
	if time.Since(pf.cache.at) < 50*time.Second {
		return pf.cache
	}
	var body struct {
		Problems []string  `json:"problems"`
		At       time.Time `json:"at"`
	}
	err := e.get(ctx, client, e.AssetsURL+"/portfolio", &body)
	pf.cache = portfolioCache{at: time.Now(), problems: body.Problems, err: err, asOf: body.At}
	return pf.cache
}

func (e *env) portfolioVenue(client *http.Client, prefixes ...string) func(context.Context) check.Verdict {
	return func(ctx context.Context) check.Verdict {
		p := e.portfolio(ctx, client)
		if p.err != nil {
			return fail("자산 서비스가 답하지 않았어요: " + p.err.Error())
		}
		for _, msg := range p.problems {
			for _, pre := range prefixes {
				if !strings.HasPrefix(msg, pre) {
					continue
				}
				detail := strings.TrimSpace(strings.TrimPrefix(msg, pre))
				if strings.Contains(detail, "설정되지 않았습니다") {
					return check.Verdict{State: check.Unknown, Summary: "키가 설정되지 않아 연결하지 않았어요."}
				}
				state := check.Fail
				if pre == "업비트 입출금 내역:" || pre == "원금 기록:" {
					state = check.Warn
				}
				return check.Verdict{State: state, Summary: clip(detail, 160)}
			}
		}
		at := p.asOf
		return check.Verdict{State: check.OK, Summary: "읽고 있어요.", ObservedAt: &at, ObservedLabel: "마지막 조회"}
	}
}

func (e *env) assetSnapshot(ctx context.Context) check.Verdict {
	if e.DB == nil {
		return check.Verdict{State: check.Unknown, Summary: "데이터베이스가 연결되지 않았어요."}
	}
	var at *time.Time
	if err := e.DB.QueryRow(ctx, `select max(taken_at) from portfolio_snapshots`).Scan(&at); err != nil {
		return fail("스냅샷을 읽지 못했어요: " + clip(err.Error(), 100))
	}
	if at == nil {
		return check.Verdict{State: check.Unknown, Summary: "아직 쌓인 스냅샷이 없어요."}
	}
	age := time.Since(*at)
	v := check.Verdict{ObservedAt: at, ObservedLabel: "마지막 기록"}
	switch {
	case age > 4*24*time.Hour:
		v.State, v.Summary = check.Fail, fmt.Sprintf("%s째 자산이 기록되지 않았어요. 자산 곡선에 빈 구간이 생겨요.", span(age))
	case age > 36*time.Hour:
		v.State, v.Summary = check.Warn, fmt.Sprintf("%s째 자산이 기록되지 않았어요.", span(age))
	default:
		v.State, v.Summary = check.OK, "매일 기록되고 있어요."
	}
	return v
}

func (e *env) calendar(ctx context.Context) check.Verdict {
	today := time.Now().Format("2006-01-02")
	q := url.Values{"from": {today}, "to": {today}}
	var body struct {
		Events []json.RawMessage `json:"events"`
	}
	err := e.get(ctx, e.client, e.CalendarURL+"/events?"+q.Encode(), &body)

	var facts []string
	var refreshed *time.Time
	if e.DB != nil {
		if e.DB.QueryRow(ctx, `select updated_at from uniple_session where id = 1`).Scan(&refreshed) == nil && refreshed != nil {
			facts = append(facts, "uniple 토큰을 마지막으로 갱신한 때: "+refreshed.Local().Format("1월 2일 15:04"))
		}
	}
	if err != nil {
		return check.Verdict{State: check.Fail, Summary: "일정을 읽지 못했어요. uniple 로그인이 끊겼을 수 있어요.",
			Facts: append(facts, clip(err.Error(), 160)), ObservedAt: refreshed, ObservedLabel: "마지막 토큰 갱신"}
	}
	return check.Verdict{State: check.OK, Summary: fmt.Sprintf("일정을 읽고 있어요. 오늘 일정 %d개.", len(body.Events)),
		Facts: facts, ObservedAt: refreshed, ObservedLabel: "마지막 토큰 갱신"}
}

// ── 기반 ───────────────────────────────────────────────────────────────

func (e *env) service(id, name, base string, deps []string) check.Probe {
	return check.Probe{
		ID: id, Group: "기반", Name: name,
		Rule: "서비스의 /health가 200이면 정상.", Fix: "docker compose ps로 컨테이너 상태와 로그를 보세요.",
		Deps: deps, Every: 30 * time.Second,
		Run: e.httpOK(base + "/health"),
	}
}

func (e *env) httpOK(u string) func(context.Context) check.Verdict {
	return func(ctx context.Context) check.Verdict {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return fail(err.Error())
		}
		start := time.Now()
		res, err := e.client.Do(req)
		if err != nil {
			return fail("응답이 없어요.")
		}
		res.Body.Close()
		if res.StatusCode != http.StatusOK {
			return fail(fmt.Sprintf("응답 코드 %d.", res.StatusCode))
		}
		return check.Verdict{State: check.OK, Summary: fmt.Sprintf("응답해요 (%dms).", time.Since(start).Milliseconds())}
	}
}

func (e *env) tcp(addr string) func(context.Context) check.Verdict {
	return func(ctx context.Context) check.Verdict {
		var d net.Dialer
		conn, err := d.DialContext(ctx, "tcp", addr)
		if err != nil {
			return fail("포트가 닫혀 있어요.")
		}
		conn.Close()
		return check.Verdict{State: check.OK, Summary: "떠 있어요."}
	}
}

func (e *env) postgres(ctx context.Context) check.Verdict {
	if e.DB == nil {
		return fail("연결 설정이 없어요.")
	}
	start := time.Now()
	if err := e.DB.Ping(ctx); err != nil {
		return fail("연결되지 않아요: " + clip(err.Error(), 120))
	}
	return check.Verdict{State: check.OK, Summary: fmt.Sprintf("연결돼요 (%dms).", time.Since(start).Milliseconds())}
}

// ── helpers ────────────────────────────────────────────────────────────

func (e *env) get(ctx context.Context, client *http.Client, u string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	if e.Token != "" {
		req.Header.Set("Authorization", "Bearer "+e.Token)
	}
	res, err := client.Do(req)
	if err != nil {
		return errors.New("연결하지 못했습니다")
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		var b struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(res.Body).Decode(&b)
		if b.Error != "" {
			return fmt.Errorf("%d %s", res.StatusCode, b.Error)
		}
		return fmt.Errorf("응답 코드 %d", res.StatusCode)
	}
	return json.NewDecoder(res.Body).Decode(out)
}

func fail(summary string) check.Verdict {
	return check.Verdict{State: check.Fail, Summary: summary}
}

func ptr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

func clip(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "…"
}

// span renders a duration the way a person says it: "3일", "14시간".
func span(d time.Duration) string {
	switch {
	case d >= 48*time.Hour:
		return fmt.Sprintf("%d일", int(d.Hours()/24))
	case d >= time.Hour:
		return fmt.Sprintf("%d시간", int(d.Hours()))
	default:
		return fmt.Sprintf("%d분", int(d.Minutes()))
	}
}
