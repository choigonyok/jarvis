// Package pay is the payments whose password is kept in the vault and typed on
// their own keypad, in one table (pins.json): the vault that keeps them, the
// agent that asks for them by name and the browser that types them each had a
// list of their own (2026-11-03).
package pay

import (
	_ "embed"
	"encoding/json"
	"strings"
)

// Pin is a payment with a keypad of its own.
type Pin struct {
	// Host is where its keypad is, and the hosts under it.
	Host string `json:"host"`
	// Name is what the operator calls it.
	Name string `json:"name"`
	// Aliases are what it may be asked for by, lowercased and without spaces.
	Aliases []string `json:"aliases"`
}

//go:embed pins.json
var pinsJSON []byte

var pins = func() []Pin {
	var t struct {
		Pins []Pin `json:"pins"`
	}
	if err := json.Unmarshal(pinsJSON, &t); err != nil || len(t.Pins) == 0 {
		panic("pay: pins.json: " + err.Error())
	}
	return t.Pins
}()

// Pins is every payment with a keypad.
func Pins() []Pin { return append([]Pin(nil), pins...) }

// PinFor is the payment someone asked for by any of its names.
func PinFor(said string) (Pin, bool) {
	said = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(said), " ", ""))
	for _, p := range pins {
		for _, a := range p.Aliases {
			if a == said {
				return p, true
			}
		}
	}
	return Pin{}, false
}

// PinAt is the payment whose keypad a host is, its own or one under it.
func PinAt(host string) (Pin, bool) {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, p := range pins {
		if host == p.Host || strings.HasSuffix(host, "."+p.Host) {
			return p, true
		}
	}
	return Pin{}, false
}

// Names is the payments as a sentence names them: "'네이버페이' 또는 '쿠팡 쿠페이'".
func Names() string {
	var out []string
	for _, p := range pins {
		out = append(out, "'"+p.Name+"'")
	}
	return strings.Join(out, " 또는 ")
}
