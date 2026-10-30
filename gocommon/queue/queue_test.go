package queue

import (
	"encoding/json"
	"testing"
	"time"
)

func TestMatches(t *testing.T) {
	cases := []struct {
		patterns []string
		kind     string
		want     bool
	}{
		{[]string{"*"}, "anything", true},
		{[]string{"mind.card"}, "mind.card", true},
		{[]string{"mind.card"}, "mind.card.closed", false},
		{[]string{"remind.*"}, "remind.asked", true},
		{[]string{"remind.*"}, "remind", false},
		{[]string{"a", "action.*"}, "action.undone", true},
		{nil, "x", false},
	}
	for _, c := range cases {
		if got := Matches(c.patterns, c.kind); got != c.want {
			t.Errorf("Matches(%v, %q) = %v", c.patterns, c.kind, got)
		}
	}
}

// A consumer that still reads the event shape gets what the log gave it.
func TestEventKeepsDataAndWordingApart(t *testing.T) {
	at := time.Date(2026, 10, 31, 1, 2, 3, 0, time.UTC)
	raw, err := Message{ID: 7, Kind: "remind.asked", Source: "mind", SourceKey: "remind:1", Subject: "s", OccurredAt: at,
		Payload: json.RawMessage(`{"card":true,"notify":{"tier":"now","title":"약"}}`)}.Event()
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		ID         int64             `json:"id"`
		Type       string            `json:"type"`
		Source     string            `json:"source"`
		Subject    string            `json:"subject"`
		Key        string            `json:"key"`
		Data       map[string]any    `json:"data"`
		Notify     map[string]string `json:"notify"`
		OccurredAt time.Time         `json:"occurredAt"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != 7 || got.Type != "remind.asked" || got.Source != "mind" || got.Subject != "s" || got.Key != "remind:1" || !got.OccurredAt.Equal(at) {
		t.Fatalf("%+v", got)
	}
	if got.Data["card"] != true || got.Data["notify"] != nil || got.Notify["title"] != "약" {
		t.Fatalf("data 와 문구가 섞였습니다: %+v", got)
	}
}

// Every stream is looked over.
func TestAllIsEveryTopic(t *testing.T) {
	if len(All) != 4 {
		t.Fatalf("%d", len(All))
	}
}

func TestEventOfAnEmptyPayload(t *testing.T) {
	raw, err := Message{ID: 1, Kind: "x"}.Event()
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Data   json.RawMessage `json:"data"`
		Notify json.RawMessage `json:"notify"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if string(got.Data) != "{}" || got.Notify != nil {
		t.Fatalf("%s", raw)
	}
}
