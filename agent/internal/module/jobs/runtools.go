package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/choigonyok/jarvis/agent/internal/core/action"
	"github.com/choigonyok/jarvis/agent/internal/core/proposal"
)

// RunServerName is the MCP server a background run is given, mounted at
// /mcp/job-run/<run id>. Never in the chat's MCP config.
const RunServerName = "job"

// running is the run in flight, if any. The run tools answer only for it: a
// URL with any other run id gets nothing.
type running struct {
	mu  sync.Mutex
	run int64
	job Job
}

func (r *running) set(run int64, j Job) {
	r.mu.Lock()
	r.run, r.job = run, j
	r.mu.Unlock()
}

func (r *running) clear() { r.set(0, Job{}) }

func (r *running) get(run int64) (Job, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.job, run != 0 && r.run == run
}

type ProgressInput struct {
	Text string `json:"text" jsonschema:"지금 무엇을 하고 있는지 한 줄. 운영자가 작업 탭에서 본다. 예: '판매 글 양식에 사진 3장을 넣었습니다'"`
}

type PhotosInput struct {
	Names []string `json:"names" jsonschema:"볼 사진 이름들(작업에 적힌 사진)."`
}

type FieldInput struct {
	Key      string   `json:"key" jsonschema:"영문 키. 예: price"`
	Label    string   `json:"label" jsonschema:"운영자가 보는 이름. 예: 가격(원)"`
	Kind     string   `json:"kind" jsonschema:"text | textarea | number | select"`
	Value    string   `json:"value,omitempty" jsonschema:"미리 채울 값. 운영자가 정해야 하는 값(가격 등)은 비운다."`
	Options  []string `json:"options,omitempty" jsonschema:"select 의 선택지"`
	Required bool     `json:"required,omitempty" jsonschema:"비어 있으면 승인할 수 없게 할지"`
	Hint     string   `json:"hint,omitempty" jsonschema:"칸 아래 한 줄 도움말(시세 등)"`
}

type AskToolInput struct {
	Question string       `json:"question" jsonschema:"카드 제목. 운영자가 무엇을 결정하는지. 예: '이 내용으로 중고나라에 올릴까요?'"`
	Body     string       `json:"body,omitempty" jsonschema:"판단에 필요한 정보(시세, 바뀌는 점, 결과). 짧게."`
	Fields   []FieldInput `json:"fields,omitempty" jsonschema:"운영자가 고치거나 채울 값. 없으면 승인/반려만."`
	Images   []string     `json:"images,omitempty" jsonschema:"카드에 보일 사진 이름"`
}

type LoginToolInput struct {
	Site   string `json:"site" jsonschema:"로그인이 필요한 사이트 호스트. 예: web.joongna.com"`
	Reason string `json:"reason,omitempty" jsonschema:"무엇이 막았는지 한 줄. 예: '네이버 보안문자'"`
}

type ScheduleInput struct {
	AfterMinutes int    `json:"afterMinutes,omitempty" jsonschema:"몇 분 뒤에 다시 볼지. at 과 둘 중 하나."`
	At           string `json:"at,omitempty" jsonschema:"다시 볼 시각. 'YYYY-MM-DD HH:MM' (한국 시간)."`
	Reason       string `json:"reason" jsonschema:"그때 무엇을 볼지 한 줄. 운영자가 작업 탭에서 본다."`
}

type FinishInput struct {
	Summary string `json:"summary" jsonschema:"결과 한두 줄"`
	Notify  *bool  `json:"notify,omitempty" jsonschema:"대화에 알릴지. 기본 true"`
}

type ReportInput struct {
	Summary string `json:"summary" jsonschema:"작업의 지금 현황 한 줄. 작업 탭의 요약이 된다. 예: '판매중 · 조회 48 · 찜 5 · 채팅 3'"`
	Notify  bool   `json:"notify,omitempty" jsonschema:"대화에도 알릴지. 운영자가 알고 싶어 할 변화(판매, 가격 하락, 도착, 문제)일 때만 true"`
}

type MemoryInput struct {
	Key   string `json:"key" jsonschema:"짧은 키. 예: posted_url, form_steps"`
	Value string `json:"value" jsonschema:"다음 실행이 알아야 할 것. 빈 문자열이면 지운다."`
}

type RecordToolInput struct {
	Key     string           `json:"key" jsonschema:"대상의 고유 id(외부 글 번호, 송장 번호, 상품 URL 등). 같은 key 면 덮어쓴다."`
	Title   string           `json:"title" jsonschema:"대상 이름"`
	Status  string           `json:"status,omitempty" jsonschema:"상태 한 단어. 예: 판매중, 예약중, 판매완료, 배송중, 도착"`
	Amount  *int64           `json:"amount,omitempty" jsonschema:"원 단위 금액(가격 등)"`
	Metrics map[string]int64 `json:"metrics,omitempty" jsonschema:"숫자들. 예: {'조회':48,'찜':5,'채팅':3}"`
	Image   string           `json:"image,omitempty" jsonschema:"대표 사진 이름(작업 사진 중 하나)"`
	URL     string           `json:"url,omitempty" jsonschema:"대상 주소"`
	Note    string           `json:"note,omitempty"`
	Remove  bool             `json:"remove,omitempty" jsonschema:"더 볼 필요 없는 대상을 지울 때 true"`
}

// RunHandler serves the run tools. The run id comes from the URL path.
func (m *Module) RunHandler() http.Handler {
	return mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		run, _ := strconv.ParseInt(parts[len(parts)-1], 10, 64)
		return m.runServer(run)
	}, nil)
}

func (m *Module) runServer(run int64) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: RunServerName, Version: "1"}, nil)
	// Every tool checks the run is the one in flight before touching anything.
	guard := func() (Job, *mcp.CallToolResult) {
		j, ok := m.current.get(run)
		if !ok {
			return Job{}, toolText("이 실행은 이미 끝났습니다.", true)
		}
		return j, nil
	}
	ok := func(s string) *mcp.CallToolResult { return toolText(s, false) }
	fail := func(err error) *mcp.CallToolResult { return toolText(err.Error(), true) }

	mcp.AddTool(server, &mcp.Tool{Name: "progress", Description: "지금 하는 일을 작업 탭에 한 줄 남긴다. 큰 단계마다 부른다."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in ProgressInput) (*mcp.CallToolResult, any, error) {
			if _, bad := guard(); bad != nil {
				return bad, nil, nil
			}
			if err := m.store.Step(ctx, run, "progress", in.Text); err != nil {
				return fail(err), nil, nil
			}
			return ok("남겼습니다."), nil, nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "view_photos", Description: "작업에 첨부된 사진을 본다."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in PhotosInput) (*mcp.CallToolResult, any, error) {
			j, bad := guard()
			if bad != nil {
				return bad, nil, nil
			}
			res := &mcp.CallToolResult{}
			for _, name := range in.Names {
				if !contains(j.Photos, name) {
					return toolText(name+" 은 이 작업의 사진이 아닙니다.", true), nil, nil
				}
				p, err := m.uploads.Path(name)
				if err != nil {
					return toolText(name+" 은 이미 지워졌습니다.", true), nil, nil
				}
				data, err := os.ReadFile(p)
				if err != nil {
					return fail(err), nil, nil
				}
				res.Content = append(res.Content, &mcp.ImageContent{Data: data, MIMEType: mimeOf(name)})
			}
			return res, nil, nil
		})

	mcp.AddTool(server, &mcp.Tool{
		Name: "ask",
		Description: "운영자에게 카드로 묻는다: 밖으로 나가는 행동(글 올리기, 구매, 삭제, 메시지 보내기) 전의 승인, 운영자가 정해야 하는 값(가격 등)이 든 폼, 판단이 필요한 질문. " +
			"부른 뒤에는 바로 실행을 마친다 - 답이 오면 다음 실행에서 이어진다.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in AskToolInput) (*mcp.CallToolResult, any, error) {
		j, bad := guard()
		if bad != nil {
			return bad, nil, nil
		}
		ask := AskInput{JobID: j.ID, JobTitle: j.Title, Question: in.Question, Body: in.Body}
		for _, name := range in.Images {
			if contains(j.Photos, name) {
				ask.Images = append(ask.Images, name)
			}
		}
		for _, f := range in.Fields {
			kind := f.Kind
			if kind != "textarea" && kind != "number" && kind != "select" {
				kind = "text"
			}
			ask.Fields = append(ask.Fields, action.Field{
				Key: f.Key, Label: f.Label, Kind: kind, Value: f.Value,
				Options: f.Options, Required: f.Required, Hint: f.Hint,
			})
		}
		raw, _ := json.Marshal(ask)
		p, _ := m.proposals.Open(proposal.Proposal{
			Origin: proposal.OriginNotice,
			Action: action.Action{Kind: KindAsk, Input: raw},
			Card:   askCard(ask),
		})
		if err := m.store.Wait(ctx, run, p.ID, in.Question); err != nil {
			m.proposals.Abandon(p.ID, "작업에 걸지 못했습니다: "+err.Error())
			return fail(err), nil, nil
		}
		return ok("카드를 올렸습니다. 운영자가 결정하면 다음 실행에서 답을 받습니다. 지금 실행을 마치세요."), nil, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "request_login",
		Description: "사이트 로그인이 풀려 스스로 로그인할 수 없을 때 부른다. 운영자에게 로그인 카드를 올리고, 그 사이트를 쓰는 작업은 로그인될 때까지 쉰다. " +
			"부른 뒤에는 바로 실행을 마친다.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in LoginToolInput) (*mcp.CallToolResult, any, error) {
		if _, bad := guard(); bad != nil {
			return bad, nil, nil
		}
		site := host(in.Site)
		if site == "" {
			return toolText("site 가 비어 있습니다.", true), nil, nil
		}
		if err := m.store.SetLogin(ctx, site, true, in.Reason); err != nil {
			return fail(err), nil, nil
		}
		login := LoginInput{Site: site, Reason: in.Reason}
		raw, _ := json.Marshal(login)
		act := action.Action{Kind: KindLogin, Input: raw}
		if _, open := m.proposals.FindPending(act); !open {
			m.proposals.Open(proposal.Proposal{Origin: proposal.OriginNotice, Action: act, Card: loginCard(login)})
		}
		// Due already skips jobs on a blocked site; this only makes the run
		// count as having decided, and comes back soon after the login.
		if _, err := m.store.Schedule(ctx, run, time.Now(), "로그인 뒤에 이어서"); err != nil {
			return fail(err), nil, nil
		}
		return ok("로그인 카드를 올렸습니다. 지금 실행을 마치세요."), nil, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "schedule_next",
		Description: "이 작업을 언제 다시 실행할지 정한다. 목표상 나중에 다시 확인할 것이 있으면 반드시 부른다(최소 5분 뒤). " +
			"얼마나 자주 볼지는 대상이 얼마나 빨리 바뀌는지로 정한다.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ScheduleInput) (*mcp.CallToolResult, any, error) {
		if _, bad := guard(); bad != nil {
			return bad, nil, nil
		}
		at := time.Now().Add(time.Duration(in.AfterMinutes) * time.Minute)
		if in.At != "" {
			t, err := time.ParseInLocation("2006-01-02 15:04", in.At, time.Local)
			if err != nil {
				return toolText("at 은 'YYYY-MM-DD HH:MM' 형식입니다.", true), nil, nil
			}
			at = t
		}
		got, err := m.store.Schedule(ctx, run, at, in.Reason)
		if err != nil {
			return fail(err), nil, nil
		}
		return ok("다음 실행: " + got.Local().Format("2006-01-02 15:04") + ". 할 일이 끝났으면 실행을 마치세요."), nil, nil
	})

	mcp.AddTool(server, &mcp.Tool{Name: "finish", Description: "목표를 이뤘거나 더 할 수 있는 일이 없을 때 작업을 끝낸다."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in FinishInput) (*mcp.CallToolResult, any, error) {
			j, bad := guard()
			if bad != nil {
				return bad, nil, nil
			}
			if err := m.store.Finish(ctx, run, in.Summary); err != nil {
				return fail(err), nil, nil
			}
			if in.Notify == nil || *in.Notify {
				m.say(j, "끝냈습니다. "+in.Summary)
			}
			return ok("작업을 끝냈습니다. 실행을 마치세요."), nil, nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "report", Description: "작업의 지금 현황을 한 줄로 갱신한다. 알릴 만한 변화면 notify=true 로 대화에도 남긴다."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in ReportInput) (*mcp.CallToolResult, any, error) {
			j, bad := guard()
			if bad != nil {
				return bad, nil, nil
			}
			if err := m.store.Report(ctx, run, in.Summary); err != nil {
				return fail(err), nil, nil
			}
			if in.Notify {
				m.say(j, in.Summary)
			}
			return ok("갱신했습니다."), nil, nil
		})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "remember",
		Description: "다음 실행에 남길 메모. 이미 한 일(올린 글 주소, 보낸 것)은 하기 직전과 직후에 남겨, 같은 일을 두 번 하지 않게 한다. 알아낸 사이트 절차도 남긴다.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in MemoryInput) (*mcp.CallToolResult, any, error) {
		if _, bad := guard(); bad != nil {
			return bad, nil, nil
		}
		if err := m.store.Remember(ctx, run, in.Key, in.Value); err != nil {
			return fail(err), nil, nil
		}
		return ok("남겼습니다."), nil, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "record",
		Description: "작업이 지켜보는 대상(판매 글, 매물, 택배 등)의 현황을 남긴다. 작업 탭에 카드로 보인다. 같은 key 는 덮어쓴다.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in RecordToolInput) (*mcp.CallToolResult, any, error) {
		j, bad := guard()
		if bad != nil {
			return bad, nil, nil
		}
		if in.Image != "" && !contains(j.Photos, in.Image) {
			in.Image = ""
		}
		if err := m.store.Record(ctx, run, RecordInput(in)); err != nil {
			return fail(err), nil, nil
		}
		return ok("남겼습니다."), nil, nil
	})
	return server
}

// say puts a job's news in the conversation.
func (m *Module) say(j Job, text string) {
	if m.notify == nil {
		return
	}
	m.notify.AppendAgent([]string{fmt.Sprintf("**작업 · %s**\n%s", j.Title, text)})
}

func mimeOf(name string) string {
	switch {
	case strings.HasSuffix(name, ".png"):
		return "image/png"
	case strings.HasSuffix(name, ".webp"):
		return "image/webp"
	}
	return "image/jpeg"
}

func host(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.TrimPrefix(strings.TrimPrefix(s, "https://"), "http://")
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	return s
}
