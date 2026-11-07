package model

import (
	"testing"

	"github.com/choigonyok/jarvis/gocommon/record"
)

// A calendar change is one of mind's or a card of the conversation's for one;
// a move is mind's update or the card for a move. A notice is neither.
func TestCalendarKindsAreToldApart(t *testing.T) {
	for kind, want := range map[string]struct{ calendar, update bool }{
		CalendarCreate:     {true, false},
		CalendarUpdate:     {true, true},
		CalendarDelete:     {true, false},
		HeldCalendarCreate: {true, false},
		HeldCalendarUpdate: {true, true},
		Info:               {false, false},
		"cart.add":         {false, false},
	} {
		if IsCalendar(kind) != want.calendar || IsCalendarUpdate(kind) != want.update {
			t.Errorf("%s: 캘린더 %v, 옮기기 %v 여야 합니다", kind, want.calendar, want.update)
		}
	}
}

// A kind is a wire value: the registry's rows, the agent, the screens and the
// rows already stored spell it. A name here changes; its value moves only with
// all of them, and with this test.
func TestTheKindsAreTheirWireValues(t *testing.T) {
	for _, k := range []struct{ name, want string }{
		// Intents; the agent's own cards held in mind's ledger, and its answers
		// to mind's.
		{Info, "info"}, {CalendarCreate, "calendar.create"}, {CalendarUpdate, "calendar.update"}, {CalendarDelete, "calendar.delete"},
		{CartAdd, "cart.add"}, {RemindSet, "remind.set"}, {MessageDraft, "message.draft"}, {SettleRequest, "settle.request"},
		{ReviewPlace, "review.place"}, {PreferenceConfirm, "preference.confirm"}, {AssetsRebalance, "assets.rebalance"},
		{PhotosAlbum, "photos.album"}, {PhotosPlace, "photos.place"}, {SpendingCategory, "spending.category"}, {NoteOnly, "note"},
		{HeldCalendarCreate, "calendar.create_event"}, {HeldCalendarUpdate, "calendar.update_event"}, {HeldPayment, "payment"},
		{MindApprove, "mind.approve"}, {MindUndo, "mind.undo"},
		// Entities.
		{KindPlan, "plan"}, {KindLoop, "loop"},
		// Occurrences.
		{Purchase, "purchase"}, {PurchaseItem, "purchase.item"}, {Visit, "visit"}, {Workout, "workout"}, {Photo, "photo"},
		{Calendar, "calendar"}, {Transfer, "transfer"}, {Asset, "asset"}, {Job, "job"}, {Deposit, "deposit"}, {Bill, "bill"},
		// Records: the adapters'.
		{KindCalendarNear, "calendar.near"}, {KindCalendarYear, "calendar.year"}, {KindContextVisits, "context.visits"},
		{KindAssetsHoldings, "assets.holdings"}, {KindContextNow, "context.now"}, {KindContextNowGuest, "context.now.guest"},
		{KindContextRegions, "context.regions"}, {KindContextTravel, "context.travel"}, {KindWeatherForecast, "weather.forecast"},
		{KindPhotosRecent, "photos.recent"}, {KindPhotosDays, "photos.days"}, {KindAssetsFX, "assets.fx"},
		{KindAssetsOutflows, "assets.outflows"}, {KindWorkoutChanges, "workout.changes"}, {KindChatTurn, "chat.turn"},
		// Records: mind's own loop coming back, or asked of it.
		{KindIntentAnswer, "intent.answer"}, {KindExecuteResult, "execute.result"}, {KindManualEdit, "manual.edit"},
		{KindPaymentRequested, "payment.requested"}, {KindTimerFired, "timer.fired"}, {KindRegistryChanged, "registry.changed"},
		{KindContextState, "context.state"},
		// Records the loop reads.
		{KindMessageObserved, "message.observed"}, {KindOccurrenceObserved, "occurrence.observed"},
		{KindActionExecuted, "action.executed"}, {KindActionUndone, "action.undone"}, {KindAssetsBand, "assets.band"},
		{KindLocationEnter, "location.enter"}, {KindLocationLeave, "location.leave"}, {KindLocationGuestEnter, "location.guest.enter"},
		{KindLocationGuestLeave, "location.guest.leave"}, {KindLocationFix, "location.fix"},
		// Records mind sends out.
		{KindCardRaised, "mind.card"}, {KindCardUpdated, "mind.card.updated"}, {KindCardClosed, "mind.card.closed"},
		{KindMindNotice, "mind.notice"}, {KindMindInfo, "mind.info"}, {KindRemindAsked, "remind.asked"},
		{KindRemindList, "remind.list"}, {KindRemindGoal, "remind.goal"}, {KindMemoryRule, "memory.rule"},
		{KindChecklistShown, "checklist.shown"}, {KindChecklistChanged, "checklist.changed"},
		{KindCalendarChanged, "calendar.changed"}, {KindJobsWake, "jobs.wake"}, {KindAdapterCall, "adapter.call"},
		{KindSuggestionRaised, "suggestion.raised"}, {KindProposalDecided, "proposal.decided"},
	} {
		if k.name != k.want {
			t.Errorf("%q 여야 하는 종류가 %q 입니다", k.want, k.name)
		}
	}
}

// Two of the stream's words are gocommon's too: what a screen changed by hand,
// and the command an answer to a card travels as. mind's names for them are
// gocommon's, or the deterministic lane would wait for a kind nobody writes.
func TestTheStreamsWordsAreGocommons(t *testing.T) {
	if KindManualEdit != record.KindManualEdit {
		t.Errorf("손으로 고친 것의 종류가 gocommon 과 다릅니다: %q, %q", KindManualEdit, record.KindManualEdit)
	}
	if got := record.CommandKind("POST", "/api/intents/in_1/approve"); KindIntentAnswer != got {
		t.Errorf("카드의 답의 종류가 gocommon 과 다릅니다: %q, %q", KindIntentAnswer, got)
	}
}
