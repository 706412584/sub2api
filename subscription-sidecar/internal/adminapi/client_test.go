package adminapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientCRUDAndOwnedList(t *testing.T) {
	mux := http.NewServeMux()
	var created CreateProxyRequest
	mux.HandleFunc("/api/v1/admin/proxies/all", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "admin-test" {
			http.Error(w, "unauth", 401)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 0, "message": "success",
			"data": []Proxy{{ID: 1, Name: "sidecar-a-x", Protocol: "socks5", Host: "127.0.0.1", Port: 21080, Status: "active"}},
		})
	})
	mux.HandleFunc("/api/v1/admin/proxies", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			// paginated owned list (includes inactive)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 0, "message": "success",
				"data": map[string]any{
					"items": []Proxy{
						{ID: 1, Name: "sidecar-a-x", Protocol: "socks5", Host: "127.0.0.1", Port: 21080, Status: "active"},
						{ID: 2, Name: "sidecar-a-y", Protocol: "socks5", Host: "127.0.0.1", Port: 21081, Status: "inactive"},
						{ID: 3, Name: "other", Protocol: "http", Host: "1.2.3.4", Port: 80, Status: "active"},
					},
					"total": 3, "page": 1, "page_size": 100,
				},
			})
		case http.MethodPost:
			_ = json.NewDecoder(r.Body).Decode(&created)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 0, "message": "success",
				"data": Proxy{ID: 9, Name: created.Name, Protocol: created.Protocol, Host: created.Host, Port: created.Port, Status: "active"},
			})
		default:
			http.Error(w, "method", 405)
		}
	})
	mux.HandleFunc("/api/v1/admin/proxies/9", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPut:
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "message": "success", "data": Proxy{ID: 9, Name: "sidecar-a-y", Status: "active", Port: 21081, Host: "127.0.0.1", Protocol: "socks5"}})
		case http.MethodDelete:
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "message": "success"})
		default:
			http.Error(w, "method", 405)
		}
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := New(srv.URL, "admin-test")
	owned, err := c.ListOwnedProxies(context.Background(), "sidecar-a-")
	if err != nil || len(owned) != 2 {
		t.Fatalf("owned err=%v list=%+v", err, owned)
	}
	list, err := c.ListAllProxies(context.Background())
	if err != nil || len(list) != 1 {
		t.Fatalf("list err=%v list=%+v", err, list)
	}
	p, err := c.CreateProxy(context.Background(), CreateProxyRequest{Name: "sidecar-a-new", Protocol: "socks5", Host: "127.0.0.1", Port: 21090})
	if err != nil || p.ID != 9 {
		t.Fatalf("create err=%v p=%+v", err, p)
	}
	if _, err := c.UpdateProxy(context.Background(), 9, UpdateProxyRequest{Port: 21081, Status: "active"}); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteProxy(context.Background(), 9); err != nil {
		t.Fatal(err)
	}
}
