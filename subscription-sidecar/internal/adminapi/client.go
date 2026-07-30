package adminapi

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

type Client struct {
	BaseURL    string
	AdminKey   string
	HTTPClient *http.Client
}

type Envelope struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

type Proxy struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	Protocol     string `json:"protocol"`
	Host         string `json:"host"`
	Port         int    `json:"port"`
	Username     string `json:"username"`
	Status       string `json:"status"`
	AccountCount int64  `json:"account_count"`
}

type CreateProxyRequest struct {
	Name     string `json:"name"`
	Protocol string `json:"protocol"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
}

// UpdateProxyRequest only includes fields the sidecar is allowed to touch.
// Backend UpdateProxy overwrites expiry/fallback from zero values — we avoid sending those keys
// by using a dedicated minimal JSON map in UpdateProxyMinimal.
type UpdateProxyRequest struct {
	Name     string `json:"name,omitempty"`
	Protocol string `json:"protocol,omitempty"`
	Host     string `json:"host,omitempty"`
	Port     int    `json:"port,omitempty"`
	Status   string `json:"status,omitempty"`
}

type pageData struct {
	Items    []Proxy `json:"items"`
	Total    int64   `json:"total"`
	Page     int     `json:"page"`
	PageSize int     `json:"page_size"`
}

func New(baseURL, adminKey string) *Client {
	return &Client{
		BaseURL:  strings.TrimRight(baseURL, "/"),
		AdminKey: adminKey,
		HTTPClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// ListOwnedProxies pages GET /api/v1/admin/proxies with search=prefix across all statuses.
func (c *Client) ListOwnedProxies(ctx context.Context, namePrefix string) ([]Proxy, error) {
	const pageSize = 100
	out := make([]Proxy, 0)
	page := 1
	for {
		q := url.Values{}
		q.Set("page", fmt.Sprintf("%d", page))
		q.Set("page_size", fmt.Sprintf("%d", pageSize))
		q.Set("search", namePrefix)
		q.Set("sort_by", "id")
		q.Set("sort_order", "asc")
		// empty status = all statuses (active + inactive + expired if listed)
		var pd pageData
		if err := c.doJSON(ctx, http.MethodGet, "/api/v1/admin/proxies?"+q.Encode(), nil, &pd); err != nil {
			return nil, err
		}
		for _, p := range pd.Items {
			if strings.HasPrefix(p.Name, namePrefix) {
				out = append(out, p)
			}
		}
		if int64(page*pageSize) >= pd.Total || len(pd.Items) == 0 {
			break
		}
		page++
		if page > 100 {
			return nil, fmt.Errorf("proxy list pagination exceeded safety limit")
		}
	}
	return out, nil
}

// ListAllProxies kept for tests / simple active listing.
func (c *Client) ListAllProxies(ctx context.Context) ([]Proxy, error) {
	var out []Proxy
	if err := c.doJSON(ctx, http.MethodGet, "/api/v1/admin/proxies/all?with_count=true", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *Client) CreateProxy(ctx context.Context, req CreateProxyRequest) (*Proxy, error) {
	var out Proxy
	if err := c.doJSON(ctx, http.MethodPost, "/api/v1/admin/proxies", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateProxyNetwork reactivates/updates only network identity fields.
// NOTE: Sub2API UpdateProxy currently overwrites expiry/fallback with zero values from the input
// struct. Sidecar-managed proxies must not rely on fallback/expiry configuration.
func (c *Client) UpdateProxy(ctx context.Context, id int64, req UpdateProxyRequest) (*Proxy, error) {
	var out Proxy
	path := fmt.Sprintf("/api/v1/admin/proxies/%d", id)
	// Send explicit JSON so we do not invent extra fields; backend still zero-resets expiry.
	body := map[string]any{
		"name":     req.Name,
		"protocol": req.Protocol,
		"host":     req.Host,
		"port":     req.Port,
		"status":   req.Status,
	}
	if err := c.doJSON(ctx, http.MethodPut, path, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) DeleteProxy(ctx context.Context, id int64) error {
	path := fmt.Sprintf("/api/v1/admin/proxies/%d", id)
	return c.doJSON(ctx, http.MethodDelete, path, nil, nil)
}

func (c *Client) doJSON(ctx context.Context, method, path string, body any, out any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, rdr)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.AdminKey != "" {
		req.Header.Set("x-api-key", c.AdminKey)
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("admin api %s %s: HTTP %d: %s", method, path, resp.StatusCode, truncate(string(raw), 300))
	}

	var env Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("decode envelope: %w", err)
	}
	if env.Code != 0 {
		return fmt.Errorf("admin api code=%d message=%s", env.Code, env.Message)
	}
	if out == nil || len(env.Data) == 0 || string(env.Data) == "null" {
		return nil
	}
	if err := json.Unmarshal(env.Data, out); err != nil {
		return fmt.Errorf("decode data: %w", err)
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
