package vault

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Login is a saved login as vault-svc lists it: never the secret itself.
type Login struct {
	Domain     string     `json:"domain"`
	Name       string     `json:"name"`
	Hint       string     `json:"hint"`
	CreatedAt  time.Time  `json:"createdAt"`
	UpdatedAt  time.Time  `json:"updatedAt"`
	LastUsedAt *time.Time `json:"lastUsedAt,omitempty"`
	Uses       int        `json:"uses"`
}

// Use is one time the browser asked for a login, and what came of it.
type Use struct {
	At      time.Time `json:"at"`
	Domain  string    `json:"domain"`
	Host    string    `json:"host"`
	Purpose string    `json:"purpose"`
	Outcome string    `json:"outcome"`
	Note    string    `json:"note"`
}

// Pin is a saved payment password as vault-svc lists it: never the digits.
type Pin struct {
	Site       string     `json:"site"`
	Name       string     `json:"name"`
	CreatedAt  time.Time  `json:"createdAt"`
	UpdatedAt  time.Time  `json:"updatedAt"`
	LastUsedAt *time.Time `json:"lastUsedAt,omitempty"`
	Uses       int        `json:"uses"`
}

// Client is vault-svc with the services' token: it saves, lists and forgets.
// It cannot read a secret back - only the browser's token can.
type Client struct {
	base  string
	token string
	http  *http.Client
}

func NewClient(base, token string) *Client {
	return &Client{base: strings.TrimRight(base, "/"), token: token, http: &http.Client{Timeout: 10 * time.Second}}
}

func (c *Client) Save(ctx context.Context, domain, name, username, password string) (Login, error) {
	body, _ := json.Marshal(map[string]string{"name": name, "username": username, "password": password})
	var out Login
	err := c.do(ctx, http.MethodPut, "/logins/"+url.PathEscape(domain), body, &out)
	return out, err
}

func (c *Client) List(ctx context.Context) ([]Login, error) {
	var out struct {
		Logins []Login `json:"logins"`
	}
	err := c.do(ctx, http.MethodGet, "/logins", nil, &out)
	return out.Logins, err
}

func (c *Client) Forget(ctx context.Context, domain string) error {
	return c.do(ctx, http.MethodDelete, "/logins/"+url.PathEscape(domain), nil, nil)
}

// SavePin keeps a payment password for the payment host its keypad is on.
func (c *Client) SavePin(ctx context.Context, site, name, pin string) error {
	body, _ := json.Marshal(map[string]string{"name": name, "pin": pin})
	return c.do(ctx, http.MethodPut, "/pins/"+url.PathEscape(site), body, nil)
}

func (c *Client) Pins(ctx context.Context) ([]Pin, error) {
	var out struct {
		Pins []Pin `json:"pins"`
	}
	err := c.do(ctx, http.MethodGet, "/pins", nil, &out)
	return out.Pins, err
}

func (c *Client) ForgetPin(ctx context.Context, site string) error {
	return c.do(ctx, http.MethodDelete, "/pins/"+url.PathEscape(site), nil, nil)
}

// Shipping is a saved orderer and shipping address, read whole: an order
// sheet is typed from it.
type Shipping struct {
	Name      string `json:"name"`
	Recipient string `json:"recipient"`
	Phone     string `json:"phone"`
	Email     string `json:"email,omitempty"`
	Zip       string `json:"zip"`
	Address   string `json:"address"`
	Detail    string `json:"detail"`
	UpdatedAt string `json:"updatedAt,omitempty"`
}

// SaveProfile keeps a shipping profile under name ('기본'); vault-svc checks
// it (a road address up to its building number, a mobile number).
func (c *Client) SaveProfile(ctx context.Context, name string, s Shipping) error {
	body, _ := json.Marshal(s)
	return c.do(ctx, http.MethodPut, "/profiles/"+url.PathEscape(name), body, nil)
}

func (c *Client) Profiles(ctx context.Context) ([]Shipping, error) {
	var out struct {
		Profiles []Shipping `json:"profiles"`
	}
	err := c.do(ctx, http.MethodGet, "/profiles", nil, &out)
	return out.Profiles, err
}

func (c *Client) ForgetProfile(ctx context.Context, name string) error {
	return c.do(ctx, http.MethodDelete, "/profiles/"+url.PathEscape(name), nil, nil)
}

func (c *Client) Uses(ctx context.Context, limit int) ([]Use, error) {
	var out struct {
		Uses []Use `json:"uses"`
	}
	err := c.do(ctx, http.MethodGet, fmt.Sprintf("/uses?limit=%d", limit), nil, &out)
	return out.Uses, err
}

func (c *Client) do(ctx context.Context, method, path string, body []byte, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("보관함에 닿지 못했습니다: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode/100 != 2 {
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(raw, &e) == nil && e.Error != "" {
			return fmt.Errorf("%s", e.Error)
		}
		return fmt.Errorf("보관함이 %d 로 답했습니다", resp.StatusCode)
	}
	if out != nil && len(raw) > 0 {
		return json.Unmarshal(raw, out)
	}
	return nil
}
