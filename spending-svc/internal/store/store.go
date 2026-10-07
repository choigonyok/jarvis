// Package store keeps charges, rules and budgets in Postgres.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/choigonyok/jarvis/spending-svc/internal/book"
	"github.com/choigonyok/jarvis/spending-svc/internal/category"
	"github.com/choigonyok/jarvis/spending-svc/internal/enrich"
	"github.com/choigonyok/jarvis/spending-svc/internal/parse"
)

var ErrNotFound = errors.New("그런 내역이 없습니다.")

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

// ── 수집 커서 ─────────────────────────────────────────────────────────────

const cursorScope, cursorKey = "spending", "kakao_cursor"

func (s *Store) Cursor(ctx context.Context) (int64, error) {
	var raw []byte
	err := s.pool.QueryRow(ctx, `select value from settings where scope=$1 and key=$2`,
		cursorScope, cursorKey).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var n int64
	err = json.Unmarshal(raw, &n)
	return n, err
}

func (s *Store) SetCursor(ctx context.Context, rowid int64) error {
	_, err := s.pool.Exec(ctx, `select set_setting($1, $2, $3::jsonb)`,
		cursorScope, cursorKey, strconv.FormatInt(rowid, 10))
	return err
}

// ── 쓰기: 알림 ────────────────────────────────────────────────────────────

// RawEvent records the alert exactly as received, before any reading of it.
// The same message twice is one row (source, ext_id is unique).
func (s *Store) RawEvent(ctx context.Context, messageID, chat, text string, sentAt time.Time) (int64, error) {
	data, _ := json.Marshal(map[string]string{"chat_name": chat, "text": text})
	var id int64
	err := s.pool.QueryRow(ctx, `
		insert into raw_events (source, type, subject, ext_id, occurred_at, data)
		values ('kakaotalk', 'card.alert', $1, $2, $3, $4)
		on conflict (source, ext_id) where ext_id is not null do nothing
		returning id`, chat, messageID, sentAt, data).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		err = s.pool.QueryRow(ctx,
			`select id from raw_events where source='kakaotalk' and ext_id=$1`, messageID).Scan(&id)
	}
	return id, err
}

// AddAlert stores a parsed alert. It returns false when the message was
// already recorded - collection resumes from a cursor, and a replay must not
// count a charge twice.
func (s *Store) AddAlert(ctx context.Context, messageID string, eventID int64, a parse.Alert, amountKrw int64, estimated bool) (bool, error) {
	cat, src, err := s.Classify(ctx, a.Merchant)
	if err != nil {
		return false, err
	}
	var foreign *float64
	if a.Currency != "KRW" {
		foreign = &a.ForeignAmount
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)

	// A marketplace charge is queued for the background lookup of what it
	// bought. Cancellations are not: there is nothing to open up.
	var enrichSource, enrichStatus *string
	if a.Kind == parse.KindApproval {
		if src := enrich.SourceOf(a.Merchant); src != "" {
			pending := "pending"
			enrichSource, enrichStatus = &src, &pending
		}
	}

	var id int64
	err = tx.QueryRow(ctx, `
		insert into card_transactions
		  (source, source_ref, source_event_id, issuer, card_tail, kind, amount_krw, currency,
		   foreign_amount, estimated, installment, merchant, category, category_source, approved_at,
		   enrich_source, enrich_status, enrich_next_at)
		values ('kakao', $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16,
		        case when $16::text is null then null else now() end)
		on conflict (source_ref) do nothing
		returning id`,
		messageID, eventID, a.Issuer, a.CardTail, a.Kind, amountKrw, a.Currency,
		foreign, estimated, a.Installment, a.Merchant, cat, src, a.ApprovedAt,
		enrichSource, enrichStatus).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if a.Kind == parse.KindCancel {
		if err := matchCancel(ctx, tx, id, a, amountKrw); err != nil {
			return false, err
		}
	}
	return true, tx.Commit(ctx)
}

// matchCancel pairs a full cancellation with the charge it undoes: same
// amount, the newest uncancelled one in the 90 days before, and either the
// same merchant or the same card. A partial cancellation has a different
// amount, finds nothing, and stays as money back.
func matchCancel(ctx context.Context, tx pgx.Tx, cancelID int64, a parse.Alert, amountKrw int64) error {
	rows, err := tx.Query(ctx, `
		select id, merchant, card_tail from card_transactions
		 where kind='approval' and cancelled_by is null and amount_krw=$1
		   and approved_at <= $2 and approved_at > $2 - interval '90 days'
		 order by approved_at desc`, amountKrw, a.ApprovedAt.Add(time.Minute))
	if err != nil {
		return err
	}
	var match int64
	key := category.Key(a.Merchant)
	for rows.Next() {
		var id int64
		var merchant, tail string
		if err := rows.Scan(&id, &merchant, &tail); err != nil {
			rows.Close()
			return err
		}
		if category.Key(merchant) == key || (a.CardTail != "" && tail == a.CardTail) {
			match = id
			break
		}
	}
	rows.Close()
	if match == 0 {
		return nil
	}
	_, err = tx.Exec(ctx, `update card_transactions set cancelled_by=$1, updated_at=now() where id=$2`, cancelID, match)
	return err
}

func (s *Store) AddUnparsed(ctx context.Context, messageID, chat, text string, sentAt time.Time) error {
	_, err := s.pool.Exec(ctx, `
		insert into spending_unparsed (message_id, chat_name, body, sent_at)
		values ($1, $2, $3, $4) on conflict do nothing`, messageID, chat, text, sentAt)
	return err
}

// ── 분류 ──────────────────────────────────────────────────────────────────

// Classify prefers what the person taught over the keyword guess. A rule for
// "스타벅스" also covers "스타벅스강남역점": the longest taught prefix wins.
func (s *Store) Classify(ctx context.Context, merchant string) (string, string, error) {
	var cat string
	err := s.pool.QueryRow(ctx, `
		select category from merchant_rules
		 where $1 like merchant_key || '%' and length(merchant_key) >= 2
		 order by length(merchant_key) desc limit 1`, category.Key(merchant)).Scan(&cat)
	if err == nil {
		return cat, "learned", nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", "", err
	}
	return category.Classify(merchant), "rule", nil
}

// ReclassifyRules re-runs classification on every charge whose category was
// a guess, so a better keyword list fixes the past too. Ones a person set -
// by hand or by teaching a merchant rule - are left as they are.
func (s *Store) ReclassifyRules(ctx context.Context) (int, error) {
	rows, err := s.pool.Query(ctx, `select id, merchant, category from card_transactions where category_source='rule'`)
	if err != nil {
		return 0, err
	}
	type row struct {
		id                int64
		merchant, current string
	}
	var guessed []row
	for rows.Next() {
		var id int64
		var merchant, current string
		if err := rows.Scan(&id, &merchant, &current); err != nil {
			rows.Close()
			return 0, err
		}
		guessed = append(guessed, row{id: id, merchant: merchant, current: current})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	n := 0
	for _, c := range guessed {
		cat, src, err := s.Classify(ctx, c.merchant)
		if err != nil {
			return n, err
		}
		if cat == c.current && src == "rule" {
			continue
		}
		if _, err := s.pool.Exec(ctx, `
			update card_transactions set category=$2, category_source=$3, updated_at=now()
			 where id=$1 and category_source='rule'`, c.id, cat, src); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// ── 읽기 ──────────────────────────────────────────────────────────────────

const txColumns = `
	t.id, t.source, t.issuer, t.card_tail, t.kind, t.amount_krw, t.currency,
	t.foreign_amount::float8, t.estimated, t.installment, t.merchant, t.category, t.category_source,
	t.approved_at, t.memo, t.excluded, t.cancelled_by,
	exists (select 1 from card_transactions o where o.cancelled_by = t.id),
	t.enrich_source, t.enrich_status, t.enrich_note`

func scanTxs(rows pgx.Rows) ([]book.Tx, error) {
	defer rows.Close()
	out := []book.Tx{}
	for rows.Next() {
		var t book.Tx
		if err := rows.Scan(&t.ID, &t.Source, &t.Issuer, &t.CardTail, &t.Kind, &t.AmountKrw, &t.Currency,
			&t.ForeignAmount, &t.Estimated, &t.Installment, &t.Merchant, &t.Category, &t.CategorySource,
			&t.ApprovedAt, &t.Memo, &t.Excluded, &t.CancelledBy, &t.Matched,
			&t.EnrichSource, &t.EnrichStatus, &t.EnrichNote); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Range returns charges approved in [from, to).
func (s *Store) Range(ctx context.Context, from, to time.Time) ([]book.Tx, error) {
	rows, err := s.pool.Query(ctx, `select `+txColumns+` from card_transactions t
		 where t.approved_at >= $1 and t.approved_at < $2 order by t.approved_at desc`, from, to)
	if err != nil {
		return nil, err
	}
	txs, err := scanTxs(rows)
	if err != nil {
		return nil, err
	}
	for i := range txs {
		txs[i].ApprovedAt = txs[i].ApprovedAt.In(from.Location())
	}
	return txs, s.attachItems(ctx, txs)
}

// attachItems loads the items of whichever of these charges have them, in
// one query rather than one per row.
func (s *Store) attachItems(ctx context.Context, txs []book.Tx) error {
	var ids []int64
	index := map[int64]int{}
	for i, t := range txs {
		if t.EnrichStatus != nil && *t.EnrichStatus == "done" {
			ids = append(ids, t.ID)
			index[t.ID] = i
		}
	}
	if len(ids) == 0 {
		return nil
	}
	rows, err := s.pool.Query(ctx, `
		select id, tx_id, name, quantity, listed_krw, amount_krw, category, category_source
		  from card_transaction_items where tx_id = any($1) order by tx_id, amount_krw desc, id`, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var it book.Item
		var txID int64
		if err := rows.Scan(&it.ID, &txID, &it.Name, &it.Quantity, &it.ListedKrw, &it.AmountKrw,
			&it.Category, &it.CategorySource); err != nil {
			return err
		}
		i := index[txID]
		txs[i].Items = append(txs[i].Items, it)
	}
	return rows.Err()
}

func (s *Store) Get(ctx context.Context, id int64) (book.Tx, error) {
	rows, err := s.pool.Query(ctx, `select `+txColumns+` from card_transactions t where t.id=$1`, id)
	if err != nil {
		return book.Tx{}, err
	}
	txs, err := scanTxs(rows)
	if err != nil {
		return book.Tx{}, err
	}
	if len(txs) == 0 {
		return book.Tx{}, ErrNotFound
	}
	return txs[0], s.attachItems(ctx, txs)
}

func (s *Store) Budgets(ctx context.Context) (map[string]int64, error) {
	rows, err := s.pool.Query(ctx, `select category, amount_krw from spending_budgets`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var c string
		var n int64
		if err := rows.Scan(&c, &n); err != nil {
			return nil, err
		}
		out[c] = n
	}
	return out, rows.Err()
}

type Unparsed struct {
	MessageID string    `json:"messageId"`
	ChatName  string    `json:"chatName"`
	Body      string    `json:"body"`
	SentAt    time.Time `json:"sentAt"`
}

func (s *Store) Unparsed(ctx context.Context) ([]Unparsed, error) {
	rows, err := s.pool.Query(ctx, `
		select message_id, chat_name, body, sent_at from spending_unparsed
		 where not resolved order by sent_at desc limit 50`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Unparsed{}
	for rows.Next() {
		var u Unparsed
		if err := rows.Scan(&u.MessageID, &u.ChatName, &u.Body, &u.SentAt); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// LastAlertAt is when the newest card alert arrived, whether or not it was
// readable - the question is whether alerts are reaching us at all.
func (s *Store) LastAlertAt(ctx context.Context) (*time.Time, error) {
	var t *time.Time
	err := s.pool.QueryRow(ctx, `
		select max(occurred_at) from raw_events where source='kakaotalk' and type='card.alert'`).Scan(&t)
	return t, err
}

// ── 쓰기: 사람 ────────────────────────────────────────────────────────────

type Manual struct {
	ApprovedAt time.Time
	Merchant   string
	AmountKrw  int64
	Category   string
	Memo       string
	// SourceRef ties an entry written from an unreadable alert back to it.
	SourceRef string
}

func (s *Store) AddManual(ctx context.Context, m Manual) (int64, error) {
	cat, src := m.Category, "manual"
	if cat == "" {
		var err error
		if cat, src, err = s.Classify(ctx, m.Merchant); err != nil {
			return 0, err
		}
	}
	var ref *string
	if m.SourceRef != "" {
		ref = &m.SourceRef
	}
	var id int64
	err := s.pool.QueryRow(ctx, `
		insert into card_transactions
		  (source, source_ref, amount_krw, merchant, category, category_source, approved_at, memo)
		values ('manual', $1, $2, $3, $4, $5, $6, $7) returning id`,
		ref, m.AmountKrw, m.Merchant, cat, src, m.ApprovedAt, m.Memo).Scan(&id)
	return id, err
}

type Patch struct {
	Category  *string
	Remember  bool
	Memo      *string
	Excluded  *bool
	Merchant  *string
	AmountKrw *int64
}

// Update applies a correction. With Remember, the category becomes a rule
// for the merchant and is applied to its other charges too - except the ones
// that were set by hand one at a time, which stay as decided.
func (s *Store) Update(ctx context.Context, id int64, p Patch) (int, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	var merchant string
	if err := tx.QueryRow(ctx, `select merchant from card_transactions where id=$1 for update`, id).Scan(&merchant); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, ErrNotFound
		}
		return 0, err
	}
	if p.Merchant != nil {
		merchant = *p.Merchant
	}

	touched := 1
	if p.Category != nil {
		src := "manual"
		if p.Remember {
			src = "learned"
			key := category.Key(merchant)
			if _, err := tx.Exec(ctx, `
				insert into merchant_rules (merchant_key, category) values ($1, $2)
				on conflict (merchant_key) do update set category=excluded.category, updated_at=now()`,
				key, *p.Category); err != nil {
				return 0, err
			}
			tag, err := tx.Exec(ctx, `
				update card_transactions set category=$1, category_source='learned', updated_at=now()
				 where id<>$2 and category_source<>'manual'
				   and upper(regexp_replace(merchant, '[\s().\-_*]', '', 'g')) like $3 || '%'`,
				*p.Category, id, key)
			if err != nil {
				return 0, err
			}
			touched += int(tag.RowsAffected())
		}
		if _, err := tx.Exec(ctx, `update card_transactions set category=$1, category_source=$2 where id=$3`,
			*p.Category, src, id); err != nil {
			return 0, err
		}
	}
	if _, err := tx.Exec(ctx, `
		update card_transactions set
		  memo       = coalesce($2, memo),
		  excluded   = coalesce($3, excluded),
		  merchant   = coalesce($4, merchant),
		  amount_krw = coalesce($5, amount_krw),
		  estimated  = case when $5::bigint is null then estimated else false end,
		  updated_at = now()
		 where id=$1`, id, p.Memo, p.Excluded, p.Merchant, p.AmountKrw); err != nil {
		return 0, err
	}
	return touched, tx.Commit(ctx)
}

// Delete removes a hand-written entry. Ones read from an alert cannot be
// deleted - the alert would still be there, and the next replay would put it
// back. Excluding is how those leave the total.
func (s *Store) Delete(ctx context.Context, id int64) error {
	var source string
	if err := s.pool.QueryRow(ctx, `select source from card_transactions where id=$1`, id).Scan(&source); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if source != "manual" {
		return errors.New("카드 알림에서 온 내역은 지울 수 없어요. 합계에서 빼기를 쓰세요.")
	}
	_, err := s.pool.Exec(ctx, `delete from card_transactions where id=$1`, id)
	return err
}

func (s *Store) SetBudget(ctx context.Context, cat string, amount int64) error {
	if amount <= 0 {
		_, err := s.pool.Exec(ctx, `delete from spending_budgets where category=$1`, cat)
		return err
	}
	_, err := s.pool.Exec(ctx, `
		insert into spending_budgets (category, amount_krw) values ($1, $2)
		on conflict (category) do update set amount_krw=excluded.amount_krw, updated_at=now()`, cat, amount)
	return err
}

func (s *Store) ResolveUnparsed(ctx context.Context, messageID string) error {
	tag, err := s.pool.Exec(ctx, `update spending_unparsed set resolved=true where message_id=$1`, messageID)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// ── 묶음 가맹점 조회 ──────────────────────────────────────────────────────

// Pending is what the background lookup should open next: charges whose turn
// has come, from sites that are not waiting on a fresh login. Blocked names
// the sites that are.
func (s *Store) Pending(ctx context.Context, limit int) ([]book.Tx, []string, error) {
	var blocked []string
	rows, err := s.pool.Query(ctx, `
		select distinct enrich_source from card_transactions
		 where enrich_status = 'login_required' and enrich_source is not null`)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var src string
		if err := rows.Scan(&src); err != nil {
			rows.Close()
			return nil, nil, err
		}
		blocked = append(blocked, src)
	}
	rows.Close()
	if blocked == nil {
		blocked = []string{}
	}

	rows, err = s.pool.Query(ctx, `select `+txColumns+` from card_transactions t
		 where t.enrich_status = 'pending' and t.enrich_next_at <= now()
		   and not (t.enrich_source = any($1))
		 order by t.approved_at limit $2`, blocked, limit)
	if err != nil {
		return nil, nil, err
	}
	txs, err := scanTxs(rows)
	return txs, blocked, err
}

// SaveItems records what an order contained and closes the lookup.
func (s *Store) SaveItems(ctx context.Context, id int64, items []enrich.Item, note string) error {
	t, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if t.EnrichSource == nil {
		return errors.New("주문을 조회하는 결제가 아닙니다.")
	}
	allocated, err := enrich.Allocate(t.AmountKrw, items, t.Category)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `delete from card_transaction_items where tx_id=$1`, id); err != nil {
		return err
	}
	for _, it := range allocated {
		if _, err := tx.Exec(ctx, `
			insert into card_transaction_items (tx_id, name, quantity, listed_krw, amount_krw, category)
			values ($1, $2, $3, $4, $5, $6)`,
			id, it.Name, max(1, it.Quantity), it.ListedKrw, it.AmountKrw, it.Category); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `
		update card_transactions set enrich_status='done', enrich_note=$2, enrich_next_at=null, updated_at=now()
		 where id=$1`, id, note); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Report records a lookup that did not end in items. Not finding the order
// is retried twice more, later - an order page can lag the card alert. A
// login wall stops every pending lookup on that site until someone signs in
// again, rather than knocking on the same wall for each charge.
func (s *Store) Report(ctx context.Context, id int64, outcome, note string) error {
	switch outcome {
	case "login_required":
		tag, err := s.pool.Exec(ctx, `
			update card_transactions set enrich_status='login_required', enrich_note=$2, updated_at=now()
			 where enrich_status='pending'
			   and enrich_source = (select enrich_source from card_transactions where id=$1)`, id, note)
		if err != nil || tag.RowsAffected() > 0 {
			return err
		}
		// Reporting the same wall twice is not an error: the first report
		// already stopped every charge on that site, this one included.
		var status *string
		if err := s.pool.QueryRow(ctx, `select enrich_status from card_transactions where id=$1`, id).Scan(&status); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		if status != nil && *status == "login_required" {
			return nil
		}
		return ErrNotFound
	case "not_found", "failed":
		tag, err := s.pool.Exec(ctx, `
			update card_transactions set
			  enrich_attempts = enrich_attempts + 1,
			  enrich_note = $2,
			  enrich_status = case when enrich_attempts + 1 >= 3 then 'not_found' else 'pending' end,
			  enrich_next_at = now() + case when enrich_attempts = 0 then interval '30 minutes' else interval '3 hours' end,
			  updated_at = now()
			 where id=$1 and enrich_status='pending'`, id, note)
		if err == nil && tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return err
	}
	return errors.New("outcome 은 not_found, login_required, failed 중 하나입니다.")
}

// Retry puts lookups back in the queue: every stopped one on a site after a
// fresh login, or one charge when asked from its sheet.
func (s *Store) Retry(ctx context.Context, source string, id int64) (int, error) {
	var tag pgconn.CommandTag
	var err error
	if id > 0 {
		tag, err = s.pool.Exec(ctx, `
			update card_transactions set enrich_status='pending', enrich_attempts=0, enrich_next_at=now(),
			       enrich_note='', updated_at=now()
			 where id=$1 and enrich_source is not null`, id)
	} else {
		tag, err = s.pool.Exec(ctx, `
			update card_transactions set enrich_status='pending', enrich_attempts=0, enrich_next_at=now(),
			       enrich_note='', updated_at=now()
			 where enrich_source=$1 and enrich_status in ('login_required', 'not_found')
			   and approved_at > now() - interval '30 days'`, source)
	}
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

func (s *Store) SetItemCategory(ctx context.Context, itemID int64, cat string) error {
	tag, err := s.pool.Exec(ctx, `
		update card_transaction_items set category=$2, category_source='manual' where id=$1`, itemID, cat)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}
