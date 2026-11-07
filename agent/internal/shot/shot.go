// Package shot takes a picture of the shared browser's page, for a person to
// see: the conversation showing what a job has on its screen, a job showing
// the order it just placed. The model's own browser_screenshot shows the page
// to the model and to nobody else; this one is kept as a photo.
//
// It asks the browser bridge itself, as one more client of its MCP: the CLI's
// tools go to the bridge directly, so their pictures never pass here. It only
// looks - nothing is pressed or opened - so it does not take the browser from
// whoever has it.
package shot

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png" // what browser-use answers with
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Tool is the bridge's (browser-use's) screenshot.
const Tool = "browser_screenshot"

// MaxHeight is how tall a full page is kept: below it is cut.
const MaxHeight = 6000

// Camera takes the browser's picture through the bridge at URL.
type Camera struct {
	URL   string
	Token string
	// Wait bounds one picture.
	Wait time.Duration
}

// New is a camera on the bridge, or nil without one.
func New(url, token string) *Camera {
	if url == "" {
		return nil
	}
	return &Camera{URL: url, Token: token, Wait: 30 * time.Second}
}

type bearer struct {
	token string
	next  http.RoundTripper
}

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	if b.token != "" {
		r.Header.Set("Authorization", "Bearer "+b.token)
	}
	return b.next.RoundTrip(r)
}

// Take is the page as it is now, as a JPEG: the part on screen, or with full
// the whole page down to MaxHeight.
func (c *Camera) Take(ctx context.Context, full bool) ([]byte, error) {
	if c == nil {
		return nil, errors.New("이 에이전트에는 브라우저가 없습니다")
	}
	ctx, cancel := context.WithTimeout(ctx, c.Wait)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "jarvis-shot", Version: "1"}, nil)
	transport := &mcp.StreamableClientTransport{
		Endpoint:             c.URL,
		HTTPClient:           &http.Client{Transport: bearer{c.Token, http.DefaultTransport}},
		MaxRetries:           -1,
		DisableStandaloneSSE: true,
	}
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return nil, fmt.Errorf("브라우저에 닿지 못했습니다: %w", err)
	}
	defer session.Close()
	res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: Tool, Arguments: map[string]any{"full_page": full}})
	if err != nil {
		return nil, fmt.Errorf("화면을 찍지 못했습니다: %w", err)
	}
	if res.IsError {
		return nil, fmt.Errorf("화면을 찍지 못했습니다: %s", text(res))
	}
	for _, c := range res.Content {
		if img, ok := c.(*mcp.ImageContent); ok && len(img.Data) > 0 {
			return smaller(img.Data)
		}
	}
	return nil, fmt.Errorf("브라우저가 사진을 주지 않았습니다: %s", text(res))
}

func text(res *mcp.CallToolResult) string {
	for _, c := range res.Content {
		if t, ok := c.(*mcp.TextContent); ok {
			if len(t.Text) > 200 {
				return t.Text[:200]
			}
			return t.Text
		}
	}
	return "답 없음"
}

// smaller is a picture as a JPEG a phone opens quickly: a screen's PNG is
// several times the size, and a full page is cut at MaxHeight.
func smaller(raw []byte) ([]byte, error) {
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("사진을 읽지 못했습니다: %w", err)
	}
	if b := img.Bounds(); b.Dy() > MaxHeight {
		if s, ok := img.(interface {
			SubImage(r image.Rectangle) image.Image
		}); ok {
			img = s.SubImage(image.Rect(b.Min.X, b.Min.Y, b.Max.X, b.Min.Y+MaxHeight))
		}
	}
	var out bytes.Buffer
	if err := jpeg.Encode(&out, img, &jpeg.Options{Quality: 82}); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
