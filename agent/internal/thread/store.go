// Package thread holds the single 1:1 conversation.
//
// It used to hold the approval cards too; those live in core/proposal now,
// because a proposal can outlive - and start outside - a conversation. And the
// transcript itself now lives in input-svc, because what was said is what the
// operator would notice losing.
//
// What is left here is the seam: the live SSE stream and the "is a run in
// flight" flag. Neither survives a restart on purpose - a process that just
// started is not in the middle of a turn, whatever it was doing when it
// stopped - so neither belongs in a database.
package thread

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/choigonyok/jarvis/agent/internal/core/bus"
)

type Role string

const (
	RoleAgent Role = "agent"
	RoleUser  Role = "user"
)

type Turn struct {
	ID   string `json:"id"`
	Role Role   `json:"role"`
	At   string `json:"at"`
	// AtISO is the full timestamp input-svc records beside the wall clock. The
	// console does not read it yet; the record surface currently parses the
	// epoch out of the id to recover the date, and this is what that should be
	// built on instead (web/src/lib/ledger.ts).
	AtISO      string   `json:"atIso,omitempty"`
	Paragraphs []string `json:"paragraphs,omitempty"`
	Text       string   `json:"text,omitempty"`
	// Images are photos by upload name: the operator's, attached to a
	// message, or the assistant's, shown (AppendAgentImages).
	Images []string `json:"images,omitempty"`
	// ProposalID points at core/proposal. The transcript records that a card
	// was raised here; what the card says, and what became of it, is the
	// proposal's business.
	ProposalID string `json:"proposalId,omitempty"`
	// Thread is whose conversation this turn belongs to; see Store.thread.
	Thread string `json:"thread,omitempty"`
	// Ref is the earlier message the operator pointed at (swiped) when
	// sending this one.
	Ref *Ref `json:"ref,omitempty"`
	// Origin, when set, says this is news from elsewhere - a background
	// job's run - not the conversation's own words. See JobNews.
	Origin *Origin `json:"origin,omitempty"`
}

// Origin is where a turn came from when not from the conversation session.
type Origin struct {
	Kind  string `json:"kind"`
	Job   int64  `json:"job,omitempty"`
	Run   int64  `json:"run,omitempty"`
	Title string `json:"title,omitempty"`
	// Ref is what a notice was sent as (its event's key), so one delivered
	// twice is laid down once.
	Ref string `json:"ref,omitempty"`
	// Checklist is the list the card shows (mind's checklist id) - as it
	// stands whenever it is drawn, its items ticked on the card itself.
	Checklist string `json:"checklist,omitempty"`
}

// Origin kinds.
const (
	// OriginJob is a background job's run speaking (JobNews).
	OriginJob = "job"
	// OriginNotice is something told at its moment - a reminder, the rain
	// at the door - laid in the conversation as well as pushed (Card).
	OriginNotice = "notice"
	// OriginChecklist is a checklist put in the conversation (Card).
	OriginChecklist = "checklist"
)

// Ref is a quoted earlier turn: which one, who said it, when, and how it began.
type Ref struct {
	ID   string `json:"id"`
	Role Role   `json:"role"`
	At   string `json:"at,omitempty"`
	Text string `json:"text"`
}

// RefTo quotes turn t: its words (the start of them) and who said them.
func RefTo(t *Turn) *Ref {
	text := t.Text
	if len(t.Paragraphs) > 0 {
		text = strings.Join(t.Paragraphs, "\n\n")
	}
	if text == "" && len(t.Images) > 0 {
		text = fmt.Sprintf("(사진 %d장)", len(t.Images))
	}
	if r := []rune(text); len(r) > 600 {
		text = string(r[:600]) + "…"
	}
	at := t.At
	// Read, not sliced: input-svc writes the stamp in the database session's
	// zone, which is not promised to be the operator's.
	if ts, err := time.Parse(time.RFC3339Nano, t.AtISO); err == nil {
		at = ts.In(refZone).Format("01-02 15:04")
	}
	return &Ref{ID: t.ID, Role: t.Role, At: at, Text: text}
}

// refZone is the operator's clock (TZ, Asia/Seoul in the container).
var refZone = time.Local

// Store is the client to the turns (input-svc), plus the run state that is nobody else's
// business.
//
// The transcript is not cached. A cached one would have to be invalidated by a
// second writer, and more importantly the snapshot endpoint exists so a
// reconnecting client re-reads the truth - serving it from memory would defeat
// the thing it is for.
type Store struct {
	base   string
	token  string
	client *http.Client
	bus    *bus.Bus

	// thread is the conversation this agent owns in input-svc: "" for the
	// operator, "guest" for the guest agent. It reads and writes only that.
	thread string
	// newsIDs is the turn each job run speaks through, by run: a run's
	// second word replaces its first. Lost on restart, when the transcript
	// is searched instead (JobNews).
	newsMu  sync.Mutex
	newsIDs map[int64]string

	mu       sync.Mutex
	thinking bool
	log      *slog.Logger

	// drafts is the message being written, sent on as it grows (draft.go).
	drafts drafts
}

// ForThread scopes the store to one conversation.
func (s *Store) ForThread(name string) *Store {
	s.thread = name
	return s
}

func NewStore(baseURL, token string, b *bus.Bus) *Store {
	return &Store{
		base:   strings.TrimRight(baseURL, "/"),
		token:  token,
		client: &http.Client{Timeout: 10 * time.Second},
		bus:    b,
		log:    slog.Default(),
	}
}

// Connect confirms the service answers, and is the boot check that used to be
// "read the file before anything can write". A boot that silently started an
// empty transcript over a full one would erase it on the first turn; the same
// risk exists over HTTP, so the same check happens first.
func (s *Store) Connect(log *slog.Logger) error {
	if log != nil {
		s.log = log
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.base+"/health", nil)
	if err != nil {
		return err
	}
	res, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("대화 서비스에 연결하지 못했습니다(%s): %w", s.base, err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("대화 서비스가 준비되지 않았습니다(%s): %s", s.base, res.Status)
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
		return fmt.Errorf("대화 서비스 호출: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode >= 300 {
		var payload struct {
			Error string `json:"error"`
		}
		raw, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		if json.Unmarshal(raw, &payload) == nil && payload.Error != "" {
			return errors.New(payload.Error)
		}
		return fmt.Errorf("대화 서비스가 거부했습니다: %s", res.Status)
	}

	if out == nil {
		return nil
	}
	return json.NewDecoder(res.Body).Decode(out)
}

// Snapshot returns the whole thread plus whether a run is in flight, for a
// client that has just connected and needs to catch up.
//
// A failure here returns an empty transcript rather than an error, because the
// signature is the one the HTTP layer already had. That is a real limitation -
// "nothing was said" and "could not ask" look the same to the caller - so it is
// logged loudly: a thread that silently renders empty is the most alarming
// thing this console can do.
func (s *Store) Snapshot() ([]*Turn, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	s.mu.Lock()
	thinking := s.thinking
	s.mu.Unlock()

	var payload struct {
		Turns []*Turn `json:"turns"`
	}
	if err := s.do(ctx, http.MethodGet, "/turns?thread="+url.QueryEscape(s.thread), nil, &payload); err != nil {
		s.log.Error("스레드를 불러오지 못했습니다", "err", err)
		return nil, thinking
	}
	return payload.Turns, thinking
}

// Window is the newest `limit` turns before `before` (a turn id, or an
// atIso; "" for the newest of all), oldest first, and whether older ones
// remain - the console's page (input-svc GET /turns?limit=).
func (s *Store) Window(before string, limit int) ([]*Turn, bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	q := url.Values{"thread": {s.thread}, "limit": {strconv.Itoa(limit)}}
	if before != "" {
		// A time cursor whose "+" became a space on the way (URL decoding
		// along a proxy) is put back; an id cursor has no such character.
		q.Set("before", strings.Replace(before, " ", "+", 1))
	}
	var payload struct {
		Turns   []*Turn `json:"turns"`
		HasMore bool    `json:"hasMore"`
	}
	if err := s.do(ctx, http.MethodGet, "/turns?"+q.Encode(), nil, &payload); err != nil {
		s.log.Error("스레드의 한 페이지를 불러오지 못했습니다", "before", before, "err", err)
		return nil, false, err
	}
	return payload.Turns, payload.HasMore, nil
}

func (s *Store) AppendUser(text string, images ...string) *Turn {
	return s.append(&Turn{Role: RoleUser, Text: text, Images: images})
}

// AppendUserRef is AppendUser for a message that quotes an earlier one.
func (s *Store) AppendUserRef(text string, ref *Ref, images ...string) *Turn {
	return s.append(&Turn{Role: RoleUser, Text: text, Images: images, Ref: ref})
}

// Find is the turn with this id, from the stored transcript.
func (s *Store) Find(id string) *Turn {
	turns, _ := s.Snapshot()
	for _, t := range turns {
		if t.ID == id {
			return t
		}
	}
	return nil
}

func (s *Store) AppendAgent(paragraphs []string) *Turn {
	if len(paragraphs) == 0 {
		return nil
	}
	return s.append(&Turn{Role: RoleAgent, Paragraphs: paragraphs})
}

// AppendAgentImages puts pictures the assistant shows in the conversation, as
// a message of their own (module show): the photos, and a line about them.
func (s *Store) AppendAgentImages(images []string, caption string) *Turn {
	if len(images) == 0 {
		return nil
	}
	t := &Turn{Role: RoleAgent, Images: images}
	if caption != "" {
		t.Paragraphs = []string{caption}
	}
	return s.append(t)
}

// JobNews puts a job run's word in the conversation: one turn per run, so a
// run that reports twice updates its card rather than stacking two. It is
// not a model turn - nothing of the session is touched, which is the point:
// the conversation goes on undisturbed, and the next turn is told (the
// runner's job news). The console draws it as a card beside the thread.
func (s *Store) JobNews(o Origin, paragraphs []string) *Turn {
	if len(paragraphs) == 0 || o.Run == 0 {
		return nil
	}
	s.newsMu.Lock()
	id, known := s.newsIDs[o.Run]
	s.newsMu.Unlock()
	if !known {
		// After a restart the map is empty; the transcript still knows.
		turns, _ := s.Snapshot()
		for i := len(turns) - 1; i >= 0; i-- {
			if og := turns[i].Origin; og != nil && og.Kind == o.Kind && og.Run == o.Run {
				id = turns[i].ID
				break
			}
		}
	}
	if id != "" && !strings.HasPrefix(id, "local-") {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		var saved Turn
		body := map[string]any{"thread": s.thread, "paragraphs": paragraphs}
		if err := s.do(ctx, http.MethodPut, "/turns/"+url.PathEscape(id), body, &saved); err == nil {
			s.bus.Publish(bus.Event{Type: "turn", Turn: saved})
			return &saved
		} else {
			s.log.Warn("작업 소식을 갱신하지 못해 새로 적습니다", "run", o.Run, "err", err)
		}
	}
	t := s.append(&Turn{Role: RoleAgent, Paragraphs: paragraphs, Origin: &o})
	s.newsMu.Lock()
	if s.newsIDs == nil {
		s.newsIDs = map[int64]string{}
	}
	s.newsIDs[o.Run] = t.ID
	s.newsMu.Unlock()
	return t
}

// Card lays a card that is no model's turn in the conversation as a turn of
// its own - a notice told at its moment, a checklist shown - where it stays.
// Like a job's news it is not said by the session, which is told of it
// before the next message instead (the runner's jobNews).
func (s *Store) Card(o Origin, paragraphs []string) *Turn {
	return s.append(&Turn{Role: RoleAgent, Paragraphs: paragraphs, Origin: &o})
}

// Told says whether a card of this kind and ref is among the recent turns:
// a notice delivered again (the stream delivers at least once, and again
// after a restart) is not laid down twice.
func (s *Store) Told(kind, ref string) bool {
	if ref == "" {
		return false
	}
	turns, _, err := s.Window("", 120)
	if err != nil {
		return false
	}
	for _, t := range turns {
		if t.Origin != nil && t.Origin.Kind == kind && t.Origin.Ref == ref {
			return true
		}
	}
	return false
}

// AttachProposal records in the transcript that a card was raised at this
// point in the conversation.
func (s *Store) AttachProposal(proposalID string) {
	s.append(&Turn{Role: RoleAgent, ProposalID: proposalID})
}

// Raise records a card nobody asked for - a suggestion - with what the
// agent says about it, as one turn. It stays where it was raised, decided or
// not, like any card raised in conversation.
func (s *Store) Raise(paragraphs []string, proposalID string) {
	s.append(&Turn{Role: RoleAgent, Paragraphs: paragraphs, ProposalID: proposalID})
}

// append stores the turn and publishes it.
//
// The id and the clock come back from the service rather than being minted
// here: two writers stamping ids from their own clocks is how you get a
// collision that only shows up in production.
//
// A failure is logged, not returned - the turn already happened, and refusing
// to show it because it could not be written would lose it twice. It is still
// published so the screen shows what was said; it will be missing after a
// reload, which is the same trade the file-backed store made.
func (s *Store) append(t *Turn) *Turn { return s.appendAs(t, "") }

// appendAs is append for the finished form of a draft: its frame says which
// one it replaces, so the screen swaps one for the other in a single step.
func (s *Store) appendAs(t *Turn, draft string) *Turn {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	t.Thread = s.thread
	var saved Turn
	if err := s.do(ctx, http.MethodPost, "/turns", t, &saved); err != nil {
		s.log.Error("턴을 저장하지 못했습니다", "err", err)
		// 저장은 실패했지만 화면에는 보여준다. 최소한의 식별자와 시각을 여기서
		// 채우는 이유는, id 가 없는 프레임은 클라이언트의 upsert 를 깨뜨리기
		// 때문이다.
		local := *t
		local.ID = fmt.Sprintf("local-%d", time.Now().UnixNano())
		local.At = time.Now().Format("15:04")
		s.bus.Publish(bus.Event{Type: "turn", Turn: local, Replaces: draft})
		return &local
	}

	s.bus.Publish(bus.Event{Type: "turn", Turn: saved, Replaces: draft})
	return &saved
}

func (s *Store) SetThinking(v bool) {
	s.mu.Lock()
	if s.thinking == v {
		s.mu.Unlock()
		return
	}
	s.thinking = v
	s.mu.Unlock()
	s.bus.Publish(bus.Event{Type: "status", Thinking: v})
}

// ShowQueue tells every client what waits behind the running turn.
func (s *Store) ShowQueue(queue any) {
	s.bus.Publish(bus.Event{Type: "queue", Queue: queue})
}

func (s *Store) Fail(msg string) {
	s.bus.Publish(bus.Event{Type: "error", Message: msg})
}
