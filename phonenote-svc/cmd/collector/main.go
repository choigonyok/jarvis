// Command collector reads this Mac's notification store and hands the
// notifications to phonenote-svc. With iPhone Mirroring set up, the phone's
// notifications land in the same store, so this is how they leave the phone.
//
// The store is ~/Library/Group Containers/group.com.apple.usernoted/db2/db,
// SQLite, one row per notification with its content as a binary plist. It is
// read read-only through /usr/bin/sqlite3 and plutil - no cgo - and only rows
// newer than the last one handed in. macOS guards the folder: this binary
// needs Full Disk Access (System Settings → Privacy & Security), and until it
// has it, it says so in each check-in, which the 상태 tab shows.
//
// What never leaves this Mac: work apps (Slack, Jira, Outlook/Teams), mail,
// KakaoTalk and Messages (read by their own collectors), password managers,
// macOS's own notices (except Wallet's Apple Pay), and the digits of a login
// code. phonenote-svc refuses the same apps again on its side.
package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// cfEpoch is where Core Foundation's dates count from (2001-01-01 UTC).
const cfEpoch = 978307200

// note is one notification as phonenote-svc takes it.
type note struct {
	Key      string    `json:"key"`
	App      string    `json:"app"`
	Title    string    `json:"title"`
	Subtitle string    `json:"subtitle"`
	Body     string    `json:"body"`
	At       time.Time `json:"at"`
}

func main() {
	home, _ := os.UserHomeDir()
	host, _ := os.Hostname()
	url := flag.String("url", os.Getenv("PHONENOTE_URL"), "phonenote-svc /notes (the gateway's /phonenote/notes)")
	tokenFile := flag.String("token-file", filepath.Join(home, ".config/jarvis/phonenote-token"), "file holding the collector token")
	db := flag.String("db", filepath.Join(home, "Library/Group Containers/group.com.apple.usernoted/db2/db"), "the notification store")
	state := flag.String("state", filepath.Join(home, ".config/jarvis/phonenote-collector.last"), "where the last row handed in is kept")
	every := flag.Duration("every", 15*time.Second, "how often to look")
	device := flag.String("device", strings.TrimSuffix(host, ".local"), "this Mac's name, as the console shows it")
	deny := flag.String("deny", "", "more bundle id prefixes never to send, comma-separated")
	flag.Parse()
	token := strings.TrimSpace(os.Getenv("PHONENOTE_TOKEN"))
	if b, err := os.ReadFile(*tokenFile); token == "" && err == nil {
		token = strings.TrimSpace(string(b))
	}
	if *url == "" || token == "" {
		log.Fatal("주소(-url 또는 PHONENOTE_URL)와 토큰(-token-file 또는 PHONENOTE_TOKEN)이 필요합니다")
	}
	for _, p := range strings.Split(*deny, ",") {
		if p = strings.ToLower(strings.TrimSpace(p)); p != "" {
			refused = append(refused, p)
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	c := &collector{url: *url, token: token, db: *db, state: *state, device: *device, http: &http.Client{Timeout: 20 * time.Second}}
	t := time.NewTicker(*every)
	defer t.Stop()
	for {
		if err := c.once(ctx); err != nil && ctx.Err() == nil {
			log.Printf("보내지 못했습니다: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

type collector struct {
	url, token, db, state, device string
	http                          *http.Client
	lastCheckIn                   time.Time
}

// refused are bundle id prefixes never sent (lower case).
var refused = []string{
	"com.tinyspeck.", "com.slack", "com.atlassian.", "com.microsoft.", "com.google.gmail", "com.apple.mail",
	"com.apple.mobilemail", "com.readdle.", "com.kakao.", "com.iwilab.kakaotalk", "com.apple.mobilesms",
	"com.apple.ichat", "com.agilebits.", "com.1password.", "com.apple.passwords",
}

func sendable(app string) bool {
	a := strings.ToLower(app)
	for _, p := range refused {
		if strings.HasPrefix(a, p) {
			return false
		}
	}
	// macOS's own notices are about the Mac; Wallet's are Apple Pay payments.
	if strings.HasPrefix(a, "com.apple.") && a != "com.apple.passbook" {
		return false
	}
	return true
}

// A login or payment code: the digits never leave.
var codeRE = regexp.MustCompile(`(인증\s*번호|인증\s*코드|보안\s*코드|OTP|verification code|일회용)`)
var digitsRE = regexp.MustCompile(`\d{4,8}`)

func redact(s string) string {
	if !codeRE.MatchString(s) {
		return s
	}
	return digitsRE.ReplaceAllString(s, "••••")
}

// once hands in what is new, and checks in every ten minutes even when
// nothing is, so the 상태 tab knows the collector is alive.
func (c *collector) once(ctx context.Context) error {
	last := c.last()
	rows, problem := c.rows(ctx, last)
	var notes []note
	newest := last
	for _, r := range rows {
		if r.ID > newest {
			newest = r.ID
		}
		if !sendable(r.App) {
			continue
		}
		n, err := decode(ctx, r)
		if err != nil {
			continue
		}
		if n.Title == "" && n.Subtitle == "" && n.Body == "" {
			continue
		}
		notes = append(notes, n)
	}
	if len(notes) == 0 && newest == last && time.Since(c.lastCheckIn) < 10*time.Minute && problem == "" {
		return nil
	}
	if err := c.send(ctx, notes, problem, newest); err != nil {
		return err
	}
	c.lastCheckIn = time.Now()
	if newest > last {
		_ = os.MkdirAll(filepath.Dir(c.state), 0o700)
		return os.WriteFile(c.state, []byte(strconv.FormatInt(newest, 10)), 0o600)
	}
	return nil
}

// last is the newest row handed in; none yet starts from the last day.
func (c *collector) last() int64 {
	b, err := os.ReadFile(c.state)
	if err != nil {
		return -1
	}
	v, _ := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	return v
}

type row struct {
	ID        int64   `json:"id"`
	App       string  `json:"app"`
	Data      string  `json:"data"`
	Delivered float64 `json:"delivered"`
	UUID      string  `json:"uuid"`
}

// rows is what the store holds after `after`, oldest first. A first run
// (after < 0) takes the last day. The second value is what is wrong, in a
// sentence for the console, when the store cannot be read.
func (c *collector) rows(ctx context.Context, after int64) ([]row, string) {
	where := fmt.Sprintf("r.rec_id > %d", after)
	if after < 0 {
		where = fmt.Sprintf("r.delivered_date > %d", time.Now().Add(-24*time.Hour).Unix()-cfEpoch)
	}
	q := `select r.rec_id as id, a.identifier as app, hex(r.data) as data, coalesce(r.delivered_date, r.request_date, 0) as delivered,
	             hex(r.uuid) as uuid
	        from record r join app a on a.app_id = r.app_id where ` + where + ` order by r.rec_id limit 300`
	cmd := exec.CommandContext(ctx, "/usr/bin/sqlite3", "-readonly", "-json", "file:"+c.db+"?mode=ro", q)
	var errb bytes.Buffer
	cmd.Stderr = &errb
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(errb.String())
		if strings.Contains(msg, "authorization denied") || strings.Contains(msg, "unable to open") || strings.Contains(msg, "Operation not permitted") {
			return nil, "알림 저장소를 읽을 권한이 없어요. 시스템 설정 → 개인정보 보호 및 보안 → 전체 디스크 접근 권한에 수집기를 더해 주세요"
		}
		return nil, "알림 저장소를 읽지 못했어요: " + firstLine(msg)
	}
	var rows []row
	if len(bytes.TrimSpace(out)) == 0 {
		return nil, ""
	}
	if err := json.Unmarshal(out, &rows); err != nil {
		return nil, "알림 저장소의 모양이 예상과 달라요: " + err.Error()
	}
	return rows, ""
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// decode reads a row's plist: the request's title, subtitle and body.
func decode(ctx context.Context, r row) (note, error) {
	raw, err := hex.DecodeString(r.Data)
	if err != nil {
		return note{}, err
	}
	cmd := exec.CommandContext(ctx, "/usr/bin/plutil", "-convert", "xml1", "-o", "-", "-")
	cmd.Stdin = bytes.NewReader(raw)
	out, err := cmd.Output()
	if err != nil {
		return note{}, err
	}
	v, err := parsePlist(out)
	if err != nil {
		return note{}, err
	}
	top, _ := v.(map[string]any)
	req, _ := top["req"].(map[string]any)
	str := func(m map[string]any, k string) string {
		s, _ := m[k].(string)
		return strings.TrimSpace(s)
	}
	at := time.Unix(int64(r.Delivered)+cfEpoch, 0)
	if d, ok := top["date"].(float64); ok && d > 0 {
		at = time.Unix(int64(d)+cfEpoch, 0)
	}
	key := strconv.FormatInt(r.ID, 10)
	if r.UUID != "" {
		key = strings.ToLower(r.UUID)
	}
	return note{
		Key: key, App: r.App, At: at,
		Title: redact(str(req, "titl")), Subtitle: redact(str(req, "subt")), Body: redact(str(req, "body")),
	}, nil
}

// parsePlist reads an XML property list into maps, slices, strings,
// float64s, int64s, bools, []byte and time.Time.
func parsePlist(b []byte) (any, error) {
	d := xml.NewDecoder(bytes.NewReader(b))
	for {
		tok, err := d.Token()
		if err != nil {
			return nil, err
		}
		if se, ok := tok.(xml.StartElement); ok && se.Name.Local != "plist" {
			return value(d, se)
		}
	}
}

func value(d *xml.Decoder, se xml.StartElement) (any, error) {
	switch se.Name.Local {
	case "dict":
		out := map[string]any{}
		key := ""
		for {
			tok, err := d.Token()
			if err != nil {
				return nil, err
			}
			switch t := tok.(type) {
			case xml.StartElement:
				if t.Name.Local == "key" {
					var k string
					if err := d.DecodeElement(&k, &t); err != nil {
						return nil, err
					}
					key = k
					continue
				}
				v, err := value(d, t)
				if err != nil {
					return nil, err
				}
				out[key] = v
			case xml.EndElement:
				return out, nil
			}
		}
	case "array":
		var out []any
		for {
			tok, err := d.Token()
			if err != nil {
				return nil, err
			}
			switch t := tok.(type) {
			case xml.StartElement:
				v, err := value(d, t)
				if err != nil {
					return nil, err
				}
				out = append(out, v)
			case xml.EndElement:
				return out, nil
			}
		}
	case "true", "false":
		if err := d.Skip(); err != nil {
			return nil, err
		}
		return se.Name.Local == "true", nil
	}
	var text string
	if err := d.DecodeElement(&text, &se); err != nil {
		return nil, err
	}
	switch se.Name.Local {
	case "real":
		return strconv.ParseFloat(strings.TrimSpace(text), 64)
	case "integer":
		return strconv.ParseInt(strings.TrimSpace(text), 10, 64)
	case "date":
		return time.Parse(time.RFC3339, strings.TrimSpace(text))
	case "data":
		return []byte(strings.TrimSpace(text)), nil
	}
	return text, nil
}

// send hands a batch in, with how this collector is doing.
func (c *collector) send(ctx context.Context, notes []note, problem string, newest int64) error {
	if notes == nil {
		notes = []note{}
	}
	for start := 0; start == 0 || start < len(notes); start += 200 {
		end := min(start+200, len(notes))
		body, _ := json.Marshal(map[string]any{"device": c.device, "problem": problem, "lastKey": strconv.FormatInt(newest, 10), "notes": notes[start:end]})
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+c.token)
		res, err := c.http.Do(req)
		if err != nil {
			return err
		}
		res.Body.Close()
		if res.StatusCode >= 300 {
			return errors.New(res.Status)
		}
		if end == len(notes) {
			break
		}
	}
	return nil
}
