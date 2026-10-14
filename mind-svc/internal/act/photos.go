package act

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Photos is where photo albums are made: Immich in the stack, a map on a
// laptop.
type Photos interface {
	CreateAlbum(ctx context.Context, name string, assets []string) (id, url string, err error)
	DeleteAlbum(ctx context.Context, id string) error
}

// PhotosActuator does photos.album: an album of a plan's photos with the
// picked ones first, ready to share from the Immich app. Sharing - sending
// to anyone - stays the operator's.
type PhotosActuator struct{ Photos Photos }

type album struct {
	PlanID string   `json:"planId"`
	Name   string   `json:"name"`
	All    []string `json:"all"`
	Picked []string `json:"picked"`
	To     string   `json:"to"`
}

func (PhotosActuator) Kinds() []string { return []string{"photos.album"} }

func (PhotosActuator) Preview(_ context.Context, _ string, payload json.RawMessage) (string, error) {
	var a album
	if err := json.Unmarshal(payload, &a); err != nil {
		return "", err
	}
	if len(a.All) == 0 || strings.TrimSpace(a.Name) == "" {
		return "", errors.New("사진과 앨범 이름이 필요합니다")
	}
	return fmt.Sprintf("앨범 '%s'에 사진 %d장(골라 둔 %d장을 맨 앞에)을 모읍니다. %s에게 보내기는 Immich 앱에서 직접", a.Name, len(a.All), len(a.Picked), a.To), nil
}

func (p PhotosActuator) Execute(ctx context.Context, _ string, payload json.RawMessage) (json.RawMessage, json.RawMessage, error) {
	var a album
	if err := json.Unmarshal(payload, &a); err != nil {
		return nil, nil, err
	}
	order := append([]string{}, a.Picked...)
	for _, id := range a.All {
		if !contains(order, id) {
			order = append(order, id)
		}
	}
	id, url, err := p.Photos.CreateAlbum(ctx, a.Name, order)
	if err != nil {
		return nil, nil, err
	}
	return mustJSON(map[string]string{"albumId": id, "url": url}), mustJSON(map[string]string{"albumId": id}), nil
}

func (p PhotosActuator) Undo(ctx context.Context, _ string, undo json.RawMessage) (json.RawMessage, error) {
	var u struct {
		AlbumID string `json:"albumId"`
	}
	if err := json.Unmarshal(undo, &u); err != nil {
		return nil, err
	}
	return json.RawMessage(`{}`), p.Photos.DeleteAlbum(ctx, u.AlbumID)
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// MemoryPhotos keeps albums in this process.
type MemoryPhotos struct {
	mu     sync.Mutex
	seq    int
	Albums map[string][]string
}

func (m *MemoryPhotos) CreateAlbum(_ context.Context, name string, assets []string) (string, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Albums == nil {
		m.Albums = map[string][]string{}
	}
	m.seq++
	id := fmt.Sprintf("album-%d", m.seq)
	m.Albums[id] = append([]string{name}, assets...)
	return id, "memory://" + id, nil
}

func (m *MemoryPhotos) DeleteAlbum(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.Albums, id)
	return nil
}

// Immich is the photo server's API (POST /api/albums, DELETE /api/albums/{id}).
// Public is the address the operator opens albums at.
type Immich struct {
	URL    string
	Key    string
	Public string
	HTTP   *http.Client
}

func (im Immich) CreateAlbum(ctx context.Context, name string, assets []string) (string, string, error) {
	var out struct {
		ID string `json:"id"`
	}
	if err := im.do(ctx, http.MethodPost, "/api/albums", map[string]any{"albumName": name, "assetIds": assets}, &out); err != nil {
		return "", "", err
	}
	base := im.Public
	if base == "" {
		base = im.URL
	}
	return out.ID, strings.TrimRight(base, "/") + "/albums/" + out.ID, nil
}

func (im Immich) DeleteAlbum(ctx context.Context, id string) error {
	return im.do(ctx, http.MethodDelete, "/api/albums/"+id, nil, nil)
}

func (im Immich) do(ctx context.Context, method, path string, in, out any) error {
	var body bytes.Buffer
	if in != nil {
		_ = json.NewEncoder(&body).Encode(in)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(im.URL, "/")+path, &body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", im.Key)
	client := im.HTTP
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("immich %s %s: %d", method, path, resp.StatusCode)
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}
