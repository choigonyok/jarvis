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
