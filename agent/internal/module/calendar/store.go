// Package calendar is the first module: it changes the world (adds and edits
// events) and it answers questions about it (what is already booked).
//
// The events themselves live in calendar-svc now. What stayed here is what the
// agent is for: the approval card, the tool schema the model sees, and the
// declaration of which actions are reversible. This file used to be the JSON
// file; it is the client to that service, with the same method set, so
// module.go and mcp.go did not change.
package calendar

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/choigonyok/jarvis/agent/internal/core/bus"
)

// Event is one entry. Times are local wall-clock strings on purpose: this is
// a personal calendar in one timezone, and storing "19:00" keeps what the
// operator typed identical to what they later read.
type Event struct {
	ID    string `json:"id"`
	Date  string `json:"date"`            // YYYY-MM-DD
	Start string `json:"start,omitempty"` // HH:MM
	End   string `json:"end,omitempty"`   // HH:MM
	Title string `json:"title"`
	Place string `json:"place,omitempty"`
	Memo  string `json:"memo,omitempty"`
	// Source records who put it here. The UI reads it; nothing branches on
	// it, because an approved AI change and a hand-typed one are equally the
	// operator's decision.
	Source    string `json:"source"` // jarvis | me | partner
	UpdatedAt string `json:"updatedAt"`
	// Set by the uniple backend - a calendar two people share. EndDate is the
	// last day of a multi-day entry; Owner is me | partner | shared and only
	// labels (both people see everything); Recurrence marks a repeating entry,
	// whose single days carry ids of the form "<series>#<date>".
	EndDate    string `json:"endDate,omitempty"`
	Owner      string `json:"owner,omitempty"`
	Recurrence string `json:"recurrence,omitempty"`
}

const (
	SourceAgent   = "jarvis"
	SourceHuman   = "me"
	SourcePartner = "partner"
)

const (
	OwnerMe      = "me"
	OwnerPartner = "partner"
	OwnerShared  = "shared"
)

// Change is what goes out on the bus when the calendar moves.
type Change struct {
	Op    string `json:"op"` // upsert | delete
	Event Event  `json:"event"`
}

var ErrNotFound = errors.New("그런 일정이 없습니다")

// Store talks to calendar-svc.
//
// There is no cache. A cached calendar would have to be invalidated by a
// second writer - the web's direct-edit path goes through this agent, but a
// future importer or a second console would not - and a stale month grid is a
// worse failure than one round trip per read. The service is on the same
// docker network; the round trip is sub-millisecond.
type Store struct {
	base   string
	token  string
	client *http.Client
	bus    *bus.Bus
}

func NewStore(baseURL, token string, b *bus.Bus) *Store {
	return &Store{
		base:  strings.TrimRight(baseURL, "/"),
		token: token,
		// 타임아웃이 있는 이유: 이 호출 중 하나는 승인 카드를 띄운 채 기다리는
		// 도구 호출 안에서 일어난다. 무기한 매달리면 사람이 승인을 눌러도
		// 아무 일도 일어나지 않는 상태가 된다.
		client: &http.Client{Timeout: 10 * time.Second},
		bus:    b,
	}
}

// Load is the health check at boot. It used to read the file; now it confirms
// the service answers, which is the same question - can this module do its job
// - asked of the new backend.
func (s *Store) Load() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.base+"/health", nil)
	if err != nil {
		return err
	}
	res, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("캘린더 서비스에 연결하지 못했습니다(%s): %w", s.base, err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("캘린더 서비스가 준비되지 않았습니다(%s): %s", s.base, res.Status)
	}
	return nil
}

func (s *Store) do(ctx context.Context, method, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, s.base+path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if s.token != "" {
		req.Header.Set("Authorization", "Bearer "+s.token)
	}

	res, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("캘린더 서비스 호출: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode == http.StatusNotFound {
		return ErrNotFound
	}
	if res.StatusCode >= 300 {
		// 서비스의 오류 메시지를 그대로 올린다. 검증 실패(400)의 본문은 모델이
		// 읽고 고쳐야 하는 문장이므로 "저장 실패"로 뭉개면 안 된다.
		var payload struct {
			Error string `json:"error"`
		}
		raw, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		if json.Unmarshal(raw, &payload) == nil && payload.Error != "" {
			return errors.New(payload.Error)
		}
		return fmt.Errorf("캘린더 서비스가 거부했습니다: %s", res.Status)
	}

	if out == nil {
		return nil
	}
	return json.NewDecoder(res.Body).Decode(out)
}

// Range returns events with date in [from, to], both inclusive, both
// YYYY-MM-DD. An empty bound means unbounded on that side.
func (s *Store) Range(from, to string) []Event {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	q := url.Values{}
	q.Set("from", from)
	q.Set("to", to)

	var payload struct {
		Events []Event `json:"events"`
	}
	if err := s.do(ctx, http.MethodGet, "/events?"+q.Encode(), nil, &payload); err != nil {
		// 이 서명은 error 를 돌려주지 않는다(파일 저장소 시절의 계약이다).
		// 빈 목록은 "일정 없음"으로 읽히는데, 조회 실패와 구분되지 않는 것이
		// 이 경로의 알려진 한계다. 호출하는 쪽은 Facts 와 /calendar 조회뿐이고
		// 둘 다 화면에 비어 보이는 것으로 드러난다.
		return nil
	}
	return payload.Events
}

func (s *Store) Get(id string) (Event, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var e Event
	if err := s.do(ctx, http.MethodGet, "/events/"+url.PathEscape(id), nil, &e); err != nil {
		return Event{}, false
	}
	return e, true
}

// Put inserts or replaces. An empty ID means insert.
//
// The id is minted by the service, not here: two writers minting ids from their
// own clocks is how you get a collision that only shows up in production.
func (s *Store) Put(e Event) (Event, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if e.Source == "" {
		e.Source = SourceHuman
	}
	// 서비스도 같은 검증을 한다. 여기서 한 번 더 하는 것은 중복이 아니라
	// 위치의 문제다: 모델이 상대 날짜를 보냈을 때 왕복 없이 즉시 거부되어야
	// 하고, 그 거부가 도구 호출의 결과로 모델에게 그대로 전달된다. 서비스의
	// 검증은 이 에이전트를 거치지 않는 두 번째 쓰기를 막는 쪽이다.
	if err := validate(&e); err != nil {
		return Event{}, err
	}

	var saved Event
	if err := s.do(ctx, http.MethodPut, "/events", e, &saved); err != nil {
		return Event{}, err
	}

	s.bus.Publish(bus.Event{Type: "calendar", Calendar: Change{Op: "upsert", Event: saved}})
	return saved, nil
}

func (s *Store) Delete(id string) (Event, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var gone Event
	if err := s.do(ctx, http.MethodDelete, "/events/"+url.PathEscape(id), nil, &gone); err != nil {
		return Event{}, err
	}

	s.bus.Publish(bus.Event{Type: "calendar", Calendar: Change{Op: "delete", Event: gone}})
	return gone, nil
}

// Conflicts asks which confirmed events overlap this one.
//
// Nothing calls this yet. It is here because the reason this module holds the
// calendar is so that the thing which will notice "this clashes with something"
// is the thing that has the data - and now that the data is in Postgres, that
// question is one SQL function away instead of a loop over a JSON file.
func (s *Store) Conflicts(id string, buffer time.Duration) ([]Event, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	path := fmt.Sprintf("/events/%s/conflicts?buffer=%s", url.PathEscape(id), buffer)
	var payload struct {
		Conflicts []Event `json:"conflicts"`
	}
	if err := s.do(ctx, http.MethodGet, path, nil, &payload); err != nil {
		return nil, err
	}
	return payload.Conflicts, nil
}
