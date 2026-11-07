// Command imessage-client collects iMessage and SMS from the Mac's Messages
// database, the way kakaotalk-client collects KakaoTalk: it reads forward from
// the last row it stored, keeps its own copy, and serves it read-only in the
// same shape, so the console's 메시지 tab reads both alike.
//
// The Mac must be signed in to Messages (with Messages in iCloud on, to have
// the phone's history), and Docker needs Full Disk Access to read
// ~/Library/Messages.
package main

import (
	"cmp"
	"os"
	"path/filepath"
	"strconv"
	"time"

	_ "time/tzdata"

	"github.com/choigonyok/jarvis/gocommon/collect"
	"github.com/choigonyok/jarvis/gocommon/health"
	"github.com/choigonyok/jarvis/gocommon/record"
	"github.com/choigonyok/jarvis/gocommon/seal"
	"github.com/choigonyok/jarvis/gocommon/svc"
	"github.com/choigonyok/jarvis/imessage-client/internal/api"
	"github.com/choigonyok/jarvis/imessage-client/internal/imsg"
	"github.com/choigonyok/jarvis/imessage-client/internal/store"
)

func main() {
	log := svc.Logger()
	token := svc.Token(log)
	ctx, stop := svc.Signals()
	defer stop()

	storePath := svc.Env("STORE_PATH", "./data/imessage.db")
	if err := os.MkdirAll(filepath.Dir(storePath), 0o755); err != nil {
		log.Error("저장소 폴더를 만들지 못했습니다", "err", err)
		os.Exit(1)
	}
	st, err := store.Open(storePath)
	if err != nil {
		log.Error("저장소를 열지 못했습니다", "err", err)
		os.Exit(1)
	}
	defer st.Close()

	status := &api.Status{}
	// A ticker needs a period: zero is the default too.
	interval := cmp.Or(svc.Duration("POLL_INTERVAL", 2*time.Second), 2*time.Second)
	cfg := imsg.Config{
		ChatDB:     svc.Env("CHAT_DB", "/messages/chat.db"),
		ContactsDB: os.Getenv("CONTACTS_DB"),
		Scratch:    svc.Env("SCRATCH_DIR", "/tmp/imessage-scratch"),
	}

	loops := health.NewLoops()
	loops.Add("read", "메시지 DB 읽기", 5*time.Minute)
	read := func(err error) {
		if err != nil {
			loops.Fail("read", err)
			return
		}
		loops.OK("read")
	}

	// The reader is opened in the loop: until Full Disk Access is granted the
	// file is not there, and the service should serve /status saying so rather
	// than exit and restart forever.
	go func() {
		var reader *imsg.Reader
		tick := time.NewTicker(interval)
		defer tick.Stop()
		for {
			if reader == nil {
				r, err := imsg.Open(cfg)
				if err != nil {
					total, _ := st.Total()
					status.Set(0, total, err)
					read(err)
				} else {
					reader = r
					log.Info("메시지 데이터베이스를 읽기 시작합니다", "path", cfg.ChatDB)
				}
			}
			if reader != nil {
				cursor, _ := st.Cursor()
				msgs, err := reader.After(cursor, 1000)
				if err == nil && len(msgs) > 0 {
					err = st.Upsert(msgs)
					cursor = msgs[len(msgs)-1].RowID
				}
				total, _ := st.Total()
				status.Set(cursor, total, err)
				read(err)
				if err != nil {
					log.Warn("메시지를 읽지 못했습니다", "err", err)
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
		}
	}()

	// 들어온 메시지는 봉인해서 입구로 넘긴다. mind 는 이제 수집기를 읽으러 오지 않는다.
	if url, key := os.Getenv("INPUT_URL"), os.Getenv("MESSAGE_KEY"); url != "" && key != "" {
		sealer, err := seal.FromBase64(key)
		if err != nil {
			log.Error("MESSAGE_KEY 를 읽지 못했습니다", "err", err)
			os.Exit(1)
		}
		pub := &collect.Publisher{
			Source: "imessage", CursorPath: storePath + ".published", Seal: sealer, Every: interval, Loops: loops, Log: log,
			Send:   record.Post(url, token, log),
			Latest: st.Cursor,
			After: func(rowid int64, limit int) ([]collect.Item, error) {
				msgs, err := st.After(rowid, limit)
				if err != nil {
					return nil, err
				}
				out := make([]collect.Item, len(msgs))
				for i, m := range msgs {
					out[i] = collect.Item{RowID: m.RowID, ChatID: m.ChatID, SentAt: m.SentAt, Body: m}
				}
				return out, nil
			},
		}
		go pub.Run(ctx)
	}

	// 보존 기한이 지난 메시지는 하루에 한 번 지운다(0 이하 = 모두 둔다).
	days := retainDays()
	go collect.Retain(ctx, days, st.Prune, func(n int64, err error) {
		log.Info("보존 기한이 지난 메시지를 지웠습니다", "count", n, "days", days, "err", err)
	})

	// The same limits as the KakaoTalk collector, whose surface it shares.
	if svc.Serve(ctx, svc.Server{Name: "imessage-client", Addr: svc.Env("LISTEN_ADDR", ":8099"), Handler: api.New(st, status, token, loops).Handler(),
		Read: 10 * time.Second, Write: 30 * time.Second, Log: log}) != nil {
		os.Exit(1)
	}
}

// retainDays is RETAIN_DAYS, how many days the copy keeps a message: 730
// when it is unset, and anything else read as a whole number, with what is
// not one read as zero. Zero or below keeps everything (collect.Retain), so a
// value typed wrong deletes nothing.
func retainDays() int {
	days, _ := strconv.Atoi(svc.Env("RETAIN_DAYS", "730"))
	return days
}
