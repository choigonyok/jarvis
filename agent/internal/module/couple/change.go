package couple

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/choigonyok/jarvis/agent/internal/tool"
)

type AddInput struct {
	Block  string `json:"block" jsonschema:"블록 id(look 의 couple). 디데이·버킷·자유·데이트 지도 블록"`
	Text   string `json:"text" jsonschema:"버킷·기념일·지도 기록의 이름, 자유 블록에 남길 글"`
	Date   string `json:"date,omitempty" jsonschema:"기념일 날짜, 지도 기록의 첫날 YYYY-MM-DD"`
	Until  string `json:"until,omitempty" jsonschema:"지도 기록의 마지막 날 YYYY-MM-DD(여러 날일 때)"`
	Yearly bool   `json:"yearly,omitempty" jsonschema:"기념일을 해마다 세면 true(생일 등)"`
	Region string `json:"region,omitempty" jsonschema:"지도 기록의 시/군/구 - 이름(해운대구, 부산 중구) 또는 다섯 자리 코드"`
	Note   string `json:"note,omitempty" jsonschema:"지도 기록의 한 줄(선택)"`
}

type ChangeInput struct {
	Block  string  `json:"block" jsonschema:"블록 id"`
	Item   string  `json:"item" jsonschema:"항목 id(look 의 couple 에 block 을 주면 보인다)"`
	Text   *string `json:"text,omitempty" jsonschema:"새 이름이나 글"`
	Done   *bool   `json:"done,omitempty" jsonschema:"버킷을 이뤘으면 true, 되돌리면 false"`
	Date   *string `json:"date,omitempty" jsonschema:"기념일의 새 날짜, 지도 기록의 새 첫날 YYYY-MM-DD"`
	Until  *string `json:"until,omitempty" jsonschema:"지도 기록의 새 마지막 날. 빈 값이면 하루"`
	Yearly *bool   `json:"yearly,omitempty" jsonschema:"기념일을 해마다 셀지"`
	Region *string `json:"region,omitempty" jsonschema:"지도 기록의 새 시/군/구"`
	Note   *string `json:"note,omitempty" jsonschema:"지도 기록의 새 한 줄. 빈 값이면 지운다"`
}

type RemoveInput struct {
	Block string `json:"block" jsonschema:"블록 id"`
	Item  string `json:"item" jsonschema:"지울 항목 id"`
}

type CheckInput struct {
	Block string `json:"block" jsonschema:"습관 체크 블록 id"`
	Day   string `json:"day,omitempty" jsonschema:"체크할 날 YYYY-MM-DD(기본 오늘). 오지 않은 날은 안 된다"`
	Undo  bool   `json:"undo,omitempty" jsonschema:"true 면 그날 운영자의 체크를 지운다"`
	Memo  string `json:"memo,omitempty" jsonschema:"그날의 한 줄(선택)"`
}

// shared is said after every write: the partner's phone has it already.
const shared = " 상대의 uniple 앱에도 바로 보입니다."

// Change puts adding, changing, removing and checking on the board.
func (m *Module) Change(b *tool.Board) {
	tool.Add(b, "couple.add", "커플 공간에 하나 더한다: 버킷, 기념일(디데이), 자유 블록의 글, 데이트 지도의 기록(사진 없이). "+
		"운영자가 그렇게 하라고 한 것만. 블록 id 는 look 의 couple.", m.add)
	tool.Add(b, "couple.change", "커플 공간의 항목을 고친다: 버킷 이름·이룸(done), 기념일 이름·날짜·매년, 자유 글, 지도 기록. "+
		"바꾸라고 한 것만 넣는다.", m.change)
	tool.Add(b, "couple.remove", "커플 공간의 항목을 지운다: 버킷, 기념일, 자유 글, 지도 기록(그 사진까지). 운영자가 그것을 지우라고 했을 때만.", m.remove)
	tool.Add(b, "couple.check", "습관 체크 블록에 운영자의 하루를 체크한다(undo 면 지운다). 운영자가 했다고 말한 날만. 상대 폰에 알림이 간다.", m.check)
}

func (m *Module) add(ctx context.Context, in AddInput) (string, error) {
	s, err := m.space(ctx)
	if err != nil {
		return "", err
	}
	b, err := typed(s, in.Block, "bucket", "dday", "free", "map")
	if err != nil {
		return "", err
	}
	text := strings.TrimSpace(in.Text)
	base := "/couple/blocks/" + url.PathEscape(b.ID)
	switch b.Type {
	case "bucket":
		var w wish
		if err := m.do(ctx, http.MethodPost, base+"/bucket", map[string]any{"title": text}, &w); err != nil {
			return "", fmt.Errorf("버킷을 더하지 못했습니다: %w", err)
		}
		return fmt.Sprintf("%s에 버킷을 더했습니다: %s (id %s).%s", b.Title, w.Title, w.ID, shared), nil
	case "dday":
		if in.Date == "" {
			return "", fmt.Errorf("기념일에는 date 가 필요합니다")
		}
		body := map[string]any{"title": text, "date": in.Date}
		if in.Yearly {
			body["repeat"] = "yearly"
		}
		var d dday
		if err := m.do(ctx, http.MethodPost, base+"/dday", body, &d); err != nil {
			return "", fmt.Errorf("기념일을 더하지 못했습니다: %w", err)
		}
		return fmt.Sprintf("%s에 기념일을 더했습니다: %s %s%s (id %s). 캘린더에도 들어갑니다.%s", b.Title, d.Title, d.Date, map[bool]string{true: " · 매년"}[in.Yearly], d.ID, shared), nil
	case "free":
		var n note
		if err := m.do(ctx, http.MethodPost, base+"/free", map[string]any{"content": text}, &n); err != nil {
			return "", fmt.Errorf("글을 남기지 못했습니다: %w", err)
		}
		return fmt.Sprintf("%s에 남겼습니다: %s (id %s).%s", b.Title, n.Content, n.ID, shared), nil
	default: // map
		code, err := regions.find(in.Region)
		if err != nil {
			return "", err
		}
		body := map[string]any{"region": code, "title": text}
		if in.Date != "" {
			body["from"] = in.Date
			body["to"] = firstOf(in.Until, in.Date)
		}
		if in.Note != "" {
			body["note"] = in.Note
		}
		var r record
		if err := m.do(ctx, http.MethodPost, base+"/records", body, &r); err != nil {
			return "", fmt.Errorf("데이트 기록을 남기지 못했습니다: %w", err)
		}
		return fmt.Sprintf("%s의 %s에 기록을 남겼습니다: %s (id %s). 사진은 일정 탭의 지도에서 붙입니다.%s", b.Title, regions.name(code), firstOf(r.Title, text), r.ID, shared), nil
	}
}

func (m *Module) change(ctx context.Context, in ChangeInput) (string, error) {
	s, err := m.space(ctx)
	if err != nil {
		return "", err
	}
	b, err := typed(s, in.Block, "bucket", "dday", "free", "map")
	if err != nil {
		return "", err
	}
	item := strings.TrimSpace(in.Item)
	if item == "" {
		return "", fmt.Errorf("item 에 항목 id 가 필요합니다. look 의 couple 에 block 을 주면 보입니다")
	}
	path := "/couple/blocks/" + url.PathEscape(b.ID)
	body := map[string]any{}
	switch b.Type {
	case "bucket":
		path += "/bucket/" + url.PathEscape(item)
		if in.Text != nil {
			body["title"] = *in.Text
		}
		if in.Done != nil {
			body["done"] = *in.Done
		}
	case "dday":
		path += "/dday/" + url.PathEscape(item)
		if in.Text != nil {
			body["title"] = *in.Text
		}
		if in.Date != nil {
			body["date"] = *in.Date
		}
		if in.Yearly != nil {
			body["repeat"] = map[bool]string{true: "yearly", false: ""}[*in.Yearly]
		}
	case "free":
		path += "/free/" + url.PathEscape(item)
		if in.Text != nil {
			body["content"] = *in.Text
		}
	case "map":
		path += "/records/" + url.PathEscape(item)
		if in.Text != nil {
			body["title"] = *in.Text
		}
		if in.Date != nil {
			body["from"] = *in.Date
		}
		if in.Until != nil {
			body["to"] = *in.Until
		}
		if in.Note != nil {
			body["note"] = *in.Note
		}
		if in.Region != nil {
			code, err := regions.find(*in.Region)
			if err != nil {
				return "", err
			}
			body["region"] = code
		}
	}
	if len(body) == 0 {
		return "", fmt.Errorf("바꿀 것이 없습니다. %s 블록에서 바꿀 수 있는 것을 넣으세요", kinds[b.Type])
	}
	if err := m.do(ctx, http.MethodPatch, path, body, nil); err != nil {
		return "", fmt.Errorf("고치지 못했습니다: %w", err)
	}
	return fmt.Sprintf("%s의 항목(id %s)을 고쳤습니다.%s", b.Title, item, shared), nil
}

func (m *Module) remove(ctx context.Context, in RemoveInput) (string, error) {
	s, err := m.space(ctx)
	if err != nil {
		return "", err
	}
	b, err := typed(s, in.Block, "bucket", "dday", "free", "map")
	if err != nil {
		return "", err
	}
	item := strings.TrimSpace(in.Item)
	if item == "" {
		return "", fmt.Errorf("item 에 지울 항목 id 가 필요합니다")
	}
	kind := map[string]string{"bucket": "bucket", "dday": "dday", "free": "free", "map": "records"}[b.Type]
	if err := m.do(ctx, http.MethodDelete, "/couple/blocks/"+url.PathEscape(b.ID)+"/"+kind+"/"+url.PathEscape(item), nil, nil); err != nil {
		return "", fmt.Errorf("지우지 못했습니다: %w", err)
	}
	return fmt.Sprintf("%s에서 지웠습니다(id %s).%s", b.Title, item, shared), nil
}

func (m *Module) check(ctx context.Context, in CheckInput) (string, error) {
	s, err := m.space(ctx)
	if err != nil {
		return "", err
	}
	b, err := typed(s, in.Block, "workout")
	if err != nil {
		return "", err
	}
	day := firstOf(strings.TrimSpace(in.Day), m.today())
	base := "/couple/blocks/" + url.PathEscape(b.ID) + "/checks"
	if in.Undo {
		cs, err := m.checks(ctx, b.ID, day, day)
		if err != nil {
			return "", err
		}
		for _, c := range cs {
			if c.Who != "me" {
				continue
			}
			if err := m.do(ctx, http.MethodDelete, base+"/"+url.PathEscape(c.ID), nil, nil); err != nil {
				return "", fmt.Errorf("체크를 지우지 못했습니다: %w", err)
			}
			return fmt.Sprintf("%s의 %s 체크를 지웠습니다.%s", b.Title, day, shared), nil
		}
		return fmt.Sprintf("%s의 %s 에는 운영자의 체크가 없습니다.", b.Title, day), nil
	}
	var c check
	if err := m.do(ctx, http.MethodPost, base, map[string]any{"date": day, "memo": in.Memo}, &c); err != nil {
		return "", fmt.Errorf("체크하지 못했습니다: %w", err)
	}
	said := fmt.Sprintf("%s에 %s 를 체크했습니다", b.Title, c.Date)
	if c.Memo != "" {
		said += "(" + c.Memo + ")"
	}
	return said + "." + shared + " 상대 폰에 알림이 갑니다.", nil
}
