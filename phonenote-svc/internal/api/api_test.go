package api

import (
	"testing"
	"time"

	"github.com/choigonyok/jarvis/phonenote-svc/internal/note"
)

// Work apps, mail, KakaoTalk and Messages never come in.
func TestRefused(t *testing.T) {
	for _, app := range []string{"com.tinyspeck.chatlyio", "com.atlassian.jira.core.ios", "com.microsoft.Office.Outlook", "com.google.Gmail", "com.iwilab.KakaoTalk", "com.apple.MobileSMS"} {
		if !Refused(app) {
			t.Fatalf("%s 는 받지 않아야 합니다", app)
		}
	}
	if Refused("com.jawebs.baedal") || Refused("com.kbstar.kbbank") {
		t.Fatal("배달앱·은행 알림은 받습니다")
	}
}

// A payment is the 가계부's, not offered twice; pay arrives as salary.
func TestOccurrences(t *testing.T) {
	at := time.Now()
	if _, ok := OccurrenceOf(note.Note{ID: 1, At: at, Reading: &note.Reading{Kind: note.Purchase, Meaningful: true}}); ok {
		t.Fatal("결제는 가계부에서 옵니다")
	}
	o, ok := OccurrenceOf(note.Note{ID: 2, App: "com.kbstar.kbbank", At: at, Title: "입금",
		Reading: &note.Reading{Kind: note.Deposit, Meaningful: true, Salary: true, AmountKrw: 3200000, Counterparty: "(주)회사", Summary: "급여 입금"}})
	if !ok || o.Kind != "deposit" || o.Class != "급여" || o.Amount != 3200000 || o.ID != "phone:2" || o.Title != "(주)회사" {
		t.Fatalf("%+v", o)
	}
	if _, ok := OccurrenceOf(note.Note{ID: 3, Reading: &note.Reading{Kind: note.Promotion}}); ok {
		t.Fatal("의미 없는 알림은 내보내지 않습니다")
	}
}
