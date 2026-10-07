// Package derive ports KakaoTalk macOS's SQLCipher key derivation.
//
// The passphrase and the on-disk database filename are both PBKDF2-SHA256
// functions of (user_id, IOPlatformUUID). This is a 1:1 port of the reference
// algorithm also implemented by lazykatok (Rust) and kakaotalk_mac.py. All
// inputs are ASCII (hex UUID + base-10 user id), so byte slicing is safe.
package derive

import (
	"crypto/pbkdf2"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"sync"
)

const (
	pbkdf2Rounds = 100_000
	pbkdf2DKLen  = 128
)

// hashedDeviceUUID = base64( sha1(uuid)[20] ++ sha256(uuid)[32] ).
func hashedDeviceUUID(uuid string) string {
	b := []byte(uuid)
	s1 := sha1.Sum(b)
	s2 := sha256.Sum256(b)
	combined := make([]byte, 0, 20+32)
	combined = append(combined, s1[:]...)
	combined = append(combined, s2[:]...)
	return base64.StdEncoding.EncodeToString(combined)
}

func reverseASCII(s string) string {
	b := []byte(s)
	for i, j := 0, len(b)-1; i < j; i, j = i+1, j-1 {
		b[i], b[j] = b[j], b[i]
	}
	return string(b)
}

// SecureKey returns the 256-lowercase-hex SQLCipher passphrase for the pair.
func SecureKey(userID int64, uuid string) string {
	hashed := hashedDeviceUUID(uuid)
	parts := []string{"A", hashed, "|", "F", uuid[0:5], "H", strconv.FormatInt(userID, 10), "|", uuid[7:]}
	hawawa := reverseASCII(strings.Join(parts, "F"))
	saltStart := int(float64(len(uuid)) * 0.3)
	salt := uuid[saltStart:]
	dk, err := pbkdf2.Key(sha256.New, hawawa, []byte(salt), pbkdf2Rounds, pbkdf2DKLen)
	if err != nil {
		panic(err) // inputs are fixed-size and valid; a failure is a programming bug
	}
	return hex.EncodeToString(dk)
}

// DatabaseName returns the 78-hex on-disk filename for the pair.
func DatabaseName(userID int64, uuid string) string {
	parts := []string{".", "F", strconv.FormatInt(userID, 10), "A", "F", reverseASCII(uuid), ".", "|"}
	hawawa := strings.Join(parts, ".")
	salt := reverseASCII(hashedDeviceUUID(uuid))
	dk, err := pbkdf2.Key(sha256.New, hawawa, []byte(salt), pbkdf2Rounds, pbkdf2DKLen)
	if err != nil {
		panic(err)
	}
	full := hex.EncodeToString(dk)
	return full[28 : 28+78]
}

// RecoverUserIDFromSHA512 finds the user id whose sha512(decimal) matches the
// target hash (the DESIGNATEDFRIENDSREVISION value in the KakaoTalk plist).
// Scans [0, max] across the given number of workers; returns (id, true) on hit.
func RecoverUserIDFromSHA512(targetHash string, max int64, workers int) (int64, bool) {
	targetHash = strings.ToLower(strings.TrimSpace(targetHash))
	if len(targetHash) != 128 {
		return 0, false
	}
	if workers < 1 {
		workers = 1
	}
	target, err := hex.DecodeString(targetHash)
	if err != nil {
		return 0, false
	}

	found := make(chan int64, 1)
	done := make(chan struct{})
	var wg sync.WaitGroup
	chunk := max/int64(workers) + 1

	for w := 0; w < workers; w++ {
		lo := int64(w) * chunk
		hi := lo + chunk
		if hi > max+1 {
			hi = max + 1
		}
		wg.Add(1)
		go func(lo, hi int64) {
			defer wg.Done()
			buf := make([]byte, 0, 12)
			for i := lo; i < hi; i++ {
				if i%(1<<20) == 0 { // cheap cancellation check
					select {
					case <-done:
						return
					default:
					}
				}
				buf = strconv.AppendInt(buf[:0], i, 10)
				sum := sha512.Sum512(buf)
				if equalBytes(sum[:], target) {
					select {
					case found <- i:
					default:
					}
					return
				}
			}
		}(lo, hi)
	}

	// Close found once every worker has exited so the reader unblocks even
	// when there is no match.
	go func() {
		wg.Wait()
		close(found)
	}()

	id, ok := <-found
	if ok {
		close(done) // stop the stragglers early
	}
	return id, ok
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Verify confirms a (user_id, uuid) pair by checking the derived filename
// equals the actual database filename found on disk.
func Verify(userID int64, uuid, actualFilename string) error {
	got := DatabaseName(userID, uuid)
	actual := strings.TrimSuffix(actualFilename, ".db")
	if got != actual {
		return fmt.Errorf("derived filename %s… does not match actual %s…", got[:12], actual[:min(12, len(actual))])
	}
	return nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
