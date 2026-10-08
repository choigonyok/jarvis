package imsg

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// Message is one iMessage/SMS, in the same JSON shape the KakaoTalk
// collector serves, so the console reads both the same way.
type Message struct {
	RowID     int64  `json:"rowid"`
	MessageID string `json:"message_id"`
	ChatID    int64  `json:"chat_id"`
	ChatName  string `json:"chat_name"`
	ChatType  string `json:"chat_type"`
	Author    string `json:"author"`
	IsMine    bool   `json:"is_mine"`
	Service   string `json:"service"`
	Text      string `json:"text"`
	SentAt    int64  `json:"sent_at"`
}

type Config struct {
	// ChatDB is ~/Library/Messages/chat.db, mounted read-only.
	ChatDB string
	// ContactsDB is AddressBook-v22.abcddb, for names. Optional.
	ContactsDB string
	// Scratch is where the live files are copied before reading.
	Scratch string
}

// Reader copies the live database (main file and WAL) into scratch when it
// changes and reads the copy. The live file is never opened: Messages holds
// it, and a reader on a read-only mount cannot create the -shm it would need.
type Reader struct {
	cfg Config

	mu       sync.Mutex
	copied   map[string]stamp
	names    map[string]string
	namesAt  time.Time
	copyFail error
}

type stamp struct {
	mod  time.Time
	size int64
}

func Open(cfg Config) (*Reader, error) {
	if cfg.ChatDB == "" {
		return nil, errors.New("CHAT_DB 가 없습니다")
	}
	if _, err := os.Stat(cfg.ChatDB); err != nil {
		return nil, fmt.Errorf("메시지 데이터베이스를 열 수 없습니다(전체 디스크 접근 권한 확인): %w", err)
	}
	if err := os.MkdirAll(cfg.Scratch, 0o700); err != nil {
		return nil, err
	}
	return &Reader{cfg: cfg, copied: map[string]stamp{}}, nil
}

// sync copies src and its -wal into dir when either changed. Returns the copy.
func (r *Reader) sync(src, dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	dst := filepath.Join(dir, filepath.Base(src))
	for _, suffix := range []string{"", "-wal"} {
		from, to := src+suffix, dst+suffix
		st, err := os.Stat(from)
		if err != nil {
			if suffix == "-wal" {
				_ = os.Remove(to)
				continue
			}
			return "", err
		}
		if prev, ok := r.copied[from]; ok && prev.mod.Equal(st.ModTime()) && prev.size == st.Size() {
			continue
		}
		if err := copyFile(from, to); err != nil {
			return "", err
		}
		// The old -shm describes the old WAL; let SQLite rebuild it.
		_ = os.Remove(dst + "-shm")
		r.copied[from] = stamp{st.ModTime(), st.Size()}
	}
	return dst, nil
}

func copyFile(from, to string) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := to + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, to)
}

const query = `
SELECT m.ROWID, m.guid, m.text, m.attributedBody, m.is_from_me, m.date,
       coalesce(m.service, ''), coalesce(h.id, ''),
       coalesce(c.ROWID, 0), coalesce(c.display_name, ''), coalesce(c.chat_identifier, ''),
       coalesce(c.style, 45)
  FROM message m
  LEFT JOIN handle h ON h.ROWID = m.handle_id
  LEFT JOIN chat_message_join cmj ON cmj.message_id = m.ROWID
  LEFT JOIN chat c ON c.ROWID = cmj.chat_id
 WHERE m.ROWID > ?
   AND m.item_type = 0
 ORDER BY m.ROWID
 LIMIT ?`

// After reads messages with ROWID above rowid, oldest first.
func (r *Reader) After(rowid int64, limit int) ([]Message, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	path, err := r.sync(r.cfg.ChatDB, filepath.Join(r.cfg.Scratch, "chat"))
	if err != nil {
		return nil, fmt.Errorf("메시지 데이터베이스 복사: %w", err)
	}
	r.loadNames()

	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(3000)")
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.Query(query, rowid, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Message
	for rows.Next() {
		var (
			m                      Message
			text                   sql.NullString
			body                   []byte
			mine                   int
			date                   int64
			handle, display, ident string
			style                  int
		)
		if err := rows.Scan(&m.RowID, &m.MessageID, &text, &body, &mine, &date, &m.Service, &handle,
			&m.ChatID, &display, &ident, &style); err != nil {
			return nil, err
		}
		m.Text = strings.TrimSpace(text.String)
		if m.Text == "" {
			m.Text = strings.TrimSpace(TextFromAttributedBody(body))
		}
		// U+FFFC stands where an attachment was; alone, it is a photo.
		m.Text = strings.TrimSpace(strings.ReplaceAll(m.Text, "￼", ""))
		if m.Text == "" && len(body) > 0 {
			m.Text = "(첨부)"
		}
		if m.Text == "" {
			continue
		}
		m.IsMine = mine == 1
		m.SentAt = appleTime(date).Unix()
		if style == 43 {
			m.ChatType = "group"
		} else {
			m.ChatType = "direct"
		}
		m.ChatName = display
		if m.ChatName == "" {
			m.ChatName = r.nameOf(ident)
		}
		if m.IsMine {
			m.Author = "나"
		} else {
			m.Author = r.nameOf(handle)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

var appleEpoch = time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)

// appleTime converts Messages' date: nanoseconds since 2001 on current macOS,
// seconds since 2001 in rows from before High Sierra.
func appleTime(v int64) time.Time {
	if v > 1e12 || v < -1e12 {
		return appleEpoch.Add(time.Duration(v))
	}
	return appleEpoch.Add(time.Duration(v) * time.Second)
}

// --- contacts --------------------------------------------------------------

func (r *Reader) nameOf(handle string) string {
	if handle == "" {
		return ""
	}
	if n, ok := r.names[strings.ToLower(handle)]; ok {
		return n
	}
	if d := digits(handle); len(d) >= 8 {
		if n, ok := r.names[d[len(d)-8:]]; ok {
			return n
		}
	}
	return handle
}

// loadNames reads the address book at most every ten minutes. Missing or
// unreadable contacts leave handles as phone numbers and addresses.
func (r *Reader) loadNames() {
	if r.cfg.ContactsDB == "" || time.Since(r.namesAt) < 10*time.Minute {
		return
	}
	r.namesAt = time.Now()
	path, err := r.sync(r.cfg.ContactsDB, filepath.Join(r.cfg.Scratch, "contacts"))
	if err != nil {
		return
	}
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(3000)")
	if err != nil {
		return
	}
	defer db.Close()
	names := map[string]string{}
	add := func(q string, key func(string) string) {
		rows, err := db.Query(q)
		if err != nil {
			return
		}
		defer rows.Close()
		for rows.Next() {
			var value, first, last, org sql.NullString
			if rows.Scan(&value, &first, &last, &org) != nil {
				continue
			}
			name := strings.TrimSpace(last.String + first.String)
			if name == "" {
				name = strings.TrimSpace(org.String)
			}
			if k := key(value.String); k != "" && name != "" {
				names[k] = name
			}
		}
	}
	add(`SELECT p.ZFULLNUMBER, r.ZFIRSTNAME, r.ZLASTNAME, r.ZORGANIZATION
	       FROM ZABCDPHONENUMBER p JOIN ZABCDRECORD r ON r.Z_PK = p.ZOWNER`, func(v string) string {
		if d := digits(v); len(d) >= 8 {
			return d[len(d)-8:]
		}
		return ""
	})
	add(`SELECT e.ZADDRESS, r.ZFIRSTNAME, r.ZLASTNAME, r.ZORGANIZATION
	       FROM ZABCDEMAILADDRESS e JOIN ZABCDRECORD r ON r.Z_PK = e.ZOWNER`, func(v string) string {
		return strings.ToLower(strings.TrimSpace(v))
	})
	if len(names) > 0 {
		r.names = names
	}
}

// digits keeps the digits of a phone number, writing +82 as a leading 0.
func digits(s string) string {
	var b strings.Builder
	for _, c := range s {
		if c >= '0' && c <= '9' {
			b.WriteRune(c)
		}
	}
	d := b.String()
	if strings.HasPrefix(strings.TrimSpace(s), "+82") && strings.HasPrefix(d, "82") {
		d = "0" + d[2:]
	}
	return d
}
