package mind

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/store"
)

func TestParsePoint(t *testing.T) {
	p, err := ParsePoint("37.5665, 126.9780, 서울")
	if err != nil || p.Name != "서울" || p.Lat != 37.5665 || p.Lon != 126.978 {
		t.Fatalf("%+v %v", p, err)
	}
	if p, err := ParsePoint("37.5,127"); err != nil || p.Name != "집" {
		t.Fatalf("no name is home: %+v %v", p, err)
	}
	for _, bad := range []string{"", "서울", "91,127", "37.5,abc"} {
		if _, err := ParsePoint(bad); err == nil {
			t.Errorf("%q should not read", bad)
		}
	}
}

// Home is the 집 region made on the phone; without one - or before context-svc
// has said - the fixed point.
func TestHome(t *testing.T) {
	st := store.NewMemory()
	seoul, _ := ParsePoint("37.5665,126.9780,서울")
	ctx := context.Background()

	// Nothing said yet: the fixed point stands in.
	h := &Home{Store: st, Fixed: seoul}
	if p, err := h.Point(ctx); err != nil || p.Name != "서울" {
		t.Fatalf("context-svc has not said: %+v %v", p, err)
	}

	answer := `{"regions":[{"name":"회사","kind":"work","lat":37.49,"lon":127.02},{"name":"집","kind":"home","lat":37.556,"lon":126.901}]}`
	if err := st.PutLatest(ctx, sensorRegions, json.RawMessage(answer), time.Now()); err != nil {
		t.Fatal(err)
	}
	h = &Home{Store: st, Fixed: seoul}
	p, err := h.Point(ctx)
	if err != nil || p.Name != "집" || p.Lat != 37.556 {
		t.Fatalf("the 집 region: %+v %v", p, err)
	}
}
