// Package holder is what the agent is holding for an answer: a stopped payment
// waiting on its card, and what an approval let through. It is kept here, not in
// mind, on purpose. A person's answer to a card somebody is waiting on is passed
// on, not judged: the payment goes on whether or not mind is up, and mind only
// closes its ledger.
//
// None of it survives a restart, and that is the safe way round: an approval
// lost is one more card, and a stopped request has gone with the process.
package holder

import (
	"context"
	"strings"
	"sync"
	"time"
)

// One approval is one payment - for whoever raised the card (the conversation,
// or one job) on one site - and these are its bounds, the same for every payer:
const (
	// Window is how long the approved payment stays open, counted from the
	// payer's first request after the approval: the payer is back at the
	// payment. A job may start a while after its card was answered (it waits
	// for the conversation to be idle), so the clock starts when it is back,
	// not at the answer.
	Window = 10 * time.Minute
	// Wait is how long an approval waits for its payer to come back.
	Wait = time.Hour
	// Hold is how long a stopped payment request waits for its card before it
	// is refused: long enough for an operator who has the phone in hand, short
	// enough that a page and a run are not left hanging. The guard waits for
	// the answer a little longer than this (JARVIS_DECISION_TIMEOUT).
	Hold = 2 * time.Minute
)

// commits says whether a request the guard stopped is the one that charges:
// Coupang's 결제하기 (the order sheet's "order" event). An approval lets one
// through; a second is a second payment, and gets a card of its own. What comes
// after it in the same payment - the PIN pad - goes through in the window.
func commits(event string) bool { return event == "order" }

// Payments is the payments a person approved and the requests held for one.
// The zero value is ready.
type Payments struct {
	mu    sync.Mutex
	sites map[key]*grant
	held  map[string]*waiters
	// Now is the clock; a test or a replay sets it.
	Now func() time.Time
}

// key is for whom an approval is: a job, or the conversation as job 0, on which
// site. One's approval is not another's: a job's card does not open the site to
// the conversation.
type key struct {
	site string
	job  int64
}

type grant struct {
	approved time.Time
	opened   time.Time // the payer's first request after approval; zero until then
	charged  bool      // the one 결제하기 has gone through
	// amount is what the approved card showed a payment window was handed. A
	// window has no 결제하기 to count: each order opens it anew, so the approval
	// is held to that amount - a window opened for another is another payment.
	// Empty when the guard could not read it. A mall's own payment carries no
	// amount of its own; its approval holds the amount its card said, which a
	// window it opens must match (Carry).
	amount string
	// last is when a request last went through on this approval: a payment
	// window that follows it within CarryGap is that payment handed to its PG.
	last time.Time
}

// CarryGap is how soon after a mall's payment request went through a payment
// window may open on its approval: a 결제하기 hands the order to its PG at
// once (under a second, 무신사 → 토스페이먼츠·헥토), and a window that opens
// later is another step - or another payment - with its own card.
const CarryGap = time.Minute

func (p *Payments) clock() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

// Grant holds an approval.
func (p *Payments) Grant(site string, job int64, amount string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.sites == nil {
		p.sites = map[key]*grant{}
	}
	p.sites[key{strings.ToLower(site), job}] = &grant{approved: p.clock(), amount: amount}
}

// Allow says whether a stopped request of this payer's on this site is part of
// the payment approved for it. amount is what the request hands a payment
// window, if the guard read one.
func (p *Payments) Allow(site string, job int64, event, amount string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	k := key{strings.ToLower(site), job}
	g := p.sites[k]
	if g == nil {
		return false
	}
	if amount != "" && g.amount != "" && amount != g.amount {
		return false
	}
	now := p.clock()
	if g.opened.IsZero() {
		if now.After(g.approved.Add(Wait)) {
			delete(p.sites, k)
			return false
		}
		g.opened = now
	}
	if now.After(g.opened.Add(Window)) {
		delete(p.sites, k)
		return false
	}
	if commits(event) {
		if g.charged {
			return false
		}
		g.charged = true
	}
	g.last = now
	return true
}

// Carry opens the payment window for this payer under the approved payment it
// is making at another site: a mall's 결제하기 hands that same payment to its
// PG (무신사 → 토스페이먼츠·헥토), and the card the operator approved said what
// it was and how much. Only right after that payment's request went through
// (CarryGap), and - when the approval and the window both know an amount -
// only at that amount. The window is then held as approved and under way, so
// what passes inside it (the card company's window, the 네이버페이 picked
// there) is that same payment. It says the site whose approval carried it.
func (p *Payments) Carry(window string, job int64, amount string) (string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	window = strings.ToLower(window)
	now := p.clock()
	from, latest := "", time.Time{}
	var g *grant
	for k, v := range p.sites {
		if k.job != job || k.site == window || v.opened.IsZero() || now.After(v.opened.Add(Window)) {
			continue
		}
		if v.last.IsZero() || now.Sub(v.last) > CarryGap {
			continue
		}
		if g == nil || v.last.After(latest) {
			from, latest, g = k.site, v.last, v
		}
	}
	if g == nil || (amount != "" && g.amount != "" && amount != g.amount) {
		return "", false
	}
	if amount == "" {
		amount = g.amount
	}
	p.sites[key{window, job}] = &grant{approved: now, opened: now, amount: amount, last: now}
	return from, true
}

// LiveOn is the payers with an approved payment on site that can still happen:
// approved and not yet come back to, or under way.
func (p *Payments) LiveOn(site string) []int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	site = strings.ToLower(site)
	now := p.clock()
	var out []int64
	for k, g := range p.sites {
		if k.site != site {
			continue
		}
		if (g.opened.IsZero() && !now.After(g.approved.Add(Wait))) || (!g.opened.IsZero() && !now.After(g.opened.Add(Window))) {
			out = append(out, k.job)
		}
	}
	return out
}

// Live says whether this payer has an approved payment that can still happen,
// on any site: approved and not yet come back to, or under way.
func (p *Payments) Live(job int64) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.clock()
	for k, g := range p.sites {
		if k.job != job {
			continue
		}
		if g.opened.IsZero() {
			if !now.After(g.approved.Add(Wait)) {
				return true
			}
		} else if !now.After(g.opened.Add(Window)) {
			return true
		}
	}
	return false
}

// UnderWay is the site of this payer's approved payment that is under way -
// the payer came back to it and its window has not run out - the latest when
// there are two. While one is, a page of the payer's going somewhere new is
// most likely that payment on its way back: a PG's result, the mall's own
// payment company finishing the order.
func (p *Payments) UnderWay(job int64) (string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.clock()
	site, latest := "", time.Time{}
	for k, g := range p.sites {
		if k.job != job || g.opened.IsZero() || now.After(g.opened.Add(Window)) {
			continue
		}
		if site == "" || g.opened.After(latest) {
			site, latest = k.site, g.opened
		}
	}
	return site, site != ""
}

// IsOpen says whether this payer's approved payment on site is under way: the
// payer came back to it and its window has not run out. It opens nothing itself.
func (p *Payments) IsOpen(site string, job int64) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	g := p.sites[key{strings.ToLower(site), job}]
	return g != nil && !g.opened.IsZero() && !p.clock().After(g.opened.Add(Window))
}

// waiters are the stopped requests held while one card waits: one payment can
// stop more than one request (two PG hosts).
type waiters struct {
	done     chan struct{}
	waiting  int
	released bool
	passes   bool
}

func (p *Payments) join(card string) *waiters {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.held == nil {
		p.held = map[string]*waiters{}
	}
	w := p.held[card]
	if w == nil {
		w = &waiters{done: make(chan struct{})}
		p.held[card] = w
	}
	w.waiting++
	return w
}

// leave is a held request done waiting, and whether it goes on: it does if the
// card was decided while it waited and the decision lets it.
func (p *Payments) leave(card string, w *waiters) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	w.waiting--
	if w.waiting <= 0 && p.held[card] == w {
		delete(p.held, card)
	}
	return w.released && w.passes
}

// Wait holds a stopped request until its card is answered or until, and says
// whether it goes on.
func (p *Payments) Wait(ctx context.Context, card string, until time.Time) bool {
	w := p.join(card)
	t := time.NewTimer(time.Until(until))
	defer t.Stop()
	select {
	case <-w.done:
	case <-t.C:
	case <-ctx.Done():
	}
	return p.leave(card, w)
}

// Release hands a card's answer to the requests held on it, and says whether
// there were any: if not, the payer takes the step again.
func (p *Payments) Release(card string, passes bool) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	w := p.held[card]
	if w == nil || w.released {
		return false
	}
	w.released, w.passes = true, passes
	delete(p.held, card)
	close(w.done)
	return true
}

// Holding is how many stopped requests are held now.
func (p *Payments) Holding() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for _, w := range p.held {
		n += w.waiting
	}
	return n
}
