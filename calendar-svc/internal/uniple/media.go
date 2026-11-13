package uniple

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/choigonyok/jarvis/calendar-svc/internal/event"
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
var ErrNoPhoto = errors.New("그런 사진이 없어요")

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

/* ───────────────────────────── uploads ───────────────────────────── */

// MaxPhoto is the largest photo the app takes, and the kinds it takes.
const MaxPhoto = 4 << 20

var photoKinds = map[string]bool{"image/jpeg": true, "image/png": true, "image/webp": true}

// cookieName is the app's session cookie (@supabase/ssr), one per project.
const cookieName = "sb-jhocdjivutqcczrjocvk-auth-token"

// cookieChunk is the most @supabase/ssr puts in one cookie before chunking.
const cookieChunk = 3180

// Upload puts one photo into the space's media the way the app does: its
// server hands out a signed slot for the block, the bytes go to storage, and
// the asset is marked ready. The asset shows nowhere until a block's row names
// it. A slot that is not filled is given back.
func (c *Client) Upload(ctx context.Context, block, kind string, body []byte) (string, error) {
	if _, err := c.typed(ctx, block, ""); err != nil {
		return "", err
	}
	if !photoKinds[kind] {
		return "", event.ValidationError{Msg: "사진은 jpeg, png, webp 만 올릴 수 있어요"}
	}
	if len(body) == 0 || len(body) > MaxPhoto {
		return "", event.ValidationError{Msg: "사진은 4MB 까지 올릴 수 있어요"}
	}
	id := newUUID()
	var slots struct {
		Items []struct {
			AssetID string `json:"assetId"`
			Files   []struct {
				Role      string `json:"role"`
				SignedURL string `json:"signedUrl"`
				Path      string `json:"path"`
				Uploaded  bool   `json:"uploaded"`
			} `json:"files"`
		} `json:"items"`
		Code string `json:"code"`
	}
	ask := map[string]any{"blockId": block, "items": []map[string]any{{
		"assetId": id, "kind": "image", "takenAt": nil,
		"files": []map[string]any{{"role": "display", "bytes": len(body), "mime": kind}},
	}}}
	if err := c.app(ctx, http.MethodPost, "/api/media/uploads", ask, &slots); err != nil {
		return "", err
	}
	giveBack := func() {
		_ = c.rest(context.WithoutCancel(ctx), http.MethodPost, "rpc/abandon_media_uploads", map[string]any{"p_ids": []string{id}}, nil)
	}
	var signed string
	uploaded := false
	for _, it := range slots.Items {
		if it.AssetID != id {
			continue
		}
		for _, f := range it.Files {
			if f.Role == "display" {
				signed, uploaded = f.SignedURL, f.Uploaded
			}
		}
	}
	if signed == "" && !uploaded {
		giveBack()
		return "", errors.New("uniple 이 사진 올릴 자리를 주지 않았습니다")
	}
	if !uploaded {
		req, err := http.NewRequestWithContext(ctx, http.MethodPut, signed, bytes.NewReader(body))
		if err != nil {
			giveBack()
			return "", err
		}
		req.Header.Set("Content-Type", kind)
		req.Header.Set("Cache-Control", "max-age=31536000")
		res, err := photoHTTP.Do(req)
		if err != nil {
			giveBack()
			return "", fmt.Errorf("uniple 사진 올리기: %w", err)
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 1<<16))
		res.Body.Close()
		if res.StatusCode >= 300 {
			giveBack()
			return "", fmt.Errorf("uniple 사진 올리기: %s", res.Status)
		}
	}
	var done []struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if err := c.rest(ctx, http.MethodPost, "rpc/complete_media_uploads", map[string]any{"p_ids": []string{id}}, &done); err != nil {
		giveBack()
		return "", err
	}
	for _, d := range done {
		if d.ID == id && d.Status == "ready" {
			return id, nil
		}
	}
	giveBack()
	return "", errors.New("uniple 이 올린 사진을 받지 않았습니다")
}

// app calls the app's own server as the signed-in person: it reads the
// session from the cookie its pages set. The cookie carries a live access
// token and no refresh token, so the app's server can check who is asking but
// never rotates the session this service keeps.
func (c *Client) app(ctx context.Context, method, path string, body any, out any) error {
	access, err := c.token(ctx, false)
	if err != nil {
		return err
	}
	c.mu.Lock()
	left := time.Until(c.accessExp)
	c.mu.Unlock()
	// The app's server refreshes a session about to end; one with room left
	// it only checks.
	if left < 5*time.Minute {
		if access, err = c.token(ctx, true); err != nil {
			return err
		}
	}
	c.mu.Lock()
	exp, me := c.accessExp, c.me
	c.mu.Unlock()
	session, _ := json.Marshal(map[string]any{
		"access_token":  access,
		"token_type":    "bearer",
		"expires_in":    int(time.Until(exp).Seconds()),
		"expires_at":    exp.Unix(),
		"refresh_token": "jarvis-never-refresh",
		"user":          map[string]any{"id": me, "aud": "authenticated", "role": "authenticated"},
	})
	encoded, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.cfg.AppURL, "/")+path, bytes.NewReader(encoded))
	if err != nil {
		return err
	}
	for _, ck := range sessionCookies("base64-" + base64.RawURLEncoding.EncodeToString(session)) {
		req.AddCookie(ck)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", strings.TrimRight(c.cfg.AppURL, "/"))
	res, err := photoHTTP.Do(req)
	if err != nil {
		return unreachable(ctx, "앱 서버", err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode >= 300 {
		var e struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			Error   string `json:"error"`
		}
		_ = json.Unmarshal(raw, &e)
		if e.Code == "storage_full" {
			return event.ValidationError{Msg: "uniple 저장 공간이 가득 찼어요. 앱에서 사진을 정리하거나 플랜을 바꿔야 해요"}
		}
		return &Refused{Status: res.StatusCode, Code: e.Code, Message: firstOf(e.Message, e.Error)}
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(raw, out)
}

// tell has the app's server push the partner's phone, as the app does after a
// photo goes on the map or a habit day is checked: the block and what was
// added. The server files it among the partner's 알림 and spaces the pushes
// out; a push that does not go is not the change's failure, so it is sent on
// its own and only logged.
func (c *Client) tell(ctx context.Context, block string, extra map[string]any) {
	if c.cfg.AppURL == "" {
		return
	}
	body := map[string]any{"blockId": block}
	for k, v := range extra {
		body[k] = v
	}
	c.pushes.Add(1)
	go func() {
		defer c.pushes.Done()
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		defer cancel()
		if err := c.app(ctx, http.MethodPost, "/api/push/notify-partner", body, nil); err != nil {
			slog.Warn("uniple 상대 알림을 보내지 못했습니다", "block", block, "err", err)
		}
	}()
}

// sessionCookies is a session cookie value as @supabase/ssr writes it: one
// cookie when it fits, else name.0, name.1, ... in order.
func sessionCookies(value string) []*http.Cookie {
	if len(value) <= cookieChunk {
		return []*http.Cookie{{Name: cookieName, Value: value}}
	}
	var out []*http.Cookie
	for i := 0; len(value) > 0; i++ {
		n := min(cookieChunk, len(value))
		out = append(out, &http.Cookie{Name: fmt.Sprintf("%s.%d", cookieName, i), Value: value[:n]})
		value = value[n:]
	}
	return out
}

func firstOf(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}
