// Package page turns a fetched page into what a reader needs: its title,
// when it was written, its text in reading order with each picture where it
// stands, and the links out of it.
//
// A page's body is found three ways, in order: a rule for a site whose
// layout is known (a Naver blog post is .se-main-container, a Naver news
// article #dic_area), Readability (Mozilla's, ported) for an article
// anywhere else, and failing both the whole body less its header, footer
// and navigation - a list of notices is not an article, and its links are
// what it is for.
package page

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	readability "codeberg.org/readeck/go-readability/v2"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
	"golang.org/x/net/html/charset"
)

// Doc is one page, read.
type Doc struct {
	Title     string
	Site      string
	Published time.Time
	Blocks    []Block
	Links     []Link
}

// Block is a paragraph of text, or a picture where it stands in the text.
// A table is one block, a row a line, its cells joined by " | ".
type Block struct {
	Text  string
	Image *Image
	row   bool
}

// Image is a picture in the body: its address (absolute), and its size when
// the page says it.
type Image struct {
	URL           string
	Alt           string
	Width, Height int
}

// Link is a link out of the page. InBody is a link from the body found
// above; the rest are from around it (a list page's other entries).
type Link struct {
	Text   string `json:"text"`
	URL    string `json:"url"`
	InBody bool   `json:"inBody,omitempty"`
}

const (
	maxText  = 300_000
	maxLinks = 80
	// minArticle is the least text Readability's pick must have to be taken
	// as the page's body.
	minArticle = 1000
)

// Source is how a page is best fetched: the address to ask, and whether to
// ask as a phone. A Naver blog's desktop page is a frame around its post;
// the mobile page is the post.
func Source(u *url.URL) (*url.URL, bool) {
	host := strings.ToLower(u.Hostname())
	switch host {
	case "blog.naver.com", "m.blog.naver.com":
		id, no := "", ""
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		switch {
		case len(parts) == 2 && isDigits(parts[1]):
			id, no = parts[0], parts[1]
		case strings.HasPrefix(strings.ToLower(u.Path), "/postview"):
			id, no = u.Query().Get("blogId"), u.Query().Get("logNo")
		}
		if id != "" && no != "" {
			return &url.URL{Scheme: "https", Host: "m.blog.naver.com", Path: "/" + id + "/" + no}, true
		}
		return u, true
	}
	if strings.HasPrefix(host, "m.") {
		return u, true
	}
	return u, false
}

// Parse reads what was fetched. HTML is read as above; plain text is its
// paragraphs; a picture is a page of one picture.
func Parse(body []byte, contentType string, pageURL *url.URL) (Doc, error) {
	media, _, _ := mime.ParseMediaType(contentType)
	switch {
	case strings.HasPrefix(media, "image/"):
		return Doc{Title: lastSegment(pageURL), Blocks: []Block{{Image: &Image{URL: pageURL.String()}}}}, nil
	case media == "application/pdf":
		return Doc{}, errors.New("PDF 는 아직 읽지 못합니다")
	case media == "text/plain", media == "application/json", media == "text/markdown":
		var d Doc
		for _, p := range strings.Split(toUTF8(body, contentType), "\n\n") {
			if p = strings.TrimSpace(p); p != "" {
				d.Blocks = append(d.Blocks, Block{Text: p})
			}
		}
		return d, nil
	case media != "" && media != "text/html" && media != "application/xhtml+xml":
		return Doc{}, fmt.Errorf("%s 형식은 읽지 못합니다", media)
	}

	doc, err := html.Parse(strings.NewReader(toUTF8(body, contentType)))
	if err != nil {
		return Doc{}, fmt.Errorf("HTML 을 읽지 못했습니다: %w", err)
	}
	d := Doc{Title: metaTitle(doc), Site: metaContent(doc, "og:site_name")}
	d.Published = published(doc)

	root := ruleRoot(doc)
	if root == nil {
		// Readability finds an article's text; a shop's or an event's page
		// keeps what matters beside it - the detail pictures, the spec list -
		// and a short "article" there means the rest of the page is the page.
		if art, err := readability.FromDocument(doc, pageURL); err == nil && art.Node != nil && textLen(art.Node) >= minArticle {
			root = art.Node
			if d.Title == "" {
				d.Title = art.Title()
			}
			if d.Published.IsZero() {
				if t, err := art.PublishedTime(); err == nil {
					d.Published = t
				}
			}
		}
	}
	whole := false
	if root == nil {
		root, whole = find(doc, func(n *html.Node) bool { return n.DataAtom == atom.Body }), true
		if root == nil {
			root = doc
		}
	}

	w := &walker{base: pageURL, seen: map[string]bool{}, skipChrome: whole}
	w.walk(root)
	w.flush()
	d.Blocks = w.blocks
	d.Links = w.links
	for i := range d.Links {
		d.Links[i].InBody = true
	}
	// Links around the body: a notice list's other notices, a post's
	// neighbours. Kept apart so a reader of the post is not shown the menu.
	if !whole {
		around := &walker{base: pageURL, seen: w.seen, skipChrome: true, linksOnly: true}
		if body := find(doc, func(n *html.Node) bool { return n.DataAtom == atom.Body }); body != nil {
			around.walk(body)
		}
		d.Links = append(d.Links, around.links...)
	}
	if len(d.Links) > maxLinks {
		d.Links = d.Links[:maxLinks]
	}
	return d, nil
}

// ruleRoot is the body of a page whose layout is known.
func ruleRoot(doc *html.Node) *html.Node {
	for _, sel := range []struct{ id, class string }{
		{class: "se-main-container"}, // Naver blog (SmartEditor ONE)
		{id: "postViewArea"},         // Naver blog, older editor
		{class: "post_ct"},           // Naver blog, mobile older editor
		{id: "dic_area"},             // Naver news
		{id: "newsct_article"},       // Naver news, mobile
		{id: "articeBody"},           // Naver sports/entertainment news (sic)
	} {
		n := find(doc, func(n *html.Node) bool {
			return n.Type == html.ElementNode && ((sel.id != "" && attr(n, "id") == sel.id) || (sel.class != "" && hasClass(n, sel.class)))
		})
		if n != nil && textLen(n) > 0 {
			return n
		}
	}
	return nil
}

type walker struct {
	base       *url.URL
	blocks     []Block
	buf        strings.Builder
	links      []Link
	seen       map[string]bool
	size       int
	skipChrome bool // the page's header, footer, navigation and menus are not read
	linksOnly  bool
}

func (w *walker) walk(n *html.Node) {
	switch n.Type {
	case html.TextNode:
		if !w.linksOnly {
			w.text(n.Data)
		}
		return
	case html.ElementNode:
		switch n.DataAtom {
		case atom.Script, atom.Style, atom.Noscript, atom.Template, atom.Svg, atom.Button, atom.Select,
			atom.Option, atom.Iframe, atom.Head, atom.Input, atom.Textarea, atom.Object, atom.Canvas:
			return
		case atom.Header, atom.Footer, atom.Nav, atom.Aside:
			if w.skipChrome {
				return
			}
		case atom.Img:
			if !w.linksOnly {
				w.image(n)
			}
			return
		case atom.Br:
			if !w.linksOnly {
				w.buf.WriteByte('\n')
			}
			return
		case atom.A:
			w.link(n)
		case atom.Tr:
			if !w.linksOnly {
				w.row(n)
				return
			}
		}
		if w.skipChrome && chrome(n) {
			return
		}
		block := isBlock(n.DataAtom)
		if block {
			w.flush()
		} else if n.DataAtom == atom.Td || n.DataAtom == atom.Th {
			w.buf.WriteString(" ")
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			w.walk(c)
		}
		if block {
			w.flush()
		}
		return
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		w.walk(c)
	}
}

// row is a table row as one line: each cell's text, then the row's
// pictures after it. Rows next to each other make one block.
func (w *walker) row(tr *html.Node) {
	w.flush()
	var cells []string
	var pictures []Block
	for c := tr.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode || (c.DataAtom != atom.Td && c.DataAtom != atom.Th) {
			continue
		}
		sub := &walker{base: w.base, seen: w.seen, skipChrome: w.skipChrome}
		sub.walk(c)
		sub.flush()
		var parts []string
		for _, b := range sub.blocks {
			if b.Image != nil {
				pictures = append(pictures, b)
			} else {
				parts = append(parts, strings.ReplaceAll(b.Text, "\n", " "))
			}
		}
		w.links = append(w.links, sub.links...)
		if cell := strings.TrimSpace(strings.Join(parts, " ")); cell != "" {
			cells = append(cells, cell)
		}
	}
	if line := strings.Join(cells, " | "); line != "" {
		w.size += len(line)
		if n := len(w.blocks); n > 0 && w.blocks[n-1].row {
			w.blocks[n-1].Text += "\n" + line
		} else {
			w.blocks = append(w.blocks, Block{Text: line, row: true})
		}
	}
	w.blocks = append(w.blocks, pictures...)
}

func (w *walker) text(s string) {
	if w.size > maxText {
		return
	}
	w.buf.WriteString(s)
}

var spaces = regexp.MustCompile(`[ \t\r\f\v\x{00a0}\x{200b}\x{3000}]+`)

func (w *walker) flush() {
	raw := w.buf.String()
	w.buf.Reset()
	var lines []string
	for _, l := range strings.Split(raw, "\n") {
		if l = strings.TrimSpace(spaces.ReplaceAllString(l, " ")); l != "" {
			lines = append(lines, l)
		}
	}
	text := strings.Join(lines, "\n")
	if text == "" {
		return
	}
	if n := len(w.blocks); n > 0 && w.blocks[n-1].Image == nil && w.blocks[n-1].Text == text {
		return
	}
	w.size += len(text)
	w.blocks = append(w.blocks, Block{Text: text})
}

func (w *walker) image(n *html.Node) {
	src := imageSource(n)
	if src == "" || strings.HasPrefix(src, "data:") {
		return
	}
	u, err := w.base.Parse(src)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return
	}
	abs := u.String()
	if skipImage(u) {
		return
	}
	width, height := dim(n, "width", "data-width"), dim(n, "height", "data-height")
	if (width > 0 && width < 120) || (height > 0 && height < 120) {
		return // an icon, a button, a bullet
	}
	w.flush()
	for _, b := range w.blocks {
		if b.Image != nil && b.Image.URL == abs {
			return
		}
	}
	w.blocks = append(w.blocks, Block{Image: &Image{URL: abs, Alt: strings.TrimSpace(attr(n, "alt")), Width: width, Height: height}})
}

func (w *walker) link(n *html.Node) {
	href := strings.TrimSpace(attr(n, "href"))
	if href == "" || strings.HasPrefix(href, "#") || strings.HasPrefix(strings.ToLower(href), "javascript:") {
		return
	}
	u, err := w.base.Parse(href)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return
	}
	u.Fragment = ""
	abs := u.String()
	if w.seen[abs] || len(w.links) >= maxLinks {
		return
	}
	text := strings.TrimSpace(spaces.ReplaceAllString(strings.ReplaceAll(innerText(n), "\n", " "), " "))
	if text == "" {
		text = strings.TrimSpace(attr(n, "title"))
	}
	if text == "" {
		return // a picture link; the picture itself is in the blocks
	}
	w.seen[abs] = true
	w.links = append(w.links, Link{Text: clip(text, 80), URL: abs})
}

// imageSource is where a picture really is: pages that load pictures as
// they scroll put the real address in a data attribute and a blur or a
// spacer in src.
func imageSource(n *html.Node) string {
	for _, k := range []string{"data-lazy-src", "data-src", "data-original", "data-lazy", "data-url"} {
		if v := strings.TrimSpace(attr(n, k)); v != "" {
			return v
		}
	}
	if set := attr(n, "srcset"); set != "" {
		best, bestW := "", -1
		for _, cand := range strings.Split(set, ",") {
			f := strings.Fields(strings.TrimSpace(cand))
			if len(f) == 0 {
				continue
			}
			w := 0
			if len(f) > 1 {
				w, _ = strconv.Atoi(strings.TrimRight(f[1], "wx"))
			}
			if w > bestW {
				best, bestW = f[0], w
			}
		}
		if best != "" {
			return best
		}
	}
	return strings.TrimSpace(attr(n, "src"))
}

// skipImage is a picture that is never content: a sticker, a spacer, a
// vector icon.
func skipImage(u *url.URL) bool {
	host, path := strings.ToLower(u.Hostname()), strings.ToLower(u.Path)
	if host == "storep-phinf.pstatic.net" || strings.HasSuffix(path, ".svg") {
		return true
	}
	for _, s := range []string{"emoticon", "sticker", "spacer", "blank.gif", "pixel.gif", "1x1"} {
		if strings.Contains(path, s) {
			return true
		}
	}
	return false
}

func dim(n *html.Node, keys ...string) int {
	for _, k := range keys {
		if v, err := strconv.Atoi(strings.TrimSuffix(strings.TrimSpace(attr(n, k)), "px")); err == nil && v > 0 {
			return v
		}
	}
	return 0
}

// chromeNames are ids and classes that Korean sites give their menus,
// headers and footers instead of the elements for them. Whole names only: a
// shop's "menu_list" is its menu, not the site's.
var chromeNames = map[string]bool{
	"gnb": true, "lnb": true, "snb": true, "header": true, "footer": true, "nav": true, "navbar": true,
	"navigation": true, "sitemap": true, "quick": true, "quickmenu": true, "quick_menu": true, "skip": true,
	"skipnav": true, "skip_nav": true, "breadcrumb": true, "topmenu": true, "top_menu": true, "util": true,
	"family_site": true, "header_wrap": true, "footer_wrap": true, "gnb_wrap": true,
}

// chrome is an element that is the site around the page, not the page.
func chrome(n *html.Node) bool {
	switch strings.ToLower(attr(n, "role")) {
	case "navigation", "banner", "contentinfo", "menu", "menubar", "search":
		return true
	}
	if chromeNames[strings.ToLower(attr(n, "id"))] {
		return true
	}
	for _, c := range strings.Fields(strings.ToLower(attr(n, "class"))) {
		if chromeNames[c] {
			return true
		}
	}
	return false
}

func isBlock(a atom.Atom) bool {
	switch a {
	case atom.P, atom.Div, atom.Section, atom.Article, atom.Main, atom.Header, atom.Footer, atom.Nav, atom.Aside,
		atom.Li, atom.Ul, atom.Ol, atom.Dl, atom.Dt, atom.Dd, atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6,
		atom.Table, atom.Tr, atom.Blockquote, atom.Pre, atom.Figure, atom.Figcaption, atom.Hr, atom.Address, atom.Caption:
		return true
	}
	return false
}

// --- metadata -------------------------------------------------------------

func metaTitle(doc *html.Node) string {
	if t := metaContent(doc, "og:title"); t != "" {
		return t
	}
	if t := find(doc, func(n *html.Node) bool { return n.DataAtom == atom.Title }); t != nil {
		return strings.TrimSpace(innerText(t))
	}
	return ""
}

func metaContent(doc *html.Node, names ...string) string {
	for _, name := range names {
		n := find(doc, func(n *html.Node) bool {
			return n.DataAtom == atom.Meta && (attr(n, "property") == name || attr(n, "name") == name || attr(n, "itemprop") == name)
		})
		if n != nil {
			if v := strings.TrimSpace(attr(n, "content")); v != "" {
				return v
			}
		}
	}
	return ""
}

// published is when the page says it was written: its metadata, a <time>,
// or the date line of a Naver blog post or news article.
func published(doc *html.Node) time.Time {
	if v := metaContent(doc, "article:published_time", "og:article:published_time", "datePublished", "pubdate", "date"); v != "" {
		if t := ParseTime(v); !t.IsZero() {
			return t
		}
	}
	if n := find(doc, func(n *html.Node) bool { return hasClass(n, "blog_date") || hasClass(n, "se_publishDate") }); n != nil {
		if t := ParseTime(innerText(n)); !t.IsZero() {
			return t
		}
	}
	if n := find(doc, func(n *html.Node) bool { return attr(n, "data-date-time") != "" }); n != nil {
		if t := ParseTime(attr(n, "data-date-time")); !t.IsZero() {
			return t
		}
	}
	if n := find(doc, func(n *html.Node) bool { return n.DataAtom == atom.Time && attr(n, "datetime") != "" }); n != nil {
		return ParseTime(attr(n, "datetime"))
	}
	return time.Time{}
}

var seoul = time.FixedZone("KST", 9*60*60)

// ParseTime reads the ways Korean and international pages write a time; a
// time with no zone is Seoul's.
func ParseTime(s string) time.Time {
	s = strings.TrimSpace(spaces.ReplaceAllString(s, " "))
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05-0700", "2006-01-02T15:04:05.000Z0700", time.RFC1123Z, time.RFC1123} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	for _, layout := range []string{
		"2006-01-02 15:04:05", "2006-01-02 15:04", "2006-01-02T15:04:05", "2006-01-02",
		"2006. 1. 2. 15:04", "2006. 1. 2. 15:04:05", "2006. 1. 2.", "2006.01.02 15:04", "2006.01.02. 15:04", "2006.01.02.", "2006.01.02",
	} {
		if t, err := time.ParseInLocation(layout, s, seoul); err == nil {
			return t
		}
	}
	return time.Time{}
}

// --- tree helpers -----------------------------------------------------------

func find(n *html.Node, match func(*html.Node) bool) *html.Node {
	if n.Type == html.ElementNode && match(n) {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if f := find(c, match); f != nil {
			return f
		}
	}
	return nil
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func hasClass(n *html.Node, class string) bool {
	for _, c := range strings.Fields(attr(n, "class")) {
		if c == class {
			return true
		}
	}
	return false
}

func innerText(n *html.Node) string {
	var b strings.Builder
	var rec func(*html.Node)
	rec = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		if n.Type == html.ElementNode && (n.DataAtom == atom.Script || n.DataAtom == atom.Style) {
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			rec(c)
		}
	}
	rec(n)
	return b.String()
}

func textLen(n *html.Node) int {
	return utf8.RuneCountInString(strings.Join(strings.Fields(innerText(n)), " "))
}

// toUTF8 decodes a body by its declared or sniffed charset - older Korean
// sites still send EUC-KR.
func toUTF8(body []byte, contentType string) string {
	r, err := charset.NewReader(bytes.NewReader(body), contentType)
	if err != nil {
		return string(body)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		return string(body)
	}
	return string(out)
}

func lastSegment(u *url.URL) string {
	p := strings.Trim(u.Path, "/")
	if i := strings.LastIndex(p, "/"); i >= 0 {
		p = p[i+1:]
	}
	return p
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
