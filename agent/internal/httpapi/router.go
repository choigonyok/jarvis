// Package httpapi exposes the agent to the web client: hydrate once over
// JSON, then follow along over SSE. Every stream frame comes off the one bus,
// so a proposal raised with no conversation behind it reaches the browser the
// same way a chat turn does.
package httpapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/choigonyok/jarvis/agent/internal/claudecode"
	"github.com/choigonyok/jarvis/agent/internal/core/action"
	"github.com/choigonyok/jarvis/agent/internal/core/bus"
	"github.com/choigonyok/jarvis/agent/internal/core/module"
	"github.com/choigonyok/jarvis/agent/internal/core/proposal"
	"github.com/choigonyok/jarvis/agent/internal/module/calendar"
	"github.com/choigonyok/jarvis/agent/internal/thread"
	"github.com/choigonyok/jarvis/agent/internal/uploads"
	"github.com/choigonyok/jarvis/agent/internal/usage"
	"github.com/choigonyok/jarvis/gocommon/health"
	"github.com/choigonyok/jarvis/gocommon/record"
)

type Server struct {
	consume       http.HandlerFunc
	browserChecks http.Handler
	vault         http.Handler
	reminders     http.Handler
	thread        *thread.Store
	proposals     *proposal.Store
	calendar      *calendar.Store
	runner        Sender
	bus           *bus.Bus
	mcp           map[string]http.Handler
	log           *slog.Logger
	allowedOrigin string
	// mcpToken guards /mcp: only the CLI this agent runs holds it.
	mcpToken string
	// interceptToken is the shared secret the browser container presents when
	// it stops a payment. Empty means the endpoint refuses everything.
	interceptToken string
	role           string
	// backgroundLogin is the sign-in hosts a background task may reach
	// without asking (see postIntercept).
	backgroundLogin map[string]bool
	// apiToken guards the routes the console drives. Empty leaves them open,
	// which is right when nothing but the compose network can reach this port
	// and wrong the moment a tunnel gives the agent a public hostname.
	apiToken     string
	approvalWait time.Duration
	// payments are the hosts a person approved a payment on, for a while.
	payments paymentGrants
	// holds are the stopped payment requests held while their card waits,
	// for holdDur at most (paymentHold; a test sets it to none).
	holds   paymentHolds
	holdDur time.Duration
	// savedAddress is the shipping address the vault keeps, for the payment
	// card; nil shows none.
	savedAddress func(ctx context.Context) (SavedAddress, bool)
	// followUp starts the turn that tells the assistant how a payment card
	// was decided.
	followUp interface{ Enqueue(prompt string) }
	// jobs is where a background job's payment card goes; nil refuses every
	// payment a background task reaches.
	jobs JobPayments
	// wakeJobs has the job scheduler look now (POST /jobs/wake).
	wakeJobs func()
	// checklists is the lists the conversation's cards show; nil: none.
	checklists Checklists
	// records writes what the payment path did as Records; nil writes none.
	records *record.Writer
	// loops is this agent's background loops, for GET /health.
	loops *health.Loops
	// uploads holds photos attached in conversation; nil turns them off.
	uploads   *uploads.Store
	modules   *module.Registry
	usage     *usage.Tracker
	apiUsage  func(ctx context.Context) (any, error)
	mindUsage func(ctx context.Context) (any, error)
}

// Sender is whatever drives a turn. Keeping it an interface means the
// transport does not care that a CLI subprocess sits behind it.
type Sender interface {
	Send(text string, images []string) error
}

// refSender also sends a message that quotes an earlier turn.
type refSender interface {
	SendRef(text string, images []string, ref *thread.Ref) error
}

// queuer takes a message while a turn runs instead of refusing it, and lets
// the operator steer a waiting one into that turn or drop it.
type queuer interface {
	Submit(text string, images []string, ref *thread.Ref) (queued bool, err error)
	Queue() []claudecode.Queued
	Steer(id string) (steered bool, err error)
	Drop(id string) error
}

type Deps struct {
	// Role is "owner" or "guest": which agent this is. Every response says so
	// (X-Jarvis-Agent), and the gateway refuses one from the wrong agent.
	Role      string
	Thread    *thread.Store
	Proposals *proposal.Store
	Calendar  *calendar.Store
	Runner    Sender
	Bus       *bus.Bus
	// MCP maps a mount path to its server: the permission gate at /mcp, one
	// module server per path beneath it.
	MCP map[string]http.Handler
	// MCPToken is what a caller of /mcp must present (config's
	// InternalMCPToken). Empty leaves /mcp open - tests only.
	MCPToken       string
	AllowedOrigin  string
	InterceptToken string
	// BackgroundLogin names hosts a background task may sign in through.
	BackgroundLogin []string
	APIToken        string
	ApprovalWait    time.Duration
	// Uploads, when set, accepts photos at /uploads and on messages.
	Uploads *uploads.Store
	// Modules is consulted when a decision carries edits to a card.
	Modules *module.Registry
	// Usage is the subscription's 5-hour and weekly use, served at /usage.
	Usage *usage.Tracker
	// APIUsage, when set, adds the memory model's pay-as-you-go spend to /usage.
	APIUsage func(ctx context.Context) (any, error)
	// MindUsage, when set, adds mind's model spend (by purpose) to /usage.
	MindUsage func(ctx context.Context) (any, error)
	// Consume, when set, takes events from events-svc (internal/suggest).
	Consume http.HandlerFunc
	// BrowserChecks serves the browser login checks (GET) and asks for one (POST).
	BrowserChecks http.Handler
	// Vault, when set, serves the saved logins to the console: the list and
	// its uses (GET /vault) and forgetting one (DELETE /vault/{domain}).
	Vault http.Handler
	// Reminders, when set, serves the console's list of reminders (GET
	// /reminders): the 작업 tab shows them beside the jobs.
	Reminders http.Handler
	// FollowUp tells the assistant how a payment card was decided.
	FollowUp interface{ Enqueue(prompt string) }
	// Jobs, when set, lets a background job's run that reaches a payment
	// raise a card for that job and pick it up once decided.
	Jobs JobPayments
	// SavedAddress, when set, is the shipping address the vault keeps: the
	// payment card shows it beside the one the job reported.
	SavedAddress func(ctx context.Context) (SavedAddress, bool)
	// WakeJobs, when set, is what jobs-svc's knock calls: a job changed on
	// the 작업 tab (instructed, run now, resumed) is looked at at once.
	WakeJobs func()
	// Checklists, when set, is the lists the conversation's cards show: read
	// with a page of the thread, ticked on a card.
	Checklists Checklists
	// Records, when set, writes the guard's questions and answers, payment
	// cards and how every card was answered, for a replay to read.
	Records *record.Writer
	// Loops is what GET /health reports.
	Loops *health.Loops
	Log   *slog.Logger
}

// Checklists is the lists behind the conversation's checklist cards (mind
// keeps them; the checklist module reads and ticks them and tells every
// screen of a change).
type Checklists interface {
	// Find is the lists of these ids, by id.
	Find(ctx context.Context, ids []string) (map[string]any, error)
	// Open is the lists still open (the 작업 tab shows the unfinished).
	Open(ctx context.Context) (any, error)
	// Check ticks (or unticks) one item from a card, and is the list after.
	Check(ctx context.Context, id, item string, done bool) (any, error)
	// Drop takes an item off a list (swiped away), and is the list after.
	Drop(ctx context.Context, id, item string) (any, error)
	// Close closes a list, and is the list after.
	Close(ctx context.Context, id string) (any, error)
}

func New(d Deps) *Server {
	s := &Server{
		thread:          d.Thread,
		proposals:       d.Proposals,
		calendar:        d.Calendar,
		runner:          d.Runner,
		bus:             d.Bus,
		mcp:             d.MCP,
		mcpToken:        d.MCPToken,
		log:             d.Log,
		allowedOrigin:   d.AllowedOrigin,
		interceptToken:  d.InterceptToken,
		role:            d.Role,
		backgroundLogin: hostSet(d.BackgroundLogin),
		apiToken:        d.APIToken,
		approvalWait:    d.ApprovalWait,
		uploads:         d.Uploads,
		modules:         d.Modules,
		usage:           d.Usage,
		apiUsage:        d.APIUsage,
		mindUsage:       d.MindUsage,
		consume:         d.Consume,
		browserChecks:   d.BrowserChecks,
		vault:           d.Vault,
		reminders:       d.Reminders,
		followUp:        d.FollowUp,
		jobs:            d.Jobs,
		savedAddress:    d.SavedAddress,
		holdDur:         paymentHold,
		wakeJobs:        d.WakeJobs,
		checklists:      d.Checklists,
		records:         d.Records,
		loops:           d.Loops,
	}
	if d.Proposals != nil {
		d.Proposals.OnDecide(s.resolvePayment)
		d.Proposals.OnDecide(s.recordDecision)
	}
	return s
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	// /healthz is the process; /health is the shape the status tab reads,
	// with what the loops of this agent are doing.
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		health.Write(w, http.StatusOK, health.Report{Loops: s.loops.List()})
	})

	// Everything the console drives. The bearer check is on each of them
	// rather than on the mux, so /healthz stays open for the healthcheck and
	// /intercept and /mcp keep the separate secrets they already have.
	mux.HandleFunc("GET /thread", s.guard(s.getThread))
	// The lists the thread's cards show, under /thread: the gateway passes
	// the agent's routes by first segment, and these are the thread's.
	mux.HandleFunc("GET /thread/checklists", s.guard(s.getChecklists))
	mux.HandleFunc("POST /thread/checklists/{id}/items/{item}", s.guard(s.checkItem))
	mux.HandleFunc("DELETE /thread/checklists/{id}/items/{item}", s.guard(s.dropItem))
	mux.HandleFunc("DELETE /thread/checklists/{id}", s.guard(s.closeList))
	mux.HandleFunc("POST /messages", s.guard(s.postMessage))
	// Under /messages: the gateway passes the agent's routes by first segment.
	mux.HandleFunc("POST /messages/{id}/steer", s.guard(s.steerMessage))
	mux.HandleFunc("POST /messages/{id}/drop", s.guard(s.dropMessage))
	mux.HandleFunc("POST /uploads", s.guard(s.postUpload))
	mux.HandleFunc("GET /usage", s.guard(s.getUsage))
	mux.HandleFunc("GET /uploads/{name}", s.guard(s.getUpload))
	mux.HandleFunc("GET /events", s.guard(s.events))

	mux.HandleFunc("GET /proposals", s.guard(s.getProposals))
	mux.HandleFunc("POST /proposals/{id}/decision", s.guard(s.postDecision))

	// The browser container dials this when a page tries to pay.
	mux.HandleFunc("POST /intercept", s.postIntercept)
	mux.HandleFunc("POST /intercept/records", s.postGuardRecords)

	mux.HandleFunc("GET /calendar", s.guard(s.getCalendar))
	mux.HandleFunc("POST /calendar", s.guard(s.postCalendar))
	mux.HandleFunc("DELETE /calendar/{id}", s.guard(s.deleteCalendar))

	// jobs-svc knocks here when a job changed by someone's hand may be due.
	// Inside the compose network only: the gateway passes no "jobs" route to
	// the agent.
	if s.wakeJobs != nil {
		mux.HandleFunc("POST /jobs/wake", s.guard(func(w http.ResponseWriter, r *http.Request) {
			s.wakeJobs()
			w.WriteHeader(http.StatusNoContent)
		}))
	}

	// events-svc delivers here what the assistant subscribed to.
	if s.consume != nil {
		mux.HandleFunc("POST /consume", s.guard(s.consume))
	}
	if s.browserChecks != nil {
		mux.HandleFunc("/browser-checks", s.guard(s.browserChecks.ServeHTTP))
	}
	if s.vault != nil {
		mux.HandleFunc("/vault", s.guard(s.vault.ServeHTTP))
		mux.HandleFunc("/vault/", s.guard(s.vault.ServeHTTP))
	}
	if s.reminders != nil {
		mux.HandleFunc("GET /reminders", s.guard(s.reminders.ServeHTTP))
	}

	// Claude Code dials these. The permission gate sits at /mcp; module
	// servers mount beneath it, and the more specific pattern wins.
	for path, handler := range s.mcp {
		guarded := s.guardMCP(handler)
		mux.Handle(path, guarded)
		mux.Handle(strings.TrimSuffix(path, "/")+"/", guarded)
	}
	return s.withCORS(s.identify(mux))
}

// guard requires the console's bearer token. It is a plain equality check on
// a value both sides read from the environment: there is exactly one caller,
// and anything more would be a login in front of a login.
func (s *Server) guard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.apiToken == "" {
			next(w, r)
			return
		}
		// Only the console's server-side proxy calls these, and it can set a
		// header - the browser never holds this token.
		// CutPrefix, not TrimPrefix: the scheme is required, so a header that
		// is only the raw token does not quietly count as presenting it.
		presented, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok {
			writeErr(w, http.StatusUnauthorized, "인증이 필요합니다.")
			return
		}
		if subtle.ConstantTimeCompare([]byte(presented), []byte(s.apiToken)) != 1 {
			writeErr(w, http.StatusUnauthorized, "인증이 필요합니다.")
			return
		}
		next(w, r)
	}
}

// guardMCP lets through only the CLI this agent started: it alone was handed
// the token, in its MCP config. Without this anything on the compose network
// could approve a card through the permission gate or raise a suggestion.
func (s *Server) guardMCP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.mcpToken != "" && subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+s.mcpToken)) != 1 {
			writeErr(w, http.StatusUnauthorized, "인증이 필요합니다.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// identify stamps every response with which agent answered. Two agents share
// a tunnel, and the gateway checks this before passing a response on - a
// connection that lands on the wrong one must not hand one person's
// conversation to the other.
func (s *Server) identify(next http.Handler) http.Handler {
	role := s.role
	if role == "" {
		role = "owner"
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Jarvis-Agent", role)
		next.ServeHTTP(w, r)
	})
}

// withCORS stays closed unless an origin is configured; the default topology
// puts the Next.js server in front, so the browser never talks here directly.
func (s *Server) withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.allowedOrigin != "" {
			w.Header().Set("Access-Control-Allow-Origin", s.allowedOrigin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) getThread(w http.ResponseWriter, r *http.Request) {
	var turns []*thread.Turn
	var thinking, hasMore bool
	// ?limit= is the console's page (the newest turns; with before=, the
	// ones above a time). Without it, the whole thread, as before.
	if limit, _ := strconv.Atoi(r.URL.Query().Get("limit")); limit > 0 {
		var err error
		turns, hasMore, err = s.thread.Window(r.URL.Query().Get("before"), min(limit, 500))
		if err != nil {
			// Said as a failure, not handed over as an empty page with no more
			// above it - the console would stop asking.
			writeErr(w, http.StatusBadGateway, "이전 대화를 불러오지 못했습니다: "+err.Error())
			return
		}
		_, thinking = s.thread.Snapshot()
	} else {
		turns, thinking = s.thread.Snapshot()
	}
	if turns == nil {
		turns = []*thread.Turn{}
	}
	// Proposals ride along: a client hydrating the thread needs the cards
	// those turns point at, and one round trip is one less way to be stale.
	body := map[string]any{
		"turns":     turns,
		"thinking":  thinking,
		"hasMore":   hasMore,
		"proposals": s.proposals.List(),
	}
	// So do the lists their checklist cards show, as they stand now. A
	// thread is not held up for them: without mind the cards say so.
	if lists := s.listsOf(r.Context(), turns); lists != nil {
		body["checklists"] = lists
	}
	if q, ok := s.runner.(queuer); ok {
		body["queue"] = q.Queue()
	}
	writeJSON(w, http.StatusOK, body)
}

// listsOf is the lists the turns' cards show, by id; nil when there are none
// or they could not be read.
func (s *Server) listsOf(ctx context.Context, turns []*thread.Turn) map[string]any {
	if s.checklists == nil {
		return nil
	}
	var ids []string
	seen := map[string]bool{}
	for _, t := range turns {
		if t.Origin != nil && t.Origin.Checklist != "" && !seen[t.Origin.Checklist] {
			seen[t.Origin.Checklist] = true
			ids = append(ids, t.Origin.Checklist)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	lists, err := s.checklists.Find(ctx, ids)
	if err != nil {
		if s.log != nil {
			s.log.Warn("대화 카드의 체크리스트를 읽지 못했습니다", "err", err)
		}
		return nil
	}
	return lists
}

// getChecklists is GET /thread/checklists?ids=a,b: the lists cards show -
// and with no ids, the lists still open (the 작업 tab).
func (s *Server) getChecklists(w http.ResponseWriter, r *http.Request) {
	if s.checklists == nil {
		writeErr(w, http.StatusNotFound, "체크리스트가 없습니다.")
		return
	}
	if strings.TrimSpace(r.URL.Query().Get("ids")) == "" {
		open, err := s.checklists.Open(r.Context())
		if err != nil {
			writeErr(w, http.StatusBadGateway, "체크리스트를 읽지 못했습니다: "+err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"open": open})
		return
	}
	var ids []string
	for _, id := range strings.Split(r.URL.Query().Get("ids"), ",") {
		if id = strings.TrimSpace(id); id != "" && len(ids) < 100 {
			ids = append(ids, id)
		}
	}
	lists, err := s.checklists.Find(r.Context(), ids)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "체크리스트를 읽지 못했습니다: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"checklists": lists})
}

// checkItem is a tap on a checklist card's item: POST {done}.
func (s *Server) checkItem(w http.ResponseWriter, r *http.Request) {
	if s.checklists == nil {
		writeErr(w, http.StatusNotFound, "체크리스트가 없습니다.")
		return
	}
	var body struct {
		Done bool `json:"done"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "본문을 읽을 수 없습니다.")
		return
	}
	list, err := s.checklists.Check(r.Context(), r.PathValue("id"), r.PathValue("item"), body.Done)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "체크하지 못했습니다: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"checklist": list})
}

// dropItem is an item swiped off a card as not needed.
func (s *Server) dropItem(w http.ResponseWriter, r *http.Request) {
	if s.checklists == nil {
		writeErr(w, http.StatusNotFound, "체크리스트가 없습니다.")
		return
	}
	list, err := s.checklists.Drop(r.Context(), r.PathValue("id"), r.PathValue("item"))
	if err != nil {
		writeErr(w, http.StatusBadGateway, "빼지 못했습니다: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"checklist": list})
}

// closeList closes a list from the console.
func (s *Server) closeList(w http.ResponseWriter, r *http.Request) {
	if s.checklists == nil {
		writeErr(w, http.StatusNotFound, "체크리스트가 없습니다.")
		return
	}
	list, err := s.checklists.Close(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadGateway, "닫지 못했습니다: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"checklist": list})
}

func (s *Server) steerMessage(w http.ResponseWriter, r *http.Request) {
	q, ok := s.runner.(queuer)
	if !ok {
		writeErr(w, http.StatusNotFound, "대기열이 없습니다.")
		return
	}
	steered, err := q.Steer(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"steered": steered})
}

func (s *Server) dropMessage(w http.ResponseWriter, r *http.Request) {
	q, ok := s.runner.(queuer)
	if !ok {
		writeErr(w, http.StatusNotFound, "대기열이 없습니다.")
		return
	}
	if err := q.Drop(r.PathValue("id")); err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) postMessage(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text   string   `json:"text"`
		Images []string `json:"images"`
		// Ref names an earlier turn the operator swiped to quote.
		Ref *struct {
			ID string `json:"id"`
		} `json:"ref"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "본문을 읽을 수 없습니다.")
		return
	}
	text := strings.TrimSpace(body.Text)
	if text == "" && len(body.Images) == 0 {
		writeErr(w, http.StatusBadRequest, "메시지가 비어 있습니다.")
		return
	}
	if len(body.Images) > 0 {
		if s.uploads == nil {
			writeErr(w, http.StatusBadRequest, "이 대화에서는 사진을 보낼 수 없습니다.")
			return
		}
		if len(body.Images) > 12 {
			writeErr(w, http.StatusBadRequest, "사진은 한 번에 12장까지입니다.")
			return
		}
		for _, name := range body.Images {
			if _, err := s.uploads.Path(name); err != nil {
				writeErr(w, http.StatusBadRequest, "올린 사진을 찾을 수 없습니다. 다시 첨부해 주세요.")
				return
			}
		}
	}
	var ref *thread.Ref
	if body.Ref != nil && body.Ref.ID != "" && s.thread != nil {
		quoted := s.thread.Find(body.Ref.ID)
		if quoted == nil {
			writeErr(w, http.StatusBadRequest, "참조한 메시지를 찾을 수 없습니다.")
			return
		}
		ref = thread.RefTo(quoted)
		// A checklist card (or a notice showing one) says which list it is,
		// so "이거 다 챙겼어" can be ticked on the right one.
		if o := quoted.Origin; o != nil && o.Checklist != "" {
			name := ""
			if o.Kind == thread.OriginChecklist && o.Title != "" {
				name = " · " + o.Title
			}
			ref.Text = strings.TrimSpace(ref.Text + "\n[체크리스트 카드 · " + o.Checklist + name + "]")
		}
		// A turn that is only a card says nothing itself; its card does.
		if ref.Text == "" && quoted.ProposalID != "" {
			if p, ok := s.proposals.Get(quoted.ProposalID); ok {
				ref.Text = strings.TrimSpace("[승인 카드 · " + string(p.State) + "] " + p.Card.Title + "\n" + p.Card.Body)
				if r := []rune(ref.Text); len(r) > 600 {
					ref.Text = string(r[:600]) + "…"
				}
			}
		}
	}
	var err error
	if q, ok := s.runner.(queuer); ok {
		var queued bool
		if queued, err = q.Submit(text, body.Images, ref); err == nil {
			writeJSON(w, http.StatusAccepted, map[string]bool{"queued": queued})
			return
		}
	} else if rs, ok := s.runner.(refSender); ok && ref != nil {
		err = rs.SendRef(text, body.Images, ref)
	} else {
		err = s.runner.Send(text, body.Images)
	}
	if err != nil {
		if errors.Is(err, claudecode.ErrBusy) {
			writeErr(w, http.StatusConflict, err.Error())
			return
		}
		writeErr(w, http.StatusInternalServerError, "요청을 시작하지 못했습니다.")
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) getProposals(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"proposals": s.proposals.List()})
}

func (s *Server) postDecision(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Decision string `json:"decision"`
		// Edits are the card's form fields as the operator left them.
		Edits map[string]string `json:"edits"`
		// Reason is why, when the operator says (a rejection, mostly).
		Reason string `json:"reason"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "본문을 읽을 수 없습니다.")
		return
	}
	id := r.PathValue("id")
	if proposal.Decision(body.Decision) == proposal.Approve {
		// Private fields first, and out of the edits before they are kept.
		if msg, code := s.acceptSecrets(r.Context(), id, body.Edits); msg != "" {
			writeErr(w, code, msg)
			return
		}
		if msg, code := s.revise(r.Context(), id, body.Edits); msg != "" {
			writeErr(w, code, msg)
			return
		}
	}
	err := s.proposals.DecideWhy(id, proposal.Decision(body.Decision), body.Reason)
	switch {
	case errors.Is(err, proposal.ErrNotFound):
		writeErr(w, http.StatusNotFound, "그런 요청이 없습니다.")
	case errors.Is(err, proposal.ErrAlreadyDecided):
		writeErr(w, http.StatusConflict, "이미 결재된 요청입니다.")
	case err != nil:
		writeErr(w, http.StatusBadRequest, err.Error())
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// acceptSecrets hands a card's private fields (a password) to its module and
// deletes them from edits, so what the card records - and what the model is
// told - never holds them. A card with private fields but no module to take
// them is refused rather than approved without them.
func (s *Server) acceptSecrets(ctx context.Context, id string, edits map[string]string) (string, int) {
	p, ok := s.proposals.Get(id)
	if !ok || p.State != proposal.Pending {
		return "", 0
	}
	var private []action.Field
	for _, f := range p.Card.Fields {
		if f.IsPrivate() {
			private = append(private, f)
		}
	}
	if len(private) == 0 {
		return "", 0
	}
	secrets := map[string]string{}
	for _, f := range private {
		secrets[f.Key] = edits[f.Key]
		delete(edits, f.Key)
	}
	for _, f := range private {
		if f.Required && strings.TrimSpace(secrets[f.Key]) == "" {
			return f.Label + " 칸을 채워 주세요.", http.StatusBadRequest
		}
	}
	var sink module.SecretSink
	if s.modules != nil {
		if actuator, _, found := s.modules.Lookup(p.Action.Kind); found {
			sink, _ = actuator.(module.SecretSink)
		}
	}
	if sink == nil {
		return "이 카드의 비밀 칸을 받을 곳이 없습니다.", http.StatusBadRequest
	}
	if err := sink.AcceptSecrets(ctx, p.Action, secrets); err != nil {
		return err.Error(), http.StatusBadRequest
	}
	return "", 0
}

// revise runs an approval of a form card through its module first: the
// operator's edits become the action that runs, and a value the module will
// not accept (an empty price) stops the approval with a reason.
func (s *Server) revise(ctx context.Context, id string, edits map[string]string) (string, int) {
	p, ok := s.proposals.Get(id)
	if !ok || p.State != proposal.Pending || len(p.Card.Fields) == 0 || s.modules == nil {
		if len(edits) > 0 && ok && len(p.Card.Fields) == 0 {
			return "이 카드는 고칠 수 없습니다.", http.StatusBadRequest
		}
		return "", 0
	}
	actuator, _, found := s.modules.Lookup(p.Action.Kind)
	editor, editable := actuator.(module.Editor)
	if !found || !editable {
		return "", 0
	}
	act, card, err := editor.Revise(ctx, p.Action, edits)
	if err != nil {
		return err.Error(), http.StatusBadRequest
	}
	if err := s.proposals.ReviseWith(id, act, card, edits); err != nil {
		return "이미 결재된 요청입니다.", http.StatusConflict
	}
	return "", 0
}

func (s *Server) getUsage(w http.ResponseWriter, r *http.Request) {
	out := struct {
		usage.Usage
		API  any `json:"api,omitempty"`
		Mind any `json:"mind,omitempty"`
	}{}
	if s.usage != nil {
		out.Usage = s.usage.Snapshot(time.Now())
	}
	if s.apiUsage != nil {
		// Memory down only drops the API panel; the subscription bars still show.
		if api, err := s.apiUsage(r.Context()); err == nil {
			out.API = api
		}
	}
	if s.mindUsage != nil {
		if mu, err := s.mindUsage(r.Context()); err == nil {
			out.Mind = mu
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}

// postUpload stores one photo, sent as the raw request body.
func (s *Server) postUpload(w http.ResponseWriter, r *http.Request) {
	if s.uploads == nil {
		writeErr(w, http.StatusNotFound, "사진을 받지 않습니다.")
		return
	}
	name, err := s.uploads.Save(http.MaxBytesReader(w, r.Body, uploads.MaxBytes+1))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"name": name})
}

func (s *Server) getUpload(w http.ResponseWriter, r *http.Request) {
	if s.uploads == nil {
		writeErr(w, http.StatusNotFound, "사진을 받지 않습니다.")
		return
	}
	path, err := s.uploads.Path(r.PathValue("name"))
	if err != nil {
		// Deleted 30 days after a sale; the console shows a placeholder.
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	// The name is random and the content never changes under it.
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeFile(w, r, path)
}

func (s *Server) getCalendar(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	writeJSON(w, http.StatusOK, map[string]any{
		"events": s.calendar.Range(q.Get("from"), q.Get("to")),
	})
}

// postCalendar is the operator editing an existing entry by hand, which needs
// no card. A new entry does: the calendar is shared with a partner since it
// moved to uniple, and the operator asked that every addition be approved.
// New entries come in through the calendar tools and their approval card.
func (s *Server) postCalendar(w http.ResponseWriter, r *http.Request) {
	var in calendar.Event
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "본문을 읽을 수 없습니다.")
		return
	}
	if in.ID == "" {
		writeErr(w, http.StatusForbidden, "새 일정은 대화에서 요청해 승인 카드를 거쳐 추가합니다.")
		return
	}
	// An edit keeps whoever created it.
	if existing, ok := s.calendar.Get(in.ID); ok {
		in.Source = existing.Source
	}

	saved, err := s.calendar.Put(in)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, saved)
}

func (s *Server) deleteCalendar(w http.ResponseWriter, r *http.Request) {
	if _, err := s.calendar.Delete(r.PathValue("id")); err != nil {
		if errors.Is(err, calendar.ErrNotFound) {
			writeErr(w, http.StatusNotFound, err.Error())
			return
		}
		writeErr(w, http.StatusInternalServerError, "삭제하지 못했습니다.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "스트리밍을 지원하지 않습니다.")
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache, no-transform")
	h.Set("Connection", "keep-alive")
	// Defeats proxy buffering, which otherwise holds frames until the stream ends.
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	events, unsubscribe := s.bus.Subscribe()
	defer unsubscribe()

	fmt.Fprint(w, ": connected\n\n")
	// An answer being written when the client (re)connects is shown whole
	// at once; its next pieces follow on the stream, which was subscribed to
	// first so none fall in between.
	if s.thread != nil {
		if d := s.thread.CurrentDraft(); d != nil {
			if payload, err := json.Marshal(bus.Event{Type: "draft", Draft: d}); err == nil {
				fmt.Fprintf(w, "data: %s\n\n", payload)
			}
		}
	}
	flusher.Flush()

	keepalive := time.NewTicker(25 * time.Second)
	defer keepalive.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-keepalive.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case ev, ok := <-events:
			if !ok {
				return
			}
			payload, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "data: %s\n\n", payload)
			flusher.Flush()
		}
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func hostSet(hosts []string) map[string]bool {
	out := map[string]bool{}
	for _, h := range hosts {
		if h = strings.ToLower(strings.TrimSpace(h)); h != "" {
			out[h] = true
		}
	}
	return out
}
