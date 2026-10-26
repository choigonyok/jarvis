// Package api serves search-svc to the agent, behind one token.
//
//	GET  /health   {"ok", "configured", "naver", "ocr"} - no token
//	POST /search   {query, sources, since, sort, limit} → hits, and what each source was asked and gave
//	POST /read     {url, focus, offset, max, look, pictures} → the page as text, its pictures read in place
//	POST /places   {query, lat, lng, radius, open, limit, area} → Naver Place's list around a point
//
// What comes back is data for a model to read: the agent words it. Nothing
// here calls a model - what a picture means beyond its words is for the
// model the agent runs, shown the picture (look).
package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/choigonyok/jarvis/search-svc/internal/fetch"
	"github.com/choigonyok/jarvis/search-svc/internal/find"
	"github.com/choigonyok/jarvis/search-svc/internal/imgtext"
	"github.com/choigonyok/jarvis/search-svc/internal/naver"
	"github.com/choigonyok/jarvis/search-svc/internal/page"
	"github.com/choigonyok/jarvis/search-svc/internal/place"
)

type Server struct {
	// Token is what the agent sends. Empty leaves every route but /health
	// shut: the service is up but off.
	Token  string
	Naver  *naver.Client
	Fetch  *fetch.Fetcher
	Images *imgtext.Reader
	Log    *slog.Logger
	// OCRHealth says whether the OCR answers (cached by the caller).
	OCRHealth func(ctx context.Context) bool

	mu    sync.Mutex
	pages map[string]cachedPage
}

type cachedPage struct {
	at  time.Time
	doc readDoc
}

const (
	pageTTL         = 10 * time.Minute
	maxCachedPages  = 200
	defaultMax      = 6000
	focusedMax      = 4000
	maxMax          = 30000
	defaultPictures = 8
	maxPictures     = 20
	maxLooks        = 4
)

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.health)
	mux.HandleFunc("POST /search", s.guard(s.search))
	mux.HandleFunc("POST /read", s.guard(s.read))
	mux.HandleFunc("POST /places", s.guard(s.places))
	return mux
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	ocr := s.OCRHealth != nil && s.OCRHealth(ctx)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "configured": s.Token != "", "naver": s.Naver.Configured(), "ocr": ocr})
}

func (s *Server) guard(next func(http.ResponseWriter, *http.Request)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.Token == "" {
			writeErr(w, http.StatusServiceUnavailable, "검색 서비스가 꺼져 있습니다(JARVIS_SEARCH_TOKEN 없음)")
			return
		}
		got := r.Header.Get("Authorization")
		if subtle.ConstantTimeCompare([]byte(got), []byte("Bearer "+s.Token)) != 1 {
			writeErr(w, http.StatusUnauthorized, "토큰이 맞지 않습니다")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
		next(w, r)
	}
}

// --- search -----------------------------------------------------------------

type searchReq struct {
	Query   string   `json:"query"`
	Sources []string `json:"sources"`
	// Since is "24h", "7d", "2w", or a day (2026-10-01).
	Since string `json:"since"`
	Sort  string `json:"sort"`
	Limit int    `json:"limit"`
}

func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	var in searchReq
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "요청을 읽지 못했습니다")
		return
	}
	since, err := Since(in.Since, time.Now())
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if !s.Naver.Configured() {
		writeErr(w, http.StatusServiceUnavailable, naver.ErrNoKey.Error())
		return
	}
	start := time.Now()
	hits, covs, err := find.Search(r.Context(), s.Naver, find.Query{Text: in.Query, Sources: in.Sources, Since: since, Sort: in.Sort, Limit: in.Limit})
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.Log.Info("검색", "sources", len(covs), "hits", len(hits), "ms", time.Since(start).Milliseconds())
	out := map[string]any{"query": in.Query, "hits": hits, "checked": covs}
	if !since.IsZero() {
		out["since"] = since.Format(time.RFC3339)
	}
	writeJSON(w, http.StatusOK, out)
}

var relative = regexp.MustCompile(`^(\d+)\s*([hdwm])$`)

// Since reads a search's "since": a span back from now, or a day.
func Since(v string, now time.Time) (time.Time, error) {
	v = strings.ToLower(strings.TrimSpace(v))
	if v == "" {
		return time.Time{}, nil
	}
	if m := relative.FindStringSubmatch(v); m != nil {
		n, _ := strconv.Atoi(m[1])
		switch m[2] {
		case "h":
			return now.Add(-time.Duration(n) * time.Hour), nil
		case "d":
			return now.AddDate(0, 0, -n), nil
		case "w":
			return now.AddDate(0, 0, -7*n), nil
		case "m":
			return now.AddDate(0, -n, 0), nil
		}
	}
	if t := page.ParseTime(v); !t.IsZero() {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("since %q 를 읽지 못했습니다 - 24h, 7d, 2w, 1m 이나 2026-10-01 처럼 주세요", v)
}

// --- read -------------------------------------------------------------------

type readReq struct {
	URL      string `json:"url"`
	Focus    string `json:"focus"`
	Offset   int    `json:"offset"`
	Max      int    `json:"max"`
	Look     bool   `json:"look"`
	Pictures int    `json:"pictures"`
}

type pictureOut struct {
	N      int    `json:"n"`
	URL    string `json:"url"`
	Width  int    `json:"width,omitempty"`
	Height int    `json:"height,omitempty"`
	Pieces int    `json:"pieces,omitempty"`
	Read   bool   `json:"read"`
	Words  int    `json:"chars,omitempty"`
	Error  string `json:"error,omitempty"`
}

type lookOut struct {
	Picture int    `json:"picture"`
	Piece   int    `json:"piece"`
	JPEG    []byte `json:"jpeg"`
}

// readDoc is a page read, before it is cut to size: what is cached.
type readDoc struct {
	Final     string
	Title     string
	Site      string
	Published time.Time
	Chunks    []chunk
	Pictures  []pictureOut
	Pieces    map[int][]imgtext.Piece // picture n → its pieces, for look
	Links     []page.Link
}

// chunk is a stretch of the page's text: a paragraph, or a few lines of
// what a picture says (Picture > 0; Piece is the piece, -1 a picture's end
// or a note, -2 a run of pictures with no words).
type chunk struct {
	Text    string
	Picture int
	Piece   int
}

func (s *Server) read(w http.ResponseWriter, r *http.Request) {
	var in readReq
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "요청을 읽지 못했습니다")
		return
	}
	u, err := url.Parse(strings.TrimSpace(in.URL))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "주소를 읽지 못했습니다")
		return
	}
	if err := s.Fetch.Check(u); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	pictures := in.Pictures
	if pictures <= 0 {
		pictures = defaultPictures
	}
	pictures = min(pictures, maxPictures)
	start := time.Now()
	key := u.String() + "#" + strconv.Itoa(pictures)
	doc, ok := s.cached(key)
	if !ok {
		doc, err = s.readPage(r.Context(), u, pictures)
		if err != nil {
			writeErr(w, http.StatusBadGateway, err.Error())
			return
		}
		s.keep(key, doc)
	}
	out := cut(doc, in)
	out["url"] = in.URL
	if in.Look {
		out["looks"] = s.looks(r.Context(), doc, in.Focus)
	}
	s.Log.Info("읽기", "host", u.Hostname(), "chars", out["chars"], "pictures", len(doc.Pictures), "cached", ok, "ms", time.Since(start).Milliseconds())
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) readPage(ctx context.Context, u *url.URL, pictures int) (readDoc, error) {
	src, mobile := page.Source(u)
	res, err := s.Fetch.Get(ctx, src.String(), fetch.Options{Mobile: mobile})
	if err != nil {
		return readDoc{}, err
	}
	if res.Status != http.StatusOK {
		return readDoc{}, fmt.Errorf("%s 가 %d 로 답했습니다", res.URL.Host, res.Status)
	}
	d, err := page.Parse(res.Body, res.ContentType, res.URL)
	if err != nil {
		return readDoc{}, err
	}
	doc := readDoc{Final: res.URL.String(), Title: d.Title, Site: d.Site, Published: d.Published, Links: d.Links, Pieces: map[int][]imgtext.Piece{}}

	// The pictures, read in order, two at a time (each sends four pieces at
	// a time to the OCR).
	type job struct {
		n   int
		img *page.Image
	}
	var jobs []job
	n := 0
	for _, b := range d.Blocks {
		if b.Image != nil {
			n++
			jobs = append(jobs, job{n, b.Image})
		}
	}
	read := map[int]imgtext.Picture{}
	errs := map[int]string{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 2)
	for _, j := range jobs {
		if j.n > pictures {
			break
		}
		wg.Add(1)
		go func(j job) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			pic, _, err := s.Images.Read(ctx, j.img.URL, res.URL.String())
			mu.Lock()
			defer mu.Unlock()
			read[j.n] = pic
			switch {
			case err != nil:
				errs[j.n] = err.Error()
			case pic.Err != "":
				errs[j.n] = pic.Err
			}
		}(j)
	}
	wg.Wait()

	n = 0
	for _, b := range d.Blocks {
		if b.Image == nil {
			doc.Chunks = append(doc.Chunks, chunk{Text: b.Text})
			continue
		}
		n++
		po := pictureOut{N: n, URL: b.Image.URL, Width: b.Image.Width, Height: b.Image.Height}
		pic, wasRead := read[n]
		if wasRead {
			if pic.Width > 0 {
				po.Width, po.Height = pic.Width, pic.Height
			}
			po.Pieces = len(pic.Pieces)
			po.Read = len(pic.Pieces) > 0
			po.Error = errs[n]
			doc.Pieces[n] = pic.Pieces
		} else {
			po.Error = fmt.Sprintf("그림이 많아 앞 %d장만 읽었습니다", pictures)
		}
		text := pic.Text()
		po.Words = utf8.RuneCountInString(text)
		doc.Pictures = append(doc.Pictures, po)
		switch {
		case po.Read && text != "":
			// What a picture says, in bands of a few lines - a band is what
			// a focused read picks or leaves.
			head := fmt.Sprintf("[그림 %d · 그림 속 글자]", n)
			for pi, pc := range pic.Pieces {
				for k := 0; k < len(pc.Lines); k += 12 {
					lines := pc.Lines[k:min(k+12, len(pc.Lines))]
					body := strings.Join(lines, "\n")
					if head != "" {
						body, head = head+"\n"+body, ""
					}
					doc.Chunks = append(doc.Chunks, chunk{Text: body, Picture: n, Piece: pi})
				}
			}
			doc.Chunks = append(doc.Chunks, chunk{Text: fmt.Sprintf("[그림 %d 끝]", n), Picture: n, Piece: -1})
		case po.Read:
			// A run of pictures with no words (product photos) is one line.
			if k := len(doc.Chunks) - 1; k >= 0 && doc.Chunks[k].Piece == -2 {
				first := doc.Chunks[k].Picture
				doc.Chunks[k].Text = fmt.Sprintf("[그림 %d~%d · 글자 없음]", first, n)
			} else {
				doc.Chunks = append(doc.Chunks, chunk{Text: fmt.Sprintf("[그림 %d · 글자 없음]", n), Picture: n, Piece: -2})
			}
		default:
			doc.Chunks = append(doc.Chunks, chunk{Text: fmt.Sprintf("[그림 %d · 읽지 않음]", n), Picture: n, Piece: -1})
		}
	}
	return doc, nil
}

// cut is the page sized for one answer: from an offset, or - for a long
// page read with a focus - the parts about it.
func cut(doc readDoc, in readReq) map[string]any {
	full := joinChunks(doc.Chunks)
	total := utf8.RuneCountInString(full)
	max := in.Max
	if max <= 0 {
		max = defaultMax
		if strings.TrimSpace(in.Focus) != "" {
			max = focusedMax
		}
	}
	max = min(max, maxMax)
	out := map[string]any{"finalUrl": doc.Final, "title": doc.Title, "chars": total, "pictures": doc.Pictures}
	if doc.Site != "" {
		out["site"] = doc.Site
	}
	if !doc.Published.IsZero() {
		out["published"] = doc.Published.Format(time.RFC3339)
	}
	if len(doc.Links) > 0 {
		out["links"] = doc.Links
	}
	terms := focusTerms(in.Focus)
	switch {
	case in.Offset > 0:
		runes := []rune(full)
		from := min(in.Offset, len(runes))
		to := min(from+max, len(runes))
		out["offset"], out["text"] = from, string(runes[from:to])
		if to < len(runes) {
			out["next"] = to
		}
	case total > max && len(terms) > 0:
		out["text"], out["focused"] = focused(doc.Chunks, terms, max), true
		out["note"] = fmt.Sprintf("'%s'와 관련된 부분만 골랐습니다(전체 %d자). 전체를 차례로 읽으려면 focus 없이 offset 0 부터 읽으세요.", strings.TrimSpace(in.Focus), total)
	default:
		runes := []rune(full)
		to := min(max, len(runes))
		out["offset"], out["text"] = 0, string(runes[:to])
		if to < len(runes) {
			out["next"] = to
		}
	}
	return out
}

func joinChunks(cs []chunk) string {
	var parts []string
	for _, c := range cs {
		parts = append(parts, c.Text)
	}
	return strings.Join(parts, "\n\n")
}

// focused picks the chunks with the most of the focus's words, keeps the
// page's opening for context, and gives them back in page order.
func focused(cs []chunk, terms []string, max int) string {
	type scored struct {
		i     int
		score int
	}
	var ranked []scored
	for i, c := range cs {
		if sc := score(c.Text, terms); sc > 0 {
			ranked = append(ranked, scored{i, sc})
		}
	}
	sort.SliceStable(ranked, func(a, b int) bool { return ranked[a].score > ranked[b].score })
	pick := map[int]bool{}
	used := 0
	add := func(i int) bool {
		n := utf8.RuneCountInString(cs[i].Text)
		if used+n > max && used > 0 {
			return false
		}
		pick[i] = true
		used += n
		return true
	}
	if len(cs) > 0 {
		add(0)
	}
	for _, r := range ranked {
		if used >= max {
			break
		}
		add(r.i)
	}
	var parts []string
	last := -2
	for i := range cs {
		if !pick[i] {
			continue
		}
		if last >= 0 && i != last+1 {
			parts = append(parts, "…")
		}
		parts = append(parts, cs[i].Text)
		last = i
	}
	return strings.Join(parts, "\n\n")
}

var termSplit = regexp.MustCompile(`[\s,.;:!?·"'()\[\]{}<>/|]+`)

func focusTerms(focus string) []string {
	var out []string
	seen := map[string]bool{}
	for _, t := range termSplit.Split(strings.ToLower(focus), -1) {
		if utf8.RuneCountInString(t) >= 2 && !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}

func score(text string, terms []string) int {
	low := strings.ToLower(text)
	n := 0
	for _, t := range terms {
		n += strings.Count(low, t)
	}
	return n
}

// looks are the pieces of the page's pictures worth seeing: the ones whose
// words match the focus most, else the first pieces of the first pictures.
func (s *Server) looks(ctx context.Context, doc readDoc, focus string) []lookOut {
	type cand struct {
		pic, piece, score int
	}
	terms := focusTerms(focus)
	var cands []cand
	for _, p := range doc.Pictures {
		for i, pc := range doc.Pieces[p.N] {
			cands = append(cands, cand{p.N, i, score(strings.Join(pc.Lines, "\n"), terms)})
		}
		if len(doc.Pieces[p.N]) == 0 && p.Error != "" && len(terms) == 0 {
			// A picture not read (no OCR, not text) can still be looked at.
			cands = append(cands, cand{p.N, 0, 0})
		}
	}
	if len(terms) > 0 {
		sort.SliceStable(cands, func(a, b int) bool { return cands[a].score > cands[b].score })
		var matched []cand
		for _, c := range cands {
			if c.score > 0 {
				matched = append(matched, c)
			}
		}
		if len(matched) > 0 {
			cands = matched
		}
	}
	if len(cands) > maxLooks {
		cands = cands[:maxLooks]
	}
	byPic := map[int][]int{}
	var order []int
	for _, c := range cands {
		if _, ok := byPic[c.pic]; !ok {
			order = append(order, c.pic)
		}
		byPic[c.pic] = append(byPic[c.pic], c.piece)
	}
	var out []lookOut
	for _, n := range order {
		var url string
		for _, p := range doc.Pictures {
			if p.N == n {
				url = p.URL
			}
		}
		res, err := s.Fetch.Get(ctx, url, fetch.Options{Referer: doc.Final, Accept: "image/webp,image/png,image/jpeg,image/gif;q=0.9,*/*;q=0.5", MaxBytes: 15 << 20, Strict: true})
		if err != nil || res.Status != http.StatusOK {
			continue
		}
		jpgs, err := imgtext.Look(res.Body, byPic[n])
		if err != nil {
			continue
		}
		for i, j := range jpgs {
			out = append(out, lookOut{Picture: n, Piece: byPic[n][i], JPEG: j})
		}
	}
	return out
}

func (s *Server) cached(key string) (readDoc, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.pages[key]
	if !ok || time.Since(c.at) > pageTTL {
		return readDoc{}, false
	}
	return c.doc, true
}

func (s *Server) keep(key string, doc readDoc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pages == nil {
		s.pages = map[string]cachedPage{}
	}
	if len(s.pages) >= maxCachedPages {
		for k, c := range s.pages {
			if time.Since(c.at) > pageTTL {
				delete(s.pages, k)
			}
		}
		if len(s.pages) >= maxCachedPages {
			s.pages = map[string]cachedPage{}
		}
	}
	s.pages[key] = cachedPage{at: time.Now(), doc: doc}
}

// --- places -----------------------------------------------------------------

type placesReq struct {
	Query  string  `json:"query"`
	Lat    float64 `json:"lat"`
	Lng    float64 `json:"lng"`
	Radius int     `json:"radius"`
	Open   bool    `json:"open"`
	Limit  int     `json:"limit"`
	// Area is a name for the place searched around (강남역, 망원동): what
	// the official local search, which takes no point, is asked with when
	// Naver Place's list cannot be read.
	Area string `json:"area"`
}

func (s *Server) places(w http.ResponseWriter, r *http.Request) {
	var in placesReq
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "요청을 읽지 못했습니다")
		return
	}
	in.Query = strings.TrimSpace(in.Query)
	if in.Query == "" {
		writeErr(w, http.StatusBadRequest, "무엇을 찾을지(query)가 없습니다")
		return
	}
	if in.Lat == 0 && in.Lng == 0 {
		writeErr(w, http.StatusBadRequest, "어디 근처인지(lat, lng)가 없습니다")
		return
	}
	radius := in.Radius
	if radius <= 0 {
		radius = 1500
	}
	radius = min(radius, 20000)
	limit := in.Limit
	if limit <= 0 {
		limit = 8
	}
	limit = min(limit, 30)

	start := time.Now()
	out := map[string]any{"query": in.Query, "radius": radius}
	all, total, err := place.Near(r.Context(), s.Fetch, in.Query, in.Lat, in.Lng)
	source := "naver-place"
	if err != nil {
		s.Log.Warn("네이버 플레이스 목록을 읽지 못해 지역 검색으로 찾습니다", "err", err)
		if !s.Naver.Configured() {
			writeErr(w, http.StatusBadGateway, err.Error())
			return
		}
		q := strings.TrimSpace(in.Area + " " + in.Query)
		res, lerr := s.Naver.Search(r.Context(), naver.Local, q, 5, 1, "")
		if lerr != nil {
			writeErr(w, http.StatusBadGateway, fmt.Sprintf("%v (지역 검색도 실패: %v)", err, lerr))
			return
		}
		all, total, source = place.FromLocal(res.Items, in.Lat, in.Lng), res.Total, "naver-local"
		out["note"] = "네이버 플레이스 목록을 읽지 못해 공식 지역 검색(최대 5곳, 영업 상태 없음)으로 찾았습니다"
	}
	var kept []place.Place
	beyond, closed := 0, 0
	for _, p := range all {
		if p.Meters > radius {
			beyond++
			continue
		}
		if in.Open && source == "naver-place" && !place.Open(p.Status) {
			closed++
			continue
		}
		if len(kept) < limit {
			kept = append(kept, p)
		}
	}
	out["places"], out["total"], out["source"] = kept, total, source
	if beyond > 0 {
		out["beyond"] = beyond
	}
	if closed > 0 {
		out["closed"] = closed
	}
	s.Log.Info("장소", "source", source, "listed", len(all), "kept", len(kept), "ms", time.Since(start).Milliseconds())
	writeJSON(w, http.StatusOK, out)
}

// --- helpers ------------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
