package model

import "strings"

// The kinds the loop's layers tell apart by name, each spelled here once, in
// three vocabularies:
//
//   - An intent's kind (Intent.Kind) is what it asks to be done or said, as
//     the registry's action rows name it: the gate does nothing for an intent
//     without its row, but a notice (Info) needs none, as it does nothing.
//   - An occurrence's kind (Occurrence.Kind) is what a structured source says
//     happened (sensor).
//   - A record's kind (queue.Message.Kind) is what a record on the stream is,
//     as the registry's kind rows name it, which say the lane that reads it.
//
// Intent and occurrence kinds go by their own names - CartAdd, PurchaseItem -
// and the agent's cards and answers that mind keeps or tells apart by Held…
// and Mind…; a record's kind is Kind…, as an entity's are (KindPlan,
// KindLoop). A test holds every one to its wire value, and the registry's
// tests hold its rows to them: an intent kind mind raises to its action row,
// a record kind the code reads to the lane that reads it.
//
// The predictor's keys are made from an occurrence's kind (predict.Keys:
// purchase, purchase/식비, purchase/식비@가게 - and predict.Delivery besides),
// and so are a prep's key (purchase.item:<class>) and the workout predictor's
// target (workout:<day>). A word spelled like a kind in another vocabulary is
// that vocabulary's: an occurrence's ID and ref (photo:<asset>), a rule's kind
// and features (predict.Daily, predict.Features), a goal's (goals.ByWorkout),
// the calendar's room (sensor.CalendarRoom).
//
// Kinds another service defines stay its own: what a collector hands on
// (gocommon/collect), the writes and commands the stream carries for mind
// (gocommon/record - the registry's rows route them, and the door takes each
// by its path), a check's result, which mind reports (gocommon/checks). Two
// of the stream's words that mind's own loop comes back on are named here as
// well, locked to the deterministic lane, and a test holds them to
// gocommon's: KindManualEdit and KindIntentAnswer. The household book's
// notices (spending.*, which its watch raises for the inbox) and its marks in
// mind.latest (spending.message, spending.alert) are the book's own
// (internal/spending): nothing else in mind names them.

// Intent kinds.
const (
	// Info is a notice: it tells, and changes nothing.
	Info = "info"
	// The calendar's changes, as mind asks them; the assistant's own cards
	// for them in the conversation are Held… below.
	CalendarCreate = "calendar.create"
	CalendarUpdate = "calendar.update"
	CalendarDelete = "calendar.delete"
	// CartAdd puts a thing in the shop's cart and stops there: a job does it,
	// and the operator orders by hand (act.CartActuator).
	CartAdd = "cart.add"
	// RemindSet keeps a reminder, at the time and in the words the operator
	// left on the card offered for a promise of their own (skill.Loops).
	RemindSet = "remind.set"
	// MessageDraft is a message written for the operator to send: nothing is
	// sent (act.DraftActuator).
	MessageDraft = "message.draft"
	// SettleRequest drafts what each person owes for a shared bill, and keeps
	// a loop for each until it is paid (act.SettleActuator).
	SettleRequest = "settle.request"
	// ReviewPlace asks how a place, a trip or a week went, and
	// PreferenceConfirm whether a taste read from such answers holds
	// (act.ReviewActuator).
	ReviewPlace       = "review.place"
	PreferenceConfirm = "preference.confirm"
	// AssetsRebalance sends the orders a rebalance card showed, those and no
	// others (act.RebalanceActuator).
	AssetsRebalance = "assets.rebalance"
	// PhotosAlbum makes an album of a plan's photos (act.PhotosActuator), and
	// PhotosPlace asks which plan photos with none of their own were of
	// (act.PhotoPlaceActuator) - the context its answers are counted under
	// too.
	PhotosAlbum = "photos.album"
	PhotosPlace = "photos.place"
	// SpendingCategory files a charge under a category in the household book
	// (act.Spending).
	SpendingCategory = "spending.category"
	// NoteOnly is a note with nothing to run: a plan for the operator to carry
	// out (rebalance this much, order that), which approving records as seen
	// and agreed (act.Note). The engine's own notes are the type Note.
	NoteOnly = "note"
)

// The assistant's own cards, as their held rows in mind's ledger have them:
// a new entry and a move (the agent's calendar.KindCreate,
// calendar.KindUpdate), and a payment card (KindPaymentRequested).
const (
	HeldCalendarCreate = "calendar.create_event"
	HeldCalendarUpdate = "calendar.update_event"
	HeldPayment        = "payment"
)

// The agent's actions that answer a card of mind's own (the agent's
// mind.Approve, mind.Undo): their answers come as commands for mind to carry
// out, and are no card of the agent's to hold.
const (
	MindApprove = "mind.approve"
	MindUndo    = "mind.undo"
)

// IsCalendar is whether an intent of kind changes the calendar: one of mind's
// calendar.* or a card of the conversation's for one.
func IsCalendar(kind string) bool { return strings.HasPrefix(kind, "calendar.") }

// IsCalendarUpdate is whether an intent of kind moves an entry already there:
// mind's calendar.update or the conversation's card for a move.
func IsCalendarUpdate(kind string) bool { return strings.HasPrefix(kind, CalendarUpdate) }

// Occurrence kinds.
const (
	// Purchase is a card payment, and PurchaseItem a product in one: an
	// order's lines, as the household book splits them.
	Purchase     = "purchase"
	PurchaseItem = "purchase.item"
	// Visit is a stay in one of the phone's regions (context-svc).
	Visit = "visit"
	// Workout is a session in the training log.
	Workout = "workout"
	// Photo is a photo the camera took (Immich).
	Photo = "photo"
	// Calendar is an entry on the calendar: what other things follow, never
	// something to predict.
	Calendar = "calendar"
	// Transfer is money received, as a payment app's room says it.
	Transfer = "transfer"
	// Asset is a holding, money in or out of the investment accounts, or a
	// sale; its Class says which.
	Asset = "asset"
	// Job is a job handed to the assistant: its own errand, not the
	// operator's life.
	Job = "job"
	// Deposit and Bill come from no source; the predictor reads them as money
	// all the same, so a source that brings them takes part as it is.
	Deposit = "deposit"
	Bill    = "bill"
)

// Record kinds. The latest record of a kind is kept under the kind itself
// (store.PutLatest), so the adapters' records are read back by these names
// too.

// What the adapters leave in the stream. mind does not go to the calendar, the
// phones or the accounts itself: each adapter reads its own outside and leaves
// a record when what it read changed (gocommon/watch), and the structured lane
// takes them by rule, no model (sensor).
const (
	KindCalendarNear   = "calendar.near"
	KindCalendarYear   = "calendar.year"
	KindContextVisits  = "context.visits"
	KindAssetsHoldings = "assets.holdings"
	// Those only kept, for whoever asks: where the phones are, the regions,
	// the ways taken.
	KindContextNow      = "context.now"
	KindContextNowGuest = "context.now.guest"
	KindContextRegions  = "context.regions"
	KindContextTravel   = "context.travel"
	// KindWeatherForecast is what is forecast at home and where the plans
	// are; each place's forecast is also kept on its own (ForecastKey).
	KindWeatherForecast = "weather.forecast"
	// The last two days' photos, and the days that are over.
	KindPhotosRecent = "photos.recent"
	KindPhotosDays   = "photos.days"
	// KindAssetsFX is the dollar's rate, only kept: a card alert in dollars is
	// estimated in won with it.
	KindAssetsFX = "assets.fx"
	// KindAssetsOutflows is what left the investment accounts and what became
	// of it.
	KindAssetsOutflows = "assets.outflows"
	// KindWorkoutChanges is what changed in the training log (input-svc).
	KindWorkoutChanges = "workout.changes"
	// KindChatTurn is a turn of the conversation with jarvis, sealed
	// (input-svc).
	KindChatTurn = "chat.turn"
)

// ForecastKey is where the forecast of a place is kept: one latest row for
// each place a weather.forecast record names, which the sensor writes and the
// loop reads.
func ForecastKey(name string) string { return "weather:" + name }

// What comes back to mind from its own loop, or is asked of it: read on the
// deterministic lane, no model. The registry locks the first six to that
// lane, whoever edits the table.
const (
	// KindIntentAnswer is the operator's answer to a card, as the agent
	// writes it: the command kind of mind's /api/intents
	// (gocommon/record.CommandKind).
	KindIntentAnswer = "intent.answer"
	// KindExecuteResult is what came of a write an adapter carried out (hand).
	KindExecuteResult = "execute.result"
	// KindManualEdit is what a person changed by hand on a screen
	// (gocommon/record.KindManualEdit).
	KindManualEdit = "manual.edit"
	// KindPaymentRequested says a payment card went up; its answer closes it.
	KindPaymentRequested = "payment.requested"
	// KindTimerFired is mind's clock on the log: a timer whose moment came.
	KindTimerFired      = "timer.fired"
	KindRegistryChanged = "registry.changed"
	// KindContextState is context-svc's word on the operator's state:
	// driving, in a meeting, in a focus.
	KindContextState = "context.state"
)

// What the loop reads, with the model where it is words.
const (
	// KindMessageObserved is a chat message; its text is in the content store.
	KindMessageObserved = "message.observed"
	// KindOccurrenceObserved is something that happened, read from a
	// structured source (a payment, a photo, a workout); the occurrence is in
	// the store.
	KindOccurrenceObserved = "occurrence.observed"
	// KindActionExecuted is the gate having done something; KindActionUndone
	// it taking that back.
	KindActionExecuted = "action.executed"
	KindActionUndone   = "action.undone"
	// KindAssetsBand is assets-svc's event: a bucket of the portfolio has left
	// the band around its target.
	KindAssetsBand = "assets.band"
	// Location events (context-svc), as the operator arrives at a region or
	// leaves it - and as the guest's phone does at the guest's own regions.
	KindLocationEnter      = "location.enter"
	KindLocationLeave      = "location.leave"
	KindLocationGuestEnter = "location.guest.enter"
	KindLocationGuestLeave = "location.guest.leave"
	// KindLocationFix is where the operator's phone is, as it reports - at
	// most about a minute apart (context-svc): what a reminder for a place on
	// the map, which no region marks, goes by.
	KindLocationFix = "location.fix"
)

// What mind sends out: the one path a card takes to the operator and back,
// notices with their wording (output-svc), and word to the agent's screens.
const (
	KindCardRaised  = "mind.card"
	KindCardUpdated = "mind.card.updated"
	KindCardClosed  = "mind.card.closed"
	// KindMindNotice is a notice that names no kind of its own. KindMindInfo
	// is one that only tells, which the inbox answers with one tap (Heard).
	KindMindNotice = "mind.notice"
	KindMindInfo   = "mind.info"
	// A reminder said at its moment rings even in quiet hours: the operator's
	// own (asked), what is left on a list the evening before its day (list),
	// a goal's (goal). The assistant's own at a moment of the operator's
	// movement goes by that moment's name (RemindKind).
	KindRemindAsked = "remind.asked"
	KindRemindList  = "remind.list"
	KindRemindGoal  = "remind.goal"
	// KindMemoryRule tells the operator a rule the assistant keeps now stands,
	// and where to take it back.
	KindMemoryRule = "memory.rule"
	// KindChecklistShown asks the agent to put a list in the conversation
	// now, as a card: data {checklist, title, lead}.
	KindChecklistShown = "checklist.shown"
	// KindChecklistChanged says how a list stands has changed: the screens
	// showing it redraw it, on every device.
	KindChecklistChanged = "checklist.changed"
	// KindCalendarChanged says what an entry became: the screens showing it
	// redraw.
	KindCalendarChanged = "calendar.changed"
	// KindJobsWake says a job may be due: the scheduler looks now rather than
	// on its own long clock.
	KindJobsWake = "jobs.wake"
	// KindAdapterCall is a write on its way to an adapter (hand).
	KindAdapterCall = "adapter.call"
)

// RemindKind is the kind a notice said at a moment of the operator's own
// movement goes out as - remind.rain, leaving home with rain ahead - which
// rings then even in quiet hours.
func RemindKind(moment string) string { return "remind." + moment }

// What output-svc still has to hear of though it carries no wording: what took
// a notification's place, and what answered it.
const (
	KindSuggestionRaised = "suggestion.raised"
	KindProposalDecided  = "proposal.decided"
)
