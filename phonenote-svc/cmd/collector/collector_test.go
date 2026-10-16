package main

import (
	"testing"
)

const sample = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>app</key><string>com.jawebs.baedal</string>
	<key>date</key><real>782301720.5</real>
	<key>req</key>
	<dict>
		<key>body</key><string>교촌치킨에서 주문을 접수했어요. 허니콤보 외 1개</string>
		<key>titl</key><string>주문 접수</string>
		<key>flags</key><array><integer>1</integer><true/></array>
	</dict>
	<key>uuid</key><data>3q2+7w==</data>
</dict>
</plist>`

func TestParsePlist(t *testing.T) {
	v, err := parsePlist([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	top := v.(map[string]any)
	req := top["req"].(map[string]any)
	if top["app"] != "com.jawebs.baedal" || req["titl"] != "주문 접수" || top["date"].(float64) != 782301720.5 {
		t.Fatalf("%+v", top)
	}
	if flags := req["flags"].([]any); len(flags) != 2 || flags[1] != true {
		t.Fatalf("배열: %+v", req["flags"])
	}
}

func TestCodesAndAppsStayOnTheMac(t *testing.T) {
	if got := redact("인증번호 482913 을 입력하세요"); got != "인증번호 •••• 을 입력하세요" {
		t.Fatalf("인증번호: %q", got)
	}
	if got := redact("주문 금액 25000원"); got != "주문 금액 25000원" {
		t.Fatalf("인증번호가 아니면 그대로: %q", got)
	}
	for app, want := range map[string]bool{
		"com.jawebs.baedal": true, "com.apple.Passbook": true, "com.apple.Music": false,
		"com.tinyspeck.slackmacgap": false, "com.iwilab.KakaoTalk": false, "com.microsoft.teams2": false,
	} {
		if sendable(app) != want {
			t.Fatalf("%s: %v", app, !want)
		}
	}
}
