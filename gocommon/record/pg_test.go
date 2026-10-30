package record

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/gocommon/health"
	"github.com/jackc/pgx/v5/pgxpool"
)

// admin is the database's owner: nothing else can read input.records.
func admin(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("JARVIS_TEST_ADMIN_DSN")
	if dsn == "" {
		t.Skip("JARVIS_TEST_ADMIN_DSN 이 없습니다 (scripts/dbtest gocommon)")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// asAgent is a DSN as the role that writes records in the stack.
func asAgent(t *testing.T) string {
	t.Helper()
	return strings.Replace(os.Getenv("JARVIS_TEST_ADMIN_DSN"), "jarvis:test@", "svc_agent:test@", 1)
}

func uniqueKey(t *testing.T, name string) string {
	return t.Name() + "-" + name + "-" + time.Now().Format("150405.000000")
}

func TestAppendKeepsOneOfAKey(t *testing.T) {
	db := admin(t)
	ctx := context.Background()
	key := uniqueKey(t, "k")
	w := New(asAgent(t), health.NewLoops(), quietLog())
	stop := start(t, w)
	r := valid(key)
	r.TraceID, r.Subject, r.CausationID, r.Channel = "job:9", "coupang.com", "q-1", "browser"
	r.OccurredAt = time.Date(2026, 10, 30, 1, 2, 3, 0, time.UTC)
	w.Write(r)
	w.Write(r)
	stop()

	var n, version int
	var kind, trace, tier, payload string
	var occurred, received time.Time
	err := db.QueryRow(ctx, `select count(*) over (), kind, trace_id, tier, schema_version, occurred_at, received_at, payload::text
		from input.records where source = 'agent' and source_key = $1`, key).
		Scan(&n, &kind, &trace, &tier, &version, &occurred, &received, &payload)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 || kind != "guard.query" || trace != "job:9" || tier != Durable || version != 1 {
		t.Fatalf("기록: n=%d %s %s %s v%d", n, kind, trace, tier, version)
	}
	if !occurred.Equal(r.OccurredAt) || time.Since(received) > time.Minute {
		t.Fatalf("일어난 시각은 낸 쪽이, 받은 시각은 서버가: %v %v", occurred, received)
	}
	if !strings.Contains(payload, `"a": "b"`) {
		t.Fatalf("payload: %s", payload)
	}
}

// What was decided is not changed; what is raw can go when its time is up.
func TestRecordsAreNotRewritten(t *testing.T) {
	db := admin(t)
	ctx := context.Background()
	durable, raw := uniqueKey(t, "durable"), uniqueKey(t, "raw")
	w := New(asAgent(t), health.NewLoops(), quietLog())
	stop := start(t, w)
	w.Write(valid(durable))
	r := valid(raw)
	r.Tier = Raw
	w.Write(r)
	stop()

	if _, err := db.Exec(ctx, `update input.records set kind = 'x' where source_key = $1`, raw); err == nil {
		t.Fatal("고칠 수 없어야 합니다")
	}
	if _, err := db.Exec(ctx, `delete from input.records where source_key = $1`, durable); err == nil {
		t.Fatal("지속층은 지울 수 없어야 합니다")
	}
	if _, err := db.Exec(ctx, `delete from input.records where source_key = $1`, raw); err != nil {
		t.Fatalf("원본층은 지울 수 있어야 합니다: %v", err)
	}
}

// The service's role writes through input.append and nothing else.
func TestTheRoleCannotTouchTheTable(t *testing.T) {
	admin(t)
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, asAgent(t))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := pool.QueryRow(ctx, `select count(*) from input.records`).Scan(new(int)); err == nil {
		t.Fatal("읽을 수 없어야 합니다")
	}
	if _, err := pool.Exec(ctx, `insert into input.records (kind, source, source_key, actor, occurred_at, tier)
		values ('x', 'x', 'x', 'x', now(), 'raw')`); err == nil {
		t.Fatal("직접 쓸 수 없어야 합니다")
	}
}

// Every line of a job's progress is a record, the guest's are not, and a
// record that cannot be written never stops the job's own line.
func TestJobEventsBecomeRecords(t *testing.T) {
	db := admin(t)
	ctx := context.Background()
	var owner, guest int64
	if err := db.QueryRow(ctx, `insert into jobs (title, goal, who) values ('기록 시험', '목표', 'owner') returning id`).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, `insert into jobs (title, goal, who) values ('손님 시험', '목표', 'guest') returning id`).Scan(&guest); err != nil {
		t.Fatal(err)
	}
	insert := func(job int64, text string) int64 {
		tx, err := db.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		// As the service does it: svc_jobs has no rights on the input schema.
		if _, err := tx.Exec(ctx, `set local role svc_jobs`); err != nil {
			t.Fatal(err)
		}
		var id int64
		if err := tx.QueryRow(ctx, `insert into job_events (job_id, kind, text) values ($1, 'step', $2) returning id`, job, text).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		return id
	}
	id := insert(owner, "열기: https://example.com")
	var kind, actor, trace, tier, text string
	err := db.QueryRow(ctx, `select kind, actor, trace_id, tier, payload->>'text' from input.records
		where source = 'jobs' and source_key = $1`, fmt.Sprintf("job_event:%d", id)).Scan(&kind, &actor, &trace, &tier, &text)
	if err != nil {
		t.Fatalf("기록이 없습니다: %v", err)
	}
	if kind != "job.step" || !strings.HasPrefix(actor, "job:") || trace != actor || tier != Raw || text != "열기: https://example.com" {
		t.Fatalf("기록: %s %s %s %s %q", kind, actor, trace, tier, text)
	}

	gid := insert(guest, "손님 일")
	var n int
	if err := db.QueryRow(ctx, `select count(*) from input.records where source_key = $1`, fmt.Sprintf("job_event:%d", gid)).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("손님의 작업은 운영자 기록에 들어가지 않습니다")
	}
}
