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

// admin is the database's owner: producers cannot read what they wrote.
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

// The Writer leaves the envelope in the outbox: input-svc stores it.
func TestWritesTheEnvelopeToTheOutbox(t *testing.T) {
	db := admin(t)
	ctx := context.Background()
	key := uniqueKey(t, "k")
	w := New(asAgent(t), health.NewLoops(), quietLog())
	stop := start(t, w)
	r := valid(key)
	r.TraceID, r.Subject, r.CausationID, r.Channel = "job:9", "coupang.com", "q-1", "browser"
	r.OccurredAt = time.Date(2026, 10, 30, 1, 2, 3, 0, time.UTC)
	w.Write(r)
	stop()

	var kind, source, actor, channel, subject, tier, trace, cause, occurred, a string
	var version int
	err := db.QueryRow(ctx, `select payload->>'kind', payload->>'source', payload->>'actor', payload->>'channel', payload->>'subject',
		payload->>'tier', payload->>'trace_id', payload->>'causation_id', payload->>'occurred_at', (payload->>'schema_version')::int, payload->'payload'->>'a'
		from input.outbox where payload->>'source_key' = $1`, key).
		Scan(&kind, &source, &actor, &channel, &subject, &tier, &trace, &cause, &occurred, &version, &a)
	if err != nil {
		t.Fatal(err)
	}
	if kind != "guard.query" || source != "agent" || actor != "guard" || channel != "browser" || subject != "coupang.com" ||
		tier != Durable || trace != "job:9" || cause != "q-1" || version != 1 || a != "b" {
		t.Fatalf("봉투: %s %s %s %s %s %s %s %s v%d %s", kind, source, actor, channel, subject, tier, trace, cause, version, a)
	}
	if at, err := time.Parse(time.RFC3339, occurred); err != nil || !at.Equal(r.OccurredAt) {
		t.Fatalf("일어난 시각: %s %v", occurred, err)
	}
	if _, err := db.Exec(ctx, `delete from input.outbox where payload->>'source_key' = $1`, key); err != nil {
		t.Fatal(err)
	}
}

// What was decided is not changed; what is raw can go when its time is up.
func TestRecordsAreNotRewritten(t *testing.T) {
	db := admin(t)
	ctx := context.Background()
	durable, raw := uniqueKey(t, "durable"), uniqueKey(t, "raw")
	store := func(key, tier string) {
		t.Helper()
		if _, err := db.Exec(ctx, `select input.append(false, 'guard.query', 'agent', $1, 'guard', '', '', now(), $2, 1, '', '', '{}'::jsonb)`, key, tier); err != nil {
			t.Fatal(err)
		}
	}
	store(durable, Durable)
	store(raw, Raw)

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

// The service's role leaves records in the outbox and nothing else.
func TestTheRoleCannotTouchTheTable(t *testing.T) {
	admin(t)
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, asAgent(t))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
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
	if err := db.QueryRow(ctx, `insert into mind.jobs (title, goal, who) values ('기록 시험', '목표', 'owner') returning id`).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, `insert into mind.jobs (title, goal, who) values ('손님 시험', '목표', 'guest') returning id`).Scan(&guest); err != nil {
		t.Fatal(err)
	}
	insert := func(job int64, text string) int64 {
		tx, err := db.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		// As the service does it: svc_mind has no rights on the input schema.
		if _, err := tx.Exec(ctx, `set local role svc_mind`); err != nil {
			t.Fatal(err)
		}
		var id int64
		if err := tx.QueryRow(ctx, `insert into mind.job_events (job_id, kind, text) values ($1, 'step', $2) returning id`, job, text).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		return id
	}
	id := insert(owner, "열기: https://example.com")
	var kind, actor, trace, tier, text string
	err := db.QueryRow(ctx, `select payload->>'kind', payload->>'actor', payload->>'trace_id', payload->>'tier', payload->'payload'->>'text'
		from input.outbox where payload->>'source' = 'jobs' and payload->>'source_key' = $1`, fmt.Sprintf("job_event:%d", id)).Scan(&kind, &actor, &trace, &tier, &text)
	if err != nil {
		t.Fatalf("outbox 에 없습니다: %v", err)
	}
	if kind != "job.step" || !strings.HasPrefix(actor, "job:") || trace != actor || tier != Raw || text != "열기: https://example.com" {
		t.Fatalf("기록: %s %s %s %s %q", kind, actor, trace, tier, text)
	}

	gid := insert(guest, "손님 일")
	var n int
	if err := db.QueryRow(ctx, `select count(*) from input.outbox where payload->>'source_key' = $1`, fmt.Sprintf("job_event:%d", gid)).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("손님의 작업은 운영자 기록에 들어가지 않습니다")
	}
	if _, err := db.Exec(ctx, `delete from input.outbox where payload->>'source' = 'jobs'`); err != nil {
		t.Fatal(err)
	}
}
