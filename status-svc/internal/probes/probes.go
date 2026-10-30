// Package probes is the list of things the status screen answers. It has no
// code of its own for any feature: a service answers its /health (its loops and
// what it set aside), the way in says how far each consumer has read, a source
// is as fresh as its last record against what the registry expects of it, and
// what only a service can see it says itself, as a check on the stream.
package probes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/choigonyok/jarvis/status-svc/internal/check"
)

type Config struct {
	Token       string
	KakaoURL    string
	SpendingURL string
	AssetsURL   string
	CalendarURL string
	AgentURL    string
	MindURL     string
	MemoryURL   string
	ContextURL  string
	VaultURL    string
	InputURL    string
	NotifyURL   string
	PhotosURL   string
	JobsURL     string
	IMessageURL string
	SearchURL   string
	// AgentGuestURL and ImmichURL are only asked whether they answer.
	AgentGuestURL string
	ImmichURL     string
	// TTSURL and OCRURL run on the Mac mini itself (LaunchAgents), reached
	// at host.docker.internal.
	TTSURL string
	OCRURL string
	// FalkorAddr is the graph memory's host:port (a Redis protocol PING).
	FalkorAddr  string
	BrowserAddr string // host:port of the browser MCP bridge
	DB          *pgxpool.Pool
}

type env struct {
	Config
	client *http.Client
}

// Groups in screen order. 격리함 is what services set aside after trying
// too often - the rule in gocommon/health - each row one service's box.
var Groups = []string{"수집", "가계부 상품 상세", "외부 계정", "격리함", "기반"}

// All is the rows that are always there.
func All(cfg Config) []check.Probe {
	e := &env{Config: cfg, client: &http.Client{Timeout: 15 * time.Second}}

	return []check.Probe{
		// ── 수집 ───────────────────────────────────────────────────────
		e.loop("kakao.collector", "수집", "카톡 수집기", cfg.KakaoURL, "read", []string{"svc.kakaotalk"},
			"수집기가 1초마다 카카오톡 DB를 읽고 있으면 정상, 읽다 실패하거나 1분 넘게 읽지 않았으면 이상.",
			"kakaotalk 컨테이너 로그를 보세요. 복호화 실패라면 맥의 카카오톡 계정이 바뀌었을 수 있어요."),
		e.loop("imessage.collector", "수집", "iMessage 수집기", cfg.IMessageURL, "read", []string{"svc.imessage"},
			"수집기가 몇 초마다 맥미니 메시지 앱의 DB 사본을 읽고 있으면 정상, 읽다 실패하거나 5분 넘게 읽지 않았으면 이상.",
			"imessage 컨테이너 로그와 맥미니의 iMessage 미러(scripts/imessage-mirror)를 보세요. 메시지 앱에 로그인돼 있지 않으면 들어오는 것이 없어요."),

		// ── 외부 계정 ──────────────────────────────────────────────────
		e.loop("calendar.uniple", "외부 계정", "커플 캘린더", cfg.CalendarURL, "watch:calendar.near", []string{"svc.calendar"},
			"캘린더 서비스가 가까운 일정을 읽고 있으면 정상, 읽기가 멈추면 이상.",
			"uniple 토큰이 끊겼다면 새 시크릿 창에서 로그인해 refresh_token을 뽑고, 로그아웃하지 말고 창만 닫은 뒤 .env의 UNIPLE_REFRESH_TOKEN을 바꿔 calendar를 재시작하세요."),

		// ── 격리함 ─────────────────────────────────────────────────────
		e.heldBox("spending.unparsed", "가계부 · 읽지 못한 카드 알림", cfg.SpendingURL,
			"카드사 알림 중 금액·가맹점을 읽지 못했거나, 다섯 번 기록하지 못해 따로 둔 것이 없으면 정상, 있으면 주의. 따로 둔 알림은 합계에서 빠져 있어요.",
			"가계부 탭의 '읽지 못한 알림'에서 직접 적거나 버리세요.", []string{"svc.spending"}),
		e.heldBox("input.held", "기록 · 처리하지 못한 기록", cfg.InputURL,
			"받는 쪽(알림·에이전트·mind)이 살아 있는데 다섯 번 처리하지 못한 기록이나 봉투가 잘못된 기록이 없으면 정상, 있으면 주의. 그 하나 때문에 뒤의 알림·카드가 멈추지 않도록 따로 두고 넘어갔어요.",
			"받는 쪽을 고쳐 다시 띄우면 저절로 한 번 더 처리해요. 버리려면 input-svc 의 DELETE /held/{소비자}/{id}.", []string{"svc.input"}),
		e.heldBox("mind.held", "기억 · 넣지 못한 사실", cfg.MindURL,
			"대화에서 확인한 사실 중 기억 서비스가 다섯 번 받지 못해(또는 받지 않겠다고 해서) 보류한 것이 없으면 정상, 있으면 주의. 보류한 사실도 생활 장부에는 남아요.",
			"기억이 꺼졌다 돌아오면 저절로 한 번 더 보내요. mind 콘솔(scripts/mini console)의 기억할 사실에서 다시 보내거나 버릴 수 있어요.", []string{"svc.mind", "svc.memory"}),
		e.heldBox("jobs.held", "작업 · 멈춘 작업", cfg.JobsURL,
			"세 번 연달아 잘 끝나지 않아 멈춘 작업이 없으면 정상, 있으면 주의. 작업 하나의 시도는 모델 실행 한 번이라 다른 것(다섯 번)보다 일찍 멈춰요.",
			"작업 탭에서 무엇이 막혔는지 보고 다시 시작하거나 그만두세요.", []string{"svc.jobs"}),

		// ── 기반 ──────────────────────────────────────────────────────
		{
			ID: "agent", Group: "기반", Name: "에이전트",
			Rule: "에이전트가 응답하면 정상.", Fix: "agent 컨테이너 로그를 보세요.",
			Every: 30 * time.Second,
			Run:   e.httpOK(cfg.AgentURL + "/healthz"),
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
		{
			ID: "agent-guest", Group: "기반", Name: "손님 에이전트",
			Rule: "손님 계정의 에이전트가 응답하면 정상.", Fix: "agent-guest 컨테이너 로그를 보세요.",
			Every: 30 * time.Second,
			Run:   e.httpOK(cfg.AgentGuestURL + "/healthz"),
		},
		// Its loop writes the payment path as records; the process itself is
		// the "agent" probe above.
		e.service("svc.agent", "에이전트의 기록", cfg.AgentURL, []string{"agent", "postgres"}, "records"),
		e.service("svc.input", "입구(기록)", cfg.InputURL, []string{"postgres"}, "drain", "keep"),
		e.delivery("mind", "기록 → mind", []string{"svc.mind"}),
		e.delivery("mind-commands", "답·리마인드·타이머 → mind", []string{"svc.mind"}),
		e.delivery("mind-structured", "어댑터 기록 → mind", []string{"svc.mind"}),
		e.delivery("mind-relay", "기록 → 알림 문구 넘기기", []string{"svc.mind"}),
		e.delivery("output-status", "상태·결재 알림 → 출구", []string{"svc.output"}),
		e.delivery("output", "효과 → 출구", []string{"svc.output", "svc.mind"}),
		e.delivery("output-calls", "승인된 쓰기 → 출구", []string{"svc.output", "svc.mind"}),
		e.delivery("mind-results", "쓰기 결과 → mind", []string{"svc.mind"}),
		e.delivery("agent", "효과 → 에이전트", []string{"agent", "svc.mind"}),
		e.delivery("agent-chat", "대화 메시지 → 에이전트", []string{"agent"}),
		// The guest's side: where the guest's phone is, what is said to the
		// guest, and reminders in the guest's conversation.
		e.delivery("mind-guest", "손님 기록 → mind", []string{"svc.mind"}),
		e.delivery("mind-commands-guest", "손님 리마인드 → mind", []string{"svc.mind"}),
		e.delivery("mind-relay-guest", "손님 기록 → 알림 문구 넘기기", []string{"svc.mind"}),
		e.delivery("output-guest", "손님 효과 → 출구", []string{"svc.output", "svc.mind"}),
		e.delivery("agent-guest", "손님 효과 → 손님 에이전트", []string{"agent-guest", "svc.mind"}),
		e.delivery("agent-chat-guest", "손님 대화 메시지 → 손님 에이전트", []string{"agent-guest"}),
		e.service("svc.output", "출구(알림)", cfg.NotifyURL, []string{"postgres"}),
		e.service("svc.jobs", "작업", cfg.JobsURL, []string{"postgres", "svc.mind", "agent"}),
		e.service("svc.photos", "사진 어댑터", cfg.PhotosURL, []string{"svc.immich"}),
		e.service("svc.kakaotalk", "카톡 수집 서비스", cfg.KakaoURL, nil),
		e.service("svc.imessage", "iMessage 수집 서비스", cfg.IMessageURL, nil),
		// Its loops read the KakaoTalk collector and assets-svc: when one of
		// them is down, that is the cause.
		e.service("svc.spending", "가계부", cfg.SpendingURL, []string{"postgres", "svc.mind"}),
		e.service("svc.assets", "자산 서비스", cfg.AssetsURL, nil),
		e.service("svc.calendar", "캘린더 서비스", cfg.CalendarURL, []string{"postgres"}),
		e.service("svc.mind", "인지 루프(mind)", cfg.MindURL, []string{"postgres"}),
		e.service("svc.memory", "기억 서비스", cfg.MemoryURL, []string{"postgres", "falkordb"}),
		{
			ID: "falkordb", Group: "기반", Name: "기억 그래프(FalkorDB)",
			Rule: "기억 그래프 데이터베이스가 PING 에 답하면 정상.", Fix: "falkordb 컨테이너와 디스크 용량을 보세요. 저장은 아래 'AOF' 항목에서 봐요.",
			Every: 30 * time.Second,
			Run:   e.redisPing(cfg.FalkorAddr),
		},
		e.service("svc.context", "위치·현재 상태", cfg.ContextURL, []string{"postgres"}),
		e.service("svc.vault", "로그인 정보 보관함", cfg.VaultURL, []string{"postgres"}),
		e.service("svc.search", "웹 찾기", cfg.SearchURL, nil),
		e.hostService("host.ocr", "그림 속 글자 읽기(OCR)", cfg.OCRURL+"/health",
			"맥미니의 OCR 서버(LaunchAgent com.jarvis.ocr)가 응답하면 정상. 웹 찾기가 페이지의 그림 속 글자를 읽을 때 써요.",
			"맥미니에서 scripts/ocr status 로 보세요. 다시 띄우려면 scripts/ocr restart.",
			func(map[string]any) check.Verdict { return check.Verdict{State: check.OK} }),
		e.hostService("host.tts", "목소리(TTS)", cfg.TTSURL+"/health",
			"맥미니의 목소리 서버(LaunchAgent com.jarvis.tts)가 응답하고 모델을 올렸으면 정상, 응답하지만 모델이 아직이면 주의.",
			"맥미니에서 scripts/tts status 로 보세요. 모델을 받는 중이면 기다리면 돼요.",
			func(b map[string]any) check.Verdict {
				if loaded, _ := b["loaded"].(bool); !loaded {
					return check.Verdict{State: check.Warn, Summary: "응답하지만 모델을 아직 올리지 못했어요. 음성 모드는 브라우저 음성으로 말해요."}
				}
				return check.Verdict{State: check.OK}
			}),
		{
			ID: "svc.immich", Group: "기반", Name: "사진(Immich)",
			Rule: "사진 서버가 ping 에 답하면 정상.", Fix: "scripts/mini compose -f immich/docker-compose.yml ps 로 보세요.",
			Every: time.Minute,
			Run:   e.httpOK(cfg.ImmichURL + "/api/server/ping"),
		},
	}
}

// ── 기반 ───────────────────────────────────────────────────────────────

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
