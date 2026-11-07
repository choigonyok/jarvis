// Command kakaotalk-client is a microservice that polls the local KakaoTalk
// SQLCipher database on a short interval, stores new messages incrementally in a
// plain SQLite file, and serves them over a small read-only HTTP API.
//
// It must run on the macOS host (or a container with the KakaoTalk container
// directory bind-mounted and KAKAO_UUID / KAKAO_USER_ID supplied), because the
// encryption key is derived from the machine's IOPlatformUUID and the account
// id, and only the host can read the live -wal file as messages arrive.
package main

import (
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/choigonyok/jarvis/gocommon/collect"
	"github.com/choigonyok/jarvis/gocommon/health"
	"github.com/choigonyok/jarvis/gocommon/record"
	"github.com/choigonyok/jarvis/gocommon/seal"
	"github.com/choigonyok/jarvis/gocommon/svc"
	"github.com/choigonyok/jarvis/kakaotalk-client/internal/api"
	"github.com/choigonyok/jarvis/kakaotalk-client/internal/kakao"
	"github.com/choigonyok/jarvis/kakaotalk-client/internal/poller"
	"github.com/choigonyok/jarvis/kakaotalk-client/internal/store"
)

func main() {
	log := svc.Logger()
	cfg := kakao.Config{
		ContainerDir: os.Getenv("KAKAO_CONTAINER_DIR"),
		HomeDir:      os.Getenv("KAKAO_HOME"),
		UUID:         os.Getenv("KAKAO_UUID"),
		UserID:       int64(svc.Int("KAKAO_USER_ID", 0)),
		Key:          os.Getenv("KAKAO_SQLCIPHER_KEY"),
		PlistDir:     os.Getenv("KAKAO_PLIST_DIR"),
		ScratchDir:   os.Getenv("KAKAO_SCRATCH_DIR"),
	}

	storePath := svc.Env("STORE_PATH", "./data/kakaotalk.db")
	if err := os.MkdirAll(filepath.Dir(storePath), 0o700); err != nil {
		log.Error("저장소 폴더를 만들지 못했습니다", "err", err)
		os.Exit(1)
	}
	interval := svc.Duration("POLL_INTERVAL", time.Second)
	batch := svc.Int("POLL_BATCH", 500)
	token := svc.Token(log)

	st, err := store.Open(storePath)
	if err != nil {
		log.Error("저장소를 열지 못했습니다", "err", err)
		os.Exit(1)
	}
	defer st.Close()

	reader, err := kakao.Open(cfg)
	if err != nil {
		log.Error("카카오톡 DB 를 열지 못했습니다", "err", err)
		os.Exit(1)
	}
	log.Info("카카오톡 DB 를 열었습니다", "user_id", reader.UserID(), "interval", interval)

	p := poller.New(reader, st, interval, batch)
	p.ImportHistory = os.Getenv("KAKAO_IMPORT_HISTORY") != "false"

	ctx, stop := svc.Signals()
	defer stop()

	loops := health.NewLoops()
	loops.Add(poller.LoopRead, "카카오톡 DB 읽기", time.Minute)
	p.Loops = loops
	go p.Run(ctx)
	// 들어온 메시지는 봉인해서 입구로 넘긴다. mind 는 이제 수집기를 읽으러 오지 않는다.
	if url, key := os.Getenv("INPUT_URL"), os.Getenv("MESSAGE_KEY"); url != "" && key != "" {
		sealer, err := seal.FromBase64(key)
		if err != nil {
			log.Error("MESSAGE_KEY 를 읽지 못했습니다", "err", err)
			os.Exit(1)
		}
		pub := &collect.Publisher{
			Source: "kakao", CursorPath: storePath + ".published", Seal: sealer, Every: interval, Loops: loops, Log: log,
			Send:   record.Post(url, token, log),
			Latest: func() (int64, error) { _, top, err := st.RowIDRange(); return top, err },
			After: func(rowid int64, limit int) ([]collect.Item, error) {
				msgs, err := st.MessagesAfter(rowid, limit)
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
	// 평문 사본의 보존 기한(일). 0 이하면 지우지 않는다.
	go collect.Retain(ctx, retainDays(), st.Prune, func(n int64, err error) {
		log.Info("보존 기한이 지난 메시지를 지웠습니다", "count", n, "err", err)
	})

	if svc.Serve(ctx, svc.Server{Name: "kakaotalk-client", Addr: svc.Env("LISTEN_ADDR", ":8090"), Handler: api.New(st, p, token, loops).Handler(),
		Read: 10 * time.Second, Write: 30 * time.Second, Log: log}) != nil {
		os.Exit(1)
	}
}

// retainDays is RETAIN_DAYS, how many days the plain copy keeps a message:
// any whole number as written - zero or below keeps everything
// (collect.Retain) - and 730 when it is unset or not a whole number.
func retainDays() int {
	if n, err := strconv.ParseInt(os.Getenv("RETAIN_DAYS"), 10, 64); err == nil {
		return int(n)
	}
	return 730
}
