package sensor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/model"
)

// PhotoDays remembers each day's photos once the day is over: where they
// were taken and with whom (named people only), as memory's photo collector
// did. Days before the first run were that collector's.
func (s *Structured) PhotoDays(ctx context.Context) error {
	if s.Records == nil || s.ImmichURL == "" || s.ImmichKey == "" {
		return nil
	}
	now := s.Now().In(s.Loc)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, s.Loc)
	next, err := s.Store.Cursor(ctx, "records:photo-days")
	if err != nil {
		return err
	}
	if next == 0 {
		return s.Store.SetCursor(ctx, "records:photo-days", dayNum(today))
	}
	day, err := time.ParseInLocation("20060102", strconv.FormatInt(next, 10), s.Loc)
	if err != nil {
		return err
	}
	for ; day.Before(today); day = day.AddDate(0, 0, 1) {
		items, err := s.photosOn(ctx, day, day.AddDate(0, 0, 1))
		if err != nil {
			return err
		}
		for _, r := range PhotoDayRecords(day, items) {
			r.ValidAt = day.Format(time.RFC3339)
			if err := s.record(ctx, r); err != nil {
				return err
			}
		}
		if err := s.Store.SetCursor(ctx, "records:photo-days", dayNum(day.AddDate(0, 0, 1))); err != nil {
			return err
		}
	}
	return nil
}

func dayNum(t time.Time) int64 {
	n, _ := strconv.ParseInt(t.Format("20060102"), 10, 64)
	return n
}

func (s *Structured) photosOn(ctx context.Context, from, to time.Time) ([]asset, error) {
	var items []asset
	for page := 1; page > 0 && page < 50; {
		body, _ := json.Marshal(map[string]any{"takenAfter": from.UTC().Format(time.RFC3339), "takenBefore": to.UTC().Format(time.RFC3339),
			"withExif": true, "withPeople": true, "size": 1000, "page": page})
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(s.ImmichURL, "/")+"/api/search/metadata", bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-api-key", s.ImmichKey)
		var out struct {
			Assets struct {
				Items    []asset `json:"items"`
				NextPage *string `json:"nextPage"`
			} `json:"assets"`
		}
		if err := s.do(req, &out); err != nil {
			return nil, err
		}
		items = append(items, out.Assets.Items...)
		page = 0
		if out.Assets.NextPage != nil {
			page, _ = strconv.Atoi(*out.Assets.NextPage)
		}
	}
	return items, nil
}

// PhotoDayRecords is a day's photos as memory keeps them: each place with
// how many and with whom, each person with how many and where - or only how
// many, when they say neither.
func PhotoDayRecords(day time.Time, items []asset) []model.Record {
	date := day.Format("2006-01-02")
	type count struct {
		name string
		n    int
	}
	var places, people []count
	add := func(list []count, name string) []count {
		for i := range list {
			if list[i].name == name {
				list[i].n++
				return list
			}
		}
		return append(list, count{name, 1})
	}
	n := 0
	for _, it := range items {
		if it.IsTrashed {
			continue
		}
		n++
		if it.ExifInfo != nil {
			var parts []string
			for _, p := range []*string{it.ExifInfo.City, it.ExifInfo.Country} {
				if p != nil && *p != "" {
					parts = append(parts, *p)
				}
			}
			if len(parts) > 0 {
				places = add(places, strings.Join(parts, ", "))
			}
		}
		for _, p := range it.People {
			if p.Name != "" {
				people = add(people, p.Name)
			}
		}
	}
	if n == 0 {
		return nil
	}
	names := func(list []count) string {
		out := make([]string, len(list))
		for i, c := range list {
			out[i] = c.name
		}
		return strings.Join(out, ", ")
	}
	var out []model.Record
	for _, p := range places {
		fact := fmt.Sprintf("%s %s에서 사진 %d장", date, p.name, p.n)
		if len(people) > 0 {
			fact += " (함께: " + names(people) + ")"
		}
		out = append(out, model.Record{Key: fmt.Sprintf("photo-place:%s:%s", date, p.name), Relation: "사진 찍은 곳",
			Target: model.Node{Kind: "Place", Key: p.name, Name: p.name}, Fact: fact})
	}
	for _, p := range people {
		fact := fmt.Sprintf("%s %s와(과) 함께 찍은 사진 %d장", date, p.name, p.n)
		if len(places) > 0 {
			fact += " (" + names(places) + ")"
		}
		out = append(out, model.Record{Key: fmt.Sprintf("photo-person:%s:%s", date, p.name), Relation: "함께 사진",
			Target: model.Node{Kind: "Person", Key: p.name, Name: p.name}, Fact: fact})
	}
	if len(places) == 0 && len(people) == 0 {
		out = append(out, model.Record{Key: "photo-day:" + date, Relation: "사진",
			Target: model.Node{Kind: "PhotoDay", Key: date, Name: date + " 사진"}, Fact: fmt.Sprintf("%s에 사진·영상 %d개", date, n)})
	}
	return out
}
