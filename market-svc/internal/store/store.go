// Package store keeps Joongna listings, the changes waiting to reach Joongna,
// and the one row of sync state, in Postgres.
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/choigonyok/jarvis/market-svc/internal/listing"
)

var ErrNotFound = errors.New("그런 글이 없습니다.")

type Store struct {
	pool *pgxpool.Pool
}

func Open(ctx context.Context, dsn string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("DSN 해석: %w", err)
	}
	cfg.MaxConns = 4
	cfg.MaxConnIdleTime = 5 * time.Minute
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("풀 생성: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("연결 확인: %w", err)
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Close() { s.pool.Close() }

func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

type Listing struct {
	ID          int64      `json:"id"`
	Status      string     `json:"status"`
	Title       string     `json:"title"`
	PriceKrw    int64      `json:"priceKrw"`
	Description string     `json:"description"`
	Category    string     `json:"category"`
	Condition   string     `json:"condition"`
	Shipping    string     `json:"shipping"`
	Photos      []string   `json:"photos"`
	JoongnaID   string     `json:"joongnaId,omitempty"`
	URL         string     `json:"url,omitempty"`
	Views       int        `json:"views"`
	Likes       int        `json:"likes"`
	Chats       int        `json:"chats"`
	Note        string     `json:"note,omitempty"`
	PostedAt    *time.Time `json:"postedAt,omitempty"`
	SoldAt      *time.Time `json:"soldAt,omitempty"`
	SyncedAt    *time.Time `json:"syncedAt,omitempty"`
	PhotosGone  bool       `json:"photosGone,omitempty"`
	CreatedAt   time.Time  `json:"createdAt"`
	// Tasks are the changes still on their way to Joongna, oldest first.
	Tasks []Task `json:"tasks"`
}

type Task struct {
	ID        int64     `json:"id"`
	ListingID int64     `json:"listingId"`
	Kind      string    `json:"kind"`
	PriceKrw  *int64    `json:"priceKrw,omitempty"`
	ToStatus  string    `json:"toStatus,omitempty"`
	State     string    `json:"state"`
	Attempts  int       `json:"attempts"`
	Note      string    `json:"note,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}

type State struct {
	LoginRequired bool       `json:"loginRequired"`
	LoginNote     string     `json:"loginNote,omitempty"`
	SyncRequested bool       `json:"syncRequested"`
	LastSyncAt    *time.Time `json:"lastSyncAt,omitempty"`
	LastSyncNote  string     `json:"lastSyncNote,omitempty"`
}

const listingCols = `id, status, title, price_krw, description, category, condition, shipping,
	photos, coalesce(joongna_id, ''), url, views, likes, chats, note,
	posted_at, sold_at, synced_at, photos_purged_at is not null, created_at`

func scanListing(row pgx.Row) (Listing, error) {
	var l Listing
	err := row.Scan(&l.ID, &l.Status, &l.Title, &l.PriceKrw, &l.Description, &l.Category,
		&l.Condition, &l.Shipping, &l.Photos, &l.JoongnaID, &l.URL, &l.Views, &l.Likes,
		&l.Chats, &l.Note, &l.PostedAt, &l.SoldAt, &l.SyncedAt, &l.PhotosGone, &l.CreatedAt)
	if l.Photos == nil {
		l.Photos = []string{}
	}
	l.Tasks = []Task{}
	return l, err
}

const taskCols = `id, listing_id, kind, price_krw, coalesce(to_status, ''), state, attempts, note, created_at`

func scanTask(row pgx.Row) (Task, error) {
	var t Task
	err := row.Scan(&t.ID, &t.ListingID, &t.Kind, &t.PriceKrw, &t.ToStatus, &t.State, &t.Attempts, &t.Note, &t.CreatedAt)
	return t, err
}

// Create stores an approved draft and queues its posting, in one transaction:
// a listing with no task would sit at "등록 대기" forever.
func (s *Store) Create(ctx context.Context, n listing.New) (Listing, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Listing{}, err
	}
	defer tx.Rollback(ctx)
	l, err := scanListing(tx.QueryRow(ctx, `
		insert into market_listings (title, price_krw, description, category, condition, shipping, photos)
		values ($1, $2, $3, $4, $5, $6, $7)
		returning `+listingCols,
		n.Title, n.PriceKrw, n.Description, n.Category, n.Condition, n.Shipping, n.Photos))
	if err != nil {
		return Listing{}, err
	}
	t, err := scanTask(tx.QueryRow(ctx, `
		insert into market_tasks (listing_id, kind) values ($1, 'post') returning `+taskCols, l.ID))
	if err != nil {
		return Listing{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Listing{}, err
	}
	l.Tasks = []Task{t}
	return l, nil
}

// List returns every listing, newest first, each with its open tasks.
func (s *Store) List(ctx context.Context) ([]Listing, error) {
	rows, err := s.pool.Query(ctx, `select `+listingCols+` from market_listings order by created_at desc`)
	if err != nil {
		return nil, err
	}
	out := []Listing{}
	index := map[int64]int{}
	for rows.Next() {
		l, err := scanListing(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		index[l.ID] = len(out)
		out = append(out, l)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Pending, plus failed ones that gave up: the tab shows why a change did
	// not happen until a newer change of the same kind replaces it.
	trows, err := s.pool.Query(ctx, `
		select `+taskCols+` from market_tasks t
		 where state = 'pending'
		    or (state = 'failed' and not exists (
		          select 1 from market_tasks n
		           where n.listing_id = t.listing_id and n.kind = t.kind and n.id > t.id))
		 order by id`)
	if err != nil {
		return nil, err
	}
	defer trows.Close()
	for trows.Next() {
		t, err := scanTask(trows)
		if err != nil {
			return nil, err
		}
		if i, ok := index[t.ListingID]; ok {
			out[i].Tasks = append(out[i].Tasks, t)
		}
	}
	return out, trows.Err()
}

func (s *Store) Get(ctx context.Context, id int64) (Listing, error) {
	l, err := scanListing(s.pool.QueryRow(ctx, `select `+listingCols+` from market_listings where id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Listing{}, ErrNotFound
	}
	return l, err
}

// AddTask queues a change from the tab. A second change of the same kind
// replaces one still waiting - the newer price is the one that was meant.
func (s *Store) AddTask(ctx context.Context, listingID int64, in listing.TaskInput) (Task, error) {
	l, err := s.Get(ctx, listingID)
	if err != nil {
		return Task{}, err
	}
	if err := listing.CheckTask(l.Status, l.JoongnaID != "", in); err != nil {
		return Task{}, err
	}
	var price *int64
	if in.Kind == listing.KindPrice {
		price = &in.PriceKrw
	}
	var to any
	if in.Kind == listing.KindStatus {
		to = in.ToStatus
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Task{}, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `
		update market_tasks set state = 'cancelled', done_at = now(), note = '새 요청으로 바뀜'
		 where listing_id = $1 and kind = $2 and state = 'pending'`, listingID, in.Kind); err != nil {
		return Task{}, err
	}
	// Deleting supersedes everything else still waiting on this listing.
	if in.Kind == listing.KindDelete {
		if _, err := tx.Exec(ctx, `
			update market_tasks set state = 'cancelled', done_at = now(), note = '삭제 요청으로 취소'
			 where listing_id = $1 and state = 'pending'`, listingID); err != nil {
			return Task{}, err
		}
	}
	if in.Kind == listing.KindPost {
		if _, err := tx.Exec(ctx, `update market_listings set status = 'queued', note = '', updated_at = now() where id = $1`, listingID); err != nil {
			return Task{}, err
		}
	}
	t, err := scanTask(tx.QueryRow(ctx, `
		insert into market_tasks (listing_id, kind, price_krw, to_status) values ($1, $2, $3, $4)
		returning `+taskCols, listingID, in.Kind, price, to))
	if err != nil {
		return Task{}, err
	}
	return t, tx.Commit(ctx)
}

// CancelTask withdraws a change that has not been tried yet. A post cannot be
// withdrawn this way once the listing exists only as a draft - deleting the
// draft is the same act, so it is allowed and removes the listing too.
func (s *Store) CancelTask(ctx context.Context, taskID int64) error {
	var listingID int64
	var kind string
	err := s.pool.QueryRow(ctx, `
		update market_tasks set state = 'cancelled', done_at = now(), note = '취소함'
		 where id = $1 and state = 'pending' and attempts = 0
		returning listing_id, kind`, taskID).Scan(&listingID, &kind)
	if errors.Is(err, pgx.ErrNoRows) {
		return errors.New("이미 처리 중이거나 끝난 작업입니다.")
	}
	if err != nil {
		return err
	}
	if kind == listing.KindPost {
		_, err = s.pool.Exec(ctx, `delete from market_listings where id = $1 and joongna_id is null`, listingID)
	}
	return err
}

// Work is one task with the listing it is about.
type Work struct {
	Task    Task    `json:"task"`
	Listing Listing `json:"listing"`
}

// NextTask is the oldest change due, or nil. Nothing is due while Joongna
// wants a fresh login: every task would hit the same login page.
func (s *Store) NextTask(ctx context.Context) (*Work, error) {
	st, err := s.State(ctx)
	if err != nil || st.LoginRequired {
		return nil, err
	}
	t, err := scanTask(s.pool.QueryRow(ctx, `
		select `+taskCols+` from market_tasks
		 where state = 'pending' and next_at <= now()
		 order by id limit 1`))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	l, err := s.Get(ctx, t.ListingID)
	if err != nil {
		return nil, err
	}
	return &Work{Task: t, Listing: l}, nil
}

type Report struct {
	Outcome   string `json:"outcome"`
	JoongnaID string `json:"joongnaId,omitempty"`
	URL       string `json:"url,omitempty"`
	Note      string `json:"note,omitempty"`
}

// ReportTask records how a try went and moves the listing with it.
func (s *Store) ReportTask(ctx context.Context, taskID int64, r Report) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	t, err := scanTask(tx.QueryRow(ctx, `select `+taskCols+` from market_tasks where id = $1 for update`, taskID))
	if errors.Is(err, pgx.ErrNoRows) {
		return errors.New("그런 작업이 없습니다.")
	}
	if err != nil {
		return err
	}
	if t.State != "pending" {
		return errors.New("이미 끝난 작업입니다.")
	}

	switch r.Outcome {
	case listing.OutcomeLoginRequired:
		// Not the task's fault, so not an attempt. Everything waits on a person.
		if _, err := tx.Exec(ctx, `update market_state set login_required = true, login_note = $1`, r.Note); err != nil {
			return err
		}

	case listing.OutcomeFailed:
		attempts := t.Attempts + 1
		if attempts >= listing.MaxAttempts {
			if _, err := tx.Exec(ctx, `update market_tasks set state = 'failed', attempts = $2, note = $3, done_at = now() where id = $1`,
				taskID, attempts, r.Note); err != nil {
				return err
			}
			if t.Kind == listing.KindPost {
				if _, err := tx.Exec(ctx, `update market_listings set status = 'failed', note = $2, updated_at = now() where id = $1`,
					t.ListingID, r.Note); err != nil {
					return err
				}
			}
		} else if _, err := tx.Exec(ctx, `update market_tasks set attempts = $2, note = $3, next_at = $4 where id = $1`,
			taskID, attempts, r.Note, time.Now().Add(listing.RetryAfter)); err != nil {
			return err
		}

	case listing.OutcomeDone:
		if err := applyDone(ctx, tx, t, r); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `update market_tasks set state = 'done', note = $2, done_at = now() where id = $1`, taskID, r.Note); err != nil {
			return err
		}

	default:
		return fmt.Errorf("outcome 은 done, failed, login_required 중 하나입니다: %q", r.Outcome)
	}
	return tx.Commit(ctx)
}

func applyDone(ctx context.Context, tx pgx.Tx, t Task, r Report) error {
	var err error
	switch t.Kind {
	case listing.KindPost:
		if r.JoongnaID == "" {
			return errors.New("등록했다면 중고나라 글 번호(joongnaId)를 함께 보내야 합니다.")
		}
		_, err = tx.Exec(ctx, `
			update market_listings set status = 'active', joongna_id = $2, url = $3, note = '',
			       posted_at = now(), synced_at = now(), updated_at = now()
			 where id = $1`, t.ListingID, r.JoongnaID, r.URL)
	case listing.KindPrice:
		_, err = tx.Exec(ctx, `update market_listings set price_krw = $2, updated_at = now() where id = $1`, t.ListingID, *t.PriceKrw)
	case listing.KindStatus:
		_, err = tx.Exec(ctx, `
			update market_listings set status = $2,
			       sold_at = case when $2 = 'sold' then coalesce(sold_at, now()) else null end,
			       updated_at = now()
			 where id = $1`, t.ListingID, t.ToStatus)
	case listing.KindDelete:
		_, err = tx.Exec(ctx, `update market_listings set status = 'deleted', updated_at = now() where id = $1`, t.ListingID)
	case listing.KindBump:
		_, err = tx.Exec(ctx, `update market_listings set note = $2, updated_at = now() where id = $1`,
			t.ListingID, "끌어올림 "+time.Now().Format("1/2 15:04"))
	}
	return err
}

func (s *Store) State(ctx context.Context) (State, error) {
	var st State
	err := s.pool.QueryRow(ctx, `
		select login_required, login_note, sync_requested, last_sync_at, last_sync_note from market_state where id = 1`).
		Scan(&st.LoginRequired, &st.LoginNote, &st.SyncRequested, &st.LastSyncAt, &st.LastSyncNote)
	return st, err
}

// LoginResolved is the person saying they logged in again; work resumes.
func (s *Store) LoginResolved(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `update market_state set login_required = false, login_note = ''`)
	return err
}

func (s *Store) RequestSync(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `update market_state set sync_requested = true`)
	return err
}

// SyncDue returns the listings to look up on Joongna, or none when it is not
// time: nothing live to watch, a login is needed, or the last look was recent.
func (s *Store) SyncDue(ctx context.Context, every time.Duration) ([]Listing, error) {
	st, err := s.State(ctx)
	if err != nil || st.LoginRequired {
		return nil, err
	}
	if !st.SyncRequested && st.LastSyncAt != nil && time.Since(*st.LastSyncAt) < every {
		return nil, nil
	}
	rows, err := s.pool.Query(ctx, `
		select `+listingCols+` from market_listings
		 where joongna_id is not null and status in ('active', 'reserved')
		 order by id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Listing{}
	for rows.Next() {
		l, err := scanListing(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 && st.SyncRequested {
		_, err = s.pool.Exec(ctx, `update market_state set sync_requested = false`)
	}
	return out, err
}

type SyncItem struct {
	JoongnaID string `json:"joongnaId"`
	// Status is Joongna's own word (판매중, 예약중, 판매완료) or ours.
	Status   string `json:"status"`
	PriceKrw int64  `json:"priceKrw"`
	Views    int    `json:"views"`
	Likes    int    `json:"likes"`
	Chats    int    `json:"chats"`
	// Gone is true when the listing is no longer on Joongna at all.
	Gone bool `json:"gone"`
}

// ReportSync writes what Joongna showed. A listing the report does not
// mention keeps what it had: not finding a row on a page is not proof it is gone.
//
// loginRequired stops the queue the same way a task's report would; the
// numbers that could still be read are kept.
func (s *Store) ReportSync(ctx context.Context, items []SyncItem, note string, loginRequired bool) (int, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	n := 0
	for _, it := range items {
		status := listing.FromJoongna(it.Status)
		if it.Gone {
			status = listing.Deleted
		}
		tag, err := tx.Exec(ctx, `
			update market_listings set
			       status = coalesce(nullif($2, ''), status),
			       sold_at = case when coalesce(nullif($2, ''), status) = 'sold' then coalesce(sold_at, now())
			                      when nullif($2, '') is null then sold_at else null end,
			       price_krw = case when $3 > 0 then $3 else price_krw end,
			       views = $4, likes = $5, chats = $6, synced_at = now(), updated_at = now()
			 where joongna_id = $1 and status not in ('deleted')`,
			it.JoongnaID, status, it.PriceKrw, it.Views, it.Likes, it.Chats)
		if err != nil {
			return 0, err
		}
		n += int(tag.RowsAffected())
	}
	if _, err := tx.Exec(ctx, `update market_state set last_sync_at = now(), sync_requested = false, last_sync_note = $1`, note); err != nil {
		return 0, err
	}
	if loginRequired {
		if _, err := tx.Exec(ctx, `update market_state set login_required = true, login_note = $1`, note); err != nil {
			return 0, err
		}
	}
	return n, tx.Commit(ctx)
}

// Purge is a listing whose photos may now be deleted.
type Purge struct {
	ID     int64    `json:"id"`
	Photos []string `json:"photos"`
}

// PurgeDue lists listings that ended (sold, deleted, never posted) more than
// after ago and still have photos on disk.
func (s *Store) PurgeDue(ctx context.Context, after time.Duration) ([]Purge, error) {
	rows, err := s.pool.Query(ctx, `
		select id, photos from market_listings
		 where photos_purged_at is null
		   and status in ('sold', 'deleted', 'failed')
		   and coalesce(sold_at, updated_at) < $1`, time.Now().Add(-after))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Purge{}
	for rows.Next() {
		var p Purge
		if err := rows.Scan(&p.ID, &p.Photos); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) MarkPurged(ctx context.Context, id int64) error {
	_, err := s.pool.Exec(ctx, `update market_listings set photos_purged_at = now() where id = $1`, id)
	return err
}
