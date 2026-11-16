package uniple

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png" // decoded to make a thumb
	"io"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // decoded to make a thumb

	"github.com/jackc/pgx/v5"

	"github.com/choigonyok/jarvis/calendar-svc/internal/event"
)

// The photos of the local space are kept in the database (couple.media), so
// they go wherever its backups go. A photo is kept as it was put - the screens
// shrink it to 1080 on the long side before sending - with a small copy for
// lists and grids.

// thumbSide is the long side of the copy lists and grids show.
const thumbSide = 360

func (l *local) photo(ctx context.Context, id, size string) (Photo, error) {
	if !uuidLike(id) {
		return Photo{}, ErrNoPhoto
	}
	var display, thumb []byte
	var mime, thumbMime string
	err := l.pool.QueryRow(ctx, `select display, mime, thumb, coalesce(thumb_mime, '') from couple.media where id = $1::uuid`, id).
		Scan(&display, &mime, &thumb, &thumbMime)
	if errors.Is(err, pgx.ErrNoRows) {
		return Photo{}, ErrNoPhoto
	}
	if err != nil {
		return Photo{}, err
	}
	body, kind := display, mime
	if size == "thumb" && len(thumb) > 0 {
		body, kind = thumb, thumbMime
	}
	return Photo{Type: kind, Size: int64(len(body)), Body: io.NopCloser(bytes.NewReader(body))}, nil
}

// upload keeps a photo put by whoever asks and names it.
func (l *local) upload(ctx context.Context, kind string, body []byte) (string, error) {
	if !photoKinds[kind] {
		return "", event.ValidationError{Msg: "사진은 jpeg, png, webp 만 올릴 수 있어요"}
	}
	if len(body) == 0 || len(body) > MaxPhoto {
		return "", event.ValidationError{Msg: "사진은 4MB 까지 올릴 수 있어요"}
	}
	me, _, err := l.who(ctx)
	if err != nil {
		return "", err
	}
	id := newUUID()
	return id, l.keep(ctx, id, kind, body, me)
}

// keep stores a photo's bytes under an id, with its thumb. A photo that cannot
// be read as an image is refused: nothing would show it.
func (l *local) keep(ctx context.Context, id, kind string, body []byte, by string) error {
	thumb, w, h, err := thumbOf(body)
	if err != nil {
		return event.ValidationError{Msg: "사진을 읽지 못했어요. jpeg, png, webp 인지 확인해 주세요"}
	}
	var creator any
	if by != "" {
		creator = by
	}
	_, err = l.pool.Exec(ctx, `
		insert into couple.media (id, mime, display, thumb, thumb_mime, width, height, created_by)
		values ($1::uuid, $2, $3, $4, 'image/jpeg', $5, $6, $7::uuid)
		on conflict (id) do update set mime = excluded.mime, display = excluded.display, thumb = excluded.thumb,
		  thumb_mime = excluded.thumb_mime, width = excluded.width, height = excluded.height`,
		id, kind, body, thumb, w, h, creator)
	if err != nil {
		return fmt.Errorf("사진 저장: %w", err)
	}
	return nil
}

// thumbOf is a JPEG no longer than thumbSide on its long side, and the
// photo's own size.
func thumbOf(body []byte) ([]byte, int, int, error) {
	img, _, err := image.Decode(bytes.NewReader(body))
	if err != nil {
		return nil, 0, 0, err
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	tw, th := w, h
	if long := max(w, h); long > thumbSide {
		tw, th = max(1, w*thumbSide/long), max(1, h*thumbSide/long)
	}
	dst := image.NewRGBA(image.Rect(0, 0, tw, th))
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, b, draw.Src, nil)
	var out bytes.Buffer
	if err := jpeg.Encode(&out, dst, &jpeg.Options{Quality: 80}); err != nil {
		return nil, 0, 0, err
	}
	return out.Bytes(), w, h, nil
}
