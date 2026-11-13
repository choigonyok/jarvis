package uniple

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Photos in uniple are media assets: one id, a few sizes (variants) in a
// public storage bucket. The console never loads another site's images (its
// CSP), so the bytes come through here, looked up the way the app looks them
// up - by id, through media_assets_lookup, which answers only for assets of
// this space.

// Photo is one size of a photo, ready to be streamed.
type Photo struct {
	Type string
	Size int64
	Body io.ReadCloser
}

// ErrNoPhoto is an asset that is not there, or has no such size yet.
var ErrNoPhoto = errors.New("그런 사진이 없습니다")

type asset struct {
	ID       string                  `json:"id"`
	Kind     string                  `json:"kind"`
	Bucket   string                  `json:"bucket"`
	Status   string                  `json:"status"`
	Variants map[string]assetVariant `json:"variants"`
}

type assetVariant struct {
	Path   string `json:"path"`
	Bucket string `json:"bucket"`
	State  string `json:"state"`
}

type assetCache struct {
	mu   sync.Mutex
	seen map[string]cached
}

type cached struct {
	a  *asset
	at time.Time
}

// An asset's variants do not move once made; a short life still lets a size
// that was being prepared show up.
const assetTTL = 10 * time.Minute

var photoHTTP = &http.Client{Timeout: 30 * time.Second}

// Photo opens one size of a photo: thumb (small, for tiles) or display.
func (c *Client) Photo(ctx context.Context, id, size string) (Photo, error) {
	if !uuidLike(id) {
		return Photo{}, ErrNoPhoto
	}
	a, err := c.asset(ctx, id)
	if err != nil {
		return Photo{}, err
	}
	if a == nil || a.Kind == "video" {
		return Photo{}, ErrNoPhoto
	}
	order := []string{"display"}
	if size == "thumb" {
		order = []string{"thumb", "display"}
	}
	for _, name := range order {
		v, ok := a.Variants[name]
		if !ok || v.Path == "" || v.State != "" {
			continue
		}
		bucket := v.Bucket
		if bucket == "" {
			bucket = a.Bucket
		}
		return c.object(ctx, bucket, v.Path)
	}
	return Photo{}, ErrNoPhoto
}

func (c *Client) asset(ctx context.Context, id string) (*asset, error) {
	c.assetsOnce.Do(func() { c.assets = &assetCache{seen: map[string]cached{}} })
	c.assets.mu.Lock()
	hit, ok := c.assets.seen[id]
	c.assets.mu.Unlock()
	if ok && time.Since(hit.at) < assetTTL {
		return hit.a, nil
	}
	var found []asset
	if err := c.rest(ctx, http.MethodPost, "rpc/media_assets_lookup", map[string]any{"p_ids": []string{id}, "p_legacy": []string{}}, &found); err != nil {
		return nil, err
	}
	var a *asset
	for i := range found {
		if found[i].ID == id {
			a = &found[i]
		}
	}
	c.assets.mu.Lock()
	c.assets.seen[id] = cached{a: a, at: time.Now()}
	if len(c.assets.seen) > 4000 {
		for k, v := range c.assets.seen {
			if time.Since(v.at) > assetTTL {
				delete(c.assets.seen, k)
			}
		}
	}
	c.assets.mu.Unlock()
	return a, nil
}

// object reads a file from the public bucket the app reads it from.
func (c *Client) object(ctx context.Context, bucket, path string) (Photo, error) {
	parts := strings.Split(path, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	target := fmt.Sprintf("%s/storage/v1/object/public/%s/%s", strings.TrimRight(c.cfg.BaseURL, "/"), url.PathEscape(bucket), strings.Join(parts, "/"))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return Photo{}, err
	}
	res, err := photoHTTP.Do(req)
	if err != nil {
		return Photo{}, fmt.Errorf("uniple 사진 읽기: %w", err)
	}
	if res.StatusCode == http.StatusNotFound || res.StatusCode == http.StatusBadRequest {
		res.Body.Close()
		return Photo{}, ErrNoPhoto
	}
	if res.StatusCode != http.StatusOK {
		res.Body.Close()
		return Photo{}, fmt.Errorf("uniple 사진 읽기: %s", res.Status)
	}
	kind := res.Header.Get("Content-Type")
	if !strings.HasPrefix(kind, "image/") {
		res.Body.Close()
		return Photo{}, ErrNoPhoto
	}
	return Photo{Type: kind, Size: res.ContentLength, Body: res.Body}, nil
}

func uuidLike(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, r := range s {
		switch {
		case i == 8 || i == 13 || i == 18 || i == 23:
			if r != '-' {
				return false
			}
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f', r >= 'A' && r <= 'F':
		default:
			return false
		}
	}
	return true
}
