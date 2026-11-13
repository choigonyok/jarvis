package photos

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/choigonyok/jarvis/agent/internal/uploads"
)

// A 1x1 PNG, what a preview is here.
var png = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89\x00\x00\x00\rIDATx\x9cc\xf8\x0f\x00\x00\x01\x01\x00\x05\x18\xd8N\x00\x00\x00\x00IEND\xaeB`\x82")

type fake struct {
	searches []string
	previews int
	windows  []Window
}

func (f *fake) serve(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("토큰이 없습니다: %s", r.URL)
		}
		switch {
		case r.URL.Path == "/search":
			f.searches = append(f.searches, r.URL.RawQuery)
			items := []Found{}
			for _, id := range []string{"p1", "p2", "p3", "p4", "p5"} {
				items = append(items, Found{ID: id, At: "2026-09-23T14:02:11.000Z", City: "Gapyeong", Favorite: id == "p1"})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"items": items})
		case strings.HasSuffix(r.URL.Path, "/preview"):
			f.previews++
			_, _ = w.Write(png)
		case r.URL.Path == "/api/photos/when":
			_ = json.NewEncoder(w).Encode(map[string]any{"windows": f.windows})
		default:
			t.Errorf("모르는 경로 %s", r.URL)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, srv.URL
}

func module(t *testing.T, base string) *Module {
	t.Helper()
	up, err := uploads.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return New(base, base, "tok", up, nil)
}

// By what is in them: the first few are fetched to look at, and the answer says
// to look before saying so.
func TestSearchBringsThePreviews(t *testing.T) {
	f := &fake{}
	_, base := f.serve(t)
	m := module(t, base)
	out, err := m.search(context.Background(), SearchIn{Query: "서버 랙", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.searches[0], "q=%EC%84%9C%EB%B2%84") || f.previews != previewed {
		t.Fatalf("%v %d", f.searches, f.previews)
	}
	for _, want := range []string{"2026-09-23 14:02 · Gapyeong · 즐겨찾기", "이름 ", "경로 ", "id p5", "show_image", "Read"} {
		if !strings.Contains(out, want) {
			t.Fatalf("%q 가 없습니다:\n%s", want, out)
		}
	}
	line := strings.Split(out, "\n")[0]
	path := line[strings.Index(line, "경로 ")+len("경로 "):]
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("미리보기가 파일로 있어야 합니다: %v", err)
	}
	// The same photo again is the same file.
	if _, err := m.search(context.Background(), SearchIn{Query: "서버 랙", Limit: 5}); err != nil {
		t.Fatal(err)
	}
	if f.previews != previewed {
		t.Fatalf("다시 가져오면 안 됩니다: %d", f.previews)
	}
}

// By a place: the days it was visited are what is looked through, and a place
// with no visit on record falls back to what is in the photos.
func TestSearchByPlace(t *testing.T) {
	f := &fake{windows: []Window{{Title: "양평캠핑", From: "2026-10-24", To: "2026-10-25"}}}
	_, base := f.serve(t)
	m := module(t, base)
	out, err := m.search(context.Background(), SearchIn{Place: "양평캠핑"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.searches[0], "from=2026-10-24") || !strings.Contains(f.searches[0], "to=2026-10-25") || !strings.Contains(out, "양평캠핑 (2026-10-24~2026-10-25): 사진 5장") {
		t.Fatalf("%v\n%s", f.searches, out)
	}
	f.windows = nil
	f.searches = nil
	out, err = m.search(context.Background(), SearchIn{Place: "모르는 곳"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.searches[0], "q=") || !strings.Contains(out, "간 기록이 없어") {
		t.Fatalf("%v\n%s", f.searches, out)
	}
}

func TestSearchAsksForSomething(t *testing.T) {
	f := &fake{}
	_, base := f.serve(t)
	if _, err := module(t, base).search(context.Background(), SearchIn{}); err == nil {
		t.Fatal("아무것도 주지 않으면 말해야 합니다")
	}
}

// Photos a search listed by id open by those ids, as many at once as a search
// opens: nothing is searched, a photo opened before is the same file, what is
// past the first few is said, and an id of another shape is not asked for.
func TestOpenByIDs(t *testing.T) {
	f := &fake{}
	_, base := f.serve(t)
	m := module(t, base)
	listed, err := m.search(context.Background(), SearchIn{Query: "캠핑", Limit: 6})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(listed, "id p5") || !strings.Contains(listed, "ids 로 다시 부르면") {
		t.Fatalf("id 만 나온 사진은 ids 로 열 수 있다고 말해야 합니다:\n%s", listed)
	}
	searched, fetched := len(f.searches), f.previews

	out, err := m.search(context.Background(), SearchIn{IDs: []string{"p5", " p6 ", "p5"}, Query: "무시"})
	if err != nil {
		t.Fatal(err)
	}
	if len(f.searches) != searched || f.previews != fetched+2 {
		t.Fatalf("찾지 않고 두 장만 가져와야 합니다: 검색 %d, 미리보기 %d", len(f.searches)-searched, f.previews-fetched)
	}
	for _, want := range []string{"id p5 · 이름 ", "id p6 · 이름 ", "경로 ", "Read", "show_image"} {
		if !strings.Contains(out, want) {
			t.Fatalf("%q 가 없습니다:\n%s", want, out)
		}
	}
	// One already opened is the same file; a search's first few too.
	if _, err := m.search(context.Background(), SearchIn{IDs: []string{"p5", "p1"}}); err != nil || f.previews != fetched+2 {
		t.Fatalf("이미 가져온 사진은 다시 받지 않습니다: %v %d", err, f.previews-fetched)
	}

	out, err = m.search(context.Background(), SearchIn{IDs: []string{"a1", "a2", "a3", "a4", "a5", "a6"}})
	if err != nil {
		t.Fatal(err)
	}
	if f.previews != fetched+2+previewed || !strings.Contains(out, "열지 않은 id: a5, a6") {
		t.Fatalf("한 번에 %d장, 나머지는 말해야 합니다 (%d):\n%s", previewed, f.previews-fetched, out)
	}
	for _, bad := range [][]string{{"../search"}, {"p1?x=1"}, {" "}} {
		if _, err := m.search(context.Background(), SearchIn{IDs: bad}); err == nil {
			t.Errorf("%q 는 열지 않아야 합니다", bad)
		}
	}
}
