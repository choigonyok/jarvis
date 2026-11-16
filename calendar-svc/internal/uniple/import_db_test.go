package uniple

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Moving a space off uniple keeps every id, ties the D-day's entries to what
// they stand for, and brings the photos.
func TestImportKeepsTheSpaceAsItWas(t *testing.T) {
	const (
		me      = "0a000000-0000-4000-8000-000000000001"
		partner = "0a000000-0000-4000-8000-000000000002"
		space   = "0b000000-0000-4000-8000-000000000001"
		calID   = "0c000000-0000-4000-8000-000000000001"
		ddayID  = "0c000000-0000-4000-8000-000000000002"
		mapID   = "0c000000-0000-4000-8000-000000000003"
		dinner  = "0d000000-0000-4000-8000-000000000001"
		bday    = "0d000000-0000-4000-8000-000000000002"
		hundred = "0d000000-0000-4000-8000-000000000003"
		item    = "0e000000-0000-4000-8000-000000000001"
		asset   = "0f000000-0000-4000-8000-000000000001"
	)
	f := &fakeREST{
		answers: map[string]string{
			"GET spaces":   `[{"id":"` + space + `","anniversary_date":"2026-08-15","week_start":"mon","plan":"free"}]`,
			"GET profiles": `[{"id":"` + me + `","nickname":"하늘","space_id":"` + space + `"},{"id":"` + partner + `","nickname":"바다","space_id":"` + space + `"}]`,
			"GET blocks": `[{"id":"` + calID + `","space_id":"` + space + `","type":"calendar","title":"우리 캘린더","order_index":1,"config":null},
				{"id":"` + ddayID + `","space_id":"` + space + `","type":"dday","title":"♥","order_index":0,"config":{"showUpcoming":false}},
				{"id":"` + mapID + `","space_id":"` + space + `","type":"map","title":"지도","order_index":2,"config":null,"archived_at":null}]`,
			"GET calendar_events": `[
				{"id":"` + dinner + `","block_id":"` + calID + `","space_id":"` + space + `","title":"저녁","start_date":"2026-12-01","start_time":"19:00:00","created_by":"` + partner + `","owner_user_id":null,"recurrence_exdates":null,"color":"#ff0000","updated_at":"2026-11-01T00:00:00+00:00"},
				{"id":"` + bday + `","block_id":"` + calID + `","space_id":"` + space + `","title":"바다 생일","start_date":"2026-11-21","recurrence":"yearly","origin":"anniversary","created_by":"` + me + `"},
				{"id":"` + hundred + `","block_id":"` + calID + `","space_id":"` + space + `","title":"100일","start_date":"2026-11-22","origin":"milestone","created_by":"` + me + `"}]`,
			"GET dday_items": `[{"id":"` + item + `","block_id":"` + ddayID + `","space_id":"` + space + `","title":"바다 생일","date":"2026-11-21","is_start_date":false,"recurrence":"yearly","created_by":"` + me + `"}]`,
			"GET map_visits": `[{"id":"0e000000-0000-4000-8000-000000000002","block_id":"` + mapID + `","space_id":"` + space + `","region_code":"S04","asset_id":"` + asset + `","is_rep":true,"rotation":0,"created_at":"2026-10-01T00:00:00+00:00"}]`,
			"GET notifications": `[{"id":"0e000000-0000-4000-8000-000000000003","space_id":"` + space + `","actor_id":"` + partner + `","block_id":"` + calID + `","block_type":"calendar","kind":"","item_id":"` + dinner + `","event_time":"19:00","created_at":"2026-11-01T00:00:00+00:00","recipient_id":"` + me + `"}]`,
			"POST rpc/media_assets_lookup": `[{"id":"` + asset + `","kind":"photo","bucket":"media","status":"ready","variants":{"display":{"path":"s/a/display.png"}}}]`,
		},
		objects: map[string]string{"media/s/a/display.png": string(pngOf(t, 800, 600))},
	}
	srv := httptest.NewServer(f.handler(t))
	t.Cleanup(srv.Close)
	remote := &Client{cfg: Config{BaseURL: srv.URL, AnonKey: "anon"}, http: srv.Client(), access: "at", accessExp: time.Now().Add(time.Hour), me: me, block: calID, space: space}

	dst, owner, guest := localSpace(t)
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	got, err := Import(ctx, remote, dst, false, log)
	if err != nil {
		t.Fatal(err)
	}
	if got.Rows["calendar_events"] != 3 || got.Rows["blocks"] != 3 || got.Photos != 1 || len(got.Missing) != 0 {
		t.Fatalf("옮긴 것: %+v", got)
	}

	sp, err := dst.Space(owner)
	if err != nil {
		t.Fatal(err)
	}
	if sp.Me.Name != "하늘" || sp.Partner == nil || sp.Partner.Name != "바다" || sp.Anniversary != "2026-08-15" || sp.WeekStart != "mon" || len(sp.Blocks) != 3 {
		t.Fatalf("공간: %+v", sp)
	}
	if gs, _ := dst.Space(guest); gs.Me.Name != "바다" {
		t.Fatalf("손님에게는 손님이 나: %+v", gs.Me)
	}
	e, err := dst.Get(owner, dinner)
	if err != nil || e.Source != "partner" || e.Owner != "shared" || e.Start != "19:00" {
		t.Fatalf("같은 id 의 일정: %+v %v", e, err)
	}
	news, _ := dst.News(owner, 10)
	if len(news) != 1 || news[0].Who != "partner" || news[0].Item == nil || news[0].Item.Title != "저녁" {
		t.Fatalf("소식: %+v", news)
	}
	if p, err := dst.Photo(owner, asset, "thumb"); err != nil || p.Size == 0 {
		t.Fatalf("사진: %+v %v", p, err)
	}

	// The next write of the D-day keeps the entries' ids.
	title := "바다 생일 🎂"
	if _, err := dst.SetDDay(owner, ddayID, item, DDayInput{Title: &title}); err != nil {
		t.Fatal(err)
	}
	if b, err := dst.Get(owner, bday); err != nil || b.Title != title {
		t.Fatalf("기념일 일정은 같은 id 로: %+v %v", b, err)
	}
	if h, err := dst.Get(owner, hundred); err != nil || h.Title != "100일" {
		t.Fatalf("100일은 같은 id 로: %+v %v", h, err)
	}

	if _, err := Import(ctx, remote, dst, false, log); err == nil || !strings.Contains(err.Error(), "-replace") {
		t.Fatalf("이미 있으면 묻지 않고 덮지 않는다: %v", err)
	}
	if _, err := Import(ctx, remote, dst, true, log); err != nil {
		t.Fatal(err)
	}
}
