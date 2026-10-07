package kakao

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/choigonyok/jarvis/kakaotalk-client/internal/derive"
)

const designatedPrefix = "DESIGNATEDFRIENDSREVISION:"

var lowerHex128 = regexp.MustCompile(`^[0-9a-f]{128}$`)

// resolveUserID finds the account id following the reference precedence:
//   1. AlertKakaoIDsList candidates (verified against the on-disk filename)
//   2. SHA-512 recovery from the active DESIGNATEDFRIENDSREVISION hash
func resolveUserID(cfg Config, uuid, filename string) (int64, error) {
	candidates, activeHash := scanPlists(cfg.PlistDir)

	for _, c := range candidates {
		if derive.Verify(c, uuid, filename) == nil {
			return c, nil
		}
	}

	if activeHash != "" {
		if id, ok := derive.RecoverUserIDFromSHA512(activeHash, cfg.RecoverMax, cfg.RecoverWorkers); ok {
			if derive.Verify(id, uuid, filename) == nil {
				return id, nil
			}
		}
	}

	return 0, fmt.Errorf("could not resolve KakaoTalk user id; set KAKAO_USER_ID explicitly")
}

// scanPlists returns AlertKakaoIDsList candidates and the active account hash
// (the DESIGNATEDFRIENDSREVISION key whose value is non-zero).
func scanPlists(dir string) (candidates []int64, activeHash string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, ""
	}
	seen := map[int64]bool{}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "com.kakao.KakaoTalkMac") || !strings.HasSuffix(name, ".plist") {
			continue
		}
		ids, hash := parsePlist(filepath.Join(dir, name))
		for _, id := range ids {
			if !seen[id] {
				seen[id] = true
				candidates = append(candidates, id)
			}
		}
		if activeHash == "" && hash != "" {
			activeHash = hash
		}
	}
	return candidates, activeHash
}

// parsePlist converts the plist to XML and scans for user-id candidates and the
// active designated-account hash. Only structure is read, never message bodies.
func parsePlist(path string) (ids []int64, activeHash string) {
	out, err := exec.Command("plutil", "-convert", "xml1", "-o", "-", path).Output()
	if err != nil {
		return nil, ""
	}
	xml := string(out)

	// AlertKakaoIDsList: <key>AlertKakaoIDsList</key><array><integer>..</integer>..
	if idx := strings.Index(xml, "<key>AlertKakaoIDsList</key>"); idx >= 0 {
		rest := xml[idx:]
		if start := strings.Index(rest, "<array>"); start >= 0 {
			if end := strings.Index(rest, "</array>"); end > start {
				for _, m := range regexp.MustCompile(`<integer>(\d+)</integer>`).FindAllStringSubmatch(rest[start:end], -1) {
					if v, err := strconv.ParseInt(m[1], 10, 64); err == nil && v > 0 {
						ids = append(ids, v)
					}
				}
			}
		}
	}

	// DESIGNATEDFRIENDSREVISION:<128hex> with a non-zero integer value marks the
	// active account. Walk key/value pairs in document order.
	frags := strings.Split(xml, "<")
	var pendingHash string
	for _, raw := range frags {
		gt := strings.Index(raw, ">")
		if gt < 0 {
			continue
		}
		tag := raw[:gt]
		body := strings.TrimSpace(raw[gt+1:])
		if tag == "key" {
			if h := strings.TrimPrefix(body, designatedPrefix); h != body && lowerHex128.MatchString(h) {
				pendingHash = h
			} else {
				pendingHash = ""
			}
			continue
		}
		if pendingHash != "" && (tag == "integer" || tag == "real") {
			if v, err := strconv.ParseInt(strings.TrimSpace(body), 10, 64); err == nil && v != 0 {
				if activeHash == "" {
					activeHash = pendingHash
				}
			}
			pendingHash = ""
		} else if pendingHash != "" && tag == "true" {
			if activeHash == "" {
				activeHash = pendingHash
			}
			pendingHash = ""
		}
	}
	return ids, activeHash
}
