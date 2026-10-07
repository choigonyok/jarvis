package market

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/choigonyok/jarvis/agent/internal/core/action"
	"github.com/choigonyok/jarvis/agent/internal/uploads"
)

// The shape of Joongna's search page, cut down: the price summary is dt/dd
// pairs, and each result is a link to /product/<id>.
const searchPage = `
<dl><div><dt class="a">평균 가격</dt><dd class="b">103,260원</dd></div>
<div><dt class="a">가장 높은 가격</dt><dd class="b">260,000원</dd></div>
<div><dt class="a">가장 낮은 가격</dt><dd class="b">8,000원</dd></div></dl>
<a class="x" href="/product/111"><div><img/></div><h2>에어팟 프로 2 C타입</h2><div><span>150,000</span><span>원</span></div><span>20분 전</span></a>
<a class="x" href="/product/111"><div>중복</div></a>
<a class="x" href="/product/222"><h2>에어팟 이어팁</h2><div><span>10,000</span><span>원</span></div><span>1시간 전</span></a>
<a class="x" href="/product/333"><h2>가격 없는 글</h2></a>`

func TestParsePrices(t *testing.T) {
	p := parsePrices(searchPage, 10)
	if p.Summary["평균 가격"] != "103,260원" || p.Summary["가장 낮은 가격"] != "8,000원" {
		t.Fatalf("summary %v", p.Summary)
	}
	if len(p.Items) != 2 {
		t.Fatalf("items %+v", p.Items)
	}
	if got := p.Items[0]; got.ID != "111" || got.Title != "에어팟 프로 2 C타입" || got.Price != "150,000원" || got.Age != "20분 전" {
		t.Fatalf("first %+v", got)
	}
	if p := parsePrices(searchPage, 1); len(p.Items) != 1 {
		t.Fatalf("limit ignored: %d", len(p.Items))
	}
}

var onePixel = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89\x00\x00\x00\rIDATx\x9cc\xf8\x0f\x00\x00\x01\x01\x00\x05\x18\xd8N\x00\x00\x00\x00IEND\xaeB`\x82")

func newModule(t *testing.T) (*Module, string) {
	t.Helper()
	up, err := uploads.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	name, err := up.Save(bytes.NewReader(onePixel))
	if err != nil {
		t.Fatal(err)
	}
	return New(NewStore("http://127.0.0.1:1", ""), up), name
}

func post(t *testing.T, in PostInput) action.Action {
	t.Helper()
	raw, _ := json.Marshal(in)
	return action.Action{Kind: KindPost, Input: raw}
}

func TestPreviewAcceptsPathAndEmptyPrice(t *testing.T) {
	m, photo := newModule(t)
	a := post(t, PostInput{Photos: []string{m.uploads.Dir + "/" + photo}, Title: "에어팟 프로 2", Category: "디지털기기"})
	c, err := m.Preview(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Images) != 1 || c.Images[0] != photo {
		t.Fatalf("images %v", c.Images)
	}
	if f := field(c, "priceKrw"); f.Value != "" || f.Kind != "number" {
		t.Fatalf("price field %+v", f)
	}
	if f := field(c, "shipping"); f.Value != "택배비 포함" {
		t.Fatalf("shipping default %+v", f)
	}
}

func TestPreviewRejectsUnknownPhoto(t *testing.T) {
	m, _ := newModule(t)
	a := post(t, PostInput{Photos: []string{"0123456789abcdef0123456789abcdef.jpg"}, Title: "x"})
	if _, err := m.Preview(context.Background(), a); err == nil {
		t.Fatal("unknown photo accepted")
	}
}

func TestReviseAppliesEditsAndNeedsPrice(t *testing.T) {
	m, photo := newModule(t)
	a := post(t, PostInput{Photos: []string{photo}, Title: "에어팟", Description: "본문"})

	if _, _, err := m.Revise(context.Background(), a, nil); err == nil || !strings.Contains(err.Error(), "가격") {
		t.Fatalf("approval without a price: %v", err)
	}

	edited, c, err := m.Revise(context.Background(), a, map[string]string{
		"priceKrw": "150,000원", "title": "에어팟 프로 2세대", "shipping": "택배비 별도",
	})
	if err != nil {
		t.Fatal(err)
	}
	var in PostInput
	_ = json.Unmarshal(edited.Input, &in)
	if in.PriceKrw != 150000 || in.Title != "에어팟 프로 2세대" || in.Shipping != "separate" || in.Description != "본문" {
		t.Fatalf("edited %+v", in)
	}
	if field(c, "priceKrw").Value != "150000" {
		t.Fatalf("card not re-rendered: %+v", field(c, "priceKrw"))
	}

	if _, _, err := m.Revise(context.Background(), a, map[string]string{"photos": "x"}); err == nil {
		t.Fatal("photos must not be editable from the card")
	}
	if _, _, err := m.Revise(context.Background(), a, map[string]string{"priceKrw": "십만원"}); err == nil {
		t.Fatal("non-numeric price accepted")
	}
}

func TestTaskPromptNamesPhotoPaths(t *testing.T) {
	m, photo := newModule(t)
	p := m.taskPrompt(Work{
		Task:    Task{ID: 7, Kind: "post"},
		Listing: Listing{Title: "에어팟", PriceKrw: 150000, Shipping: "included", Photos: []string{photo}},
	})
	if !strings.Contains(p, m.uploads.Dir+"/"+photo) || !strings.Contains(p, "작업 id 7") {
		t.Fatalf("prompt:\n%s", p)
	}
}

func TestKrw(t *testing.T) {
	for in, want := range map[int64]string{0: "0원", 900: "900원", 1000: "1,000원", 150000: "150,000원", 1234567: "1,234,567원"} {
		if got := krw(in); got != want {
			t.Errorf("%d: %s", in, got)
		}
	}
}

func field(c action.Card, key string) action.Field {
	for _, f := range c.Fields {
		if f.Key == key {
			return f
		}
	}
	return action.Field{}
}
