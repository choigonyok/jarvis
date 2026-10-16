package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/choigonyok/jarvis/agent/internal/core/action"
	"github.com/choigonyok/jarvis/agent/internal/core/bus"
	"github.com/choigonyok/jarvis/agent/internal/core/module"
	"github.com/choigonyok/jarvis/agent/internal/core/proposal"
	"github.com/choigonyok/jarvis/agent/internal/module/vault"
)

// A card's private fields (a password) go to the module at approval and are
// kept nowhere else: not in the card's edits, not in what the decision
// publishes, not in what the model is later told.
func TestPrivateFieldsGoToTheVaultOnly(t *testing.T) {
	var saved map[string]string
	fakeVault := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut && r.URL.Path == "/logins/coupang.com" && r.Header.Get("Authorization") == "Bearer svc" {
			_ = json.NewDecoder(r.Body).Decode(&saved)
			_, _ = w.Write([]byte(`{"domain":"coupang.com","name":"쿠팡","hint":"ab***"}`))
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/logins" {
			_, _ = w.Write([]byte(`{"logins":[]}`))
			return
		}
		http.Error(w, "no", http.StatusNotFound)
	}))
	defer fakeVault.Close()

	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	v := vault.New(vault.NewClient(fakeVault.URL, "svc"), quiet)
	reg := module.NewRegistry()
	reg.Add(v)
	b := bus.New()
	events, stop := b.Subscribe()
	defer stop()
	props := proposal.NewStore(b)
	s := &Server{proposals: props, modules: reg, log: quiet}

	act := action.Action{Kind: vault.KindSave, Input: json.RawMessage(`{"site":"https://login.coupang.com","name":"쿠팡"}`)}
	card, err := v.Preview(context.Background(), act)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range card.Fields {
		if !f.IsPrivate() || f.Value != "" {
			t.Fatalf("저장 카드의 칸은 비공개이고 비어 있어야 합니다: %+v", f)
		}
	}
	p, _ := props.Open(proposal.Proposal{Action: act, Card: card})

	decide := func(body string) (int, string) {
		req := httptest.NewRequest(http.MethodPost, "/proposals/"+p.ID+"/decision", strings.NewReader(body))
		req.SetPathValue("id", p.ID)
		rec := httptest.NewRecorder()
		s.postDecision(rec, req)
		return rec.Code, rec.Body.String()
	}
	if code, _ := decide(`{"decision":"approved","edits":{"username":"abcdef"}}`); code != http.StatusBadRequest || saved != nil {
		t.Fatalf("비밀번호 없이 승인되면 안 됩니다: %d %v", code, saved)
	}
	if code, body := decide(`{"decision":"approved","edits":{"username":"abcdef","password":"s3cret!"}}`); code != http.StatusNoContent {
		t.Fatalf("승인: %d %s", code, body)
	}
	if saved["username"] != "abcdef" || saved["password"] != "s3cret!" || saved["name"] != "쿠팡" {
		t.Fatalf("보관함에 그대로 가야 합니다: %v", saved)
	}
	got, _ := props.Get(p.ID)
	record, _ := json.Marshal(got)
	if got.State != proposal.Approved || strings.Contains(string(record), "s3cret") || strings.Contains(string(record), "abcdef") {
		t.Fatalf("카드 기록에 값이 남으면 안 됩니다: %s", record)
	}
	for {
		select {
		case e := <-events:
			raw, _ := json.Marshal(e)
			if strings.Contains(string(raw), "s3cret") {
				t.Fatalf("화면으로 나간 이벤트에 비밀번호: %s", raw)
			}
			continue
		default:
		}
		break
	}
}

// A card with a private field and no module to take it is not approved
// without it.
func TestPrivateFieldWithoutASinkIsRefused(t *testing.T) {
	props := proposal.NewStore(bus.New())
	reg := module.NewRegistry()
	reg.Add(editor{})
	s := &Server{proposals: props, modules: reg, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	p, _ := props.Open(proposal.Proposal{
		Action: action.Action{Kind: "shop.sell", Input: json.RawMessage(`{}`)},
		Card:   action.Card{Title: "sell", Fields: []action.Field{{Key: "pin", Kind: "secret", Required: true}}},
	})
	req := httptest.NewRequest(http.MethodPost, "/proposals/"+p.ID+"/decision", strings.NewReader(`{"decision":"approved","edits":{"pin":"1234"}}`))
	req.SetPathValue("id", p.ID)
	rec := httptest.NewRecorder()
	s.postDecision(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("받을 곳 없는 비밀 칸: %d", rec.Code)
	}
	if got, _ := props.Get(p.ID); got.State != proposal.Pending || len(got.Edits) != 0 {
		t.Fatalf("그대로 남아야 합니다: %+v", got)
	}
}
