// Package kakao opens the local KakaoTalk SQLCipher database read-only and maps
// its schema to plain message records. It never writes to the source database:
// the three live files (main, -wal, -shm) are copied into a scratch directory
// and read from there via the `sqlcipher` CLI, so the running KakaoTalk app is
// never disturbed. The CLI is used because it is the only path that reliably
// applies `cipher_compatibility=3`, the SQLCipher-3 parameter set KakaoTalk uses.
package kakao

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/choigonyok/jarvis/kakaotalk-client/internal/derive"
)

var hexDBName = regexp.MustCompile(`^[0-9a-f]{78}(\.db)?$`)

// Config controls how the source database is located and unlocked.
type Config struct {
	ContainerDir   string // KakaoTalk Application Support dir; default: standard sandbox path
	HomeDir        string // macOS home; default: $HOME
	UUID           string // IOPlatformUUID; default: read from `ioreg`
	UserID         int64  // account id; default: recovered from plist
	Key            string // 256-hex passphrase; default: derived from (UserID, UUID)
	PlistDir       string // preferences dir for user-id recovery
	RecoverMax     int64  // upper bound for the SHA-512 recovery scan
	RecoverWorkers int    // recovery scan parallelism
	ScratchDir     string // where live db files are copied before reading
	SqlcipherBin   string // sqlcipher executable; default: "sqlcipher" on PATH
}

// Reader holds a resolved, verified handle to the source database.
type Reader struct {
	cfg     Config
	dbPath  string
	key     string
	scratch string
	bin     string
	userID  int64
	uuid    string
	mainMod time.Time
}

// Message is one row of NTChatMessage joined with sender/room names.
type Message struct {
	RowID     int64  `json:"rowid"`
	MessageID string `json:"message_id"`
	ChatID    int64  `json:"chat_id"`
	ChatName  string `json:"chat_name"`
	ChatType  string `json:"chat_type"`
	AuthorID  int64  `json:"author_id"`
	Author    string `json:"author"`
	IsMine    bool   `json:"is_mine"`
	Type      int64  `json:"type"`
	Text      string `json:"text"`
	SentAt    int64  `json:"sent_at"`
}

// Chat is a room summary.
type Chat struct {
	ChatID        int64  `json:"chat_id"`
	ChatName      string `json:"chat_name"`
	ChatType      string `json:"chat_type"`
	LastMessageAt int64  `json:"last_message_at"`
	MessageCount  int64  `json:"message_count"`
}

// Open resolves auth, verifies the key against the on-disk filename, and probes
// that the key actually decrypts the database.
func Open(cfg Config) (*Reader, error) {
	if cfg.HomeDir == "" {
		cfg.HomeDir, _ = os.UserHomeDir()
	}
	if cfg.ContainerDir == "" {
		cfg.ContainerDir = filepath.Join(cfg.HomeDir,
			"Library", "Containers", "com.kakao.KakaoTalkMac", "Data",
			"Library", "Application Support", "com.kakao.KakaoTalkMac")
	}
	if cfg.PlistDir == "" {
		cfg.PlistDir = filepath.Join(cfg.HomeDir,
			"Library", "Containers", "com.kakao.KakaoTalkMac", "Data",
			"Library", "Preferences")
	}
	if cfg.RecoverMax == 0 {
		cfg.RecoverMax = 1_000_000_000
	}
	if cfg.RecoverWorkers == 0 {
		cfg.RecoverWorkers = 10
	}
	if cfg.ScratchDir == "" {
		cfg.ScratchDir = filepath.Join(os.TempDir(), "kakaotalk-client-scratch")
	}
	if cfg.SqlcipherBin == "" {
		cfg.SqlcipherBin = "sqlcipher"
	}
	if err := os.MkdirAll(cfg.ScratchDir, 0o700); err != nil {
		return nil, fmt.Errorf("scratch dir: %w", err)
	}
	if _, err := exec.LookPath(cfg.SqlcipherBin); err != nil {
		return nil, fmt.Errorf("sqlcipher CLI not found (%s): %w", cfg.SqlcipherBin, err)
	}

	dbPath, err := findDBFile(cfg.ContainerDir)
	if err != nil {
		return nil, err
	}

	uuid := cfg.UUID
	if uuid == "" {
		uuid, err = platformUUID()
		if err != nil {
			return nil, fmt.Errorf("resolve UUID: %w (set KAKAO_UUID when running in a container)", err)
		}
	}

	userID := cfg.UserID
	filename := filepath.Base(dbPath)
	if userID == 0 {
		userID, err = resolveUserID(cfg, uuid, filename)
		if err != nil {
			return nil, err
		}
	}
	if err := derive.Verify(userID, uuid, filename); err != nil {
		return nil, fmt.Errorf("auth verification failed: %w", err)
	}

	key := cfg.Key
	if key == "" {
		key = derive.SecureKey(userID, uuid)
	}

	r := &Reader{cfg: cfg, dbPath: dbPath, key: key, scratch: cfg.ScratchDir,
		bin: cfg.SqlcipherBin, userID: userID, uuid: uuid}

	var n int64
	if err := r.queryScalar("SELECT count(*) FROM sqlite_master", &n); err != nil {
		return nil, fmt.Errorf("decrypt probe failed: %w", err)
	}
	return r, nil
}

// UserID returns the resolved account id.
func (r *Reader) UserID() int64 { return r.userID }

func findDBFile(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("read container %s: %w", dir, err)
	}
	var candidates []string
	for _, e := range entries {
		name := e.Name()
		if strings.HasSuffix(name, "-wal") || strings.HasSuffix(name, "-shm") {
			continue
		}
		if hexDBName.MatchString(name) {
			candidates = append(candidates, filepath.Join(dir, name))
		}
	}
	switch len(candidates) {
	case 0:
		return "", fmt.Errorf("no KakaoTalk database file found in %s", dir)
	case 1:
		return candidates[0], nil
	default:
		best, bestMod := candidates[0], time.Time{}
		for _, c := range candidates {
			if fi, err := os.Stat(c); err == nil && fi.ModTime().After(bestMod) {
				best, bestMod = c, fi.ModTime()
			}
		}
		return best, nil
	}
}

// copyLiveFiles copies main (only when its mtime changed) plus -wal/-shm into the
// scratch dir. Copying all three keeps the SQLite snapshot consistent so the
// newest, still-WAL-resident messages are visible.
func (r *Reader) copyLiveFiles() (string, error) {
	dst := filepath.Join(r.scratch, "work.db")
	fi, err := os.Stat(r.dbPath)
	if err != nil {
		return "", err
	}
	if !fi.ModTime().Equal(r.mainMod) {
		if err := copyFile(r.dbPath, dst); err != nil {
			return "", err
		}
		r.mainMod = fi.ModTime()
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		src := r.dbPath + suffix
		out := dst + suffix
		if _, err := os.Stat(src); err != nil {
			_ = os.Remove(out)
			continue
		}
		if err := copyFile(src, out); err != nil {
			return "", err
		}
	}
	return dst, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// runSQL copies the live files, runs the given SELECT through the sqlcipher CLI
// in JSON mode, and returns the raw JSON array bytes.
func (r *Reader) runSQL(query string) ([]byte, error) {
	work, err := r.copyLiveFiles()
	if err != nil {
		return nil, fmt.Errorf("copy live files: %w", err)
	}
	script := fmt.Sprintf("PRAGMA key='%s';\nPRAGMA cipher_compatibility=3;\n.mode json\n%s;\n",
		r.key, strings.TrimRight(query, "; \n"))
	cmd := exec.Command(r.bin, work)
	cmd.Stdin = strings.NewReader(script)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("sqlcipher: %v: %s", err, strings.TrimSpace(stderr.String()))
	}
	out := stdout.Bytes()
	// Strip the leading "ok" line emitted by the pragmas; JSON starts at '['.
	start := bytes.IndexByte(out, '[')
	end := bytes.LastIndexByte(out, ']')
	if start < 0 || end < start {
		return []byte("[]"), nil // no rows
	}
	return out[start : end+1], nil
}

func (r *Reader) queryScalar(query string, dst *int64) error {
	raw, err := r.runSQL(query)
	if err != nil {
		return err
	}
	var rows []map[string]any
	if err := json.Unmarshal(raw, &rows); err != nil {
		return fmt.Errorf("parse scalar: %w", err)
	}
	if len(rows) == 0 {
		*dst = 0
		return nil
	}
	for _, v := range rows[0] {
		switch n := v.(type) {
		case float64:
			*dst = int64(n)
		case json.Number:
			i, _ := n.Int64()
			*dst = i
		}
		break
	}
	return nil
}

// messageRow mirrors the SELECT alias set below.
type messageRow struct {
	RowID         int64  `json:"rowid"`
	ChatID        int64  `json:"chat_id"`
	LogID         int64  `json:"log_id"`
	AuthorID      int64  `json:"author_id"`
	Type          int64  `json:"type"`
	Text          string `json:"text"`
	SentAt        int64  `json:"sent_at"`
	RoomType      int64  `json:"room_type"`
	DirectMember  int64  `json:"direct_member"`
	ActiveMembers int64  `json:"active_members"`
	ChatName      string `json:"chat_name"`
	OpenLink      string `json:"open_link"`
	PeerName      string `json:"peer_name"`
	Nick          string `json:"nick"`
	Attachment    string `json:"attachment"`
}

// messageSelect joins each message to its room (NTChatRoom.linkId → NTOpenLink),
// the author's display name, and, for 1:1 rooms, the peer's name so direct chats
// (which store no chatName) still resolve to a human label.
const messageSelect = `
SELECT m.ROWID AS rowid, m.chatId AS chat_id, m.logId AS log_id, m.authorId AS author_id,
       m.type AS type, m.message AS text, m.sentAt AS sent_at,
       COALESCE(r.type,0) AS room_type,
       COALESCE(r.directChatMemberUserId,0) AS direct_member,
       COALESCE(r.activeMembersCount,0) AS active_members,
       COALESCE(r.chatName,'') AS chat_name,
       COALESCE(o.linkName,'') AS open_link,
       COALESCE(pu.nickName,pu.friendNickName,'') AS peer_name,
       COALESCE(u.nickName,u.friendNickName,'') AS nick,
       COALESCE(m.attachment,'') AS attachment
FROM NTChatMessage m
LEFT JOIN NTChatRoom r ON r.chatId=m.chatId
LEFT JOIN NTOpenLink o ON o.linkId=r.linkId
LEFT JOIN NTUser u  ON u.userId=m.authorId
LEFT JOIN NTUser pu ON pu.userId=r.directChatMemberUserId`

func (r *Reader) toMessage(row messageRow) Message {
	m := Message{
		RowID: row.RowID, ChatID: row.ChatID, AuthorID: row.AuthorID,
		Type: row.Type, Text: fullText(row.Text, row.Attachment), SentAt: row.SentAt,
	}
	m.MessageID = fmt.Sprintf("%d-%d", row.ChatID, row.LogID)
	m.IsMine = row.AuthorID == r.userID
	m.ChatType = classifyRoom(row.RoomType, row.DirectMember, row.ActiveMembers)
	m.ChatName = firstNonEmpty(row.ChatName, row.OpenLink, row.PeerName, fmt.Sprintf("chat-%d", row.ChatID))
	if m.IsMine {
		m.Author = firstNonEmpty(row.Nick, "나")
	} else {
		m.Author = firstNonEmpty(row.Nick, fmt.Sprintf("user-%d", row.AuthorID))
	}
	return m
}

// fullText returns the untruncated body. For AlimTalk/bizmessage rows (type 72)
// the message column and attachment P.ME hold only a ≤200-char chat-list
// preview; the full body lives in attachment C.TI.TD.T. Other layouts (e.g.
// type 71) keep a short title there, so the longest candidate wins.
func fullText(message, attachment string) string {
	if !strings.Contains(attachment, `"ME"`) && !strings.Contains(attachment, `"TD"`) {
		return message
	}
	var a struct {
		P struct {
			ME string `json:"ME"`
		} `json:"P"`
		C struct {
			TI struct {
				TD struct {
					T string `json:"T"`
				} `json:"TD"`
			} `json:"TI"`
		} `json:"C"`
	}
	if err := json.Unmarshal([]byte(attachment), &a); err != nil {
		return message
	}
	best := message
	for _, c := range []string{a.P.ME, a.C.TI.TD.T} {
		if len([]rune(c)) > len([]rune(best)) {
			best = c
		}
	}
	return best
}

// MessagesSinceRowID returns text messages with ROWID greater than the cursor,
// ascending, capped at limit. ROWID is the PK B-tree so this is the indexed
// incremental fast path for the poller.
func (r *Reader) MessagesSinceRowID(cursor int64, limit int) ([]Message, error) {
	q := fmt.Sprintf(`%s WHERE m.ROWID > %d AND m.message IS NOT NULL AND m.message <> ''
		ORDER BY m.ROWID ASC LIMIT %d`, messageSelect, cursor, limit)
	raw, err := r.runSQL(q)
	if err != nil {
		return nil, err
	}
	var rows []messageRow
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, fmt.Errorf("parse messages: %w", err)
	}
	out := make([]Message, 0, len(rows))
	for _, row := range rows {
		out = append(out, r.toMessage(row))
	}
	return out, nil
}

// MessagesWithAttachmentBody returns text rows in [from, to] whose attachment
// carries a P.ME or C.TI.TD body, ascending. Used to re-read rows stored before fullText
// existed so their truncated previews can be replaced.
func (r *Reader) MessagesWithAttachmentBody(from, to int64) ([]Message, error) {
	q := fmt.Sprintf(`%s WHERE m.ROWID BETWEEN %d AND %d AND m.message IS NOT NULL AND m.message <> ''
		AND (m.attachment LIKE '%%"ME"%%' OR m.attachment LIKE '%%"TD"%%')
		ORDER BY m.ROWID ASC`, messageSelect, from, to)
	raw, err := r.runSQL(q)
	if err != nil {
		return nil, err
	}
	var rows []messageRow
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, fmt.Errorf("parse messages: %w", err)
	}
	out := make([]Message, 0, len(rows))
	for _, row := range rows {
		out = append(out, r.toMessage(row))
	}
	return out, nil
}

// MaxRowID returns the current maximum ROWID of NTChatMessage.
func (r *Reader) MaxRowID() (int64, error) {
	var max int64
	err := r.queryScalar("SELECT COALESCE(MAX(ROWID),0) AS n FROM NTChatMessage", &max)
	return max, err
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func classifyRoom(roomType, directMember, activeMembers int64) string {
	if roomType == 0 || roomType == 2 || directMember > 0 || activeMembers == 2 {
		return "direct"
	}
	return "group"
}

func platformUUID() (string, error) {
	out, err := exec.Command("ioreg", "-rd1", "-c", "IOPlatformExpertDevice").Output()
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "IOPlatformUUID") {
			parts := strings.Split(line, "\"")
			if len(parts) >= 4 {
				return parts[3], nil
			}
		}
	}
	return "", fmt.Errorf("IOPlatformUUID not found in ioreg output")
}
