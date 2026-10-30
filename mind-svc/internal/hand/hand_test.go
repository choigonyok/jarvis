package hand

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/gocommon/queue"
	"github.com/choigonyok/jarvis/mind-svc/internal/store"
)

// lane answers each call as output-svc would, by way of the store.
type lane struct {
	st     *store.Memory
	got    []queue.Message
	status int
	body   string
	silent bool
}

func (l *lane) publish(ctx context.Context, m queue.Message) error {
	l.got = append(l.got, m)
	if l.silent {
		return nil
	}
	raw, _ := json.Marshal(map[string]any{"key": m.SourceKey, "status": l.status, "body": json.RawMessage(l.body)})
	go func() { _ = Result(ctx, l.st, queue.Message{Payload: raw}) }()
	return nil
}

// A write goes out as an effect, and comes back as the answer the actuator
// was expecting from the adapter.
func TestAWriteGoesThroughTheHand(t *testing.T) {
	l := &lane{st: store.NewMemory(), status: 201, body: `{"id":"e1"}`}
	client := &http.Client{Transport: &Transport{Publish: l.publish, Store: l.st, Now: func() time.Time { return time.Unix(1000, 0) }}}
	resp, err := client.Post("http://calendar:8093/events?x=1", "application/json", strings.NewReader(`{"title":"치과"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 201 || string(body) != `{"id":"e1"}` {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	if len(l.got) != 1 {
		t.Fatalf("%d", len(l.got))
	}
	m := l.got[0]
	var c Call
	if err := json.Unmarshal(m.Payload, &c); err != nil {
		t.Fatal(err)
	}
	if m.Kind != KindCall || c.Service != "calendar" || c.Method != "POST" || c.Path != "/events?x=1" || string(c.Body) != `{"title":"치과"}` {
		t.Fatalf("%+v %+v", m, c)
	}
	if !c.Deadline.Equal(time.Unix(1000, 0).Add(Wait)) {
		t.Fatalf("기한: %v", c.Deadline)
	}
}

// A read is not a write: it goes as it did.
func TestAReadGoesDirectly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{}`)) }))
	defer srv.Close()
	l := &lane{st: store.NewMemory()}
	client := &http.Client{Transport: &Transport{Publish: l.publish, Store: l.st}}
	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if len(l.got) != 0 {
		t.Fatalf("읽기가 효과로 나갔습니다: %+v", l.got)
	}
}

// No answer in time is an error, and the deadline in the call is what stops
// it happening after all.
func TestNoAnswerIsAFailure(t *testing.T) {
	l := &lane{st: store.NewMemory(), silent: true}
	client := &http.Client{Transport: &Transport{Publish: l.publish, Store: l.st, Wait: 80 * time.Millisecond}}
	if _, err := client.Post("http://assets:8092/orders", "application/json", strings.NewReader(`{}`)); err == nil {
		t.Fatal("답이 없으면 실패입니다")
	}
}

func TestAPublishThatFailsIsAFailure(t *testing.T) {
	boom := errors.New("끊김")
	client := &http.Client{Transport: &Transport{Store: store.NewMemory(), Publish: func(context.Context, queue.Message) error { return boom }}}
	if _, err := client.Post("http://assets:8092/orders", "application/json", nil); !errors.Is(err, boom) {
		t.Fatalf("%v", err)
	}
}

func TestAResultWithNoKeyIsRefused(t *testing.T) {
	err := Result(context.Background(), store.NewMemory(), queue.Message{Payload: json.RawMessage(`{}`)})
	if !errors.Is(err, queue.ErrRefused) {
		t.Fatalf("%v", err)
	}
}
