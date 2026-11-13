package couple

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// regions.json is the date map's 시/군/구 - their codes and names - as the web
// draws them (web/public/maps/korea.json, from SGIS). A region is said by
// name in the conversation and kept by code.
//
//go:embed regions.json
var regionsJSON []byte

type region struct {
	Code string `json:"code"`
	Sido string `json:"sido"`
	Name string `json:"name"`
}

type regionTable struct {
	list      []region
	provinces map[string]string
	byCode    map[string]region
}

var regions = func() regionTable {
	var raw struct {
		Provinces map[string]string `json:"provinces"`
		Regions   []region          `json:"regions"`
	}
	if err := json.Unmarshal(regionsJSON, &raw); err != nil {
		panic("couple: regions.json: " + err.Error())
	}
	t := regionTable{list: raw.Regions, provinces: raw.Provinces, byCode: map[string]region{}}
	for _, r := range raw.Regions {
		t.byCode[r.Code] = r
	}
	return t
}()

// name is a region as people say it: "부산 해운대구". An unknown code is itself.
func (t regionTable) name(code string) string {
	r, ok := t.byCode[code]
	if !ok {
		return code
	}
	return t.provinces[r.Sido] + " " + r.Name
}

var fiveDigits = regexp.MustCompile(`^\d{5}$`)

// find is the one region a name means: "해운대구", "해운대", "부산 해운대구",
// "고양시 덕양구", or a five-digit code. A name several regions share says
// which ones, so the right one can be asked for.
func (t regionTable) find(said string) (string, error) {
	said = strings.TrimSpace(said)
	if fiveDigits.MatchString(said) {
		if _, ok := t.byCode[said]; ok {
			return said, nil
		}
		return "", fmt.Errorf("그런 지역 코드가 없습니다: %s", said)
	}
	flat := func(s string) string { return strings.ReplaceAll(s, " ", "") }
	want := flat(said)
	if want == "" {
		return "", fmt.Errorf("지역을 말해 주세요")
	}
	var exact, loose []region
	for _, r := range t.list {
		name, full := flat(r.Name), flat(t.provinces[r.Sido]+r.Name)
		switch {
		case want == name || want == full:
			exact = append(exact, r)
		case strings.HasPrefix(name, want) || strings.HasPrefix(full, want) || strings.Contains(name, want):
			loose = append(loose, r)
		}
	}
	found := exact
	if len(found) == 0 {
		found = loose
	}
	switch len(found) {
	case 1:
		return found[0].Code, nil
	case 0:
		return "", fmt.Errorf("'%s' 에 맞는 시/군/구가 없습니다. 시/군/구 이름으로 말해 주세요", said)
	}
	names := make([]string, 0, len(found))
	for i, r := range found {
		if i == 8 {
			names = append(names, "…")
			break
		}
		names = append(names, fmt.Sprintf("%s(%s)", t.provinces[r.Sido]+" "+r.Name, r.Code))
	}
	return "", fmt.Errorf("'%s' 인 곳이 여럿입니다: %s. 시/도와 함께 말하거나 코드를 넣으세요", said, strings.Join(names, ", "))
}
