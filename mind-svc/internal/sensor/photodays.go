package sensor

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/model"
)

// takePhotoDays remembers each day's photos once the day is over: where they
// were taken and with whom (named people only). Seeing a day again sends only
// what changed (a phone that uploaded late).
func (s *Structured) takePhotoDays(ctx context.Context, days []photoDay) error {
	if s.Records == nil {
		return nil
	}
	for _, d := range days {
		day, err := time.ParseInLocation("2006-01-02", d.Date, s.Loc)
		if err != nil {
			return err
		}
		for _, r := range PhotoDayRecords(day, d.Items) {
			r.ValidAt = day.Format(time.RFC3339)
			if err := s.record(ctx, r); err != nil {
				return err
			}
		}
	}
	return nil
}

type photoDay struct {
	Date  string  `json:"date"`
	Items []asset `json:"items"`
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
