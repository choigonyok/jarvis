package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"testing"

	"github.com/choigonyok/jarvis/gocommon/health"
	"github.com/choigonyok/jarvis/imessage-client/internal/imsg"
	"github.com/choigonyok/jarvis/imessage-client/internal/store"
)

// GET /messages pages back with before=<message_id> as the KakaoTalk
// collector does: the same shape and order, the messages older than that
// one. Without it the read is the newest as it was, and a message the chat
// does not have is a 404, never an empty page that would read as the start.
func TestMessagesBefore(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "imessage.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Upsert([]imsg.Message{
		{RowID: 1, MessageID: "g1", ChatID: 7, SentAt: 1000, Text: "가"},
		{RowID: 2, MessageID: "g2", ChatID: 7, SentAt: 1000, Text: "나"},
		{RowID: 3, MessageID: "g3", ChatID: 7, SentAt: 1001, Text: "다"},
	}); err != nil {
		t.Fatal(err)
	}
	h := New(st, &Status{}, "", health.NewLoops()).Handler()
	get := func(query string) (int, []string) {
		t.Helper()
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/messages?"+query, nil))
		var body struct {
			Messages []imsg.Message `json:"messages"`
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
		{"chat=7&limit=2", 200, []string{"g3", "g2"}},
		{"chat=7&limit=2&before=", 200, []string{"g3", "g2"}},
		{"chat=7&limit=2&before=g2", 200, []string{"g1"}},
		{"chat=7&limit=2&before=g1", 200, nil},
		{"chat=7&before=g404", 404, nil},
		{"chat=8&before=g2", 404, nil},
		{"before=g2", 400, nil},
	} {
		code, got := get(c.query)
		if code != c.code || !slices.Equal(got, c.want) {
			t.Errorf("%s: %d %v, 기대 %d %v", c.query, code, got, c.code, c.want)
		}
	}
}
