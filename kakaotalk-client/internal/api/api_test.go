package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"testing"

	"github.com/choigonyok/jarvis/gocommon/health"
	"github.com/choigonyok/jarvis/kakaotalk-client/internal/kakao"
	"github.com/choigonyok/jarvis/kakaotalk-client/internal/store"
)

// GET /messages pages back with before=<message_id>: the same shape and
// order as the newest page, the messages older than that one. Without it the
// read is the newest as it was, and a message the room does not have is a
// 404, never an empty page that would read as the start.
func TestMessagesBefore(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "kakaotalk.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.UpsertMessages([]kakao.Message{
		{RowID: 1, MessageID: "7-1", ChatID: 7, SentAt: 1000, Text: "가"},
		{RowID: 2, MessageID: "7-2", ChatID: 7, SentAt: 1000, Text: "나"},
		{RowID: 3, MessageID: "7-3", ChatID: 7, SentAt: 1001, Text: "다"},
	}, 1); err != nil {
		t.Fatal(err)
	}
	h := New(st, nil, "", health.NewLoops()).Handler()
	get := func(query string) (int, []string) {
		t.Helper()
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/messages?"+query, nil))
		var body struct {
			Messages []kakao.Message `json:"messages"`
		}
		if rec.Code == http.StatusOK {
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("%s: %v", query, err)
			}
		}
		var got []string
		for _, m := range body.Messages {
			got = append(got, m.MessageID)
		}
		return rec.Code, got
	}

	for _, c := range []struct {
		query string
		code  int
		want  []string
	}{
		{"chat=7&limit=2", 200, []string{"7-3", "7-2"}},
		{"chat=7&limit=2&before=", 200, []string{"7-3", "7-2"}},
		{"chat=7&limit=2&before=7-2", 200, []string{"7-1"}},
		{"chat=7&limit=2&before=7-1", 200, nil},
		{"chat=7&before=7-404", 404, nil},
		{"chat=8&before=7-2", 404, nil},
		{"before=7-2", 400, nil},
	} {
		code, got := get(c.query)
		if code != c.code || !slices.Equal(got, c.want) {
			t.Errorf("%s: %d %v, 기대 %d %v", c.query, code, got, c.code, c.want)
		}
	}
}
