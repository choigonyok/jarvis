package uniple

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/choigonyok/jarvis/calendar-svc/internal/event"
)

// The space kept here, end to end against a real Postgres (scripts/dbtest):
// what each of the two writes, what the other then reads, and what uniple's
// server used to do on its own.

func localSpace(t *testing.T) (*Client, context.Context, context.Context) {
	t.Helper()
	dsn := os.Getenv("JARVIS_TEST_DSN")
	if dsn == "" {
		t.Skip("JARVIS_TEST_DSN 이 없어 건너뜁니다 (scripts/dbtest)")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `truncate couple.media, couple.news_seen, couple.notifications, couple.budget_expenses, couple.budget_dates,
		couple.diary_comments, couple.diary_entries, couple.food_entries, couple.log_entries, couple.calendar_day_photos, couple.album_photos,
		couple.worldmap_visits, couple.worldmap_records, couple.map_visits, couple.map_records, couple.workout_checks, couple.free_items,
		couple.bucket_items, couple.dday_items, couple.calendar_events, couple.blocks, couple.sides, couple.profiles, couple.spaces`); err != nil {
		t.Fatal(err)
	}
	admin.Close()
	c, err := OpenLocal(ctx, dsn, Config{}, LocalConfig{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	return c, ctx, As(ctx, "partner")
}

func TestLocalCalendarIsEachOnesOwn(t *testing.T) {
	c, owner, guest := localSpace(t)
	if !c.Native() {
		t.Fatal("로컬 공간은 묻는 사람으로 읽는다")
	}
	mine, err := c.Put(owner, event.Event{Date: "2026-12-01", Start: "19:00", Title: "저녁", Owner: event.OwnerMe})
	if err != nil {
		t.Fatal(err)
	}
	theirs, err := c.Put(guest, event.Event{Date: "2026-12-01", Title: "휴가", Owner: event.OwnerMe})
	if err != nil {
		t.Fatal(err)
	}
	shared, err := c.Put(guest, event.Event{Date: "2026-12-02", Title: "같이 영화", Owner: event.OwnerShared})
	if err != nil {
		t.Fatal(err)
	}
	read := func(ctx context.Context) map[string]event.Event {
		list, err := c.Range(ctx, "2026-11-30", "2026-12-03")
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]event.Event{}
		for _, e := range list {
			out[e.ID] = e
		}
		return out
	}
	o, g := read(owner), read(guest)
	if o[mine.ID].Owner != event.OwnerMe || o[mine.ID].Source != event.SourceHuman || g[mine.ID].Owner != event.OwnerPartner || g[mine.ID].Source != event.SourcePartner {
		t.Fatalf("운영자의 일정: 운영자 %+v, 손님 %+v", o[mine.ID], g[mine.ID])
	}
	if o[theirs.ID].Owner != event.OwnerPartner || o[theirs.ID].Source != event.SourcePartner || g[theirs.ID].Owner != event.OwnerMe {
		t.Fatalf("손님이 쓴 손님의 일정: 운영자 %+v, 손님 %+v", o[theirs.ID], g[theirs.ID])
	}
	if o[shared.ID].Owner != event.OwnerShared {
		t.Fatalf("함께: %+v", o[shared.ID])
	}

	// A weekly series, one day of it taken off.
	weekly := "weekly"
	series, err := c.Put(owner, event.Event{Date: "2026-12-07", Start: "07:00", Title: "수영", Repeat: &weekly})
	if err != nil {
		t.Fatal(err)
	}
	days, _ := c.Range(owner, "2026-12-07", "2026-12-28")
	n := 0
	for _, e := range days {
		if strings.HasPrefix(e.ID, series.ID+"#") {
			n++
		}
	}
	if n != 4 {
		t.Fatalf("주마다 네 번: %d %+v", n, days)
	}
	if _, err := c.Delete(owner, series.ID+"#2026-12-14"); err != nil {
		t.Fatal(err)
	}
	days, _ = c.Range(owner, "2026-12-14", "2026-12-14")
	for _, e := range days {
		if strings.HasPrefix(e.ID, series.ID) {
			t.Fatalf("지운 하루가 남았다: %+v", e)
		}
	}
	if _, err := c.Delete(guest, theirs.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Get(owner, theirs.ID); !errors.Is(err, event.ErrNotFound) {
		t.Fatalf("지운 일정: %v", err)
	}

	news, err := c.News(owner, 20)
	if err != nil {
		t.Fatal(err)
	}
	var partnerAdds, partnerGone, mineToo int
	for _, x := range news {
		switch {
		case x.Who == "partner" && x.BlockType == "calendar" && x.Kind == "":
			partnerAdds++
		case x.Who == "partner" && x.Kind == "delete":
			partnerGone++
		case x.Who == "me":
			mineToo++
		}
	}
	if partnerAdds != 2 || partnerGone != 1 || mineToo < 2 {
		t.Fatalf("소식: 상대 추가 %d, 상대 지움 %d, 내 것 %d: %+v", partnerAdds, partnerGone, mineToo, news)
	}
	for i := 1; i < len(news); i++ {
		if news[i-1].At < news[i].At {
			t.Fatalf("소식은 늦은 것부터: %+v", news)
		}
	}
	if seen, _ := c.NewsSeen(owner); seen != "" {
		t.Fatalf("아직 읽지 않았다: %q", seen)
	}
	if err := c.SeeNews(owner, news[0].At); err != nil {
		t.Fatal(err)
	}
	if err := c.SeeNews(owner, "2020-01-01T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	seen, _ := c.NewsSeen(owner)
	if a, _ := time.Parse(time.RFC3339Nano, seen); !a.Equal(mustTime(t, news[0].At)) {
		t.Fatalf("읽은 데까지는 뒤로 가지 않는다: %q vs %q", seen, news[0].At)
	}
	if other, _ := c.NewsSeen(guest); other != "" {
		t.Fatalf("손님은 따로 읽는다: %q", other)
	}
}

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestLocalDDayWritesTheCalendar(t *testing.T) {
	c, owner, _ := localSpace(t)
	b, err := c.AddBlock(owner, "dday", "")
	if err != nil {
		t.Fatal(err)
	}
	title, date, yearly := "지수 생일", "2026-11-21", "yearly"
	d, err := c.AddDDay(owner, b.ID, DDayInput{Title: &title, Date: &date, Repeat: &yearly})
	if err != nil {
		t.Fatal(err)
	}
	met := time.Now().AddDate(0, 0, -50).Format(time.DateOnly)
	if err := c.SetStart(owner, b.ID, "", met); err != nil {
		t.Fatal(err)
	}
	space, err := c.Space(owner)
	if err != nil {
		t.Fatal(err)
	}
	if space.Anniversary != met {
		t.Fatalf("만난 날: %q", space.Anniversary)
	}
	from := time.Now().AddDate(-1, 0, 0).Format(time.DateOnly)
	to := time.Now().AddDate(3, 0, 0).Format(time.DateOnly)
	list, err := c.Range(owner, from, to)
	if err != nil {
		t.Fatal(err)
	}
	var birthdays, hundreds, years int
	ids := map[string]string{}
	for _, e := range list {
		switch e.Origin {
		case "anniversary":
			birthdays++
			if e.Title != title || e.Owner != event.OwnerShared || e.Start != "" {
				t.Fatalf("기념일 일정: %+v", e)
			}
		case "milestone":
			ids[e.Title] = strings.SplitN(e.ID, "#", 2)[0]
			if strings.HasSuffix(e.Title, "주년") {
				years++
			} else {
				hundreds++
			}
		}
	}
	if birthdays < 3 || hundreds+years != milestonesAhead || ids["100일"] == "" {
		t.Fatalf("생일 %d, 100일 단위 %d, 주년 %d: %+v", birthdays, hundreds, years, list)
	}
	hundred, _ := time.Parse(time.DateOnly, met)
	if got, _ := c.Get(owner, ids["100일"]); got.Date != hundred.AddDate(0, 0, 99).Format(time.DateOnly) {
		t.Fatalf("100일은 만난 날이 1일: %+v", got)
	}
	// Anything put on the calendar from the D-day is changed there.
	if _, err := c.Delete(owner, ids["100일"]); err == nil {
		t.Fatal("기념일에서 만든 일정은 캘린더에서 지우지 않는다")
	}
	renamed := "지수 생신"
	if _, err := c.SetDDay(owner, b.ID, d.ID, DDayInput{Title: &renamed}); err != nil {
		t.Fatal(err)
	}
	again, _ := c.Range(owner, from, to)
	for _, e := range again {
		if e.Origin == "milestone" && e.Title == "100일" && strings.SplitN(e.ID, "#", 2)[0] != ids["100일"] {
			t.Fatalf("다시 써도 같은 id: %s → %s", ids["100일"], e.ID)
		}
		if e.Origin == "anniversary" && e.Title != renamed {
			t.Fatalf("바뀐 이름: %+v", e)
		}
	}
	if err := c.DeleteDDay(owner, b.ID, d.ID); err != nil {
		t.Fatal(err)
	}
	gone, _ := c.Range(owner, from, to)
	for _, e := range gone {
		if e.Origin == "anniversary" {
			t.Fatalf("지운 기념일의 일정이 남았다: %+v", e)
		}
	}
}

func TestLocalBlocksAreWrittenByBoth(t *testing.T) {
	c, owner, guest := localSpace(t)
	bucket, err := c.AddBlock(owner, "bucket", "하고 싶은 것")
	if err != nil {
		t.Fatal(err)
	}
	wish, err := c.AddBucket(guest, bucket.ID, "제주 한 달 살기")
	if err != nil {
		t.Fatal(err)
	}
	second, _ := c.AddBucket(owner, bucket.ID, "스카이다이빙")
	done := true
	if _, err := c.SetBucket(owner, bucket.ID, wish.ID, BucketInput{Done: &done}); err != nil {
		t.Fatal(err)
	}
	if err := c.OrderItems(owner, bucket.ID, "bucket", []string{second.ID, wish.ID}); err != nil {
		t.Fatal(err)
	}
	b, err := c.Block(owner, bucket.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Bucket) != 2 || b.Bucket[0].ID != second.ID || !b.Bucket[1].Done {
		t.Fatalf("버킷: %+v", b.Bucket)
	}

	habit, _ := c.AddBlock(owner, "workout", "")
	day := today()
	ck, err := c.Check(guest, habit.ID, day, "30분")
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := c.Check(guest, habit.ID, day, ""); again.ID != ck.ID {
		t.Fatalf("같은 날 두 번은 하나: %+v", again)
	}
	if _, err := c.Check(owner, habit.ID, time.Now().AddDate(0, 0, 2).Format(time.DateOnly), ""); err == nil {
		t.Fatal("오지 않은 날은 체크하지 않는다")
	}
	checks, err := c.Checks(owner, habit.ID, day, day)
	if err != nil || len(checks) != 1 || checks[0].Who != "partner" || checks[0].Memo != "30분" {
		t.Fatalf("운영자가 본 손님의 체크: %+v %v", checks, err)
	}
	if err := c.Uncheck(owner, habit.ID, ck.ID); !errors.Is(err, event.ErrNotFound) {
		t.Fatalf("남의 체크는 취소하지 않는다: %v", err)
	}
	if err := c.Uncheck(guest, habit.ID, ck.ID); err != nil {
		t.Fatal(err)
	}

	free, _ := c.AddBlock(owner, "free", "")
	note, err := c.AddFree(guest, free.ID, "다음엔 바다")
	if err != nil || note.By != "me" {
		t.Fatalf("손님이 쓴 기록은 손님에게 내 것: %+v %v", note, err)
	}
	if fb, _ := c.Block(owner, free.ID); len(fb.Free) != 1 || fb.Free[0].By != "partner" {
		t.Fatalf("운영자에게는 상대 것: %+v", fb.Free)
	}

	if err := c.ArchiveBlock(owner, free.ID); err != nil {
		t.Fatal(err)
	}
	if kept, _ := c.Archived(owner); len(kept) != 1 || kept[0].ID != free.ID {
		t.Fatalf("보관: %+v", kept)
	}
	if err := c.Restore(owner, free.ID); err != nil {
		t.Fatal(err)
	}
	space, _ := c.Space(owner)
	var order []string
	for _, b := range space.Blocks {
		order = append(order, b.ID)
	}
	if len(order) != 4 || order[len(order)-1] != free.ID {
		t.Fatalf("꺼낸 블록은 맨 뒤: %v", order)
	}
	reversed := []string{order[3], order[2], order[1], order[0]}
	if err := c.OrderBlocks(owner, reversed); err != nil {
		t.Fatal(err)
	}
	if err := c.UpdateBlock(owner, bucket.ID, BlockInput{ShowUpcoming: &done}); err != nil {
		t.Fatal(err)
	}
	space, _ = c.Space(owner)
	if space.Blocks[0].ID != reversed[0] {
		t.Fatalf("블록 순서: %+v", space.Blocks)
	}
	for _, b := range space.Blocks {
		if b.ID == bucket.ID && !strings.Contains(string(b.Config), `"showUpcoming": true`) {
			t.Fatalf("블록 설정: %s", b.Config)
		}
	}

	news, _ := c.News(owner, 50)
	kinds := map[string]bool{}
	for _, n := range news {
		if n.Who == "partner" {
			kinds[n.BlockType+"/"+n.Kind] = true
		}
	}
	for _, want := range []string{"bucket/", "workout/", "workout/uncheck", "free/"} {
		if !kinds[want] {
			t.Fatalf("상대 소식 %s 없음: %v", want, kinds)
		}
	}
	if _, ok := kinds["bucket/done"]; ok {
		t.Fatalf("운영자가 이룬 버킷은 운영자의 소식: %v", kinds)
	}
}

func pngOf(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := 0; x < w; x++ {
		img.Set(x, x%h, color.RGBA{R: 200, A: 255})
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestLocalPhotos(t *testing.T) {
	c, owner, guest := localSpace(t)
	mapBlock, _ := c.AddBlock(owner, "map", "")
	asset, err := c.Upload(guest, mapBlock.ID, "image/png", pngOf(t, 1080, 720))
	if err != nil {
		t.Fatal(err)
	}
	for size, long := range map[string]int{"display": 1080, "thumb": thumbSide} {
		p, err := c.Photo(owner, asset, size)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(p.Body)
		img, _, err := image.Decode(bytes.NewReader(raw))
		if err != nil || img.Bounds().Dx() != long || int64(len(raw)) != p.Size {
			t.Fatalf("%s: %v %v %d", size, err, img.Bounds(), p.Size)
		}
	}
	if _, err := c.Upload(owner, mapBlock.ID, "image/png", []byte("not a photo")); err == nil {
		t.Fatal("그림이 아닌 것은 받지 않는다")
	}

	face, err := c.AddVisit(guest, mapBlock.ID, VisitInput{Region: "11040", Asset: asset, Face: true})
	if err != nil {
		t.Fatal(err)
	}
	other, _ := c.Upload(owner, mapBlock.ID, "image/png", pngOf(t, 40, 40))
	if _, err := c.AddVisit(owner, mapBlock.ID, VisitInput{Region: "11040", Asset: other, Face: true}); err != nil {
		t.Fatal(err)
	}
	rec, err := c.AddRecord(owner, mapBlock.ID, RecordInput{Region: ptr("11040"), Title: ptr("성수 산책")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.AddVisit(owner, mapBlock.ID, VisitInput{Asset: asset, Record: rec.ID}); err != nil {
		t.Fatal(err)
	}
	b, err := c.Block(owner, mapBlock.ID)
	if err != nil {
		t.Fatal(err)
	}
	reps := 0
	for _, v := range b.Visits {
		if v.Rep {
			reps++
			if v.Asset != other {
				t.Fatalf("나중에 고른 얼굴: %+v", v)
			}
		}
		if v.ID == face.ID && v.Region != "11040" {
			t.Fatalf("서울의 구는 다섯 자리로: %+v", v)
		}
	}
	if reps != 1 || len(b.Records) != 1 {
		t.Fatalf("지역의 얼굴은 하나: %+v", b)
	}
	if err := c.DeleteRecord(owner, mapBlock.ID, rec.ID); err != nil {
		t.Fatal(err)
	}
	if b, _ = c.Block(owner, mapBlock.ID); len(b.Visits) != 2 {
		t.Fatalf("기록을 지우면 그 사진도: %+v", b.Visits)
	}

	day := "2026-12-24"
	p1, err := c.AddDayPhoto(guest, DayPhotoInput{Date: day, Asset: asset})
	if err != nil {
		t.Fatal(err)
	}
	p2, _ := c.AddDayPhoto(owner, DayPhotoInput{Date: day, Asset: other})
	if err := c.CoverDayPhoto(owner, p2.ID); err != nil {
		t.Fatal(err)
	}
	photos, _ := c.DayPhotos(owner, day, day)
	if len(photos) != 2 || photos[0].ID != p2.ID || !photos[0].Cover || photos[1].Cover {
		t.Fatalf("그날의 대표: %+v", photos)
	}
	if cover, _ := c.Cover(owner, "2026-12-30"); cover == nil || cover.ID != p2.ID {
		t.Fatalf("카드의 사진: %+v", cover)
	}
	if err := c.DeleteDayPhoto(owner, p1.ID); err != nil {
		t.Fatal(err)
	}

	album, _ := c.AddBlock(owner, "album", "")
	in := AlbumInput{Snap: true}
	in.Photos = append(in.Photos, struct {
		Asset  string `json:"asset"`
		Width  int    `json:"width"`
		Height int    `json:"height"`
	}{Asset: asset, Width: 1080, Height: 720})
	added, err := c.AddAlbumPhotos(guest, album.ID, in)
	if err != nil || len(added) != 1 || added[0].By != "me" {
		t.Fatalf("앨범: %+v %v", added, err)
	}
	if err := c.PinAlbum(guest, album.ID, asset); err != nil {
		t.Fatal(err)
	}
	ab, _ := c.Block(owner, album.ID)
	if !strings.Contains(string(ab.Config), asset) || len(ab.Album) != 1 || ab.Album[0].By != "partner" {
		t.Fatalf("앨범의 최신·고정: %s %+v", ab.Config, ab.Album)
	}
	if err := c.DeleteAlbumPhotos(owner, album.ID, []string{added[0].ID}); err != nil {
		t.Fatal(err)
	}
	ab, _ = c.Block(owner, album.ID)
	if strings.Contains(string(ab.Config), asset) || len(ab.Album) != 0 {
		t.Fatalf("지운 사진은 최신·고정에서도: %s", ab.Config)
	}

	news, _ := c.News(owner, 50)
	kinds := map[string]bool{}
	for _, n := range news {
		if n.Who == "partner" {
			kinds[n.BlockType+"/"+n.Kind] = true
		}
	}
	for _, want := range []string{"map/", "calendar/photo", "album/snap"} {
		if !kinds[want] {
			t.Fatalf("상대 소식 %s 없음: %v", want, kinds)
		}
	}
}

