// Package imsg reads the Messages app's database (chat.db).
package imsg

import (
	"bytes"
	"encoding/binary"
	"unicode/utf8"
)

// TextFromAttributedBody recovers the plain text of a message whose `text`
// column is empty. Since macOS Ventura most messages keep their text only in
// attributedBody: an NSAttributedString archived as a typedstream. The string
// is the first NSString in it, written as a marker, a length, then UTF-8.
//
// The length is one byte, or 0x81 followed by a little-endian uint16, or 0x82
// followed by a uint32. Anything that does not parse returns "".
func TextFromAttributedBody(blob []byte) string {
	i := bytes.Index(blob, []byte("NSString"))
	if i < 0 {
		return ""
	}
	rest := blob[i+len("NSString"):]
	// The class name is followed by its version bytes and then '+', which
	// starts the string's own data.
	j := bytes.IndexByte(rest, '+')
	if j < 0 || j > 8 {
		return ""
	}
	rest = rest[j+1:]
	if len(rest) == 0 {
		return ""
	}
	var n, skip int
	switch rest[0] {
	case 0x81:
		if len(rest) < 3 {
			return ""
		}
		n, skip = int(binary.LittleEndian.Uint16(rest[1:3])), 3
	case 0x82:
		if len(rest) < 5 {
			return ""
		}
		n, skip = int(binary.LittleEndian.Uint32(rest[1:5])), 5
	default:
		n, skip = int(rest[0]), 1
	}
	if skip+n > len(rest) {
		return ""
	}
	text := rest[skip : skip+n]
	if !utf8.Valid(text) {
		return ""
	}
	return string(text)
}
