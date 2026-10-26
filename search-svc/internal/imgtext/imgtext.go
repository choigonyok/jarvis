// Package imgtext reads what a page's pictures say.
//
// Korean pages put the facts in pictures: an event's rewards on a card, a
// product's size and material in a detail image 20,000 pixels tall. Each
// picture is fetched, cut into pieces when it is tall (a reader of a whole
// 860×21,799 image sees it shrunk to a smear - four lines out of 158 in a
// test), and every piece is read by the OCR on the Mac mini (ocr/, Apple
// Vision, Korean and English). What a piece says is kept by the picture's
// hash, so a page read again is not read again.
//
// The pieces are also what a model can be shown (Look) when the words are
// not enough - a number on a diagram is only a length when you see the line
// it labels.
package imgtext

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	_ "image/gif"
	_ "image/png"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"

	"github.com/choigonyok/jarvis/search-svc/internal/fetch"
)

const (
	// A picture is cut when it is taller than tallRatio times its width,
	// into pieces pieceRatio times as tall as wide, each overlapping the
	// next by overlap pixels so a line cut by one is whole in the other.
	tallRatio  = 1.6
	pieceRatio = 1.4
	overlap    = 80
	// Wider than this, a piece is scaled down before it is read.
	maxReadWidth = 2000
	// A picture this small is an icon, a bullet, a button.
	minSide = 120
	// Decompression bombs: a picture with more pixels than this is refused.
	maxPixels = 80_000_000
	// What a model is shown: its long side at most this.
	lookEdge = 1400
	// A picture is read this many pieces deep at most (40 pieces of an
	// 860-wide detail image are 45,000 pixels).
	maxPieces = 40
)

// Picture is one picture, read.
type Picture struct {
	URL           string
	Width, Height int
	Pieces        []Piece
	// Err is why it was not read, when it was not.
	Err string
}

// Piece is a band of the picture, top to bottom, and what it says.
type Piece struct {
	Top, Bottom int
	Lines       []string
}

// Text is everything the picture says, overlap lines said once.
func (p Picture) Text() string {
	var out []string
	for _, pc := range p.Pieces {
		out = append(out, pc.Lines...)
	}
	return strings.Join(out, "\n")
}

// Reader fetches pictures and has them read.
type Reader struct {
	// OCRURL is the OCR service (http://host.docker.internal:8107); empty
	// turns reading off - pictures are then only listed.
	OCRURL string
	Token  string
	HTTP   *http.Client
	Fetch  *fetch.Fetcher

	mu    sync.Mutex
	cache map[[32]byte]cached
	order [][32]byte
}

type cached struct {
	w, h   int
	pieces []Piece
}

const cacheSize = 400

// ErrOff is a picture that was not read because no OCR is configured.
var ErrOff = errors.New("그림 글자 읽기가 꺼져 있습니다(OCR 없음)")

// Fetched is a picture's bytes, kept by the caller for Look.
type Fetched struct {
	Raw []byte
}

// Read fetches one picture and reads it. referer is the page it is on -
// image hosts that refuse other sites' pages look at it.
func (r *Reader) Read(ctx context.Context, url, referer string) (Picture, Fetched, error) {
	pic := Picture{URL: url}
	res, err := r.Fetch.Get(ctx, url, fetch.Options{
		Referer: referer,
		// No AVIF: nothing here decodes it.
		Accept:   "image/webp,image/png,image/jpeg,image/gif;q=0.9,*/*;q=0.5",
		MaxBytes: 15 << 20,
		Strict:   true,
	})
	if err != nil {
		return pic, Fetched{}, err
	}
	if res.Status != http.StatusOK {
		return pic, Fetched{}, fmt.Errorf("그림을 받지 못했습니다(%d)", res.Status)
	}
	raw := res.Body
	cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return pic, Fetched{}, fmt.Errorf("그림 형식을 읽지 못했습니다: %w", err)
	}
	pic.Width, pic.Height = cfg.Width, cfg.Height
	if cfg.Width*cfg.Height > maxPixels {
		return pic, Fetched{}, errors.New("그림이 너무 큽니다")
	}
	if cfg.Width < minSide || cfg.Height < minSide {
		return pic, Fetched{Raw: raw}, errors.New("작은 그림이라 읽지 않았습니다")
	}
	key := sha256.Sum256(raw)
	if c, ok := r.get(key); ok {
		pic.Pieces = c.pieces
		return pic, Fetched{Raw: raw}, nil
	}
	if r.OCRURL == "" {
		return pic, Fetched{Raw: raw}, ErrOff
	}
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return pic, Fetched{}, fmt.Errorf("그림을 풀지 못했습니다: %w", err)
	}
	rects := Pieces(img.Bounds())
	cut := len(rects) > maxPieces
	if cut {
		rects = rects[:maxPieces]
	}
	pieces := make([]Piece, len(rects))
	errs := make([]error, len(rects))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for i, rect := range rects {
		wg.Add(1)
		go func(i int, rect image.Rectangle) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			jpg, err := encode(img, rect, maxReadWidth, 90)
			if err != nil {
				errs[i] = err
				return
			}
			lines, err := r.ocr(ctx, jpg)
			pieces[i] = Piece{Top: rect.Min.Y - img.Bounds().Min.Y, Bottom: rect.Max.Y - img.Bounds().Min.Y, Lines: lines}
			errs[i] = err
		}(i, rect)
	}
	wg.Wait()
	var failed error
	read := 0
	for _, err := range errs {
		if err == nil {
			read++
		} else if failed == nil {
			failed = err
		}
	}
	if read == 0 {
		return pic, Fetched{Raw: raw}, failed
	}
	dedupe(pieces)
	pic.Pieces = pieces
	switch {
	case failed != nil:
		// Some pieces said nothing because they were not read: not kept, so
		// the next read tries them again.
		pic.Err = fmt.Sprintf("%d조각 중 %d조각만 읽었습니다: %v", len(rects), read, failed)
	case cut:
		pic.Err = fmt.Sprintf("너무 길어서 위쪽 %d조각만 읽었습니다", maxPieces)
		r.put(key, cached{w: pic.Width, h: pic.Height, pieces: pieces})
	default:
		r.put(key, cached{w: pic.Width, h: pic.Height, pieces: pieces})
	}
	return pic, Fetched{Raw: raw}, nil
}

// Pieces is how a picture is cut: whole, or in overlapping bands down a tall
// one.
func Pieces(b image.Rectangle) []image.Rectangle {
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 || float64(h) <= tallRatio*float64(w) {
		return []image.Rectangle{b}
	}
	piece := int(pieceRatio * float64(w))
	step := piece - overlap
	if step < piece/2 {
		step = piece / 2
	}
	var out []image.Rectangle
	for y := 0; ; y += step {
		bottom := min(y+piece, h)
		out = append(out, image.Rect(b.Min.X, b.Min.Y+y, b.Max.X, b.Min.Y+bottom))
		if bottom >= h {
			break
		}
	}
	return out
}

// encode is one band of a picture as a JPEG, scaled so its width is at most
// maxWidth (and, for a model's eyes, its long side at most maxEdge).
func encode(img image.Image, rect image.Rectangle, maxWidth, quality int) ([]byte, error) {
	sub, ok := img.(interface {
		SubImage(image.Rectangle) image.Image
	})
	var part image.Image = img
	if ok {
		part = sub.SubImage(rect)
	} else {
		dst := image.NewRGBA(image.Rect(0, 0, rect.Dx(), rect.Dy()))
		draw.Draw(dst, dst.Bounds(), img, rect.Min, draw.Src)
		part = dst
	}
	if w := part.Bounds().Dx(); w > maxWidth {
		h := part.Bounds().Dy() * maxWidth / w
		dst := image.NewRGBA(image.Rect(0, 0, maxWidth, max(h, 1)))
		draw.ApproxBiLinear.Scale(dst, dst.Bounds(), part, part.Bounds(), draw.Src, nil)
		part = dst
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, part, &jpeg.Options{Quality: quality}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Look is the chosen pieces of a picture as JPEGs a model can be shown.
func Look(raw []byte, which []int) ([][]byte, error) {
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	rects := Pieces(img.Bounds())
	var out [][]byte
	for _, i := range which {
		if i < 0 || i >= len(rects) {
			continue
		}
		r := rects[i]
		width := r.Dx()
		if long := max(r.Dx(), r.Dy()); long > lookEdge {
			width = r.Dx() * lookEdge / long
		}
		jpg, err := encode(img, r, max(width, 1), 80)
		if err != nil {
			return nil, err
		}
		out = append(out, jpg)
	}
	return out, nil
}

// dedupe drops the lines a piece repeats from the overlap with the piece
// above it.
func dedupe(pieces []Piece) {
	// The piece above as it was read, not as already trimmed: its own
	// overlap with the one above it is still the bottom of what it saw.
	var prev []string
	for i := range pieces {
		read := pieces[i].Lines
		if i > 0 {
			tail := prev[max(0, len(prev)-4):]
			lines := read
			for len(lines) > 0 && containsLine(tail, lines[0]) {
				lines = lines[1:]
			}
			pieces[i].Lines = lines
		}
		prev = read
	}
}

func containsLine(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func (r *Reader) ocr(ctx context.Context, jpg []byte) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(r.OCRURL, "/")+"/ocr", bytes.NewReader(jpg))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "image/jpeg")
	req.Header.Set("Authorization", "Bearer "+r.Token)
	client := r.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("글자 읽기(OCR)에 연결하지 못했습니다: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("글자 읽기(OCR)가 %d 로 답했습니다: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var out struct {
		Lines []struct {
			Text string  `json:"text"`
			Conf float64 `json:"conf"`
		} `json:"lines"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	var lines []string
	for _, l := range out.Lines {
		if t := strings.TrimSpace(l.Text); t != "" && l.Conf >= 0.3 {
			lines = append(lines, t)
		}
	}
	return lines, nil
}

func (r *Reader) get(k [32]byte) (cached, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.cache[k]
	return c, ok
}

func (r *Reader) put(k [32]byte, c cached) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cache == nil {
		r.cache = map[[32]byte]cached{}
	}
	if _, ok := r.cache[k]; !ok {
		r.order = append(r.order, k)
	}
	r.cache[k] = c
	for len(r.order) > cacheSize {
		delete(r.cache, r.order[0])
		r.order = r.order[1:]
	}
}
