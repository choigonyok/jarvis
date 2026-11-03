package pay

import "testing"

// A payment is found by any of its names and by its keypad's host or one under
// it; the table has no name twice.
func TestOneTable(t *testing.T) {
	if p, ok := PinFor(" 네이버 페이 "); !ok || p.Host != "pay.naver.com" {
		t.Fatalf("%+v %v", p, ok)
	}
	if p, ok := PinFor("COUPANG"); !ok || p.Host != "coupang.com" {
		t.Fatalf("%+v %v", p, ok)
	}
	if _, ok := PinFor("토스페이"); ok {
		t.Fatal("표에 없는 결제")
	}
	if p, ok := PinAt("rocketpay.coupang.com"); !ok || p.Name != "쿠팡 쿠페이" {
		t.Fatalf("%+v %v", p, ok)
	}
	if _, ok := PinAt("blog.naver.com"); ok {
		t.Fatal("네이버의 다른 곳은 결제 키패드가 아닙니다")
	}
	whose := map[string]string{}
	for _, p := range Pins() {
		for _, a := range append([]string{p.Host}, p.Aliases...) {
			if other, ok := whose[a]; ok && other != p.Host {
				t.Fatalf("%q 가 %s 와 %s 둘 다의 이름", a, other, p.Host)
			}
			whose[a] = p.Host
		}
	}
	if Names() != "'네이버페이' 또는 '쿠팡 쿠페이'" {
		t.Fatal(Names())
	}
}
