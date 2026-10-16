package uniple

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
)

// B-7: the refresh token rotates on every use. When writing the new one down
// fails, the client must still use the new one next time - sending the spent
// one logs the session out. Against a throwaway Postgres (scripts/dbtest) and
// a fake uniple.
func TestRotatedTokenSurvivesAFailedSave(t *testing.T) {
	dsn := os.Getenv("JARVIS_TEST_DSN")
	if dsn == "" {
		t.Skip("JARVIS_TEST_DSN 이 없어 건너뜁니다 (scripts/dbtest)")
	}
	ctx := context.Background()
	admin := os.Getenv("JARVIS_TEST_ADMIN_DSN")
	if admin == "" {
		admin = dsn
	}
	conn, err := pgx.Connect(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	_, err = conn.Exec(ctx, `delete from uniple_session`)
	conn.Close(ctx)
	if err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var sent []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			RefreshToken string `json:"refresh_token"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		mu.Lock()
		sent = append(sent, in.RefreshToken)
		n := len(sent)
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": fmt.Sprintf("at-%d", n), "refresh_token": fmt.Sprintf("rt-%d", n),
			"expires_in": 3600, "user": map[string]string{"id": "u1"},
		})
	}))
	defer srv.Close()

	c, err := Open(ctx, dsn, Config{BaseURL: srv.URL, AnonKey: "anon", SeedRefresh: "rt-0"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.token(ctx, true); err != nil { // rt-0 → rt-1, saved
		t.Fatal(err)
	}
	c.pool.Close() // from here every save fails
	if _, err := c.token(ctx, true); err != nil { // rt-1 → rt-2, not saved
		t.Fatalf("저장에 실패해도 받은 토큰으로 계속해야 합니다: %v", err)
	}
	if _, err := c.token(ctx, true); err != nil { // must send rt-2
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if want := []string{"rt-0", "rt-1", "rt-2"}; fmt.Sprint(sent) != fmt.Sprint(want) {
		t.Fatalf("보낸 refresh 토큰 %v, want %v (쓴 토큰을 다시 보내면 세션이 끊긴다)", sent, want)
	}
	if !c.unsaved {
		t.Fatal("적지 못한 토큰은 다음에 다시 적어야 합니다")
	}
}
