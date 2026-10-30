// Package remind is the conversation's reminders - "몇 시에 알려줘", "집에
// 가면 알려줘", "나갈 때 알려줘" - kept by mind-svc, which says each at its
// moment as a push to the phone.
//
// They used to be jobs, and a job was the wrong thing for them: a job run
// knows neither where the operator is nor how to ring the phone (its news
// lands in the conversation only), so "퇴근해서 집에 오면" could not be kept
// and nothing rang at the time (2026-10-20). A reminder has nothing to watch
// or do on the web, only a moment to wait for.
//
// What a row answers is what mind stored, so the conversation tells the
// operator what will happen - not what it meant to set.
package remind

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/choigonyok/jarvis/agent/internal/tool"
)

// Name is the module. None of it needs a card: a reminder only rings the
// operator's own phone, and they asked for it.
const Name = "remind"

// Reminder is one as mind's /api/reminders answers it.
type Reminder struct {
	ID     string `json:"id"`
	Text   string `json:"text"`
	Source string `json:"source"`
	Status string `json:"status"`
	When   string `json:"when"`
	Line   string `json:"line"`
}

// Sender sends what would change mind through the stream (mindcmd).
type Sender interface {
	Do(ctx context.Context, method, path string, in, out any) error
}

type Module struct {
	url, token string
	http       *http.Client
	send       Sender
}

// New reads mind-svc at url with the services' token and changes it through
// send.
func New(url, token string, send Sender) *Module {
	return &Module{url: strings.TrimRight(url, "/"), token: token, http: &http.Client{Timeout: 15 * time.Second}, send: send}
}

func (m *Module) Name() string                { return Name }
func (m *Module) Start(context.Context) error { return nil }
func (m *Module) Stop(context.Context) error  { return nil }

func (m *Module) do(ctx context.Context, method, path string, in, out any) error {
	if method != http.MethodGet && m.send != nil {
		return m.send.Do(ctx, method, path, in, out)
	}
	var body io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, m.url+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if m.token != "" {
		req.Header.Set("Authorization", "Bearer "+m.token)
	}
	res, err := m.http.Do(req)
	if err != nil {
		return fmt.Errorf("mind 에 연결하지 못했습니다: %w", err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(raw, &e) == nil && e.Error != "" {
			return fmt.Errorf("%s", e.Error)
		}
		return fmt.Errorf("mind 가 %d 로 응답했습니다", res.StatusCode)
	}
	return json.Unmarshal(raw, out)
}

type SetInput struct {
	Text      string `json:"text" jsonschema:"폰에 뜰 한 줄, 운영자에게 하는 말로. 예: '내일 캠핑, 장갑 챙기세요'"`
	Body      string `json:"body,omitempty" jsonschema:"그 아래 한 줄 더 붙일 맥락(선택). 예: '장작 팰 때 쓸 작업용 장갑'"`
	At        string `json:"at,omitempty" jsonschema:"알릴 시각 YYYY-MM-DDTHH:MM. place 와 함께 주면 '늦어도 이때는': 그때까지 도착·출발 소식이 없으면 이 시각에 대신 알린다"`
	Place     string `json:"place,omitempty" jsonschema:"장소 조건: home(집)·work(회사) 또는 등록된 영역 이름. 비우면 시각(at)만으로 알린다"`
	Move      string `json:"move,omitempty" jsonschema:"arrive(그 장소에 도착할 때, 기본) 또는 leave(그 장소에서 나갈 때)"`
	After     string `json:"after,omitempty" jsonschema:"장소 조건을 이때부터 센다 YYYY-MM-DDTHH:MM. 이때 이미 그 장소에 있으면 그때 바로 알린다(예: '금요일 퇴근하고 집에 오면' → 그날 19:00). 비우면 지금부터 다음 도착·출발에"`
	Until     string `json:"until,omitempty" jsonschema:"at 없이 장소 조건만일 때, 이때가 지나도록 도착·출발이 없으면 알리지 않고 끝낸다(선택)"`
	Checklist string `json:"checklist,omitempty" jsonschema:"그때 함께 띄울 체크리스트 id(chk_…, 선택). 그 순간의 체크 상태 그대로 대화에 카드로 뜨고, 푸시에는 남은 항목이 붙는다"`
}

type ChangeInput struct {
	ID        string   `json:"id" jsonschema:"리마인드 id(rem_…). 지금 상황의 '맡아 둔 알림'이나 look 의 reminders 에서 확인한다"`
	Text      string   `json:"text,omitempty" jsonschema:"새 문구. 비우면 그대로"`
	Body      string   `json:"body,omitempty" jsonschema:"새 맥락 한 줄. 비우면 그대로"`
	At        string   `json:"at,omitempty" jsonschema:"새 시각(또는 '늦어도') YYYY-MM-DDTHH:MM. 비우면 그대로"`
	Place     string   `json:"place,omitempty" jsonschema:"새 장소 조건 home·work·영역 이름. 비우면 그대로"`
	Move      string   `json:"move,omitempty" jsonschema:"arrive 또는 leave. 비우면 그대로"`
	After     string   `json:"after,omitempty" jsonschema:"장소 조건을 셀 새 시작 YYYY-MM-DDTHH:MM. 비우면 그대로"`
	Until     string   `json:"until,omitempty" jsonschema:"새 기한 YYYY-MM-DDTHH:MM. 비우면 그대로"`
	Checklist string   `json:"checklist,omitempty" jsonschema:"함께 띄울 체크리스트 id(chk_…). 비우면 그대로"`
	Clear     []string `json:"clear,omitempty" jsonschema:"없앨 조건: at, place, after, until, checklist 중. 예: 장소 조건을 빼고 시각만 남기려면 [\"place\", \"after\"]와 함께 at"`
}

type ListInput struct {
	All bool `json:"all,omitempty" jsonschema:"true 면 최근 3일 안에 보냈거나 취소한 것까지"`
}

type CancelInput struct {
	ID string `json:"id" jsonschema:"취소할 리마인드 id(rem_…)"`
}

// Look puts the reminders on the board.
func (m *Module) Look(b *tool.Board) {
	tool.Add(b, "reminders", "맡아 둔 리마인드(기다리는 것, 가까운 때 먼저). all 이면 최근 3일 안에 보냈거나 취소한 것도.",
		func(ctx context.Context, in ListInput) (string, error) {
			path := "/api/reminders"
			if in.All {
				path += "?all=1"
			}
			var out struct {
				Reminders []Reminder `json:"reminders"`
			}
			if err := m.do(ctx, http.MethodGet, path, nil, &out); err != nil {
				return "", err
			}
			return renderList(out.Reminders), nil
		})
}

// Change puts setting, changing and cancelling one on the board.
func (m *Module) Change(b *tool.Board) {
	tool.Add(b, "reminder.set", "운영자가 정해진 때에 알려 달라고 한 것을 그때 폰 푸시로 알리고, 대화에도 안내 카드로 남긴다(방해 금지 시간·운전 중에도 울린다). "+
		"때는 시각(at), 장소에 도착·출발할 때(place, move), 둘 다(장소를 기다리되 늦어도 at 에)로 정한다. "+
		"챙길 것·할 일이 여럿이면 checklist.make 로 목록을 만들고 checklist 에 그 id 를 넣는다 - 그때 카드가 목록을 보여 준다. "+
		"웹을 보거나 무언가를 지켜봐야 하는 일은 이것이 아니라 작업(create_job)이다. 저장된 때와 문구를 돌려준다.",
		func(ctx context.Context, in SetInput) (string, error) {
			body := map[string]any{"text": in.Text, "source": "chat"}
			for k, v := range map[string]string{"body": in.Body, "at": in.At, "place": in.Place, "move": in.Move, "after": in.After, "until": in.Until, "checklist": in.Checklist} {
				if strings.TrimSpace(v) != "" {
					body[k] = v
				}
			}
			var out struct {
				Reminder Reminder `json:"reminder"`
			}
			if err := m.do(ctx, http.MethodPost, "/api/reminders", body, &out); err != nil {
				return "", failed("저장하지 못했습니다", err)
			}
			return "저장했습니다: " + out.Reminder.Line + "\n" +
				"그때 폰으로 푸시가 가고 대화에 안내 카드가 남습니다. 운영자에게는 위의 때와 문구를 그대로 말하세요. 운영자가 말하지 않은 시각을 정했으면 그 시각을 밝히세요.", nil
		})
	tool.Add(b, "reminder.change", "리마인드의 때나 문구를 고친다. 준 칸만 바뀌고, 이미 보낸 것도 새 때를 다시 기다린다. "+
		"운영자가 시각·조건을 바꿔 달라고 하면 새로 만들지 말고 이것으로 고친다. 고친 뒤 저장된 때와 문구를 돌려준다.",
		func(ctx context.Context, in ChangeInput) (string, error) {
			body := map[string]any{}
			for k, v := range map[string]string{"text": in.Text, "body": in.Body, "at": in.At, "place": in.Place, "move": in.Move, "after": in.After, "until": in.Until, "checklist": in.Checklist} {
				if strings.TrimSpace(v) != "" {
					body[k] = v
				}
			}
			if len(in.Clear) > 0 {
				body["clear"] = in.Clear
			}
			var out struct {
				Reminder Reminder `json:"reminder"`
			}
			if err := m.do(ctx, http.MethodPatch, "/api/reminders/"+in.ID, body, &out); err != nil {
				return "", failed("고치지 못했습니다", err)
			}
			return "고쳤습니다: " + out.Reminder.Line + "\n운영자에게는 위의 바뀐 때와 문구를 그대로 말하세요.", nil
		})
	tool.Add(b, "reminder.cancel", "기다리는 리마인드를 취소한다. 그때가 와도 알리지 않는다.",
		func(ctx context.Context, in CancelInput) (string, error) {
			var out struct {
				Reminder Reminder `json:"reminder"`
			}
			if err := m.do(ctx, http.MethodDelete, "/api/reminders/"+in.ID, nil, &out); err != nil {
				return "", failed("취소하지 못했습니다", err)
			}
			return "취소했습니다: " + out.Reminder.Line, nil
		})
}

// failed is a change that did not happen, said so the conversation does not
// tell the operator it did.
func failed(what string, err error) error {
	return fmt.Errorf("%s: %w\n바뀐 것이 없습니다. 운영자에게 됐다고 말하지 마세요.", what, err)
}

func renderList(rs []Reminder) string {
	if len(rs) == 0 {
		return "맡아 둔 리마인드가 없습니다."
	}
	var b strings.Builder
	for _, r := range rs {
		line := r.Line
		if r.Source != "chat" && r.Source != "console" {
			line += " - 자비스가 그때로 미뤄 둔 알림"
		}
		b.WriteString(line + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
