package mind

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/act"
	"github.com/choigonyok/jarvis/mind-svc/internal/clock"
	"github.com/choigonyok/jarvis/mind-svc/internal/llm"
	"github.com/choigonyok/jarvis/mind-svc/internal/memory"
	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/obs"
	"github.com/choigonyok/jarvis/mind-svc/internal/store"
)

// Report is what a replayed loop came to believe and want.
type Report struct {
	Observations int            `json:"observations"`
	Messages     int            `json:"messages"`
	Reads        int            `json:"reads"`
	Entities     []model.Entity `json:"entities"`
	Intents      []model.Intent `json:"intents"`
	Facts        []model.Fact   `json:"facts"`
	Errors       []string       `json:"errors,omitempty"`
}

// Replay runs a stretch of the log through a fresh loop held in memory -
// with the model given, which may be a different one - and reports what it
// came to. Time is the log's: a room is read when the next message shows it
// had gone quiet, as it would have been live. Nothing it does reaches the
// real world model, calendar or anyone's phone.
func (m *Mind) Replay(ctx context.Context, client llm.Client, after int64, limit int) (Report, error) {
	var rep Report
	clk := clock.New()
	st := store.NewMemory()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	log := obs.NewMemoryLog(quiet)
	opts := m.Opts
	opts.Store, opts.Log, opts.LLM, opts.Now = st, log, client, clk.Now
	opts.Calendar, opts.Notifier, opts.Logger, opts.Mode = act.NewMemoryCalendar(), &act.MemoryNotifier{}, quiet, "replay"
	opts.Settle = 0
	// Facts stay in the replay: a memory of its own, nothing sent to the
	// real one, no plan announced to the agent.
	opts.Memory, opts.PlanEvents = memory.NewFake(clk.Now), false
	r := New(opts)
	rctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go log.Run(rctx, r.Handle)

	last := map[string]time.Time{} // room -> its latest unread message
	others := map[string]bool{}
	idle := func(room string) time.Duration {
		if !others[room] {
			return opts.Interpret.IdleSelf
		}
		return opts.Interpret.Idle
	}
	readQuiet := func(at time.Time, all bool) {
		for room, t := range last {
			if all || !at.Before(t.Add(idle(room))) {
				if err := r.Conv.Read(rctx, room); err != nil {
					rep.Errors = append(rep.Errors, room+": "+err.Error())
				}
				rep.Reads++
				delete(last, room)
				log.Drain()
				r.Attn.Settle(rctx)
			}
		}
	}

	obsList, err := m.Opts.Log.Read(ctx, after, limit)
	if err != nil {
		return rep, err
	}
	for _, o := range obsList {
		rep.Observations++
		if o.Type != obs.MessageObserved {
			continue
		}
		msg, err := m.messageOf(ctx, o)
		if errors.Is(err, model.ErrNotFound) {
			continue
		}
		if err != nil {
			return rep, err
		}
		readQuiet(msg.SentAt, false)
		clk.Pin(msg.SentAt)
		if !msg.IsMine {
			others[msg.Room] = true
		}
		if err := Observe(rctx, st, log, msg); err != nil {
			return rep, err
		}
		log.Drain()
		last[msg.Room] = msg.SentAt
		rep.Messages++
	}
	readQuiet(clk.Now(), true)
	rep.Entities, _ = st.Entities(rctx, "", "", false)
	rep.Intents, _ = st.Intents(rctx, 1000)
	if r.Remember != nil {
		_, _ = r.Remember.Flush(rctx)
	}
	rep.Facts, _ = st.Facts(rctx, 1000)
	return rep, nil
}

func (m *Mind) messageOf(ctx context.Context, o obs.Observation) (model.Message, error) {
	var ref obs.MessageRef
	if err := jsonUnmarshal(o.Data, &ref); err != nil {
		return model.Message{}, model.ErrNotFound
	}
	return m.Opts.Store.Message(ctx, ref.Ref)
}
