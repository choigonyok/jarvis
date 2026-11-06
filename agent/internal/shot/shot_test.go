package shot

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// bridge is the browser bridge as far as a picture goes: browser-use's
// screenshot, a line of text and a PNG, behind the bridge's token.
func bridge(t *testing.T, height int, asked *map[string]any) *httptest.Server {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 40, height))
	for y := range height {
		img.Set(1, y, color.RGBA{R: 200, A: 255})
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "mcp-browser", Version: "1"}, nil)
	server.AddTool(&mcp.Tool{Name: Tool, InputSchema: map[string]any{"type": "object"}}, func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		_ = json.Unmarshal(req.Params.Arguments, asked)
		return &mcp.CallToolResult{Content: []mcp.Content{
			&mcp.TextContent{Text: `{"viewport":{"width":40}}`},
			&mcp.ImageContent{Data: b.Bytes(), MIMEType: "image/png"},
		}}, nil
	})
	h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		h.ServeHTTP(w, r)
	}))
}

func TestTakeIsAJPEGOfThePage(t *testing.T) {
	var asked map[string]any
	srv := bridge(t, 30, &asked)
	defer srv.Close()
	got, err := New(srv.URL, "secret").Take(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) < 3 || got[0] != 0xFF || got[1] != 0xD8 {
		t.Fatalf("JPEG 이어야 합니다: % x", got[:min(4, len(got))])
	}
	if asked["full_page"] != true {
		t.Fatalf("전체 페이지를 물었어야 합니다: %v", asked)
	}
	if _, err := New(srv.URL, "wrong").Take(context.Background(), false); err == nil {
		t.Fatal("토큰이 틀리면 찍지 못해야 합니다")
	}
}

func TestATallPageIsCut(t *testing.T) {
	var asked map[string]any
	srv := bridge(t, MaxHeight+500, &asked)
	defer srv.Close()
	got, err := New(srv.URL, "secret").Take(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(got))
	if err != nil || cfg.Height != MaxHeight {
		t.Fatalf("높이 %d (%v)", cfg.Height, err)
	}
}
