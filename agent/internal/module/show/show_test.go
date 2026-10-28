package show

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/choigonyok/jarvis/agent/internal/thread"
	"github.com/choigonyok/jarvis/agent/internal/uploads"
)

// shown is the thread: what was put in it.
type shown struct {
	mu      sync.Mutex
	images  [][]string
	caption []string
}

func (s *shown) AppendAgentImages(images []string, caption string) *thread.Turn {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.images = append(s.images, images)
	s.caption = append(s.caption, caption)
	return &thread.Turn{Images: images}
}

func pngBytes(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	img.Set(1, 1, color.RGBA{R: 200, A: 255})
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func text(r *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range r.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

// A picture from a page the conversation read is fetched, kept with the
// uploads and shown as a message of its own with its caption; one already
// here is shown as it is.
func TestShowsAPictureFromAReadPageAndOneAlreadyHere(t *testing.T) {
	pic := pngBytes(t)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/big.png" {
			w.Header().Set("Content-Length", "99999999")
		}
		_, _ = w.Write(pic)
	}))
	defer srv.Close()
	up, err := uploads.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	have, err := up.Save(bytes.NewReader(pic))
	if err != nil {
		t.Fatal(err)
	}
	th := &shown{}
	seen := map[string]bool{srv.URL + "/item.png": true, srv.URL + "/big.png": true}
	m := New(up, th, func(u string) bool { return seen[u] }, nil)
	m.client = srv.Client() // the test server is on loopback; publicClient is tested below

	res := m.show(context.Background(), ShowInput{URLs: []string{srv.URL + "/item.png"}, Uploads: []string{have}, Caption: "장작 두 묶음"})
	if res.IsError || !strings.Contains(text(res), "2장") {
		t.Fatalf("보여 줘야 합니다: %s", text(res))
	}
	if len(th.images) != 1 || len(th.images[0]) != 2 || th.caption[0] != "장작 두 묶음" {
		t.Fatalf("대화에 남은 것: %+v %+v", th.images, th.caption)
	}
	fetched := th.images[0][1]
	if fetched == have || !uploads.Valid(fetched) {
		t.Fatalf("웹 사진은 새 이름으로 보관합니다: %q", fetched)
	}
	if b, err := os.ReadFile(filepath.Join(up.Dir, fetched)); err != nil || !bytes.Equal(b, pic) {
		t.Fatalf("보관한 사진: %v", err)
	}

	// Never seen, not https, too big, not there: refused, each saying why.
	for _, c := range []struct {
		in   ShowInput
		want string
	}{
		{ShowInput{URLs: []string{srv.URL + "/made-up.png"}}, "나온 적 없는 주소"},
		{ShowInput{URLs: []string{"http://example.com/a.png"}}, "https 주소만"},
		{ShowInput{URLs: []string{srv.URL + "/big.png"}}, "너무 큽니다"},
		{ShowInput{Uploads: []string{"0123456789abcdef0123456789abcdef.jpg"}}, "그런 사진이 없습니다"},
		{ShowInput{Uploads: []string{"../../etc/passwd"}}, "그런 사진이 없습니다"},
		{ShowInput{}, "보여 줄 사진이 없습니다"},
		{ShowInput{Uploads: []string{have, have, have, have, have}}, "4장까지"},
	} {
		res := m.show(context.Background(), c.in)
		if !res.IsError || !strings.Contains(text(res), c.want) {
			t.Fatalf("%+v: %s", c.in, text(res))
		}
	}
	if len(th.images) != 1 {
		t.Fatalf("거절한 것은 대화에 남지 않습니다: %+v", th.images)
	}
}

// Nothing inside this network is fetched, whatever the address says.
func TestOnlyThePublicInternet(t *testing.T) {
	for addr, want := range map[string]bool{
		"8.8.8.8": true, "2606:4700:4700::1111": true, "211.249.220.24": true,
		"127.0.0.1": false, "10.0.0.5": false, "172.18.0.4": false, "192.168.0.104": false, "169.254.169.254": false,
		"100.101.102.103": false, "0.0.0.0": false, "::1": false, "fd7a:115c:a1e0::1": false, "::ffff:127.0.0.1": false,
	} {
		if got := public(netip.MustParseAddr(addr)); got != want {
			t.Fatalf("%s: %v", addr, got)
		}
	}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(pngBytes(t)) }))
	defer srv.Close()
	up, _ := uploads.New(t.TempDir())
	m := New(up, &shown{}, func(string) bool { return true }, nil)
	res := m.show(context.Background(), ShowInput{URLs: []string{srv.URL + "/a.png"}})
	if !res.IsError || !strings.Contains(text(res), "내부 주소") {
		t.Fatalf("루프백은 열지 않습니다: %s", text(res))
	}
}
