package sensor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/interpret"
	"github.com/choigonyok/jarvis/mind-svc/internal/llm"
	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/store"
)

// History is the past the sensors did not read on their first start (only
// MIND_BACKFILL of it): brought in once, and read for facts only - no plan,
// no loop, no card, nothing the attention loop sees.
//
// The line between past and present is the earliest message mind already
// had when History first ran: everything before it is the past, everything
// after it was read live. It is kept, so a second run draws the same line.
// Each past message's room is marked read through that line, so the live
// reading never takes the past for something new.
type History struct {
	Messengers []*Messenger
	Chat       *Chat
	Store      store.Store
	Reader     *interpret.Conversation
	Noise      *regexp.Regexp // rooms not worth reading
	Batch      int            // messages per read
	Out        *slog.Logger
	// Wait is how long to wait when the day's LLM budget is spent.
	Wait time.Duration
}

const (
	historyBefore = "history:before"
	historyDone   = "history:done"
	historyRoom   = "history:room:" // + room: read through, in microseconds
	// quarantineHistory is how many times one message may come back
	// unreadable before it is passed over.
	quarantineHistory = 3
)

// Run brings the past in and reads it, room by room, oldest first; once
// done it does nothing on later starts.
func (h *History) Run(ctx context.Context) error {
	if done, err := h.Store.Cursor(ctx, historyDone); err != nil || done > 0 {
		return err
	}
	before, err := h.line(ctx)
	if err != nil || before.IsZero() {
		return err
	}
	n, err := h.Import(ctx, before)
	if err != nil {
		return err
	}
	h.Out.Info("지난 대화를 들여왔습니다", "before", before, "messages", n)
	facts, err := h.ReadAll(ctx, before)
	if err != nil {
		return err
	}
	h.Out.Info("지난 대화를 다 읽었습니다", "before", before, "facts", facts)
	return h.Store.SetCursor(ctx, historyDone, 1)
}

// line is the boundary between past and present: kept from the first run,
// or the earliest message mind holds now (zero: none yet, nothing to do).
func (h *History) line(ctx context.Context) (time.Time, error) {
	if v, err := h.Store.Cursor(ctx, historyBefore); err != nil || v > 0 {
		return time.UnixMicro(v), err
	}
	rooms, err := h.Store.Rooms(ctx)
	if err != nil {
		return time.Time{}, err
	}
	var first time.Time
	for _, r := range rooms {
		ms, err := h.Store.RoomMessagesFrom(ctx, r.Room, time.Time{}, time.Time{}, 1)
		if err != nil {
			return time.Time{}, err
		}
		if len(ms) > 0 && (first.IsZero() || ms[0].SentAt.Before(first)) {
			first = ms[0].SentAt
		}
	}
	if first.IsZero() {
		return first, nil
	}
	return first, h.Store.SetCursor(ctx, historyBefore, first.UnixMicro())
}

// Import puts the messages sent before the line into the content store -
// not the observation log, so nothing reacts to them - after marking their
// rooms read through the line.
func (h *History) Import(ctx context.Context, before time.Time) (int, error) {
	marked := map[string]bool{}
	put := func(m model.Message) error {
		if !marked[m.Room] {
			if err := h.Store.MarkRoom(ctx, m.Room, before); err != nil {
				return err
			}
			marked[m.Room] = true
		}
		return h.Store.PutMessage(ctx, m)
	}
	n := 0
	for _, s := range h.Messengers {
		if s.HTTP == nil {
			s.HTTP = &http.Client{Timeout: 30 * time.Second}
		}
		var cur int64
		for {
			batch, err := s.fetch(ctx, cur)
			if err != nil {
				return n, err
			}
			for _, m := range batch {
				cur = m.RowID
				sent := time.Unix(m.SentAt, 0)
				if !sent.Before(before) || !s.worth(m) {
					continue
				}
				if err := put(model.Message{
					Ref: fmt.Sprintf("%s:%d", s.Name, m.RowID), Source: s.Name,
					Room: fmt.Sprintf("%s:%d", s.Name, m.ChatID), RoomName: m.ChatName,
					Sender: m.Author, IsMine: m.IsMine, SentAt: sent, Text: m.Text,
				}); err != nil {
					return n, err
				}
				n++
			}
			if len(batch) < 500 {
				break
			}
		}
	}
	if h.Chat != nil {
		turns, err := h.Chat.turns(ctx, time.Time{})
		if err != nil {
			return n, err
		}
		for _, t := range turns {
			if msg, ok := t.message(); ok && msg.SentAt.Before(before) {
				if err := put(msg); err != nil {
					return n, err
				}
				n++
			}
		}
	}
	return n, nil
}

// ReadAll reads every room's past through, each from where it left off. A
// spent budget is waited out; an answer that will not parse is read again in
// smaller pieces, and a single message that still will not is passed over.
func (h *History) ReadAll(ctx context.Context, before time.Time) (int, error) {
	rooms, err := h.Store.Rooms(ctx)
	if err != nil {
		return 0, err
	}
	facts := 0
	for _, r := range rooms {
		if h.Noise != nil && h.Noise.MatchString(r.Name) {
			continue
		}
		key := historyRoom + r.Room
		v, err := h.Store.Cursor(ctx, key)
		if err != nil {
			return facts, err
		}
		after := time.Time{}
		if v > 0 {
			after = time.UnixMicro(v)
		}
		size, fails := h.Batch, 0
		for {
			through, n, got, err := h.Reader.ReadHistory(ctx, r.Room, r.Name, after, before, size)
			switch {
			case errors.Is(err, llm.ErrBudget):
				h.Out.Info("오늘 LLM 예산을 다 써서 지난 대화 읽기를 쉽니다", "room", r.Room, "wait", h.Wait)
				select {
				case <-ctx.Done():
					return facts, ctx.Err()
				case <-time.After(h.Wait):
				}
				continue
			case errors.Is(err, interpret.ErrUnreadable):
				if size > 1 {
					size = max(1, size/2)
					continue
				}
				if fails++; fails < quarantineHistory {
					continue
				}
				h.Out.Error("지난 대화에서 해석할 수 없는 메시지를 건너뜁니다", "room", r.Room, "at", through, "err", err)
			case err != nil:
				return facts, err
			}
			if n == 0 {
				break
			}
			facts += got
			after, size, fails = through, h.Batch, 0
			if err := h.Store.SetCursor(ctx, key, after.UnixMicro()); err != nil {
				return facts, err
			}
		}
	}
	return facts, nil
}

// turns is the operator's jarvis conversation after since (zero: all of it).
func (s *Chat) turns(ctx context.Context, since time.Time) ([]turn, error) {
	if s.HTTP == nil {
		s.HTTP = &http.Client{Timeout: 30 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(s.URL, "/")+"/turns?thread=&since="+url.QueryEscape(since.Format(time.RFC3339Nano)), nil)
	if err != nil {
		return nil, err
	}
	if s.Token != "" {
		req.Header.Set("Authorization", "Bearer "+s.Token)
	}
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("chat-svc: %d", resp.StatusCode)
	}
	var out struct {
		Turns []turn `json:"turns"`
	}
	return out.Turns, json.NewDecoder(resp.Body).Decode(&out)
}
