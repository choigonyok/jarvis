package imgtext

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/choigonyok/jarvis/search-svc/internal/fetch"
)

func TestATallPictureIsCutInOverlappingBands(t *testing.T) {
	got := Pieces(image.Rect(0, 0, 860, 21799))
	if len(got) != 20 {
		t.Fatalf("pieces = %d, want 20 (as the OCR test on the real detail image)", len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i].Min.Y >= got[i-1].Max.Y {
			t.Errorf("piece %d does not overlap the one above: %v %v", i, got[i-1], got[i])
		}
	}
	if last := got[len(got)-1]; last.Max.Y != 21799 {
		t.Errorf("the last piece ends at %d", last.Max.Y)
	}
	if one := Pieces(image.Rect(0, 0, 1080, 1350)); len(one) != 1 {
		t.Errorf("a card news picture was cut: %v", one)
	}
}

func TestDedupeDropsTheOverlapsRepeat(t *testing.T) {
	pieces := []Piece{
		{Lines: []string{"제품 스펙", "날의 길이가 15cm 이상의 도검을 구매할 경우에는"}},
		{Lines: []string{"날의 길이가 15cm 이상의 도검을 구매할 경우에는", "도소증 발급이 필요하며,"}},
	}
	dedupe(pieces)
	if strings.Join(pieces[1].Lines, "|") != "도소증 발급이 필요하며," {
		t.Errorf("second piece = %v", pieces[1].Lines)
	}
}

func pngOf(w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), 100, 255})
		}
	}
	var b bytes.Buffer
	png.Encode(&b, img)
	return b.Bytes()
}

func TestReadSendsEachPieceToTheOCRAndRemembersThePicture(t *testing.T) {
	var calls atomic.Int32
	ocr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" || r.URL.Path != "/ocr" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		body, _ := io.ReadAll(r.Body)
		img, err := jpegConfig(body)
		if err != nil {
			w.WriteHeader(http.StatusUnsupportedMediaType)
			return
		}
		calls.Add(1)
		_ = img
		w.Write([]byte(`{"width":300,"height":420,"lines":[{"text":"보상 표","conf":1},{"text":"흐릿함","conf":0.1}]}`))
	}))
	defer ocr.Close()
	tall := pngOf(300, 1500)
	pics := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Referer") != "https://blog.example.com/post" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Write(tall)
	}))
	defer pics.Close()

	r := &Reader{OCRURL: ocr.URL, Token: "tok", Fetch: fetch.NewAllowingLocal()}
	pic, got, err := r.Read(context.Background(), pics.URL+"/a.png", "https://blog.example.com/post")
	if err != nil {
		t.Fatal(err)
	}
	if pic.Width != 300 || pic.Height != 1500 || len(pic.Pieces) != 5 || int(calls.Load()) != 5 {
		t.Fatalf("picture %dx%d, %d pieces, %d OCR calls", pic.Width, pic.Height, len(pic.Pieces), calls.Load())
	}
	// Every piece said the same line: said once. The unsure line is dropped.
	if pic.Text() != "보상 표" {
		t.Errorf("text = %q", pic.Text())
	}
	if _, _, err := r.Read(context.Background(), pics.URL+"/a.png", "https://blog.example.com/post"); err != nil || calls.Load() != 5 {
		t.Errorf("a picture read before was read again: %d calls, err %v", calls.Load(), err)
	}
	looks, err := Look(got.Raw, []int{0, 4, 9})
	if err != nil || len(looks) != 2 {
		t.Fatalf("look = %d pictures, err %v", len(looks), err)
	}
}

func TestWithoutOCRAPictureIsOnlyListed(t *testing.T) {
	pics := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(pngOf(400, 300)) }))
	defer pics.Close()
	r := &Reader{Fetch: fetch.NewAllowingLocal()}
	pic, _, err := r.Read(context.Background(), pics.URL, "")
	if err != ErrOff || pic.Width != 400 {
		t.Errorf("pic %+v err %v", pic, err)
	}
	small := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(pngOf(40, 40)) }))
	defer small.Close()
	if _, _, err := r.Read(context.Background(), small.URL, ""); err == nil || !strings.Contains(err.Error(), "작은") {
		t.Errorf("an icon should be skipped: %v", err)
	}
}

func jpegConfig(b []byte) (image.Config, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(b))
	return cfg, err
}
