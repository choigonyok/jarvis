package api

import "testing"

// A notification for the guest account stays the guest's on its way through
// the log: the audience rides in the wording notify-svc reads.
func TestNotificationKeepsItsAudience(t *testing.T) {
	e, err := Parse([]byte(`{"source":"agent","kind":"approval.pending","tier":"now","title":"결재 대기","url":"/?proposal=p-1","audience":"guest"}`))
	if err != nil {
		t.Fatal(err)
	}
	if e.Notify == nil || e.Notify.Audience != "guest" || e.Notify.URL != "/?proposal=p-1" {
		t.Fatalf("notify: %+v", e.Notify)
	}
	e, err = Parse([]byte(`{"source":"spending","kind":"spending.big","tier":"digest","title":"큰 결제"}`))
	if err != nil || e.Notify.Audience != "" {
		t.Fatalf("운영자 것은 비어 있어야 합니다: %+v %v", e.Notify, err)
	}
}
