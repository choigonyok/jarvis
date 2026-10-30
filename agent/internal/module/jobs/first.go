package jobs

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/choigonyok/jarvis/agent/internal/core/action"
	"github.com/choigonyok/jarvis/agent/internal/core/proposal"
)

// The browser is one window, and its jobs go in the order they were asked
// for: mind's line (next_at). A job the operator asks for while another
// job's run has the browser is put in that line, and a card asks whether it
// should go first instead (2026-10-28: "브라우저 작업은 요청 순대로 처리하고
// 큐에 넣은 뒤에 먼저 인터셉트하고 처리할지 묻는 카드"). Approved, the job goes
// to the front (mind's "first") and the run on the browser stops; that
// run's job goes on right after it, in its own place (outcome "deferred").

// askedFor is whether the operator is waiting on a job right now: one they
// made, instructed, ran or restarted - not a job's own clock, nor the run
// after a card's answer (they have just been asked something already).
func askedFor(j Job) bool {
	if j.LastRunAt == nil {
		return true
	}
	switch j.NextReason {
	case "첫 실행", "새 지시", "지금 실행", "다시 시작", "끝내는 중에 온 지시":
		return true
	}
	return false
}

// offerFirst raises a job's 먼저 card, once per wait: the job was asked for
// and waits for the browser behind holder's run.
func (m *Module) offerFirst(j, holder Job) {
	if m.noBrowser || !askedFor(j) {
		return
	}
	m.offerMu.Lock()
	if _, asked := m.offered[j.ID]; asked {
		m.offerMu.Unlock()
		return
	}
	if m.offered == nil {
		m.offered = map[int64]string{}
	}
	m.offered[j.ID] = "" // taken, while the card is opened
	m.offerMu.Unlock()

	in := FirstInput{JobID: j.ID, JobTitle: j.Title, Holder: holder.Title}
	raw, _ := json.Marshal(in)
	p, _ := m.proposals.Open(proposal.Proposal{
		Origin: proposal.OriginNotice,
		Action: action.Action{Kind: KindFirst, Input: raw},
		Card:   firstCard(in),
	})
	m.offerMu.Lock()
	m.offered[j.ID] = p.ID
	m.offerMu.Unlock()
	m.attach(p.ID)
	m.log.Info("브라우저를 기다리는 작업을 먼저 할지 묻습니다", "job", j.ID, "holder", holder.ID, "card", p.ID)
}

// closeOffer forgets a job's 먼저 card - its turn came, or it left the line -
// and takes the card down if it is still up.
func (m *Module) closeOffer(job int64, why string) {
	m.offerMu.Lock()
	id, asked := m.offered[job]
	delete(m.offered, job)
	m.offerMu.Unlock()
	if !asked || id == "" {
		return
	}
	if p, ok := m.proposals.Get(id); ok && p.State == proposal.Pending {
		m.proposals.Abandon(id, why)
	}
}

// dropOffers closes the 먼저 cards of jobs no longer in the line: due is all
// of it, and a job with a run in flight here is in its turn.
func (m *Module) dropOffers(due []Job) {
	inLine := map[int64]bool{}
	for _, j := range due {
		inLine[j.ID] = true
	}
	m.offerMu.Lock()
	var gone []int64
	for job := range m.offered {
		if !inLine[job] {
			gone = append(gone, job)
		}
	}
	m.offerMu.Unlock()
	for _, job := range gone {
		if m.inFlight(job) {
			continue
		}
		m.closeOffer(job, "작업이 더 기다리지 않아 내렸습니다.")
	}
}

// inFlight is whether a run of the job is in flight here.
func (m *Module) inFlight(job int64) bool {
	m.activeMu.Lock()
	defer m.activeMu.Unlock()
	_, busy := m.active[job]
	return busy
}

// putFirst carries out a 먼저 card's decision.
func (m *Module) putFirst(ctx context.Context, p proposal.Proposal, in FirstInput, d proposal.Decision) {
	if d != proposal.Approve {
		m.proposals.Settle(p.ID, proposal.Rejected, "요청한 순서대로 합니다.")
		return
	}
	if err := m.store.First(ctx, in.JobID); err != nil {
		// Its turn came meanwhile, or it was called off: nothing to put first.
		m.proposals.Settle(p.ID, proposal.Failed, "먼저 하지 못했습니다: "+err.Error())
		return
	}
	note := "이 작업을 먼저 합니다."
	if run, holder := m.current.now(); run != 0 && holder.ID != in.JobID && m.bg != nil {
		m.current.putBehind(run, in.JobTitle)
		if m.bg.YieldBrowser(fmt.Sprintf("운영자가 '%s' 작업을 먼저 하기로 했습니다", in.JobTitle)) {
			note = fmt.Sprintf("'%s' 작업을 잠시 멈추고 이 작업을 먼저 합니다. 끝나면 멈춘 작업을 이어서 합니다.", holder.Title)
		}
	}
	m.proposals.Settle(p.ID, proposal.Executed, note)
	m.log.Info("작업을 먼저 하기로 했습니다", "job", in.JobID, "card", p.ID)
	m.wake()
}
