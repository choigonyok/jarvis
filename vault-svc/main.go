// Command vault-svc keeps the operator's logins, sealed, and hands one to the
// browser when it is on that login's own site.
//
// A login reaches it from the operator - a card in the conversation with a
// password field, whose value goes from the card straight here, never to the
// model and never into the transcript. It leaves only to the browser
// container, which types it into the page it is on; the model asks for "the
// login of this page" and is told it was filled, never what it was.
package main

import (
	"os"
	"time"

	"github.com/choigonyok/jarvis/gocommon/seal"
	"github.com/choigonyok/jarvis/gocommon/svc"
	"github.com/choigonyok/jarvis/gocommon/svc/pg"
	"github.com/choigonyok/jarvis/vault-svc/internal/api"
	"github.com/choigonyok/jarvis/vault-svc/internal/store"
)

func main() {
	log := svc.Logger()
	token, release := os.Getenv("API_TOKEN"), os.Getenv("VAULT_RELEASE_TOKEN")
	if token == "" || release == "" || token == release {
		log.Error("API_TOKEN 과 VAULT_RELEASE_TOKEN 이 둘 다 있어야 하고, 서로 달라야 합니다")
		os.Exit(1)
	}
	sealer, err := seal.FromBase64(os.Getenv("VAULT_KEY"))
	if err != nil {
		log.Error("VAULT_KEY 를 읽지 못했습니다", "err", err)
		os.Exit(1)
	}
	dsn := svc.Need(log, "DATABASE_URL")
	ctx, stop := svc.Signals()
	defer stop()

	pool, err := pg.Open(ctx, dsn, 4, log)
	if err != nil {
		log.Error("데이터베이스에 연결하지 못했습니다", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	if svc.Serve(ctx, svc.Server{
		Name: "vault-svc", Addr: svc.Env("LISTEN_ADDR", ":8105"), Log: log, Says: []any{"key", sealer.ID()},
		Handler: (&api.Server{Store: store.New(pool), Sealer: sealer, Token: token, ReleaseToken: release, Log: log}).Handler(),
		Read:    10 * time.Second,
		Write:   10 * time.Second,
	}) != nil {
		os.Exit(1)
	}
}
