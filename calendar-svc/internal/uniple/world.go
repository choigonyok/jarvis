package uniple

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/choigonyok/jarvis/calendar-svc/internal/event"
)

// The world map is the date map's twin: a country (ISO 3166-1 alpha-2) where
// the date map has a region, a trip (a city, its days, a note) where it has a
// record. They share the Visit and Record shapes - Region holds the country,
// Title the city - and the same routes; the block's type says which tables.

const (
	worldVisitColumns  = "id,country_code,asset_id,clip_asset_id,created_at,rotation,order_index,record_id"
	worldRecordColumns = "id,country_code,city,date_start,date_end,note,order_index,created_at"
)

var countryCode = regexp.MustCompile(`^[A-Z]{2}$`)

type worldVisitRow struct {
	ID          string   `json:"id"`
	CountryCode string   `json:"country_code"`
	AssetID     *string  `json:"asset_id"`
	ClipID      *string  `json:"clip_asset_id"`
	CreatedAt   string   `json:"created_at"`
	Rotation    *float64 `json:"rotation"`
	OrderIndex  int      `json:"order_index"`
	RecordID    *string  `json:"record_id"`
}

// toVisit is a country's photo. Its face - the photo the country is filled
// with - is the one of no trip.
func (v worldVisitRow) toVisit() Visit {
	out := Visit{ID: v.ID, Region: v.CountryCode, Asset: str(v.AssetID), Rep: v.RecordID == nil, Record: str(v.RecordID), CreatedAt: v.CreatedAt}
	if clip := str(v.ClipID); clip != out.Asset {
		out.Clip = clip
	}
	if v.Rotation != nil {
		out.Rotation = *v.Rotation
	}
	return out
}

type worldRecordRow struct {
	ID          string  `json:"id"`
	CountryCode string  `json:"country_code"`
	City        *string `json:"city"`
	DateStart   *string `json:"date_start"`
	DateEnd     *string `json:"date_end"`
	Note        *string `json:"note"`
	OrderIndex  int     `json:"order_index"`
	CreatedAt   string  `json:"created_at"`
}

func (r worldRecordRow) toRecord() Record {
	return Record{ID: r.ID, Region: r.CountryCode, Title: str(r.City), From: str(r.DateStart), To: str(r.DateEnd), Note: str(r.Note), Order: r.OrderIndex, CreatedAt: r.CreatedAt}
}

// kindOf is a block of the space and its type, refused unless it is one of kinds.
func (c *Client) kindOf(ctx context.Context, id string, kinds ...string) (space, kind string, err error) {
	space, _, err = c.where(ctx)
	if err != nil {
		return "", "", err
	}
	var rows []struct {
		ID   string `json:"id"`
		Type string `json:"type"`
	}
	q := url.Values{"id": {"eq." + id}, "space_id": {"eq." + space}, "archived_at": {"is.null"}, "select": {"id,type"}}
	if err := c.rest(ctx, http.MethodGet, "blocks?"+q.Encode(), nil, &rows); err != nil {
		return "", "", err
	}
	if len(rows) == 0 || !slices.Contains(kinds, rows[0].Type) {
		return "", "", ErrBlock
	}
	return space, rows[0].Type, nil
}

// worldItems is a world map whole: every photo, newest first, and every trip.
func (c *Client) worldItems(ctx context.Context, block string) ([]Visit, []Record, error) {
	var visits []worldVisitRow
	q := url.Values{"block_id": {"eq." + block}, "select": {worldVisitColumns}, "order": {"created_at.desc,id.asc"}}
	if err := c.rest(ctx, http.MethodGet, "worldmap_visits?"+q.Encode(), nil, &visits); err != nil {
		return nil, nil, err
	}
	var records []worldRecordRow
	q = url.Values{"block_id": {"eq." + block}, "select": {worldRecordColumns}}
	if err := c.rest(ctx, http.MethodGet, "worldmap_records?"+q.Encode(), nil, &records); err != nil {
		return nil, nil, err
	}
	vs := make([]Visit, 0, len(visits))
	for _, v := range visits {
		vs = append(vs, v.toVisit())
	}
	rs := make([]Record, 0, len(records))
	for _, r := range records {
		rs = append(rs, r.toRecord())
	}
	sort.SliceStable(rs, func(i, j int) bool {
		a, b := firstOf(rs[i].From, rs[i].CreatedAt), firstOf(rs[j].From, rs[j].CreatedAt)
		if a != b {
			return a > b
		}
		return rs[i].CreatedAt > rs[j].CreatedAt
	})
	return vs, rs, nil
}

func country(code string) (string, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	if !countryCode.MatchString(code) {
		return "", event.ValidationError{Msg: "나라는 두 글자 국가 코드여야 해요"}
	}
	return code, nil
}

// addWorldVisit fills a country with a photo, or puts a photo on a trip. The
// face goes through the app's own call; the first face a country has is
// told to the partner's phone, as the app tells it.
func (c *Client) addWorldVisit(ctx context.Context, block, space string, in VisitInput) (Visit, error) {
	if !uuidLike(in.Asset) {
		return Visit{}, event.ValidationError{Msg: "올린 사진이 없어요"}
	}
	if in.Face {
		code, err := country(in.Region)
		if err != nil {
			return Visit{}, err
		}
		return c.worldFace(ctx, block, space, code, in.Asset)
	}
	var recs []worldRecordRow
	q := url.Values{"id": {"eq." + in.Record}, "block_id": {"eq." + block}, "select": {worldRecordColumns}}
	if err := c.rest(ctx, http.MethodGet, "worldmap_records?"+q.Encode(), nil, &recs); err != nil {
		return Visit{}, err
	}
	if len(recs) == 0 {
		return Visit{}, event.ValidationError{Msg: "사진을 붙일 여행 기록이 없어요"}
	}
	var have []worldVisitRow
	q = url.Values{"block_id": {"eq." + block}, "record_id": {"eq." + in.Record}, "select": {"id"}}
	if err := c.rest(ctx, http.MethodGet, "worldmap_visits?"+q.Encode(), nil, &have); err != nil {
		return Visit{}, err
	}
	body := map[string]any{
		"id": newUUID(), "block_id": block, "space_id": space, "country_code": recs[0].CountryCode,
		"record_id": in.Record, "asset_id": in.Asset, "order_index": len(have),
	}
	var saved []worldVisitRow
	if err := c.rest(ctx, http.MethodPost, "worldmap_visits?select="+worldVisitColumns, body, &saved); err != nil {
		return Visit{}, err
	}
	if len(saved) == 0 {
		return Visit{}, errors.New("uniple 이 저장한 사진을 돌려주지 않았습니다")
	}
	return saved[0].toVisit(), nil
}

// worldFace puts a photo as a country's face, in place of any it had.
func (c *Client) worldFace(ctx context.Context, block, space, code, asset string) (Visit, error) {
	var had []worldVisitRow
	q := url.Values{"block_id": {"eq." + block}, "country_code": {"eq." + code}, "record_id": {"is.null"}, "select": {"id"}}
	if err := c.rest(ctx, http.MethodGet, "worldmap_visits?"+q.Encode(), nil, &had); err != nil {
		return Visit{}, err
	}
	var raw json.RawMessage
	if err := c.rest(ctx, http.MethodPost, "rpc/set_worldmap_fill_asset", map[string]any{
		"p_block_id": block, "p_space_id": space, "p_country_code": code,
		"p_asset_id": asset, "p_clip_asset_id": asset, "p_rotation": 0,
	}, &raw); err != nil {
		return Visit{}, err
	}
	var face worldVisitRow
	var rows []worldVisitRow
	if json.Unmarshal(raw, &rows) == nil && len(rows) > 0 {
		face = rows[0]
	} else if json.Unmarshal(raw, &face) != nil || face.ID == "" {
		// The call said nothing of the row: read it back.
		q.Set("select", worldVisitColumns)
		if err := c.rest(ctx, http.MethodGet, "worldmap_visits?"+q.Encode(), nil, &rows); err != nil {
			return Visit{}, err
		}
		if len(rows) == 0 {
			return Visit{}, errors.New("uniple 이 나라 사진을 돌려주지 않았습니다")
		}
		face = rows[0]
	}
	if len(had) == 0 {
		c.tell(ctx, block, map[string]any{"itemId": face.ID})
	}
	return face.toVisit(), nil
}

func (c *Client) setWorldVisit(ctx context.Context, block, space, id string, in VisitChange) (Visit, error) {
	var rows []worldVisitRow
	q := url.Values{"id": {"eq." + id}, "block_id": {"eq." + block}, "select": {worldVisitColumns}}
	if err := c.rest(ctx, http.MethodGet, "worldmap_visits?"+q.Encode(), nil, &rows); err != nil {
		return Visit{}, err
	}
	if len(rows) == 0 {
		return Visit{}, event.ErrNotFound
	}
	v := rows[0]
	if in.Asset != "" {
		if !uuidLike(in.Asset) {
			return Visit{}, event.ValidationError{Msg: "올린 사진이 없어요"}
		}
		if v.RecordID != nil {
			return Visit{}, event.ValidationError{Msg: "여행 기록의 사진은 바꾸지 않고 지우고 새로 올려요"}
		}
		return c.worldFace(ctx, block, space, v.CountryCode, in.Asset)
	}
	if in.Rotation == nil {
		return Visit{}, event.ValidationError{Msg: "바꿀 것이 없어요"}
	}
	turn, err := turned(*in.Rotation)
	if err != nil {
		return Visit{}, err
	}
	var saved []worldVisitRow
	q.Set("select", worldVisitColumns)
	if err := c.rest(ctx, http.MethodPatch, "worldmap_visits?"+q.Encode(), map[string]any{"rotation": turn}, &saved); err != nil {
		return Visit{}, err
	}
	if len(saved) == 0 {
		return Visit{}, event.ErrNotFound
	}
	return saved[0].toVisit(), nil
}

// worldRecordFields is a trip as written: the country only when new, the
// city where the date map has a title.
func worldRecordFields(in RecordInput, isNew bool) (map[string]any, error) {
	region := in.Region
	in.Region = nil
	f, err := in.fields(false)
	if err != nil {
		return nil, err
	}
	if t, ok := f["title"]; ok {
		delete(f, "title")
		f["city"] = t
	}
	if isNew {
		code, err := country(deref(region))
		if err != nil {
			return nil, err
		}
		f["country_code"] = code
	}
	return f, nil
}

func (c *Client) addWorldRecord(ctx context.Context, block, space string, in RecordInput) (Record, error) {
	f, err := worldRecordFields(in, true)
	if err != nil {
		return Record{}, err
	}
	var have []worldRecordRow
	q := url.Values{"block_id": {"eq." + block}, "country_code": {"eq." + f["country_code"].(string)}, "select": {"id,order_index"}, "order": {"order_index.desc"}, "limit": {"1"}}
	if err := c.rest(ctx, http.MethodGet, "worldmap_records?"+q.Encode(), nil, &have); err != nil {
		return Record{}, err
	}
	next := 0
	if len(have) > 0 {
		next = have[0].OrderIndex + 1
	}
	f["id"], f["block_id"], f["space_id"], f["order_index"] = newUUID(), block, space, next
	var saved []worldRecordRow
	if err := c.rest(ctx, http.MethodPost, "worldmap_records?select="+worldRecordColumns, f, &saved); err != nil {
		return Record{}, err
	}
	if len(saved) == 0 {
		return Record{}, errors.New("uniple 이 저장한 여행 기록을 돌려주지 않았습니다")
	}
	return saved[0].toRecord(), nil
}

func (c *Client) setWorldRecord(ctx context.Context, block, id string, in RecordInput) (Record, error) {
	f, err := worldRecordFields(in, false)
	if err != nil {
		return Record{}, err
	}
	if len(f) == 0 {
		return Record{}, event.ValidationError{Msg: "바꿀 것이 없어요"}
	}
	q := url.Values{"id": {"eq." + id}, "block_id": {"eq." + block}, "select": {worldRecordColumns}}
	var saved []worldRecordRow
	if err := c.rest(ctx, http.MethodPatch, "worldmap_records?"+q.Encode(), f, &saved); err != nil {
		return Record{}, err
	}
	if len(saved) == 0 {
		return Record{}, event.ErrNotFound
	}
	return saved[0].toRecord(), nil
}

// deleteWorldRecord takes a trip off, its photos with it, as the app does.
func (c *Client) deleteWorldRecord(ctx context.Context, block, id string) error {
	q := url.Values{"block_id": {"eq." + block}, "record_id": {"eq." + id}}
	if err := c.rest(ctx, http.MethodDelete, "worldmap_visits?"+q.Encode(), nil, nil); err != nil {
		return err
	}
	return c.deleteOne(ctx, "worldmap_records", block, id, nil)
}
