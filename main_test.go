package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/openfaas/faas/gateway/types"
)

func TestNewRouter_ForwardsFunctionStatsRoute(t *testing.T) {
	var gotMethod string
	var gotPath string
	var gotQuery string

	h := func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}

	r := newRouter(types.HandlerSet{FunctionStats: h}, h, h, h)
	req := httptest.NewRequest(http.MethodGet, "/system/stats/function/echo-js?namespace=openfaas-fn", nil)
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	resp := rec.Result()
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want status %d, got %d body=%s", http.StatusOK, resp.StatusCode, string(body))
	}
	if gotMethod != http.MethodGet {
		t.Fatalf("want method %q, got %q", http.MethodGet, gotMethod)
	}
	if gotPath != "/system/stats/function/echo-js" {
		t.Fatalf("want path %q, got %q", "/system/stats/function/echo-js", gotPath)
	}
	if gotQuery != "namespace=openfaas-fn" {
		t.Fatalf("want query %q, got %q", "namespace=openfaas-fn", gotQuery)
	}
	if string(body) != `{"ok":true}` {
		t.Fatalf("want body %q, got %q", `{"ok":true}`, string(body))
	}
}
