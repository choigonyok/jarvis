// Package web is the agent's window on search-svc: Naver's search, a page
// read together with what its pictures say, and Naver Place near a point.
//
// The model gets two tools, search and read; find_nearby (module place) asks
// Places. All of it only reads.
//
// read opens only an address that was seen: in a search result, among a read
// page's links, in what a tool returned (the CLI's own WebSearch too), or in
// the operator's message. A page that talks the model into "reading"
// https://somewhere.example/?q=<what it knows> finds that address was never
// seen - the model cannot put words of its own into an address and send them
// out. (Search words still go to Naver, which is not the page's author.)
package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/choigonyok/jarvis/agent/internal/core/module"
)

var _ module.Module = (*Module)(nil)

const (
	Name       = "web"
	ServerName = "web"
)

func QualifiedToolName(tool string) string { return "mcp__" + ServerName + "__" + tool }

// Tools are the model's; both read.
var Tools = []string{QualifiedToolName("search"), QualifiedToolName("read")}

// --- client -----------------------------------------------------------------

// Client is search-svc.
type Client struct {
	base, token string
	http        *http.Client
}

func NewClient(base, token string) *Client {
	// A page with a long detail image is read in tens of seconds.
	return &Client{base: strings.TrimRight(base, "/"), token: token, http: &http.Client{Timeout: 160 * time.Second}}
}

func (c *Client) post(ctx context.Context, path string, in, out any) error {
	body, err := json.Marshal(in)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("검색 서비스에 연결하지 못했습니다: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(raw, &e) == nil && e.Error != "" {
			return errors.New(e.Error)
		}
		return fmt.Errorf("검색 서비스가 %d 로 답했습니다", resp.StatusCode)
	}
	return json.Unmarshal(raw, out)
}

// SearchInput is one search: the tool's input and search-svc's request.
type SearchInput struct {
	Query   string   `json:"query" jsonschema:"검색어. 문장 말고 핵심 낱말로 짧게(예: '롤토체스 20판 이벤트', '카즈미 레노칸 날 길이')."`
	Sources []string `json:"sources,omitempty" jsonschema:"어디서 찾을지: blog(블로그) news(뉴스) cafe(카페) kin(지식iN) web(웹문서) encyc(백과) local(업체·기관 이름). 비우면 blog·news·cafe·web."`
	Since   string   `json:"since,omitempty" jsonschema:"이 기간의 것만: 24h, 7d, 2w, 1m 또는 2026-10-01. 이벤트·공지·가격처럼 바뀌는 것이면 거세요(블로그·뉴스만 날짜로 걸러집니다)."`
	Sort    string   `json:"sort,omitempty" jsonschema:"date 면 최신순. 비우면 정확도순."`
	Limit   int      `json:"limit,omitempty" jsonschema:"출처마다 몇 개(기본 6, 최대 30)."`
}

type Hit struct {
	Source   string     `json:"source"`
	Title    string     `json:"title"`
	URL      string     `json:"url"`
	Snippet  string     `json:"snippet"`
	Date     *time.Time `json:"date"`
	Author   string     `json:"author"`
	Category string     `json:"category"`
	Address  string     `json:"address"`
}

type Checked struct {
	Source string `json:"source"`
	Total  int    `json:"total"`
	Shown  int    `json:"shown"`
	Older  int    `json:"older"`
	Error  string `json:"error"`
}

type SearchResult struct {
	Query   string    `json:"query"`
	Since   string    `json:"since"`
	Hits    []Hit     `json:"hits"`
	Checked []Checked `json:"checked"`
}

func (c *Client) Search(ctx context.Context, in SearchInput) (SearchResult, error) {
	var out SearchResult
	err := c.post(ctx, "/search", in, &out)
	return out, err
}

// ReadInput is one page to read.
type ReadInput struct {
	URL    string `json:"url" jsonschema:"읽을 주소: 검색 결과나 읽은 페이지의 링크, 도구가 돌려준 주소, 운영자가 준 주소 그대로."`
	Focus  string `json:"focus,omitempty" jsonschema:"찾는 것(예: '날 길이 무게', '이벤트 기간 보상 조건'). 긴 페이지면 관련 부분만 주고, look 이면 관련 그림 조각을 고릅니다."`
	Offset int    `json:"offset,omitempty" jsonschema:"이어 읽기: 앞 결과가 알려 준 offset."`
	Look   bool   `json:"look,omitempty" jsonschema:"그림 조각을 직접 봅니다(최대 4장). 그림 속 글자만으로 모호할 때 - 표·도면·치수처럼 위치가 뜻인 것."`
}

type Picture struct {
	N      int    `json:"n"`
	URL    string `json:"url"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	Pieces int    `json:"pieces"`
	Read   bool   `json:"read"`
	Chars  int    `json:"chars"`
	Error  string `json:"error"`
}

type Link struct {
	Text   string `json:"text"`
	URL    string `json:"url"`
	InBody bool   `json:"inBody"`
}

type Look struct {
	Picture int    `json:"picture"`
	Piece   int    `json:"piece"`
	JPEG    []byte `json:"jpeg"`
}

type Page struct {
	FinalURL  string    `json:"finalUrl"`
	Title     string    `json:"title"`
	Site      string    `json:"site"`
	Published string    `json:"published"`
	Text      string    `json:"text"`
	Chars     int       `json:"chars"`
	Offset    int       `json:"offset"`
	Next      int       `json:"next"`
	Focused   bool      `json:"focused"`
	Note      string    `json:"note"`
	Pictures  []Picture `json:"pictures"`
	Links     []Link    `json:"links"`
	Looks     []Look    `json:"looks"`
}

func (c *Client) Read(ctx context.Context, in ReadInput) (Page, error) {
	var out Page
	err := c.post(ctx, "/read", in, &out)
	return out, err
}

// PlacesQuery is a search for places around a point.
type PlacesQuery struct {
	Query  string  `json:"query"`
	Lat    float64 `json:"lat"`
	Lng    float64 `json:"lng"`
	Radius int     `json:"radius,omitempty"`
	Open   bool    `json:"open,omitempty"`
	Limit  int     `json:"limit,omitempty"`
	Area   string  `json:"area,omitempty"`
}

type Place struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Category string  `json:"category"`
	Meters   int     `json:"meters"`
	Status   string  `json:"status"`
	Hours    string  `json:"hours"`
	Rating   float64 `json:"rating"`
	Reviews  int     `json:"reviews"`
	Blog     int     `json:"blogReviews"`
	Address  string  `json:"address"`
	Phone    string  `json:"phone"`
	Options  string  `json:"options"`
	URL      string  `json:"url"`
}

type Places struct {
	Places []Place `json:"places"`
	Total  int     `json:"total"`
	Source string  `json:"source"`
	Note   string  `json:"note"`
	Beyond int     `json:"beyond"`
	Closed int     `json:"closed"`
}

func (c *Client) Places(ctx context.Context, q PlacesQuery) (Places, error) {
	var out Places
	err := c.post(ctx, "/places", q, &out)
	return out, err
}

// --- what was seen ------------------------------------------------------------

// Ledger is every address seen lately - the addresses read may open.
type Ledger struct {
	mu   sync.Mutex
	seen map[string]time.Time
	now  func() time.Time
}

const (
	seenFor  = 7 * 24 * time.Hour
	seenMost = 20000
)

// jsonEscapes undo what JSON does to an address inside a tool result:
// https:\/\/… and & for "&" (spelled in pieces here so no editor turns
// them into the characters they stand for).
var jsonEscapes = strings.NewReplacer(`\/`, `/`, `\`+`u0026`, `&`, `\`+`u003d`, `=`, `\`+`u003c`, `<`, `\`+`u003e`, `>`)

var urlPattern = regexp.MustCompile(`https?://[^\s<>"'` + "`" + `\\{}|^\[\]]+`)

// Saw notes every address in a piece of text.
func (l *Ledger) Saw(text string) {
	if !strings.Contains(text, "http") {
		return
	}
	// JSON inside a tool result writes https:\/\/ and &.
	text = jsonEscapes.Replace(text)
	for _, u := range urlPattern.FindAllString(text, -1) {
		l.add(trimURL(u))
	}
}

// trimURL drops what follows an address in a sentence: a full stop, a
// comma, the ")" of a markdown link - but not a ")" the address opened (a
// Wikipedia title like Foo_(bar)).
func trimURL(u string) string {
	for {
		switch {
		case u == "":
			return u
		case strings.ContainsRune(".,;:!?'\"", rune(u[len(u)-1])):
			u = u[:len(u)-1]
		case u[len(u)-1] == ')' && strings.Count(u, ")") > strings.Count(u, "("):
			u = u[:len(u)-1]
		default:
			trimmed := false
			for _, s := range []string{"”", "’", "。", "、"} {
				if strings.HasSuffix(u, s) {
					u, trimmed = strings.TrimSuffix(u, s), true
					break
				}
			}
			if !trimmed {
				return u
			}
		}
	}
}

func (l *Ledger) add(raw string) {
	k := norm(raw)
	if k == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.seen == nil {
		l.seen = map[string]time.Time{}
	}
	now := l.clock()
	l.seen[k] = now
	if len(l.seen) > seenMost {
		for key, at := range l.seen {
			if now.Sub(at) > seenFor || len(l.seen) > seenMost {
				delete(l.seen, key)
			}
		}
	}
}

// Has is whether an address was seen in the last week.
func (l *Ledger) Has(raw string) bool {
	k := norm(raw)
	l.mu.Lock()
	defer l.mu.Unlock()
	at, ok := l.seen[k]
	return ok && l.clock().Sub(at) <= seenFor
}

func (l *Ledger) clock() time.Time {
	if l.now != nil {
		return l.now()
	}
	return time.Now()
}

// norm is an address as compared: scheme and host in lower case, no
// fragment, no trailing slash. The query stays as it is - it is where words
// would ride out.
func norm(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return ""
	}
	u.Scheme, u.Host, u.Fragment = strings.ToLower(u.Scheme), strings.ToLower(u.Host), ""
	if u.Scheme == "http" {
		u.Scheme = "https" // a page seen as http is the same page asked as https
	}
	s := u.String()
	return strings.TrimRight(s, "/")
}

// --- module -------------------------------------------------------------------

type Module struct {
	client *Client
	seen   *Ledger
}

func New(c *Client) *Module { return &Module{client: c, seen: &Ledger{}} }

func (*Module) Name() string                { return Name }
func (*Module) Start(context.Context) error { return nil }
func (*Module) Stop(context.Context) error  { return nil }

// Client is search-svc, for the modules that ask it themselves (place).
func (m *Module) Client() *Client { return m.client }

// Saw is the runner telling what went past: the operator's messages and the
// tools' results.
func (m *Module) Saw(text string) { m.seen.Saw(text) }

// Seen is whether an address went past in the last week: what another module
// may open on the conversation's behalf (show's pictures).
func (m *Module) Seen(raw string) bool { return m.seen.Has(raw) }

func (m *Module) Handler() http.Handler {
	server := mcp.NewServer(&mcp.Implementation{Name: ServerName, Version: "1"}, nil)
	mcp.AddTool(server, &mcp.Tool{
		Name: "search",
		Description: "네이버 검색(블로그·뉴스·카페·지식iN·웹문서·백과·업체)을 한 번에 찾는다. 한국의 이벤트·공지·상품·가게·후기는 여기서 먼저 찾는다. " +
			"결과마다 출처·날짜·요약·주소, 그리고 출처마다 몇 건 중 몇 건을 봤는지가 온다. 찾은 글은 read 로 원문을 읽고 답한다. 읽기만 한다.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in SearchInput) (*mcp.CallToolResult, any, error) {
		return m.search(ctx, in), nil, nil
	})
	mcp.AddTool(server, &mcp.Tool{
		Name: "read",
		Description: "웹 페이지 하나를 읽는다: 제목·날짜·본문, 본문 속 그림에 쓰인 글자(공지 카드·상품 상세 이미지·포스터)를 그 자리에, 그리고 링크. " +
			"검색 결과·읽은 페이지의 링크·도구가 돌려준 주소·운영자가 준 주소만 연다. 긴 페이지는 focus 로 관련 부분만, offset 으로 이어 읽는다. " +
			"그림을 직접 봐야 하면 look. 읽기만 한다.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ReadInput) (*mcp.CallToolResult, any, error) {
		return m.read(ctx, in), nil, nil
	})
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
}

func (m *Module) search(ctx context.Context, in SearchInput) *mcp.CallToolResult {
	res, err := m.client.Search(ctx, in)
	if err != nil {
		return fail(err)
	}
	for _, h := range res.Hits {
		m.seen.add(h.URL)
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: renderSearch(in, res, time.Now())}}}
}

func (m *Module) read(ctx context.Context, in ReadInput) *mcp.CallToolResult {
	if strings.TrimSpace(in.URL) == "" {
		return fail(errors.New("읽을 주소(url)가 없습니다"))
	}
	if !m.seen.Has(in.URL) {
		return fail(errors.New("이 주소는 검색 결과·읽은 페이지의 링크·도구 결과·운영자의 말에 나온 적이 없어 열지 않습니다. " +
			"주소를 짐작해 만들지 말고 search 로 먼저 찾거나, 나온 주소를 그대로 쓰세요."))
	}
	page, err := m.client.Read(ctx, in)
	if err != nil {
		return fail(err)
	}
	m.seen.add(page.FinalURL)
	for _, l := range page.Links {
		m.seen.add(l.URL)
	}
	// Its pictures too: what show_image may put in front of the operator.
	for _, pic := range page.Pictures {
		m.seen.add(pic.URL)
	}
	content := []mcp.Content{&mcp.TextContent{Text: renderPage(in, page)}}
	for _, l := range page.Looks {
		content = append(content,
			&mcp.TextContent{Text: fmt.Sprintf("그림 %d · 조각 %d", l.Picture, l.Piece+1)},
			&mcp.ImageContent{Data: l.JPEG, MIMEType: "image/jpeg"})
	}
	return &mcp.CallToolResult{Content: content}
}

func fail(err error) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}
}

// maxPictureLinks is how many of a page's picture addresses its reading lists.
const maxPictureLinks = 8

// --- words --------------------------------------------------------------------

var sourceNames = map[string]string{
	"blog": "블로그", "news": "뉴스", "cafe": "카페", "kin": "지식iN", "web": "웹문서", "encyc": "백과", "local": "업체",
}

func sourceName(s string) string {
	if n, ok := sourceNames[s]; ok {
		return n
	}
	return s
}

func renderSearch(in SearchInput, res SearchResult, now time.Time) string {
	var b strings.Builder
	var where []string
	for _, c := range res.Checked {
		where = append(where, sourceName(c.Source))
	}
	fmt.Fprintf(&b, "네이버 검색 '%s' · %s", in.Query, strings.Join(where, "·"))
	if res.Since != "" {
		if t, err := time.Parse(time.RFC3339, res.Since); err == nil {
			fmt.Fprintf(&b, " · %s 이후", day(t, now))
		}
	}
	b.WriteString("\n")
	if len(res.Hits) == 0 {
		b.WriteString("찾은 것이 없습니다.\n")
	}
	undated := false
	for i, h := range res.Hits {
		meta := []string{sourceName(h.Source)}
		if h.Date != nil {
			meta = append(meta, day(*h.Date, now))
		} else if res.Since != "" {
			undated = true
		}
		if h.Author != "" {
			meta = append(meta, h.Author)
		}
		if h.Category != "" {
			meta = append(meta, h.Category)
		}
		fmt.Fprintf(&b, "[%d] %s\n    %s\n", i+1, strings.Join(meta, " · "), h.Title)
		if h.Address != "" {
			fmt.Fprintf(&b, "    %s\n", h.Address)
		}
		if h.Snippet != "" {
			fmt.Fprintf(&b, "    %s\n", h.Snippet)
		}
		if h.URL != "" {
			fmt.Fprintf(&b, "    %s\n", h.URL)
		}
	}
	var checked []string
	for _, c := range res.Checked {
		switch {
		case c.Error != "":
			checked = append(checked, fmt.Sprintf("%s 실패(%s)", sourceName(c.Source), c.Error))
		case c.Older > 0:
			checked = append(checked, fmt.Sprintf("%s %s건 중 %d (기간 밖 %d)", sourceName(c.Source), comma(c.Total), c.Shown, c.Older))
		default:
			checked = append(checked, fmt.Sprintf("%s %s건 중 %d", sourceName(c.Source), comma(c.Total), c.Shown))
		}
	}
	b.WriteString("확인한 곳: " + strings.Join(checked, " · ") + "\n")
	if undated {
		b.WriteString("(카페·웹문서·지식iN 은 날짜를 알려 주지 않아 기간으로 거르지 못했습니다 - 읽어서 날짜를 확인하세요)\n")
	}
	return b.String()
}

func renderPage(in ReadInput, p Page) string {
	var b strings.Builder
	head := []string{firstOf(p.Title, "(제목 없음)")}
	if p.Site != "" {
		head = append(head, p.Site)
	}
	if t, err := time.Parse(time.RFC3339, p.Published); err == nil {
		head = append(head, t.In(time.Local).Format("2006-01-02 15:04"))
	}
	head = append(head, p.FinalURL)
	b.WriteString(strings.Join(head, " · ") + "\n")
	textLen := len([]rune(p.Text))
	switch {
	case p.Focused:
		b.WriteString("(" + p.Note + ")\n")
	case p.Next > 0:
		fmt.Fprintf(&b, "(전체 %s자 중 %s~%s자 · 이어 읽기: offset %d)\n", comma(p.Chars), comma(p.Offset), comma(p.Offset+textLen), p.Next)
	case p.Offset > 0:
		fmt.Fprintf(&b, "(전체 %s자 중 %s자부터 끝까지)\n", comma(p.Chars), comma(p.Offset))
	}
	if n := len(p.Pictures); n > 0 {
		read, words := 0, 0
		var skipped []string
		for _, pic := range p.Pictures {
			if pic.Read {
				read++
				if pic.Chars > 0 {
					words++
				}
			} else if pic.Error != "" && len(skipped) < 2 {
				skipped = append(skipped, fmt.Sprintf("그림 %d: %s", pic.N, pic.Error))
			}
		}
		line := fmt.Sprintf("(그림 %d장 중 %d장을 읽었고 %d장에 글자가 있습니다", n, read, words)
		if len(skipped) > 0 {
			line += " · " + strings.Join(skipped, " · ")
		}
		b.WriteString(line + ")\n")
	}
	b.WriteString("\n")
	b.WriteString(strings.TrimSpace(p.Text))
	b.WriteString("\n")
	var links []string
	for _, l := range p.Links {
		if l.InBody && len(links) < 12 {
			links = append(links, fmt.Sprintf("- %s %s", l.Text, l.URL))
		}
	}
	if len(links) == 0 {
		// A list page's own entries: what there is to read next.
		for _, l := range p.Links {
			if len(links) >= 12 {
				break
			}
			links = append(links, fmt.Sprintf("- %s %s", l.Text, l.URL))
		}
	}
	if len(links) > 0 {
		b.WriteString("\n링크:\n" + strings.Join(links, "\n") + "\n")
	}
	// The pictures' addresses, for show_image: the product's photo shown
	// rather than described.
	var pics []string
	for _, pic := range p.Pictures {
		if strings.HasPrefix(pic.URL, "https://") && len(pics) < maxPictureLinks {
			pics = append(pics, fmt.Sprintf("- 그림 %d %s", pic.N, pic.URL))
		}
	}
	if len(pics) > 0 {
		b.WriteString("\n그림 주소(show_image 로 띄울 수 있음):\n" + strings.Join(pics, "\n") + "\n")
	}
	if in.Look && len(p.Looks) == 0 {
		b.WriteString("\n(볼 그림 조각이 없습니다)\n")
	}
	return b.String()
}

// day is a date as the operator says it: 10/25 this year, 2025-10-25 before.
func day(t time.Time, now time.Time) string {
	t = t.In(time.Local)
	if t.Year() == now.Year() {
		return t.Format("1/2")
	}
	return t.Format("2006-01-02")
}

func comma(n int) string {
	s := fmt.Sprint(n)
	if n < 0 {
		return s
	}
	var out []byte
	for i := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, s[i])
	}
	return string(out)
}

func firstOf(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}
