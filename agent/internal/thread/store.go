// Package thread holds the single 1:1 conversation.
//
// It used to hold the approval cards too; those live in core/proposal now,
// because a proposal can outlive - and start outside - a conversation. And the
// transcript itself now lives in chat-svc, because what was said is what the
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
	// AtISO is the full timestamp chat-svc records beside the wall clock. The
	// console does not read it yet; the record surface currently parses the
	// epoch out of the id to recover the date, and this is what that should be
	// built on instead (web/src/lib/ledger.ts).
	AtISO      string   `json:"atIso,omitempty"`
	Paragraphs []string `json:"paragraphs,omitempty"`
	Text       string   `json:"text,omitempty"`
	// ProposalID points at core/proposal. The transcript records that a card
	// was raised here; what the card says, and what became of it, is the
	// proposal's business.
	ProposalID string `json:"proposalId,omitempty"`
}

// Store is the client to chat-svc, plus the run state that is nobody else's
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

	mu       sync.Mutex
	thinking bool
	log      *slog.Logger
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
	if err := s.do(ctx, http.MethodGet, "/turns", nil, &payload); err != nil {
		s.log.Error("스레드를 불러오지 못했습니다", "err", err)
		return nil, thinking
	}
	return payload.Turns, thinking
}

func (s *Store) AppendUser(text string) *Turn {
	return s.append(&Turn{Role: RoleUser, Text: text})
}

func (s *Store) AppendAgent(paragraphs []string) *Turn {
	if len(paragraphs) == 0 {
		return nil
	}
	return s.append(&Turn{Role: RoleAgent, Paragraphs: paragraphs})
}

// AttachProposal records in the transcript that a card was raised at this
// point in the conversation.
func (s *Store) AttachProposal(proposalID string) {
	s.append(&Turn{Role: RoleAgent, ProposalID: proposalID})
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
func (s *Store) append(t *Turn) *Turn {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var saved Turn
	if err := s.do(ctx, http.MethodPost, "/turns", t, &saved); err != nil {
		s.log.Error("턴을 저장하지 못했습니다", "err", err)
		// 저장은 실패했지만 화면에는 보여준다. 최소한의 식별자와 시각을 여기서
		// 채우는 이유는, id 가 없는 프레임은 클라이언트의 upsert 를 깨뜨리기
		// 때문이다.
		local := *t
		local.ID = fmt.Sprintf("local-%d", time.Now().UnixNano())
		local.At = time.Now().Format("15:04")
		s.bus.Publish(bus.Event{Type: "turn", Turn: local})
		return &local
	}

	s.bus.Publish(bus.Event{Type: "turn", Turn: saved})
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

func (s *Store) Fail(msg string) {
	s.bus.Publish(bus.Event{Type: "error", Message: msg})
}
