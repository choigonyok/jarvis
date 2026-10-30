package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func fakeImmich(t *testing.T, pages int) (*httptest.Server, *[]map[string]any) {
	t.Helper()
	var asked []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/search/metadata" || r.Header.Get("x-api-key") != "key" {
			t.Errorf("%s %s", r.URL.Path, r.Header.Get("x-api-key"))
		}
		var q map[string]any
		_ = json.NewDecoder(r.Body).Decode(&q)
		asked = append(asked, q)
		page := int(q["page"].(float64))
		var next any
		if page < pages {
			next = strconv.Itoa(page + 1)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"assets": map[string]any{
			"items":    []map[string]any{{"id": "a" + strconv.Itoa(page), "fileCreatedAt": "2026-10-30T10:00:00Z", "exifInfo": map[string]any{"city": "가평"}, "people": []map[string]any{{"name": "민수"}}, "thumbhash": "버릴것"}},
			"nextPage": next,
		}})
	}))
	t.Cleanup(srv.Close)
	return srv, &asked
}

// Every page is read, and what goes out is what is used of a photo.
func TestRecentReadsEveryPage(t *testing.T) {
	srv, asked := fakeImmich(t, 3)
	im := Immich{URL: srv.URL + "/", Key: "key", HTTP: srv.Client()}
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	v, err := im.Recent(func() time.Time { return now })(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(v)
	var got struct {
		Items []Asset `json:"items"`
	}
	if err := json.Unmarshal(raw, &got); err != nil || len(got.Items) != 3 || got.Items[0].People[0].Name != "민수" {
		t.Fatalf("%s %v", raw, err)
	}
	if strings.Contains(string(raw), "버릴것") {
		t.Fatalf("쓰지 않는 것은 나가지 않습니다: %s", raw)
	}
	if (*asked)[0]["takenAfter"] != "2026-10-29T12:00:00Z" || (*asked)[0]["takenBefore"] != nil {
		t.Fatalf("%v", (*asked)[0])
	}
}

// The days that are over, each by itself, in the home time zone.
func TestDaysAreTheFinishedOnes(t *testing.T) {
	srv, asked := fakeImmich(t, 1)
	loc := time.FixedZone("KST", 9*60*60)
	im := Immich{URL: srv.URL, Key: "key", HTTP: srv.Client()}
	now := time.Date(2026, 10, 31, 1, 0, 0, 0, loc)
	v, err := im.Days(func() time.Time { return now }, loc)(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(v)
	var got struct {
		Days []Day `json:"days"`
	}
	if err := json.Unmarshal(raw, &got); err != nil || len(got.Days) != daysBack {
		t.Fatalf("%s %v", raw, err)
	}
	if got.Days[0].Date != "2026-10-28" || got.Days[2].Date != "2026-10-30" {
		t.Fatalf("%+v", got.Days)
	}
	last := (*asked)[len(*asked)-1]
	if last["takenAfter"] != "2026-10-29T15:00:00Z" || last["takenBefore"] != "2026-10-30T15:00:00Z" {
		t.Fatalf("%v", last)
	}
}

func TestAFailureIsSaid(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) }))
	defer srv.Close()
	im := Immich{URL: srv.URL, Key: "key", HTTP: srv.Client()}
	if _, err := im.Taken(context.Background(), time.Now(), nil); err == nil {
		t.Fatal("실패는 오류입니다")
	}
}
