package api

import (
	"bufio"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/output-svc/internal/live"
	"github.com/choigonyok/jarvis/output-svc/internal/store"
)

// An open console's stream outlives the server's read and write limits, and
// what goes out reaches it long after both - not only within them.
func TestTheStreamOutlivesTheServersLimits(t *testing.T) {
	hub := live.New()
	s := New(nil, nil, nil, hub, "tok", slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv := httptest.NewUnstartedServer(s.Handler())
	srv.Config.ReadTimeout, srv.Config.WriteTimeout = 200*time.Millisecond, 300*time.Millisecond
	srv.Start()
	defer srv.Close()

	if res, err := http.Get(srv.URL + "/stream"); err != nil || res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("토큰 없이: %v %v", res, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/stream", nil)
	req.Header.Set("Authorization", "Bearer tok")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	lines := make(chan string)
	go func() {
		sc := bufio.NewScanner(res.Body)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()
	if first := <-lines; first != ": open" {
		t.Fatalf("첫 줄 %q", first)
	}
	time.Sleep(800 * time.Millisecond)
	hub.Broadcast(store.Owner, []byte(`{"id":1}`))
	for line := range lines {
		if strings.HasPrefix(line, "data: ") {
			if line != `data: {"id":1}` {
				t.Fatalf("%q", line)
			}
			return
		}
	}
	t.Fatal("한도가 지난 뒤 보낸 것이 닿지 않았습니다")
}
