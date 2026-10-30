// Package mindcmd is how the agent changes anything in mind. There is no write
// API: what would change its state goes to the outbox as a record, like
// everything else that happens, and mind's deterministic lane does it. So
// what was asked survives mind restarting, and is on the log to be replayed.
//
// The tools still answer with what mind stored, not what they meant to store:
// Do waits for the lane's answer at mind's read API (GET /api/commands/{key}).
package mindcmd

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/choigonyok/jarvis/gocommon/record"
)

const (
	source = "agent"
	// Wait is how long Do waits for the answer unless told otherwise: mind
	// restarting takes less.
	Wait = 20 * time.Second
	poll = 40 * time.Millisecond
)

// ErrWaiting says the record is on its way and no answer came in time. What
// was asked will still happen.
var ErrWaiting = errors.New("기록은 남겼지만 mind 가 아직 답하지 않았습니다. 곧 처리됩니다")

// Refused is mind saying it could not be done: its status, and its words.
type Refused struct {
	Status int
	Msg    string
}

func (e *Refused) Error() string { return e.Msg }

// Client sends commands.
type Client struct {
	URL   string
	Token string
	// Records is where they are written.
	Records *record.Writer
	// Actor is who asks: operator, or guest.
	Actor string
	HTTP  *http.Client
	Wait  time.Duration
}

// Do sends what would have been method on path with in, and decodes the answer
// into out. An answer that says it could not be done is a Refused with
// mind's words.
func (c *Client) Do(ctx context.Context, method, path string, in, out any) error {
	key, err := c.send(method, path, in)
	if err != nil {
		return err
	}
	return c.answer(ctx, key, out)
}

// Leave sends the same and does not wait: a line of progress nobody waits on.
func (c *Client) Leave(method, path string, in any) error {
	_, err := c.send(method, path, in)
	return err
}

func (c *Client) send(method, path string, in any) (string, error) {
	if c == nil || c.Records == nil {
		return "", errors.New("기록을 남길 곳이 없습니다")
	}
	key, err := newKey()
	if err != nil {
		return "", err
	}
	var body json.RawMessage
	if in != nil {
		if body, err = json.Marshal(in); err != nil {
			return "", err
		}
	}
	subject, _, _ := strings.Cut(path, "?")
	c.Records.Write(record.Record{
		Kind: record.CommandKind(method, path), Source: source, SourceKey: key, Actor: c.actor(), Channel: "tool",
		Subject: subject, Tier: record.Raw,
		Payload: map[string]any{"method": method, "path": path, "body": body},
	})
	return key, nil
}

func (c *Client) actor() string {
	if c.Actor == "" {
		return "operator"
	}
	return c.Actor
}

func newKey() (string, error) {
	raw := make([]byte, 12)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return "cmd-" + hex.EncodeToString(raw), nil
}

// answer waits for what came of a command.
func (c *Client) answer(ctx context.Context, key string, out any) error {
	wait := c.Wait
	if wait <= 0 {
		wait = Wait
	}
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	for {
		done, err := c.look(ctx, client, key, out)
		if done {
			return err
		}
		select {
		case <-ctx.Done():
			return ErrWaiting
		case <-time.After(poll):
		}
	}
}

// look reads the answer once. It is not done while the lane has not got to it
// or mind cannot be reached: a restart is what the record is there to outlast.
func (c *Client) look(ctx context.Context, client *http.Client, key string, out any) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.URL, "/")+"/api/commands/"+url.PathEscape(key), nil)
	if err != nil {
		return true, err
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return false, nil
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil || resp.StatusCode != http.StatusOK {
		return false, nil
	}
	var got struct {
		Status int             `json:"status"`
		Body   json.RawMessage `json:"body"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		return true, err
	}
	if got.Status >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(got.Body, &e) == nil && e.Error != "" {
			return true, &Refused{Status: got.Status, Msg: e.Error}
		}
		return true, &Refused{Status: got.Status, Msg: fmt.Sprintf("mind 가 %d 로 응답했습니다", got.Status)}
	}
	if out == nil || len(bytes.TrimSpace(got.Body)) == 0 {
		return true, nil
	}
	return true, json.Unmarshal(got.Body, out)
}
