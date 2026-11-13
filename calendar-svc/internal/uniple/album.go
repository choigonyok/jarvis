package uniple

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"github.com/choigonyok/jarvis/calendar-svc/internal/event"
)

// AlbumPhoto is one photo of the shared album, newest taken first.
type AlbumPhoto struct {
	ID     string `json:"id"`
	Asset  string `json:"asset"`
	Width  int    `json:"width,omitempty"`
	Height int    `json:"height,omitempty"`
	// Media is photo or video. A video plays only in the app.
	Media     string `json:"media"`
	Duration  int    `json:"duration,omitempty"`
	By        string `json:"by"` // me | partner
	Folder    string `json:"folder,omitempty"`
	TakenAt   string `json:"takenAt,omitempty"`
	At        string `json:"at"`
	CreatedAt string `json:"createdAt"`
}

const albumColumns = "id,folder_id,uploaded_by,taken_at,sort_at,width,height,created_at,media_type,duration_ms,asset_id"

type albumRow struct {
	ID         string  `json:"id"`
	FolderID   *string `json:"folder_id"`
	UploadedBy *string `json:"uploaded_by"`
	TakenAt    *string `json:"taken_at"`
	SortAt     *string `json:"sort_at"`
	Width      *int    `json:"width"`
	Height     *int    `json:"height"`
	CreatedAt  string  `json:"created_at"`
	MediaType  *string `json:"media_type"`
	DurationMs *int    `json:"duration_ms"`
	AssetID    *string `json:"asset_id"`
}

func (r albumRow) toPhoto(me string) AlbumPhoto {
	p := AlbumPhoto{ID: r.ID, Asset: str(r.AssetID), Media: firstOf(str(r.MediaType), "photo"), By: side(str(r.UploadedBy), me),
		Folder: str(r.FolderID), TakenAt: str(r.TakenAt), At: firstOf(str(r.SortAt), r.CreatedAt), CreatedAt: r.CreatedAt}
	if r.Width != nil {
		p.Width = *r.Width
	}
	if r.Height != nil {
		p.Height = *r.Height
	}
	if r.DurationMs != nil {
		p.Duration = *r.DurationMs
	}
	return p
}

// albumPages is how many thousand photos one read goes through: the app keeps
// ten thousand at most.
const albumPages = 10

func (c *Client) albumItems(ctx context.Context, block, me string) ([]AlbumPhoto, error) {
	var out []AlbumPhoto
	for page := 0; page < albumPages; page++ {
		var rows []albumRow
		q := url.Values{"block_id": {"eq." + block}, "select": {albumColumns}, "order": {"sort_at.desc.nullslast,id.desc"},
			"limit": {"1000"}, "offset": {fmt.Sprint(page * 1000)}}
		if err := c.rest(ctx, http.MethodGet, "album_photos?"+q.Encode(), nil, &rows); err != nil {
			return nil, err
		}
		for _, r := range rows {
			out = append(out, r.toPhoto(me))
		}
		if len(rows) < 1000 {
			break
		}
	}
	return out, nil
}

// AlbumInput is photos uploaded for the album. A snap is one taken with the
// camera just now: the app shows it on the home as the latest, and tells the
// partner's phone.
type AlbumInput struct {
	Photos []struct {
		Asset  string `json:"asset"`
		Width  int    `json:"width"`
		Height int    `json:"height"`
	} `json:"photos"`
	Snap bool `json:"snap"`
}

// albumBatch is the most one call puts in, as the app lets one upload go.
const albumBatch = 30

// AddAlbumPhotos puts uploaded photos in the album.
func (c *Client) AddAlbumPhotos(ctx context.Context, block string, in AlbumInput) ([]AlbumPhoto, error) {
	space, _, err := c.kindOf(ctx, block, "album")
	if err != nil {
		return nil, err
	}
	_, me, _ := c.where(ctx)
	if len(in.Photos) == 0 {
		return nil, event.ValidationError{Msg: "올린 사진이 없어요"}
	}
	if len(in.Photos) > albumBatch {
		return nil, event.ValidationError{Msg: fmt.Sprintf("한 번에 %d장까지 올릴 수 있어요. 나눠서 올려 주세요", albumBatch)}
	}
	rows := make([]map[string]any, 0, len(in.Photos))
	for _, p := range in.Photos {
		if !uuidLike(p.Asset) {
			return nil, event.ValidationError{Msg: "올린 사진이 없어요"}
		}
		rows = append(rows, map[string]any{
			"id": newUUID(), "block_id": block, "space_id": space, "uploaded_by": me, "folder_id": nil,
			"asset_id": p.Asset, "width": nullableInt(p.Width), "height": nullableInt(p.Height),
			"is_snap": in.Snap, "media_type": "photo", "duration_ms": nil,
		})
	}
	var saved []albumRow
	if err := c.rest(ctx, http.MethodPost, "album_photos?select="+albumColumns, rows, &saved); err != nil {
		return nil, err
	}
	if len(saved) == 0 {
		return nil, errors.New("uniple 이 앨범에 넣은 사진을 돌려주지 않았습니다")
	}
	if in.Snap {
		c.tell(ctx, block, map[string]any{"kind": "snap"})
	}
	out := make([]AlbumPhoto, 0, len(saved))
	for _, r := range saved {
		out = append(out, r.toPhoto(me))
	}
	return out, nil
}

// DeleteAlbumPhotos takes photos out of the album, through the app's own call.
func (c *Client) DeleteAlbumPhotos(ctx context.Context, block string, ids []string) error {
	if _, _, err := c.kindOf(ctx, block, "album"); err != nil {
		return err
	}
	if len(ids) == 0 {
		return event.ValidationError{Msg: "지울 사진을 골라 주세요"}
	}
	for _, id := range ids {
		if !uuidLike(id) {
			return event.ValidationError{Msg: "그런 사진이 없어요"}
		}
	}
	return c.rest(ctx, http.MethodPost, "rpc/album_bulk_delete", map[string]any{"p_block_id": block, "p_photo_ids": ids, "p_request_id": newUUID()}, nil)
}

// PinAlbum puts a photo on the home's album card as mine (홈에 띄우기); no
// photo takes mine down.
func (c *Client) PinAlbum(ctx context.Context, block, asset string) error {
	if _, _, err := c.kindOf(ctx, block, "album"); err != nil {
		return err
	}
	if asset != "" && !uuidLike(asset) {
		return event.ValidationError{Msg: "그런 사진이 없어요"}
	}
	return c.rest(ctx, http.MethodPost, "rpc/set_album_pinned_asset", map[string]any{"p_block_id": block, "p_asset_id": nullable(asset)}, nil)
}

func nullableInt(n int) any {
	if n <= 0 {
		return nil
	}
	return n
}
