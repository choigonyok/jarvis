package queue

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/choigonyok/jarvis/gocommon/health"
)

const (
	defaultLease   = 5 * time.Minute
	defaultTimeout = 2 * time.Minute
	defaultPoll    = 5 * time.Second
	settleTimeout  = 5 * time.Second
	mostOutageWait = 5 * time.Minute
)

// Consumer reads one topic under one name. A process runs one Consumer per
// name: Run gives back whatever the last one held.
type Consumer struct {
	Pool   *pgxpool.Pool
	Topic  Topic
	Name   string
	Kinds  []string
	Handle func(ctx context.Context, m Message) error
	Log    *slog.Logger
	// Workers is how many messages are handled at once; 1 by default, which
	// keeps the whole stream in order. More keep order per subject only.
	Workers int
	// Timeout bounds one message; Lease is how long a dead worker's message
	// waits to go again. Lease must be the longer.
	Timeout time.Duration
	Lease   time.Duration
	// Poll is how often it looks without being woken.
	Poll time.Duration
	// Backoff is the wait after the n-th failed try. A test shortens it.
	Backoff func(n int) time.Duration
}

func (c *Consumer) defaults() {
	if c.Workers <= 0 {
		c.Workers = 1
	}
	if c.Timeout <= 0 {
		c.Timeout = defaultTimeout
	}
	if c.Lease <= 0 {
		c.Lease = defaultLease
	}
	if c.Poll <= 0 {
		c.Poll = defaultPoll
	}
	if c.Backoff == nil {
		c.Backoff = func(n int) time.Duration { return health.Backoff(n, 5*time.Second, 5*time.Minute) }
	}
	if c.Log == nil {
		c.Log = slog.Default()
	}
}

// Run takes messages until ctx ends. It registers first, retrying until the
// database answers: a consumer started before the database is late, not gone.
func (c *Consumer) Run(ctx context.Context) {
	c.defaults()
	for wait := time.Second; ; wait = min(wait*2, time.Minute) {
		err := c.register(ctx)
		if err == nil {
			break
		}
		c.Log.Warn("구독을 등록하지 못해 다시 시도합니다", "consumer", c.Name, "err", err)
		if !sleep(ctx, wait) {
			return
		}
	}
	c.Log.Info("스트림을 읽기 시작했습니다", "consumer", c.Name, "topic", c.Topic.Name, "kinds", c.Kinds)

	wakes := make([]chan struct{}, c.Workers)
	for i := range wakes {
		wakes[i] = make(chan struct{}, 1)
	}
	go listen(ctx, c.Pool, c.Topic.channel(), func() {
		for _, w := range wakes {
			select {
			case w <- struct{}{}:
			default:
			}
		}
	}, c.Log)

	var wg sync.WaitGroup
	for _, w := range wakes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.work(ctx, w)
		}()
	}
	wg.Wait()
}

// register says what this consumer wants and starts clean. A process is the
// only one under its name, so what the name held belonged to a process that
// is gone, and a restart is usually what a held message was waiting for.
func (c *Consumer) register(ctx context.Context) error {
	tx, err := c.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `insert into `+c.Topic.Consumers+` (name, kinds) values ($1, $2)
		on conflict (name) do update set kinds = excluded.kinds, registered_at = now()`, c.Name, c.Kinds); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `update `+c.Topic.Deliveries+` set lease_until = null
		where consumer = $1 and lease_until is not null`, c.Name); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `update `+c.Topic.Deliveries+` set not_before = now()
		where consumer = $1 and held_at is not null and not_before is null`, c.Name); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// work is one worker: take the next message, handle it, say how it went.
func (c *Consumer) work(ctx context.Context, wake <-chan struct{}) {
	outages := 0
	for ctx.Err() == nil {
		d, ok, err := c.claim(ctx)
		switch {
		case err != nil:
			if ctx.Err() == nil {
				c.Log.Warn("메시지를 집지 못했습니다", "consumer", c.Name, "err", err)
			}
			if !sleep(ctx, c.Poll) {
				return
			}
			continue
		case !ok:
			select {
			case <-ctx.Done():
				return
			case <-wake:
			case <-time.After(c.Poll):
			}
			continue
		}
		wait, err := c.process(ctx, d, &outages)
		if err != nil && ctx.Err() == nil {
			// The verdict did not reach the database. The lease brings the
			// message back, and handlers take a message twice.
			c.Log.Warn("처리 결과를 남기지 못했습니다", "consumer", c.Name, "id", d.ID, "err", err)
		}
		if wait > 0 && !sleep(ctx, wait) {
			return
		}
	}
}

// delivery is a claimed row and its message.
type delivery struct {
	Message
	tries int
	held  bool
	// gone: the record is no longer there, which the retention should not
	// have let happen.
	gone bool
}

// claim takes the next message that may go: due, not leased, and with nothing
// of its subject before it that is not held.
func (c *Consumer) claim(ctx context.Context) (delivery, bool, error) {
	t := c.Topic
	q := fmt.Sprintf(`
		with next as (
		  select d.consumer, d.id from %[1]s d
		   where d.consumer = $1
		     and d.not_before is not null and d.not_before <= now()
		     and (d.lease_until is null or d.lease_until <= now())
		     and not exists (
		       select 1 from %[1]s e
		        where e.consumer = d.consumer and e.subject = d.subject and e.id < d.id and e.held_at is null)
		   order by d.id
		   limit 1
		   for update of d skip locked
		), taken as (
		  update %[1]s d set lease_until = now() + make_interval(secs => $2)
		    from next where d.consumer = next.consumer and d.id = next.id
		  returning d.id, d.at, d.tries, d.held_at
		)
		select t.id, t.at, t.tries, t.held_at is not null, m.id is null,
		       coalesce(m.kind, ''), coalesce(m.source, ''), coalesce(m.source_key, ''), coalesce(m.actor, ''),
		       coalesce(m.channel, ''), coalesce(m.subject, ''), coalesce(m.occurred_at, t.at), coalesce(m.tier, ''),
		       coalesce(m.schema_version, 1), coalesce(m.trace_id, ''), coalesce(m.causation_id, ''), m.payload
		  from taken t left join %[2]s m on m.received_at = t.at and m.id = t.id`, t.Deliveries, t.Messages)
	var d delivery
	err := c.Pool.QueryRow(ctx, q, c.Name, c.Lease.Seconds()).Scan(&d.ID, &d.At, &d.tries, &d.held, &d.gone,
		&d.Kind, &d.Source, &d.SourceKey, &d.Actor, &d.Channel, &d.Subject, &d.OccurredAt, &d.Tier,
		&d.SchemaVersion, &d.TraceID, &d.CausationID, &d.Payload)
	if errors.Is(err, pgx.ErrNoRows) {
		return delivery{}, false, nil
	}
	if err != nil {
		return delivery{}, false, err
	}
	return d, true, nil
}

// process handles one message and settles it, and says how long to wait
// before the next: an outage holds the worker, not the message's own try.
func (c *Consumer) process(ctx context.Context, d delivery, outages *int) (time.Duration, error) {
	if d.gone {
		c.Log.Error("기록이 없어 배달을 지웁니다", "consumer", c.Name, "id", d.ID)
		return 0, c.settle(c.ack(d.ID))
	}
	hctx, cancel := context.WithTimeout(ctx, c.Timeout)
	err := c.call(hctx, d.Message)
	cancel()
	if ctx.Err() != nil {
		// Stopping: it goes again next time, at no cost.
		return 0, c.settle(c.release(d.ID))
	}
	switch {
	case err == nil:
		*outages = 0
		return 0, c.settle(c.ack(d.ID))
	case errors.Is(err, ErrRefused):
		c.Log.Warn("받지 않는 메시지를 건너뜁니다", "consumer", c.Name, "id", d.ID, "kind", d.Kind, "err", err)
		*outages = 0
		return 0, c.settle(c.ack(d.ID))
	case errors.Is(err, ErrOutage):
		*outages++
		wait := min(time.Second<<min(*outages-1, 10), mostOutageWait)
		c.Log.Warn("상대가 받을 수 없어 기다립니다", "consumer", c.Name, "id", d.ID, "err", err, "wait", wait)
		return wait, c.settle(c.outage(d.ID, err))
	}
	*outages = 0
	tries := d.tries + 1
	c.Log.Warn("메시지를 처리하지 못했습니다", "consumer", c.Name, "id", d.ID, "kind", d.Kind, "tries", tries, "err", err)
	switch {
	case d.held:
		return 0, c.settle(c.heldFailed(d.ID, tries, err))
	case tries >= health.MaxTries:
		c.Log.Warn("처리하지 못하는 메시지를 격리함으로 빼고 넘어갑니다", "consumer", c.Name, "id", d.ID, "kind", d.Kind, "tries", tries)
		return 0, c.settle(c.hold(d.ID, tries, err))
	}
	return 0, c.settle(c.failed(d.ID, tries, c.Backoff(tries), err))
}

// call runs the handler; a panic is this message's failure, not the worker's.
func (c *Consumer) call(ctx context.Context, m Message) (err error) {
	defer func() {
		if r := recover(); r != nil {
			c.Log.Error("처리 중 패닉", "consumer", c.Name, "id", m.ID, "panic", r, "stack", string(debug.Stack()))
			err = fmt.Errorf("처리 중 패닉: %v", r)
		}
	}()
	return c.Handle(ctx, m)
}

// A verdict is settled whatever is happening to ctx: it is what stops the
// message going again for nothing.
func (c *Consumer) settle(f func(ctx context.Context, tx pgx.Tx) error) error {
	ctx, cancel := context.WithTimeout(context.Background(), settleTimeout)
	defer cancel()
	tx, err := c.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := f(ctx, tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// advance moves the cursor to the lower bound of what still waits.
func (c *Consumer) advance(ctx context.Context, tx pgx.Tx, id int64) error {
	_, err := tx.Exec(ctx, fmt.Sprintf(`
		update %[2]s c set high = greatest(c.high, $2), cursor = greatest(c.cursor, coalesce(
		    (select min(d.id) - 1 from %[1]s d where d.consumer = c.name and d.held_at is null), greatest(c.high, $2)))
		 where c.name = $1`, c.Topic.Deliveries, c.Topic.Consumers), c.Name, id)
	return err
}

// ack: it was taken. A consumer that was failing and now takes something
// works again, so what was held for it goes once more.
func (c *Consumer) ack(id int64) func(context.Context, pgx.Tx) error {
	return func(ctx context.Context, tx pgx.Tx) error {
		t := c.Topic
		if _, err := tx.Exec(ctx, `delete from `+t.Deliveries+` where consumer = $1 and id = $2`, c.Name, id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, fmt.Sprintf(`
			update %[1]s d set not_before = now()
			 where d.consumer = $1 and d.held_at is not null and d.not_before is null
			   and exists (select 1 from %[2]s c where c.name = $1 and c.failures > 0)`, t.Deliveries, t.Consumers), c.Name); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `update `+t.Consumers+` set failures = 0, last_error = '', failing_since = null,
			last_ok_at = now() where name = $1`, c.Name); err != nil {
			return err
		}
		return c.advance(ctx, tx, id)
	}
}

// release gives a message back without a verdict.
func (c *Consumer) release(id int64) func(context.Context, pgx.Tx) error {
	return func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `update `+c.Topic.Deliveries+` set lease_until = null where consumer = $1 and id = $2`, c.Name, id)
		return err
	}
}

func clip(err error) string {
	if r := []rune(err.Error()); len(r) > 300 {
		return string(r[:300])
	}
	return err.Error()
}

func (c *Consumer) failing(ctx context.Context, tx pgx.Tx, err error) error {
	_, e := tx.Exec(ctx, `update `+c.Topic.Consumers+` set failures = failures + 1, last_error = $2,
		failing_since = coalesce(failing_since, now()) where name = $1`, c.Name, clip(err))
	return e
}

// outage: the message is not at fault and goes again as it was.
func (c *Consumer) outage(id int64, cause error) func(context.Context, pgx.Tx) error {
	return func(ctx context.Context, tx pgx.Tx) error {
		if err := c.release(id)(ctx, tx); err != nil {
			return err
		}
		return c.failing(ctx, tx, cause)
	}
}

// failed: it goes again after wait.
func (c *Consumer) failed(id int64, tries int, wait time.Duration, cause error) func(context.Context, pgx.Tx) error {
	return func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `update `+c.Topic.Deliveries+` set tries = $3, last_error = $4, lease_until = null,
			not_before = now() + make_interval(secs => $5) where consumer = $1 and id = $2`,
			c.Name, id, tries, clip(cause), wait.Seconds()); err != nil {
			return err
		}
		return c.failing(ctx, tx, cause)
	}
}

// hold sets it aside: the stream goes on, and the failing was this message's
// and not the consumer's.
func (c *Consumer) hold(id int64, tries int, cause error) func(context.Context, pgx.Tx) error {
	return func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `update `+c.Topic.Deliveries+` set tries = $3, last_error = $4, lease_until = null,
			not_before = null, held_at = now() where consumer = $1 and id = $2`, c.Name, id, tries, clip(cause)); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `update `+c.Topic.Consumers+` set failures = 0, last_error = '', failing_since = null
			where name = $1`, c.Name); err != nil {
			return err
		}
		return c.advance(ctx, tx, id)
	}
}

// heldFailed: its one more go did not work. It waits for the next occasion.
func (c *Consumer) heldFailed(id int64, tries int, cause error) func(context.Context, pgx.Tx) error {
	return func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `update `+c.Topic.Deliveries+` set tries = $3, last_error = $4, lease_until = null,
			not_before = null where consumer = $1 and id = $2`, c.Name, id, tries, clip(cause))
		return err
	}
}

// listen calls wake on every notification on channel, reconnecting for as
// long as ctx lasts. It only wakes: a missed notification costs a poll.
func listen(ctx context.Context, pool *pgxpool.Pool, channel string, wake func(), log *slog.Logger) {
	for wait := time.Second; ctx.Err() == nil; wait = min(wait*2, time.Minute) {
		conn, err := pgx.ConnectConfig(ctx, pool.Config().ConnConfig)
		if err == nil {
			_, err = conn.Exec(ctx, "listen "+pgx.Identifier{channel}.Sanitize())
			if err == nil {
				wait = time.Second
				wake()
				for err == nil {
					_, err = conn.WaitForNotification(ctx)
					if err == nil {
						wake()
					}
				}
			}
			closing, cancel := context.WithTimeout(context.Background(), time.Second)
			_ = conn.Close(closing)
			cancel()
		}
		if ctx.Err() != nil {
			return
		}
		log.Debug("알림 연결을 다시 맺습니다", "channel", channel, "err", err)
		if !sleep(ctx, wait) {
			return
		}
	}
}

// Listen wakes on channel; a spare for those who are not a Consumer (input-svc
// on the outbox).
func Listen(ctx context.Context, pool *pgxpool.Pool, channel string, wake func(), log *slog.Logger) {
	listen(ctx, pool, channel, wake, log)
}

func sleep(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}
