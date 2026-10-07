package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/choigonyok/jarvis/agent/internal/claudecode"
)

// BrowserServer is the JARVIS_EXTRA_MCP entry for the shared browser.
const BrowserServer = "mcp-browser"

func runTools() []string {
	tools := []string{
		"browser_navigate", "browser_get_state", "browser_extract_content", "browser_screenshot",
		"browser_scroll", "browser_click", "browser_type", "browser_go_back", "browser_list_tabs",
		"browser_switch_tab", "browser_close_tab", "browser_upload_files",
	}
	out := make([]string, 0, len(tools)+9)
	for _, t := range tools {
		out = append(out, "mcp__"+BrowserServer+"__"+t)
	}
	for _, t := range []string{"progress", "view_photos", "ask", "request_login", "schedule_next", "finish", "report", "remember", "record"} {
		out = append(out, "mcp__"+RunServerName+"__"+t)
	}
	return out
}

type Background interface {
	Idle() bool
	Background(ctx context.Context, t claudecode.Task) (string, error)
}

type SchedulerConfig struct {
	// RunURL is this agent's /mcp/job-run base; the run id is appended.
	RunURL   string
	Interval time.Duration
	// Cooldown is the least time between two runs of any job. The browser
	// is one window shared with the person, and sites watch for bots.
	Cooldown time.Duration
	Timeout  time.Duration
	// UploadsDir is where the job's photos are, for the prompt.
	UploadsDir string
}

// Run takes due jobs one at a time, whenever the person is not using the
// assistant.
func (m *Module) Run(ctx context.Context, bg Background, cfg SchedulerConfig) {
	if err := m.store.CloseOrphans(ctx); err != nil {
		m.log.Warn("남은 실행을 정리하지 못했습니다", "err", err)
	}
	var last time.Time
	tick := time.NewTicker(cfg.Interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		case <-m.kick:
		}
		m.purge(ctx)
		if time.Since(last) < cfg.Cooldown || !bg.Idle() {
			continue
		}
		j, err := m.store.Due(ctx)
		if err != nil || j == nil {
			continue
		}
		last = time.Now()
		m.runOnce(ctx, bg, cfg, *j)
		// Another job may be due right away; look again on the next tick
		// rather than sleeping a whole interval.
	}
}

func (m *Module) runOnce(ctx context.Context, bg Background, cfg SchedulerConfig, j Job) {
	d, err := m.store.Detail(ctx, j.ID)
	if err != nil {
		m.log.Warn("작업을 읽지 못했습니다", "job", j.ID, "err", err)
		return
	}
	run, err := m.store.StartRun(ctx, j.ID)
	if err != nil {
		m.log.Warn("실행을 시작하지 못했습니다", "job", j.ID, "err", err)
		return
	}
	m.current.set(run, d.Job)
	defer m.current.clear()
	m.log.Info("작업을 실행합니다", "job", j.ID, "run", run, "title", j.Title)

	bctx := context.WithoutCancel(ctx)
	result, err := bg.Background(ctx, claudecode.Task{
		Name:    "작업 " + j.Title,
		Prompt:  prompt(d, cfg.UploadsDir, time.Now()),
		System:  runSystem,
		Servers: map[string]string{RunServerName: fmt.Sprintf("%s/%d", cfg.RunURL, run)},
		Extra:   []string{BrowserServer},
		Allowed: runTools(),
		Timeout: cfg.Timeout,
		OnStep: func(s claudecode.Step) {
			if kind, text := describe(s); text != "" {
				_ = m.store.Step(bctx, run, kind, text)
			}
		},
	})

	outcome, note := "ok", ""
	switch {
	case errors.Is(err, claudecode.ErrYielded), errors.Is(err, claudecode.ErrBusy):
		outcome = "yielded"
	case err != nil:
		outcome, note = "error", clampText(err.Error(), 200)
	}
	end, eerr := m.store.EndRun(bctx, run, outcome, note)
	if eerr != nil {
		m.log.Warn("실행을 닫지 못했습니다", "run", run, "err", eerr)
		return
	}
	m.log.Info("작업 실행이 끝났습니다", "job", j.ID, "run", run, "outcome", outcome, "result", clampText(result, 200))
	if end.Failed {
		m.say(d.Job, "세 번 연달아 잘 끝나지 않아 멈췄습니다: "+end.Note+"\n작업 탭에서 확인하고 다시 시작할 수 있습니다.")
	}
}

// purge deletes the photos of jobs that ended long enough ago.
func (m *Module) purge(ctx context.Context) {
	due, err := m.store.PurgeDue(ctx)
	if err != nil {
		return
	}
	for _, p := range due {
		failed := false
		for _, name := range p.Photos {
			if err := m.uploads.Remove(name); err != nil {
				failed = true
			}
		}
		if !failed {
			_ = m.store.MarkPurged(ctx, p.ID)
			m.log.Info("끝난 작업의 사진을 지웠습니다", "job", p.ID, "count", len(p.Photos))
		}
	}
}

// describe turns one step of a run into a progress line, or "" to skip it.
// What a person watching wants: where it went, what it pressed and typed.
func describe(s claudecode.Step) (string, string) {
	if s.Tool == "" {
		return "note", clampText(s.Text, 300)
	}
	var in map[string]any
	_ = json.Unmarshal(s.Input, &in)
	str := func(k string) string {
		v, _ := in[k].(string)
		return v
	}
	name := s.Tool[strings.LastIndex(s.Tool, "__")+2:]
	switch name {
	case "browser_navigate":
		return "step", "열기: " + clampText(str("url"), 120)
	case "browser_click":
		return "step", "누르기"
	case "browser_type":
		return "step", "입력: " + clampText(str("text"), 40)
	case "browser_get_state", "browser_extract_content":
		return "step", "화면 읽기"
	case "browser_screenshot":
		return "step", "화면 보기"
	case "browser_scroll":
		return "step", "스크롤"
	case "browser_go_back":
		return "step", "뒤로"
	case "browser_upload_files":
		n := 0
		if ps, ok := in["paths"].([]any); ok {
			n = len(ps)
		}
		return "step", fmt.Sprintf("사진 %d장 넣기", n)
	case "view_photos":
		return "step", "사진 보기"
	}
	// The job's own tools log themselves in jobs-svc (schedule, card, report).
	return "", ""
}

const runSystem = `당신은 Jarvis의 백그라운드 작업 실행입니다. 운영자가 대화에서 맡긴 작업 하나를, 이미 로그인된 공용 브라우저로 진행합니다. 대화 상대는 없고, 운영자는 작업 탭에서 진행을 지켜봅니다.

이번 실행은 작업의 한 토막입니다. 작업 상태(메모, 지켜보는 대상, 최근 기록, 새 지시와 카드 답)를 보고 지금 할 일을 하고, 반드시 다음 셋 중 하나로 끝냅니다.
- schedule_next: 나중에 다시 확인할 것이 있을 때. 운영자가 주기를 말하지 않았어도 목표를 보고 스스로 정합니다. 예) 판매 글 반응 확인은 1~3시간, 가격 하락 감시는 하루 1~2번, 배송 추적은 몇 시간, 내일 오픈하는 예약은 그 시각 직후. 바뀔 일이 없는 밤 시간은 피하세요.
- ask 또는 request_login: 운영자의 결정이나 로그인이 필요할 때. 부른 뒤 바로 마칩니다.
- finish: 목표를 이뤘거나 더 할 수 있는 일이 없을 때. 결과를 운영자가 알 수 있게 씁니다.
셋 중 아무것도 부르지 않고 끝나면 실패로 칩니다.

반드시 지킬 것
- 밖으로 나가는 행동(글 올리기·고치기·지우기, 구매·예약·신청, 다른 사람에게 메시지 보내기)은 하기 전에 ask 로 승인을 받습니다. 새 지시나 카드 답에서 운영자가 바로 그 일을 이미 승인했다면 다시 묻지 않습니다.
- 운영자가 정해야 하는 값(가격, 수량, 날짜 등)은 지어내지 말고 ask 의 폼 칸(required)으로 받습니다. 참고 정보(시세 등)는 hint 나 body 에 넣습니다.
- 결제는 하지 않습니다. 돈이 드는 단계(결제, 유료 옵션, 충전)가 나오면 멈추고 ask 로 알립니다. 결제 요청은 네트워크에서도 막힙니다.
- 같은 일을 두 번 하지 마세요. 밖으로 나가는 행동 직전에 remember 로 "하는 중", 직후에 결과(글 주소 등)를 남깁니다. 메모에 "하는 중"이 남아 있으면 먼저 실제로 됐는지 확인합니다.
- 작업과 관계없는 페이지, 다른 사람의 글·채팅에는 손대지 마세요.
- 직접 연 탭은 끝날 때 닫으세요.

로그인
- 로그인 화면이 나오면, 네이버 로그인 버튼이 있는 사이트는 그 버튼으로 갑니다.
- 네이버 로그인 화면에서 아이디·비밀번호가 브라우저에 저장된 값으로 이미 채워져 있으면, 다른 것은 아무것도 누르지 말고 로그인 버튼을 한 번 누릅니다("로그인 상태 유지"를 먼저 누르면 칸이 지워집니다). 연결 동의 화면의 "동의/계속"은 눌러도 됩니다.
- 칸이 비어 있거나, 보안문자·기기 인증이 나오거나, 한 번 눌러 안 되면 더 시도하지 말고 request_login(site, 이유)를 부르고 마칩니다.

진행 알리기
- 큰 단계마다 progress 로 한 줄 남깁니다(운영자가 작업 탭에서 실시간으로 봅니다).
- 지켜보는 대상이 있으면 record 로 현황을 남깁니다(판매 글이면 key=글 번호, 상태, 가격, 조회·찜·채팅 수, 대표 사진).
- 실행을 마치기 전에 report 로 작업 한 줄 현황을 갱신합니다. 운영자가 알아야 할 변화(팔림, 가격 하락, 도착, 문제)는 notify=true.
- 다음 실행에 필요한 것(알아낸 사이트 절차, 올린 글 주소)은 remember 로 남깁니다.

화면 다루기
- browser_get_state 로 요소 번호를 보고 browser_click·browser_type 으로 조작합니다. 확인이 필요하면 browser_screenshot.
- 사진은 browser_upload_files 로 파일 칸에 바로 넣습니다. 파일 선택 창을 열려고 버튼을 누르지 마세요(운영체제 창이 떠서 멈춥니다).
- 작업 사진을 직접 보려면 view_photos 를 씁니다.`

var weekdays = [...]string{"일", "월", "화", "수", "목", "금", "토"}

func prompt(d Detail, uploadsDir string, now time.Time) string {
	j := d.Job
	var b strings.Builder
	fmt.Fprintf(&b, "작업 %d: %s\n지금: %s (%s)\n\n", j.ID, j.Title, now.Format("2006-01-02 15:04"), weekdays[int(now.Weekday())])
	fmt.Fprintf(&b, "목표\n%s\n", j.Goal)
	if strings.TrimSpace(j.Instructions) != "" {
		fmt.Fprintf(&b, "\n운영자 지시\n%s\n", j.Instructions)
	}
	if len(j.Sites) > 0 {
		fmt.Fprintf(&b, "\n쓰는 사이트: %s\n", strings.Join(j.Sites, ", "))
	}
	if len(j.Photos) > 0 {
		b.WriteString("\n첨부 사진 (view_photos 로 보고, 업로드는 아래 경로로 browser_upload_files)\n")
		for _, p := range j.Photos {
			fmt.Fprintf(&b, "- %s  →  %s/%s\n", p, uploadsDir, p)
		}
	}
	if len(d.Inbox) > 0 {
		b.WriteString("\n새로 온 것 (이번 실행에서 반영하세요)\n")
		for _, in := range d.Inbox {
			label := "지시"
			if in.Kind == "answer" {
				label = "카드 답"
			}
			fmt.Fprintf(&b, "- [%s %s] %s\n", label, in.At.Local().Format("1/2 15:04"), in.Text)
		}
	}
	if len(d.Memory) > 0 {
		b.WriteString("\n메모 (지난 실행들이 남긴 것)\n")
		keys := make([]string, 0, len(d.Memory))
		for k := range d.Memory {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, "- %s: %s\n", k, d.Memory[k])
		}
	}
	if len(d.Records) > 0 {
		b.WriteString("\n지켜보는 대상\n")
		for _, r := range d.Records {
			fmt.Fprintf(&b, "- [%s] %s", r.Key, r.Title)
			if r.Status != "" {
				fmt.Fprintf(&b, " · %s", r.Status)
			}
			if r.Amount != nil {
				fmt.Fprintf(&b, " · %d원", *r.Amount)
			}
			if r.URL != "" {
				fmt.Fprintf(&b, " · %s", r.URL)
			}
			b.WriteString("\n")
		}
	}
	if len(d.Events) > 0 {
		b.WriteString("\n최근 기록 (최신이 위)\n")
		n := 0
		for _, e := range d.Events {
			if e.Kind == "note" || e.Kind == "run_start" {
				continue
			}
			fmt.Fprintf(&b, "- %s %s\n", e.At.Local().Format("1/2 15:04"), clampText(e.Text, 200))
			if n++; n >= 20 {
				break
			}
		}
	}
	if j.Attempts > 0 {
		fmt.Fprintf(&b, "\n지난 %d번의 실행이 잘 끝나지 않았습니다. 같은 방법을 반복하지 말고, 막히면 ask 로 운영자에게 알리세요.\n", j.Attempts)
	}
	b.WriteString("\n지금 할 일을 하고, schedule_next·ask·request_login·finish 중 하나로 마치세요.")
	return b.String()
}

func clampText(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "…"
}
