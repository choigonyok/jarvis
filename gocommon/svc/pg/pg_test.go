package pg

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func shortly(t *testing.T) {
	t.Helper()
	oldTries, oldWait := tries, wait
	tries, wait = 3, 10*time.Millisecond
	t.Cleanup(func() { tries, wait = oldTries, oldWait })
}

// A start waits for the database a while, then gives up with what last went
// wrong; one that is told to stop stops waiting.
func TestRetryWaitsThenGivesUp(t *testing.T) {
	shortly(t)
	calls := 0
	got, err := Retry(context.Background(), quiet, func(context.Context) (string, error) {
		calls++
		if calls < 3 {
			return "", errors.New("아직")
		}
		return "열림", nil
	})
	if err != nil || got != "열림" || calls != 3 {
		t.Fatalf("%q %v %d", got, err, calls)
	}
	calls = 0
	_, err = Retry(context.Background(), quiet, func(context.Context) (int, error) {
		calls++
		return 0, errors.New("꺼져 있음")
	})
	if err == nil || err.Error() != "꺼져 있음" || calls != 3 {
		t.Fatalf("%v %d", err, calls)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Retry(ctx, quiet, func(context.Context) (int, error) { return 0, errors.New("x") }); !errors.Is(err, context.Canceled) {
		t.Fatalf("멈추라면 멈춥니다: %v", err)
	}
}

func TestADSNThatIsNotOneIsSaidAtOnce(t *testing.T) {
	shortly(t)
	start := time.Now()
	if _, err := Open(context.Background(), "postgres://a b c:::/", 2, quiet); err == nil {
		t.Fatal("해석되지 않는 주소")
	}
	if time.Since(start) > time.Second {
		t.Fatal("해석 오류는 기다리지 않습니다")
	}
	pool, err := Lazy(context.Background(), "postgres://svc@127.0.0.1:1/jarvis", 3)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if pool.Config().MaxConns != 3 {
		t.Fatalf("연결 수 %d", pool.Config().MaxConns)
	}
}

// Runs against a throwaway database (scripts/dbtest): a pool that answers.
func TestOpenAgainstADatabase(t *testing.T) {
	dsn := os.Getenv("JARVIS_TEST_DSN")
	if dsn == "" {
		t.Skip("JARVIS_TEST_DSN 이 없어 건너뜁니다")
	}
	pool, err := Open(context.Background(), dsn, 2, quiet)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if pool.Config().MaxConns != 2 {
		t.Fatalf("연결 수 %d", pool.Config().MaxConns)
	}
}
