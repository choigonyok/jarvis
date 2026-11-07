package model

// Record kinds: what a record on the stream is, as the registry's kind rows
// name it (registry/seed.json says which lane reads each). Every layer names
// a kind by these, so each is spelled once, and the registry's tests hold the
// rows to what the code reads.
//
// The latest record of a kind is kept under the kind itself
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
	// are.
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

// What comes back to mind from its own loop, or is asked of it: read on the
// deterministic lane, no model. The registry locks the first six to that
// lane, whoever edits the table.
const (
	// KindIntentAnswer is the operator's answer to a card, as the agent
	// writes it.
	KindIntentAnswer = "intent.answer"
	// KindExecuteResult is what came of a write an adapter carried out (hand).
	KindExecuteResult = "execute.result"
	KindManualEdit    = "manual.edit"
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
	// KindMindNotice is a notice that says no kind of its own. KindMindInfo
	// is one that only tells, which the inbox answers with one tap (Heard).
	KindMindNotice = "mind.notice"
	KindMindInfo   = "mind.info"
	// A reminder said at its moment rings even in quiet hours: the operator's
	// own, a list's left the evening before its day, a goal's. The assistant's
	// own at a moment goes by the moment's name (RemindKind).
	KindRemindAsked = "remind.asked"
	KindRemindList  = "remind.list"
	KindRemindGoal  = "remind.goal"
	// KindMemoryRule tells the operator a rule of theirs now stands.
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
