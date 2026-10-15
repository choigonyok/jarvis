package main

import "testing"

func TestKindsAndPatterns(t *testing.T) {
	for app, want := range map[string]string{"zoom.us": "meeting", "Code": "code", "KakaoTalk": "chat", "Google Chrome": "browser", "Finder": "other"} {
		if got := Kind(app); got != want {
			t.Errorf("Kind(%q) = %q, want %q", app, got, want)
		}
	}
	for text, want := range map[string]string{
		"512345678901":                 "tracking",
		"서울시 마포구 망원로 12":               "address",
		"2026.10.30 예약":                "date",
		"10월 30일 7시":                   "date",
		"그냥 메모":                        "",
		"sk-ant-api03-very-secret-key": "",
	} {
		if got := Pattern(text); got != want {
			t.Errorf("Pattern(%q) = %q, want %q", text, got, want)
		}
	}
	if !Secret("1Password 7") || Secret("Notes") {
		t.Error("비밀번호 관리자에서 복사한 것은 보지 않아야 합니다")
	}
}

func TestDelivery(t *testing.T) {
	line := "2026-10-15 18:33:58.244 Df NotificationCenter[794:1da9] [com.apple.unc:application] Adding notification to storage: com.jawebs.baedal:BC9E0BFE"
	if app, id := Delivery(line); app != "baemin" || id != "com.jawebs.baedal:BC9E0BFE" {
		t.Fatalf("%q %q", app, id)
	}
	if app, _ := Delivery("… Adding notification to storage: com.linkedin.LinkedIn:47062BDE"); app != "" {
		t.Fatalf("배달앱이 아닌 알림: %q", app)
	}
	if app, _ := Delivery("… Re-add com.jawebs.baedal:BC9E0BFE, com.jawebs.baedal with visibility"); app != "" {
		t.Fatalf("다시 넣기는 새 알림이 아님: %q", app)
	}
}
