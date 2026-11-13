// Package photos lets the conversation look through the operator's photo
// library: by what is in a photo ("서버 사진 있어?"), by where and when, or by a
// place they went to, when it is brought up again. Only reading: photos-svc
// finds and hands over a preview, and mind says when a place was visited. A
// preview comes into the uploads, where the model can open it to see for itself
// and show_image can put it in the thread.
//
// Likeness search always answers, whether or not anything is alike, so what it
// finds is opened and looked at before it is called the thing asked for.
package photos

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/choigonyok/jarvis/agent/internal/tool"
	"github.com/choigonyok/jarvis/agent/internal/uploads"
)

// Name is the module.
const Name = "photos"

const (
	// defaultLimit is how many a search lists, maxLimit the most it may.
	defaultLimit, maxLimit = 6, 12
	// previewed is how many of them are fetched to look at.
	previewed = 4
	// windows is how many visits to a place are looked through.
	windows = 4
)

// Found is a photo as photos-svc answers it.
type Found struct {
	ID       string   `json:"id"`
	At       string   `json:"at"`
	City     string   `json:"city"`
	State    string   `json:"state"`
	Country  string   `json:"country"`
	People   []string `json:"people"`
	Favorite bool     `json:"favorite"`
}

// Window is when photos of a place were taken, as mind answers it.
type Window struct {
	Title  string   `json:"title"`
	From   string   `json:"from"`
	To     string   `json:"to"`
	Place  string   `json:"place"`
	Photos []string `json:"photos"`
}

type Module struct {
	url, mind, token string
	http             *http.Client
	up               *uploads.Store
	log              *slog.Logger

	mu sync.Mutex
	// names is the previews already kept: a photo looked at twice is one file.
	names map[string]string
}

// New reads photos-svc at url and mind at mindURL with the services' token,
// keeping previews in up.
func New(url, mindURL, token string, up *uploads.Store, log *slog.Logger) *Module {
	if log == nil {
		log = slog.Default()
	}
	return &Module{url: strings.TrimRight(url, "/"), mind: strings.TrimRight(mindURL, "/"), token: token,
		http: &http.Client{Timeout: 60 * time.Second}, up: up, log: log, names: map[string]string{}}
}

func (m *Module) Name() string                { return Name }
func (m *Module) Start(context.Context) error { return nil }
func (m *Module) Stop(context.Context) error  { return nil }

// SearchIn is what the conversation asks for.
type SearchIn struct {
	Query    string `json:"query,omitempty" jsonschema:"사진에 담긴 것을 말로(서버 랙, 캠핑 장작, 고양이). 한국어로 된다"`
	Place    string `json:"place,omitempty" jsonschema:"갔던 곳이나 일정의 이름. 그곳에 갔던 날들의 사진을 찾는다(메리지아레 캠핑장, 양평캠핑)"`
	City     string `json:"city,omitempty" jsonschema:"찍은 도시의 영어 이름(Seoul, Gapyeong)"`
	From     string `json:"from,omitempty" jsonschema:"이 날부터(YYYY-MM-DD)"`
	To       string `json:"to,omitempty" jsonschema:"이 날까지(YYYY-MM-DD)"`
	Favorite bool   `json:"favorite,omitempty" jsonschema:"즐겨찾기한 것만"`
	Limit    int    `json:"limit,omitempty" jsonschema:"몇 장까지(기본 6, 최대 12)"`
	// IDs open photos already found rather than find: what a search lists
	// past the first few, by id.
	IDs []string `json:"ids,omitempty" jsonschema:"열어 볼 사진의 id(앞서 찾은 목록의 'id …', 한 번에 4장까지). 주면 찾지 않고 이 사진들만 가져온다"`
}

// Look puts the search on the board.
func (m *Module) Look(b *tool.Board) {
	tool.Add(b, "photos", "운영자의 사진 보관함(사진 탭, 지금까지 찍은 사진 전부)을 찾는다. 사진에 담긴 것(query), 갔던 곳(place), 도시(city), 날(from·to)로 찾고 함께 쓸 수 있다. "+
		"앞의 몇 장은 사진 파일로 가져와 이름과 경로를 답하고, 나머지는 id 로 답한다 - ids 로 다시 부르면 그 사진들을 가져온다. query 는 비슷한 순으로 언제나 답이 나오니 그 경로를 Read 로 열어 정말 그 사진인지 보고 나서 말하고, 보여 줄 때는 그 이름을 show_image 의 uploads 에 넣는다.",
		func(ctx context.Context, in SearchIn) (string, error) { return m.search(ctx, in) })
}

func (m *Module) search(ctx context.Context, in SearchIn) (string, error) {
	if len(in.IDs) > 0 {
		return m.open(ctx, in.IDs)
	}
	in.Query, in.Place = strings.TrimSpace(in.Query), strings.TrimSpace(in.Place)
	if in.Query == "" && in.Place == "" && in.City == "" && in.From == "" && in.To == "" && !in.Favorite {
		return "", errors.New("무엇을 찾을지 query, place, city, from·to 중 하나는 주세요(찾은 사진을 열려면 ids)")
	}
	limit := in.Limit
	if limit <= 0 {
		limit = defaultLimit
	}
	limit = min(limit, maxLimit)
	var lines []string
	var found []Found
	if in.Place != "" {
		ws, err := m.when(ctx, in.Place)
		if err != nil {
			return "", err
		}
		for _, w := range ws[:min(len(ws), windows)] {
			got, err := m.find(ctx, SearchIn{Query: in.Query, City: in.City, From: w.From, To: w.To, Favorite: in.Favorite}, limit)
			if err != nil {
				return "", err
			}
			lines = append(lines, fmt.Sprintf("%s (%s~%s): 사진 %d장", w.Title, w.From, w.To, len(got)))
			found = append(found, got...)
		}
		if len(ws) == 0 {
			// No visit on record: what is in the photos may still say.
			got, err := m.find(ctx, SearchIn{Query: strings.TrimSpace(in.Place + " " + in.Query), City: in.City, From: in.From, To: in.To, Favorite: in.Favorite}, limit)
			if err != nil {
				return "", err
			}
			lines = append(lines, "'"+in.Place+"'에 간 기록이 없어 사진에 담긴 것으로 찾았습니다.")
			found = got
		}
	} else {
		got, err := m.find(ctx, in, limit)
		if err != nil {
			return "", err
		}
		found = got
	}
	if len(found) == 0 {
		return strings.TrimSpace(strings.Join(append(lines, "찾은 사진이 없습니다."), "\n")), nil
	}
	found = found[:min(len(found), limit)]
	for i, f := range found {
		line := "- " + describe(f)
		if i < previewed {
			name, path, err := m.Keep(ctx, f.ID)
			if err != nil {
				m.log.Warn("사진을 가져오지 못했습니다", "photo", f.ID, "err", err)
				line += " · (가져오지 못함)"
			} else {
				line += " · 이름 " + name + " · 경로 " + path
			}
		} else {
			line += " · id " + f.ID
		}
		lines = append(lines, line)
	}
	lines = append(lines, "보여 줄 때는 이름을 show_image 의 uploads 에 넣는다.")
	if len(found) > previewed {
		lines = append(lines, fmt.Sprintf("id 만 있는 사진은 ids 로 다시 부르면 가져온다(한 번에 %d장까지).", previewed))
	}
	if in.Query != "" {
		lines = append(lines, "비슷한 순이라 아닌 것이 섞일 수 있다: 말하기 전에 경로를 Read 로 열어 본다.")
	}
	return strings.Join(lines, "\n"), nil
}

// photoID is the shape of a photo's id (Immich's are UUIDs): anything else
// is not asked for.
var photoID = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)

// open brings photos already found into the uploads by their ids, as many as
// a search would at once: each opens to look at, and what is past them is
// said, for a call of its own.
func (m *Module) open(ctx context.Context, ids []string) (string, error) {
	var want []string
	seen := map[string]bool{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		if !photoID.MatchString(id) {
			return "", fmt.Errorf("사진 id 가 아닙니다: %q - 찾은 목록의 'id …' 를 그대로 주세요", id)
		}
		seen[id] = true
		want = append(want, id)
	}
	if len(want) == 0 {
		return "", errors.New("열어 볼 사진의 id 를 주세요")
	}
	var lines []string
	for _, id := range want[:min(len(want), previewed)] {
		name, path, err := m.Keep(ctx, id)
		if err != nil {
			m.log.Warn("사진을 가져오지 못했습니다", "photo", id, "err", err)
			lines = append(lines, "- id "+id+" · (가져오지 못함: "+err.Error()+")")
			continue
		}
		lines = append(lines, "- id "+id+" · 이름 "+name+" · 경로 "+path)
	}
	if rest := want[min(len(want), previewed):]; len(rest) > 0 {
		lines = append(lines, fmt.Sprintf("한 번에 %d장까지라 열지 않은 id: %s - 다시 ids 로 부른다.", previewed, strings.Join(rest, ", ")))
	}
	lines = append(lines, "경로를 Read 로 열어 본다. 보여 줄 때는 이름을 show_image 의 uploads 에 넣는다.")
	return strings.Join(lines, "\n"), nil
}

func describe(f Found) string {
	at := strings.TrimSuffix(strings.Replace(f.At, "T", " ", 1), "Z")
	if len(at) >= 16 {
		at = at[:16]
	}
	parts := []string{at}
	if f.City != "" {
		parts = append(parts, f.City)
	}
	if len(f.People) > 0 {
		parts = append(parts, strings.Join(f.People, ", "))
	}
	if f.Favorite {
		parts = append(parts, "즐겨찾기")
	}
	return strings.Join(parts, " · ")
}

func (m *Module) find(ctx context.Context, in SearchIn, limit int) ([]Found, error) {
	q := url.Values{"limit": {fmt.Sprint(limit)}}
	for key, v := range map[string]string{"q": in.Query, "city": in.City, "from": in.From, "to": in.To} {
		if v = strings.TrimSpace(v); v != "" {
			q.Set(key, v)
		}
	}
	if in.Favorite {
		q.Set("favorite", "true")
	}
	raw, _, err := m.get(ctx, m.url+"/search?"+q.Encode())
	if err != nil {
		return nil, fmt.Errorf("사진을 찾지 못했습니다: %w", err)
	}
	var out struct {
		Items []Found `json:"items"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

func (m *Module) when(ctx context.Context, place string) ([]Window, error) {
	raw, _, err := m.get(ctx, m.mind+"/api/photos/when?q="+url.QueryEscape(place))
	if err != nil {
		return nil, fmt.Errorf("간 날을 찾지 못했습니다: %w", err)
	}
	var out struct {
		Windows []Window `json:"windows"`
	}
	return out.Windows, json.Unmarshal(raw, &out)
}

// Keep brings a photo into the uploads and is its name and where it lies.
func (m *Module) Keep(ctx context.Context, id string) (name, path string, err error) {
	m.mu.Lock()
	name, ok := m.names[id]
	m.mu.Unlock()
	if ok {
		if path, err := m.up.Path(name); err == nil {
			return name, path, nil
		}
	}
	raw, _, err := m.get(ctx, m.url+"/assets/"+url.PathEscape(id)+"/preview")
	if err != nil {
		return "", "", err
	}
	name, err = m.up.Save(bytes.NewReader(raw))
	if err != nil {
		return "", "", err
	}
	m.mu.Lock()
	m.names[id] = name
	m.mu.Unlock()
	path, err = m.up.Path(name)
	return name, path, err
}

func (m *Module) get(ctx context.Context, target string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, "", err
	}
	if m.token != "" {
		req.Header.Set("Authorization", "Bearer "+m.token)
	}
	res, err := m.http.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, uploads.MaxBytes+1))
	if err != nil {
		return nil, "", err
	}
	if res.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(raw, &e) == nil && e.Error != "" {
			return nil, "", errors.New(e.Error)
		}
		return nil, "", fmt.Errorf("%d 로 답했습니다", res.StatusCode)
	}
	return raw, res.Header.Get("Content-Type"), nil
}
