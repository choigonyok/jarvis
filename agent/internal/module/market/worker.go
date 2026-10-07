package market

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/choigonyok/jarvis/agent/internal/claudecode"
	"github.com/choigonyok/jarvis/agent/internal/core/action"
	"github.com/choigonyok/jarvis/agent/internal/core/proposal"
)

// KindLogin is the card that asks the operator to log in to Joongna again.
// It is not an Actuator action - nothing runs on approval but the resume.
const KindLogin = "market.login_required"

var loginAction = action.Action{Kind: KindLogin, Input: []byte(`{}`)}

// Notify raises a card when Joongna wants a fresh login, and resumes the
// queue when that card is approved. Register before RunWorker.
func (m *Module) Notify(p *proposal.Store) {
	m.proposals = p
	p.OnDecide(func(pr proposal.Proposal, d proposal.Decision) {
		if pr.Action.Kind != KindLogin {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if d != proposal.Approve {
			p.Settle(pr.ID, proposal.Rejected, "중고나라 작업은 멈춘 채로 둡니다. 탭에서 다시 시작할 수 있습니다.")
			return
		}
		if err := m.store.LoginResolved(ctx); err != nil {
			p.Settle(pr.ID, proposal.Failed, err.Error())
			return
		}
		p.Settle(pr.ID, proposal.Executed, "멈춘 작업을 이어서 합니다.")
		select {
		case m.kick <- struct{}{}:
		default:
		}
	})
}

// askLogin opens the login card once per stop. A stop the operator lifted
// from the tab instead settles the card that was still waiting.
func (m *Module) askLogin(ctx context.Context) {
	if m.proposals == nil {
		return
	}
	_, st, err := m.store.List(ctx)
	if err != nil {
		return
	}
	waiting, open := m.proposals.FindPending(loginAction)
	switch {
	case st.LoginRequired && !open:
		body := "중고나라 작업이 로그인 화면에서 멈췄습니다."
		if st.LoginNote != "" {
			body += "\n" + st.LoginNote
		}
		m.proposals.Open(proposal.Proposal{
			Origin: proposal.OriginNotice,
			Action: loginAction,
			Card: action.Card{
				Title:       "중고나라에 다시 로그인해 주세요",
				Body:        body + "\n\n화면 탭에서 중고나라(네이버 로그인)에 로그인한 뒤 승인을 누르세요.",
				Consequence: "승인하면 멈춘 등록·변경을 이어서 합니다. 반려하면 멈춘 채로 둡니다.",
			},
		})
	case !st.LoginRequired && open:
		m.proposals.Settle(waiting.ID, proposal.Executed, "중고나라 탭에서 다시 시작했습니다.")
	}
}

// WorkerServerName is the MCP server only the background worker is given.
// Mounted on this agent but never put in the chat's MCP config.
const WorkerServerName = "market-worker"

// BrowserServer is the JARVIS_EXTRA_MCP entry for the shared browser.
const BrowserServer = "mcp-browser"

// workerTools is everything a Joongna run may call. Unlike the order lookup
// it types: titles, prices and descriptions go into Joongna's form.
var workerTools = []string{
	"mcp__" + BrowserServer + "__browser_navigate",
	"mcp__" + BrowserServer + "__browser_get_state",
	"mcp__" + BrowserServer + "__browser_extract_content",
	"mcp__" + BrowserServer + "__browser_screenshot",
	"mcp__" + BrowserServer + "__browser_scroll",
	"mcp__" + BrowserServer + "__browser_click",
	"mcp__" + BrowserServer + "__browser_type",
	"mcp__" + BrowserServer + "__browser_go_back",
	"mcp__" + BrowserServer + "__browser_list_tabs",
	"mcp__" + BrowserServer + "__browser_switch_tab",
	"mcp__" + BrowserServer + "__browser_close_tab",
	"mcp__" + BrowserServer + "__browser_upload_files",
	"mcp__" + WorkerServerName + "__report_task",
	"mcp__" + WorkerServerName + "__report_sync",
}

type ReportTaskInput struct {
	TaskID    int64  `json:"taskId" jsonschema:"작업 id"`
	Outcome   string `json:"outcome" jsonschema:"done(끝냄) | failed(못 함) | login_required(로그인 화면에서 막힘)"`
	JoongnaID string `json:"joongnaId,omitempty" jsonschema:"등록(post)을 끝냈을 때 필수: 글 주소 https://web.joongna.com/product/<번호> 의 번호"`
	URL       string `json:"url,omitempty" jsonschema:"등록한 글의 주소"`
	Note      string `json:"note" jsonschema:"무엇을 했는지, 못 했다면 무엇이 막았는지 한 줄"`
}

type SyncItemInput struct {
	JoongnaID string `json:"joongnaId" jsonschema:"글 번호"`
	Status    string `json:"status" jsonschema:"화면에 보이는 상태: 판매중, 예약중, 판매완료"`
	PriceKrw  int64  `json:"priceKrw" jsonschema:"화면에 보이는 가격(원). 모르면 0"`
	Views     int    `json:"views" jsonschema:"조회수"`
	Likes     int    `json:"likes" jsonschema:"찜(관심) 수"`
	Chats     int    `json:"chats" jsonschema:"채팅 수"`
	Gone      bool   `json:"gone,omitempty" jsonschema:"글이 삭제되어 열리지 않으면 true"`
}

type ReportSyncInput struct {
	Items         []SyncItemInput `json:"items"`
	Note          string          `json:"note,omitempty" jsonschema:"확인하지 못한 글이 있으면 이유"`
	LoginRequired bool            `json:"loginRequired,omitempty" jsonschema:"로그인 화면에 막혀 글을 볼 수 없었으면 true"`
}

// WorkerHandler serves the tools a background run reports through.
func (m *Module) WorkerHandler() http.Handler {
	server := mcp.NewServer(&mcp.Implementation{Name: WorkerServerName, Version: "1"}, nil)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "report_task",
		Description: "작업 결과를 남긴다. 작업마다 반드시 한 번 부른다.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ReportTaskInput) (*mcp.CallToolResult, any, error) {
		err := m.store.ReportTask(ctx, in.TaskID, Report{
			Outcome: in.Outcome, JoongnaID: strings.TrimSpace(in.JoongnaID), URL: in.URL, Note: in.Note,
		})
		if err != nil {
			return toolText(err.Error(), true), nil, nil
		}
		return toolText("기록했습니다. 작업을 마치세요.", false), nil, nil
	})
	mcp.AddTool(server, &mcp.Tool{
		Name:        "report_sync",
		Description: "글마다 본 상태·가격·조회·찜·채팅 수를 한 번에 남긴다.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ReportSyncInput) (*mcp.CallToolResult, any, error) {
		items := make([]SyncItem, len(in.Items))
		for i, it := range in.Items {
			items[i] = SyncItem(it)
		}
		n, err := m.store.ReportSync(ctx, items, in.Note, in.LoginRequired)
		if err != nil {
			return toolText(err.Error(), true), nil, nil
		}
		return toolText(fmt.Sprintf("%d개 글을 갱신했습니다. 작업을 마치세요.", n), false), nil, nil
	})
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
}

// --- the loop --------------------------------------------------------------

type Background interface {
	Idle() bool
	Background(ctx context.Context, t claudecode.Task) (string, error)
}

type WorkerConfig struct {
	// WorkerURL is this agent's own market-worker endpoint.
	WorkerURL string
	Interval  time.Duration
	Cooldown  time.Duration
	Timeout   time.Duration
}

// syncRetry is how long a sync that ended without a report waits before the
// next try - long enough not to hammer Joongna, short of the hourly cadence.
const syncRetry = 15 * time.Minute

// RunWorker carries queued changes to Joongna and keeps the listings' numbers
// fresh. One browser run at a time, never while the operator is talking, and
// never two runs closer than the cooldown.
func (m *Module) RunWorker(ctx context.Context, bg Background, cfg WorkerConfig, log *slog.Logger) {
	var lastRun, lastSyncTry time.Time
	tick := time.NewTicker(cfg.Interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		case <-m.kick:
		}
		m.purge(ctx, log)
		m.askLogin(ctx)
		if time.Since(lastRun) < cfg.Cooldown || !bg.Idle() {
			continue
		}
		work, err := m.store.NextTask(ctx)
		if err != nil {
			continue
		}
		if work != nil {
			lastRun = time.Now()
			m.runTask(ctx, bg, cfg, *work, log)
			continue
		}
		if time.Since(lastSyncTry) < syncRetry {
			continue
		}
		due, err := m.store.SyncDue(ctx)
		if err != nil || len(due) == 0 {
			continue
		}
		lastRun, lastSyncTry = time.Now(), time.Now()
		m.runSync(ctx, bg, cfg, due, log)
	}
}

func (m *Module) task(cfg WorkerConfig, name, prompt string) claudecode.Task {
	return claudecode.Task{
		Name:    name,
		Prompt:  prompt,
		System:  workerSystem,
		Servers: map[string]string{WorkerServerName: cfg.WorkerURL},
		Extra:   []string{BrowserServer},
		Allowed: workerTools,
		Timeout: cfg.Timeout,
	}
}

func (m *Module) runTask(ctx context.Context, bg Background, cfg WorkerConfig, w Work, log *slog.Logger) {
	log.Info("중고나라 작업을 합니다", "task", w.Task.ID, "kind", w.Task.Kind, "listing", w.Listing.ID)
	result, err := bg.Background(ctx, m.task(cfg, "중고나라 "+taskLabel[w.Task.Kind], m.taskPrompt(w)))
	switch {
	case errors.Is(err, claudecode.ErrYielded), errors.Is(err, claudecode.ErrBusy):
		log.Info("중고나라 작업을 미뤘습니다", "reason", err)
		return
	case err != nil:
		log.Warn("중고나라 작업이 실패했습니다", "err", err)
	default:
		log.Info("중고나라 작업을 마쳤습니다", "result", clampText(result, 300))
	}

	// A run that ended without reporting counts as a failed attempt, so the
	// task cannot come back forever. Asked again rather than assumed: the
	// model may have reported just before the error.
	bctx := context.WithoutCancel(ctx)
	still, qerr := m.store.NextTask(bctx)
	if qerr != nil || still == nil || still.Task.ID != w.Task.ID || still.Task.Attempts != w.Task.Attempts {
		return
	}
	note := "작업이 끝났지만 결과가 남지 않았습니다"
	if err != nil {
		note = clampText(err.Error(), 150)
	}
	_ = m.store.ReportTask(bctx, w.Task.ID, Report{Outcome: "failed", Note: note})
}

func (m *Module) runSync(ctx context.Context, bg Background, cfg WorkerConfig, due []Listing, log *slog.Logger) {
	log.Info("중고나라 글 현황을 확인합니다", "count", len(due))
	result, err := bg.Background(ctx, m.task(cfg, "중고나라 현황", syncPrompt(due)))
	switch {
	case errors.Is(err, claudecode.ErrYielded), errors.Is(err, claudecode.ErrBusy):
		log.Info("중고나라 현황 확인을 미뤘습니다", "reason", err)
	case err != nil:
		log.Warn("중고나라 현황 확인이 실패했습니다", "err", err)
	default:
		log.Info("중고나라 현황을 확인했습니다", "result", clampText(result, 300))
	}
}

// purge deletes the photos of listings that ended long enough ago. No
// browser involved, so it runs on every tick regardless of the cooldown.
func (m *Module) purge(ctx context.Context, log *slog.Logger) {
	due, err := m.store.PurgeDue(ctx)
	if err != nil {
		return
	}
	for _, p := range due {
		failed := false
		for _, name := range p.Photos {
			if err := m.uploads.Remove(name); err != nil {
				log.Warn("사진을 지우지 못했습니다", "listing", p.ID, "photo", name, "err", err)
				failed = true
			}
		}
		if !failed {
			_ = m.store.MarkPurged(ctx, p.ID)
			log.Info("판매가 끝난 글의 사진을 지웠습니다", "listing", p.ID, "count", len(p.Photos))
		}
	}
}

const workerSystem = `당신은 Jarvis의 백그라운드 중고나라 작업입니다. 대화 상대는 없고, 결과는 도구로만 남깁니다.

할 일: 운영자 본인의 중고나라 계정으로, 이미 로그인된 브라우저를 써서 주어진 작업 하나를 끝내고 report_task 로 결과를 남깁니다.

반드시 지킬 것
- web.joongna.com 안에서만 일합니다. 로그인 때 거치는 nid.naver.com 말고 다른 사이트로 가지 마세요.
- 구매, 결제, 중고나라페이·안전결제 가입, 유료 광고, 유료 끌어올리기, 포인트 충전 버튼은 절대 누르지 마세요. 돈이 드는 단계가 나오면 멈추고 failed 로 보고하세요.
- 다른 사람의 글, 채팅, 후기에는 손대지 마세요. 내 글만 다룹니다.
- 글자는 판매 글 양식(제목·가격·설명·카테고리 검색 칸)에만 입력합니다.
- 같은 글을 두 번 등록하지 마세요. 등록 버튼은 한 번만 누릅니다. 눌렀는데 결과가 불확실하면 다시 누르지 말고 내 상점(판매 내역)에서 글이 생겼는지 확인하세요.
- 작업이 끝나면 직접 연 탭은 닫으세요.

로그인
- 중고나라 로그인 화면이 나오면 네이버 로그인 버튼(초록색 N 아이콘)을 누르세요.
- 네이버 로그인 화면에서 아이디·비밀번호가 이미 채워져 있으면 다른 것은 아무것도 누르지 말고 로그인 버튼을 한 번 누르세요. "로그인 상태 유지" 같은 체크박스를 먼저 누르면 채워진 칸이 지워집니다.
- 네이버의 "동의하기"·"계속" 같은 연결 확인 화면은 눌러도 됩니다.
- 칸이 비어 있거나, 보안문자·기기 인증·휴대폰 인증이 나오거나, 한 번 눌러서 로그인되지 않으면 더 시도하지 말고 report_task(outcome=login_required)로 끝내세요.

화면 다루기
- browser_get_state 로 요소 번호를 보고 browser_click·browser_type 으로 조작합니다. 화면 확인이 필요하면 browser_screenshot 을 씁니다.
- 사진은 browser_upload_files 로 넣습니다. 파일 선택 창을 열려고 버튼을 누르지 마세요(운영체제 창이 떠서 멈춥니다). 넣은 뒤 미리보기 개수를 확인하세요.
- 선택지(카테고리, 상품 상태, 거래 방식)는 주어진 값과 가장 가까운 것을 고르세요. 꼭 맞는 것이 없으면 가장 가까운 것을 고르고 note 에 적으세요.

모든 작업은 report_task 로 끝나야 합니다.`

func (m *Module) taskPrompt(w Work) string {
	l, t := w.Listing, w.Task
	var b strings.Builder
	fmt.Fprintf(&b, "작업 id %d: %s\n\n", t.ID, taskLabel[t.Kind])
	if t.Attempts > 0 {
		fmt.Fprintf(&b, "이번이 %d번째 시도입니다. 지난번: %s\n\n", t.Attempts+1, t.Note)
	}
	switch t.Kind {
	case "post":
		b.WriteString("새 판매 글을 등록합니다.\n")
		b.WriteString("1. 먼저 내 상점의 판매 내역에 같은 제목의 글이 이미 있는지 보세요(지난 시도가 실제로는 등록됐을 수 있습니다). 있으면 등록하지 말고 그 글 번호로 done 을 보고하세요.\n")
		b.WriteString("2. https://web.joongna.com/product/form 을 엽니다.\n")
		b.WriteString("3. 사진을 이 순서대로 browser_upload_files 로 넣습니다(첫 장이 대표 사진):\n")
		for _, p := range l.Photos {
			b.WriteString("   " + filepath.Join(m.uploads.Dir, p) + "\n")
		}
		fmt.Fprintf(&b, "4. 제목: %s\n", l.Title)
		fmt.Fprintf(&b, "5. 카테고리: %s\n", fallback(l.Category, "제목에 맞는 것"))
		fmt.Fprintf(&b, "6. 가격: %d (숫자만)\n", l.PriceKrw)
		fmt.Fprintf(&b, "7. 상품 상태: %s\n", fallback(l.Condition, "사용감 적음"))
		fmt.Fprintf(&b, "8. 거래 방식: 택배거래만 (직거래는 끕니다). 택배비: %s\n", shippingLabel[l.Shipping])
		b.WriteString("9. 설명:\n" + l.Description + "\n")
		b.WriteString("10. 입력한 값을 한 번 확인하고 등록 버튼을 한 번 누릅니다.\n")
		b.WriteString("11. 등록된 글의 주소(https://web.joongna.com/product/<번호>)에서 번호를 읽어 report_task(outcome=done, joongnaId, url)로 보고합니다.\n")
	case "price":
		fmt.Fprintf(&b, "글 %s (%s)의 가격을 %s 에서 %s 로 바꿉니다.\n", l.URL, l.Title, krw(l.PriceKrw), krw(*t.PriceKrw))
		b.WriteString("글을 열고 수정하기로 들어가 가격만 바꿔 저장하세요. 다른 칸은 건드리지 마세요. 저장 후 글에 새 가격이 보이면 done.\n")
	case "status":
		fmt.Fprintf(&b, "글 %s (%s)의 상태를 %s 로 바꿉니다.\n", l.URL, l.Title, statusLabel[t.ToStatus])
		b.WriteString("글 화면이나 내 상점의 상태 변경 메뉴에서 바꾸세요. 판매완료로 바꿀 때 구매자를 고르라고 하면 '구매자 선택 안 함'이나 건너뛰기를 고르세요. 바뀐 상태가 보이면 done.\n")
	case "bump":
		fmt.Fprintf(&b, "글 %s (%s)를 끌어올립니다.\n", l.URL, l.Title)
		b.WriteString("무료 끌어올리기만 합니다. 유료이거나 아직 쓸 수 없다고 나오면 누르지 말고 그 문구를 note 에 적어 failed 로 보고하세요.\n")
	case "delete":
		fmt.Fprintf(&b, "글 %s (%s)를 삭제합니다.\n", l.URL, l.Title)
		b.WriteString("글 화면의 메뉴에서 삭제하고 확인을 누르세요. 글이 열리지 않거나 삭제된 글이라고 나오면 done.\n")
	}
	return b.String()
}

func syncPrompt(ls []Listing) string {
	var b strings.Builder
	b.WriteString("내 중고나라 글들의 지금 상태를 확인해 report_sync 로 한 번에 남기세요. 아무것도 바꾸지 말고 보기만 합니다.\n")
	b.WriteString("각 글 주소를 열어 상태(판매중·예약중·판매완료), 가격, 조회수, 찜 수, 채팅 수를 읽으세요. 숫자가 화면에 없으면 0.\n")
	b.WriteString("글이 삭제되어 열리지 않으면 gone=true. 로그인 화면에 막혀(시스템 안내의 로그인 규칙대로 해도 안 되면) 볼 수 없었으면 loginRequired=true 로, 본 것만 보고하세요.\n\n")
	for _, l := range ls {
		fmt.Fprintf(&b, "- 글 번호 %s: %s (%s, 지금 기록: %s)\n", l.JoongnaID, l.URL, l.Title, statusLabel[l.Status])
	}
	return b.String()
}

func fallback(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}

func clampText(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
