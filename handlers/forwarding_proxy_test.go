// License: OpenFaaS Community Edition (CE) EULA
// Copyright (c) 2017,2019-2024 OpenFaaS Author(s)

// Copyright (c) OpenFaaS Author(s). All rights reserved.

package handlers

import (
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/openfaas/faas/gateway/pkg/middleware"
	"github.com/openfaas/faas/gateway/types"
)

func Test_buildUpstreamRequest_Body_Method_Query(t *testing.T) {
	srcBytes := []byte("hello world")

	reader := bytes.NewReader(srcBytes)
	request, _ := http.NewRequest(http.MethodPost, "/?code=1", reader)
	request.Header.Set("X-Source", "unit-test")

	if request.URL.RawQuery != "code=1" {
		t.Errorf("Query - want: %s, got: %s", "code=1", request.URL.RawQuery)
		t.Fail()
	}

	upstream := buildUpstreamRequest(request, "/", "")

	if request.Method != upstream.Method {
		t.Errorf("Method - want: %s, got: %s", request.Method, upstream.Method)
		t.Fail()
	}

	upstreamBytes, _ := io.ReadAll(upstream.Body)

	if string(upstreamBytes) != string(srcBytes) {
		t.Errorf("Body - want: %s, got: %s", string(upstreamBytes), string(srcBytes))
		t.Fail()
	}

	if request.Header.Get("X-Source") != upstream.Header.Get("X-Source") {
		t.Errorf("Header X-Source - want: %s, got: %s", request.Header.Get("X-Source"), upstream.Header.Get("X-Source"))
		t.Fail()
	}

	if request.URL.RawQuery != upstream.URL.RawQuery {
		t.Errorf("URL.RawQuery - want: %s, got: %s", request.URL.RawQuery, upstream.URL.RawQuery)
		t.Fail()
	}

}

func Test_buildUpstreamRequest_NoBody_GetMethod_NoQuery(t *testing.T) {
	request, _ := http.NewRequest(http.MethodGet, "/", nil)

	upstream := buildUpstreamRequest(request, "/", "")

	if request.Method != upstream.Method {
		t.Errorf("Method - want: %s, got: %s", request.Method, upstream.Method)
		t.Fail()
	}

	if upstream.Body != nil {
		t.Errorf("Body - expected nil")
		t.Fail()
	}

	if request.URL.RawQuery != upstream.URL.RawQuery {
		t.Errorf("URL.RawQuery - want: %s, got: %s", request.URL.RawQuery, upstream.URL.RawQuery)
		t.Fail()
	}

}

func Test_buildUpstreamRequest_HasXForwardedHostHeaderWhenSet(t *testing.T) {
	srcBytes := []byte("hello world")

	reader := bytes.NewReader(srcBytes)
	request, err := http.NewRequest(http.MethodPost, "http://gateway/function?code=1", reader)

	if err != nil {
		t.Fatal(err)
	}

	upstream := buildUpstreamRequest(request, "/", "/")

	if request.Host != upstream.Header.Get("X-Forwarded-Host") {
		t.Errorf("Host - want: %s, got: %s", request.Host, upstream.Header.Get("X-Forwarded-Host"))
	}
}

func Test_buildUpstreamRequest_XForwardedHostHeader_Empty_WhenNotSet(t *testing.T) {
	srcBytes := []byte("hello world")

	reader := bytes.NewReader(srcBytes)
	request, err := http.NewRequest(http.MethodPost, "/function", reader)

	if err != nil {
		t.Fatal(err)
	}

	upstream := buildUpstreamRequest(request, "/", "/")

	if request.Host != upstream.Header.Get("X-Forwarded-Host") {
		t.Errorf("Host - want: %s, got: %s", request.Host, upstream.Header.Get("X-Forwarded-Host"))
	}
}

func Test_buildUpstreamRequest_XForwardedHostHeader_WhenAlreadyPresent(t *testing.T) {
	srcBytes := []byte("hello world")
	headerValue := "test.openfaas.com"
	reader := bytes.NewReader(srcBytes)
	request, err := http.NewRequest(http.MethodPost, "/function/test", reader)

	if err != nil {
		t.Fatal(err)
	}

	request.Header.Set("X-Forwarded-Host", headerValue)
	upstream := buildUpstreamRequest(request, "/", "/")

	if upstream.Header.Get("X-Forwarded-Host") != headerValue {
		t.Errorf("X-Forwarded-Host - want: %s, got: %s", headerValue, upstream.Header.Get("X-Forwarded-Host"))
	}
}

func Test_buildUpstreamRequest_AppendsXForwardedForWhenPresent(t *testing.T) {
	request, err := http.NewRequest(http.MethodPost, "/function/test", bytes.NewReader([]byte("hello")))
	if err != nil {
		t.Fatal(err)
	}

	request.RemoteAddr = "10.62.0.42:39818"
	request.Header.Set("X-Forwarded-For", "203.0.113.10")

	upstream := buildUpstreamRequest(request, "http://faasd-provider:8081", "/function/test")

	got := upstream.Header.Get("X-Forwarded-For")
	want := "203.0.113.10, 10.62.0.42:39818"
	if got != want {
		t.Fatalf("X-Forwarded-For - want: %s, got: %s", want, got)
	}
}

func Test_MakeArchiveUploadForwardingProxyHandler_ArchiveDeployUsesArchiveTimeout(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodPut} {
		t.Run(method, func(t *testing.T) {
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/system/functions" {
					t.Fatalf("unexpected provider path: %s", r.URL.Path)
				}
				if r.Method != method {
					t.Fatalf("unexpected provider method: %s", r.Method)
				}
				_, _ = io.Copy(io.Discard, r.Body)
				time.Sleep(50 * time.Millisecond)
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("ok"))
			}))
			defer provider.Close()

			handler := newArchiveUploadTestHandler(t, provider.URL, 10*time.Millisecond, 200*time.Millisecond)
			body, contentType := multipartBody(t)
			req := httptest.NewRequest(method, "/system/functions", body)
			req.Header.Set("Content-Type", contentType)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("expected archive upload to use archive timeout, got status %d body=%q", rec.Code, rec.Body.String())
			}
			if rec.Body.String() != "ok" {
				t.Fatalf("unexpected body: %q", rec.Body.String())
			}
		})
	}
}

func Test_MakeArchiveUploadForwardingProxyHandler_JSONDeployUsesNormalTimeout(t *testing.T) {
	provider := slowProvider(t, 75*time.Millisecond)
	defer provider.Close()

	handler := newArchiveUploadTestHandler(t, provider.URL, 10*time.Millisecond, 200*time.Millisecond)
	req := httptest.NewRequest(http.MethodPost, "/system/functions", strings.NewReader(`{"service":"echo"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected JSON deploy to use normal timeout and fail with 502, got status %d body=%q", rec.Code, rec.Body.String())
	}
}

func Test_MakeArchiveUploadForwardingProxyHandler_UnrelatedMultipartRouteUsesNormalTimeout(t *testing.T) {
	provider := slowProvider(t, 75*time.Millisecond)
	defer provider.Close()

	handler := newArchiveUploadTestHandler(t, provider.URL, 10*time.Millisecond, 200*time.Millisecond)
	body, contentType := multipartBody(t)
	req := httptest.NewRequest(http.MethodPost, "/system/secrets", body)
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected unrelated multipart route to use normal timeout and fail with 502, got status %d body=%q", rec.Code, rec.Body.String())
	}
}

func Test_MakeArchiveUploadForwardingProxyHandler_ArchiveTimeoutReturnsMeaningfulMessage(t *testing.T) {
	provider := slowProvider(t, 75*time.Millisecond)
	defer provider.Close()

	handler := newArchiveUploadTestHandler(t, provider.URL, 10*time.Millisecond, 20*time.Millisecond)
	body, contentType := multipartBody(t)
	req := httptest.NewRequest(http.MethodPost, "/system/functions", body)
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("expected archive timeout to return 504, got status %d body=%q", rec.Code, rec.Body.String())
	}
	want := "archive upload timed out after 20ms while forwarding to provider"
	if !strings.Contains(rec.Body.String(), want) {
		t.Fatalf("expected timeout message %q, got %q", want, rec.Body.String())
	}
}

func newArchiveUploadTestHandler(t *testing.T, providerURL string, upstreamTimeout time.Duration, archiveTimeout time.Duration) http.HandlerFunc {
	t.Helper()
	parsed, err := url.Parse(providerURL)
	if err != nil {
		t.Fatal(err)
	}
	proxy := types.NewHTTPClientReverseProxy(parsed, upstreamTimeout, 10, 10)
	resolver := middleware.SingleHostBaseURLResolver{BaseURL: providerURL}
	transformer := middleware.TransparentURLPathTransformer{}
	return MakeArchiveUploadForwardingProxyHandler(proxy, nil, resolver, transformer, nil, archiveTimeout)
}

func slowProvider(t *testing.T, delay time.Duration) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		time.Sleep(delay)
		w.WriteHeader(http.StatusOK)
	}))
}

func multipartBody(t *testing.T) (*bytes.Buffer, string) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	field, err := writer.CreateFormField("deployment")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := field.Write([]byte(`{"service":"echo"}`)); err != nil {
		t.Fatal(err)
	}
	part, err := writer.CreateFormFile("image", "echo.tar")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte("archive")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return &body, writer.FormDataContentType()
}

func Test_getServiceName(t *testing.T) {
	scenarios := []struct {
		name        string
		url         string
		serviceName string
	}{
		{
			name:        "can handle request without trailing slash",
			url:         "/function/testFunc",
			serviceName: "testFunc",
		},
		{
			name:        "includes namespace",
			url:         "/function/test1.fn",
			serviceName: "test1.fn",
		},
		{
			name:        "can handle request with trailing slash",
			url:         "/function/testFunc/",
			serviceName: "testFunc",
		},
		{
			name:        "can handle request with query parameters",
			url:         "/function/testFunc?name=foo",
			serviceName: "testFunc",
		},
		{
			name:        "can handle request with trailing slash and query parameters",
			url:         "/function/testFunc/?name=foo",
			serviceName: "testFunc",
		},
		{
			name:        "can handle request with a fragment",
			url:         "/function/testFunc#fragment",
			serviceName: "testFunc",
		},
	}

	for _, s := range scenarios {
		t.Run(s.name, func(t *testing.T) {

			u, err := url.Parse("http://openfaas.local" + s.url)
			if err != nil {
				t.Fatal(err)
			}

			service := middleware.GetServiceName(u.Path)
			if service != s.serviceName {
				t.Fatalf("Incorrect service name - want: %s, got: %s", s.serviceName, service)
			}
		})
	}
}

func Test_buildUpstreamRequest_WithPathNoQuery(t *testing.T) {
	srcBytes := []byte("hello world")
	functionPath := "/employee/info/300"

	requestPath := fmt.Sprintf("/function/xyz%s", functionPath)

	reader := bytes.NewReader(srcBytes)
	request, _ := http.NewRequest(http.MethodPost, requestPath, reader)
	request.Header.Set("X-Source", "unit-test")

	queryWant := ""
	if request.URL.RawQuery != queryWant {

		t.Errorf("Query - want: %s, got: %s", queryWant, request.URL.RawQuery)
		t.Fail()
	}

	transformer := middleware.FunctionPrefixTrimmingURLPathTransformer{}
	transformedPath := transformer.Transform(request)

	wantTransformedPath := functionPath
	if transformedPath != wantTransformedPath {
		t.Errorf("transformedPath want: %s, got %s", wantTransformedPath, transformedPath)
	}

	upstream := buildUpstreamRequest(request, "http://xyz:8080", transformedPath)

	if request.Method != upstream.Method {
		t.Errorf("Method - want: %s, got: %s", request.Method, upstream.Method)
		t.Fail()
	}

	upstreamBytes, _ := io.ReadAll(upstream.Body)

	if string(upstreamBytes) != string(srcBytes) {
		t.Errorf("Body - want: %s, got: %s", string(upstreamBytes), string(srcBytes))
		t.Fail()
	}

	if request.Header.Get("X-Source") != upstream.Header.Get("X-Source") {
		t.Errorf("Header X-Source - want: %s, got: %s", request.Header.Get("X-Source"), upstream.Header.Get("X-Source"))
		t.Fail()
	}

	if request.URL.RawQuery != upstream.URL.RawQuery {
		t.Errorf("URL.RawQuery - want: %s, got: %s", request.URL.RawQuery, upstream.URL.RawQuery)
		t.Fail()
	}

	if functionPath != upstream.URL.Path {
		t.Errorf("URL.Path - want: %s, got: %s", functionPath, upstream.URL.Path)
		t.Fail()
	}

}

func Test_buildUpstreamRequest_WithNoPathNoQuery(t *testing.T) {
	srcBytes := []byte("hello world")
	functionPath := "/"

	requestPath := fmt.Sprintf("/function/xyz%s", functionPath)

	reader := bytes.NewReader(srcBytes)
	request, _ := http.NewRequest(http.MethodPost, requestPath, reader)
	request.Header.Set("X-Source", "unit-test")

	queryWant := ""
	if request.URL.RawQuery != queryWant {

		t.Errorf("Query - want: %s, got: %s", queryWant, request.URL.RawQuery)
		t.Fail()
	}

	transformer := middleware.FunctionPrefixTrimmingURLPathTransformer{}
	transformedPath := transformer.Transform(request)

	wantTransformedPath := "/"
	if transformedPath != wantTransformedPath {
		t.Errorf("transformedPath want: %s, got %s", wantTransformedPath, transformedPath)
	}

	upstream := buildUpstreamRequest(request, "http://xyz:8080", transformedPath)

	if request.Method != upstream.Method {
		t.Errorf("Method - want: %s, got: %s", request.Method, upstream.Method)
		t.Fail()
	}

	upstreamBytes, _ := io.ReadAll(upstream.Body)

	if string(upstreamBytes) != string(srcBytes) {
		t.Errorf("Body - want: %s, got: %s", string(upstreamBytes), string(srcBytes))
		t.Fail()
	}

	if request.Header.Get("X-Source") != upstream.Header.Get("X-Source") {
		t.Errorf("Header X-Source - want: %s, got: %s", request.Header.Get("X-Source"), upstream.Header.Get("X-Source"))
		t.Fail()
	}

	if request.URL.RawQuery != upstream.URL.RawQuery {
		t.Errorf("URL.RawQuery - want: %s, got: %s", request.URL.RawQuery, upstream.URL.RawQuery)
		t.Fail()
	}

	if functionPath != upstream.URL.Path {
		t.Errorf("URL.Path - want: %s, got: %s", functionPath, upstream.URL.Path)
		t.Fail()
	}

}

func Test_buildUpstreamRequest_WithPathAndQuery(t *testing.T) {
	srcBytes := []byte("hello world")
	functionPath := "/employee/info/300"

	requestPath := fmt.Sprintf("/function/xyz%s?code=1", functionPath)

	reader := bytes.NewReader(srcBytes)
	request, _ := http.NewRequest(http.MethodPost, requestPath, reader)
	request.Header.Set("X-Source", "unit-test")

	if request.URL.RawQuery != "code=1" {
		t.Errorf("Query - want: %s, got: %s", "code=1", request.URL.RawQuery)
		t.Fail()
	}

	transformer := middleware.FunctionPrefixTrimmingURLPathTransformer{}
	transformedPath := transformer.Transform(request)

	wantTransformedPath := functionPath
	if transformedPath != wantTransformedPath {
		t.Errorf("transformedPath want: %s, got %s", wantTransformedPath, transformedPath)
	}

	upstream := buildUpstreamRequest(request, "http://xyz:8080", transformedPath)

	if request.Method != upstream.Method {
		t.Errorf("Method - want: %s, got: %s", request.Method, upstream.Method)
		t.Fail()
	}

	upstreamBytes, _ := io.ReadAll(upstream.Body)

	if string(upstreamBytes) != string(srcBytes) {
		t.Errorf("Body - want: %s, got: %s", string(upstreamBytes), string(srcBytes))
		t.Fail()
	}

	if request.Header.Get("X-Source") != upstream.Header.Get("X-Source") {
		t.Errorf("Header X-Source - want: %s, got: %s", request.Header.Get("X-Source"), upstream.Header.Get("X-Source"))
		t.Fail()
	}

	if request.URL.RawQuery != upstream.URL.RawQuery {
		t.Errorf("URL.RawQuery - want: %s, got: %s", request.URL.RawQuery, upstream.URL.RawQuery)
		t.Fail()
	}

	if functionPath != upstream.URL.Path {
		t.Errorf("URL.Path - want: %s, got: %s", functionPath, upstream.URL.Path)
		t.Fail()
	}

}

func Test_deleteHeaders(t *testing.T) {
	h := http.Header{}
	target := "X-Dont-Forward"
	h.Add(target, "value1")
	h.Add("X-Keep-This", "value2")

	deleteHeaders(&h, &[]string{target})

	if h.Get(target) == "value1" {
		t.Errorf("want %s to be removed from headers", target)
		t.Fail()
	}

	if h.Get("X-Keep-This") != "value2" {
		t.Errorf("want %s to remain in headers", "X-Keep-This")
		t.Fail()
	}
}
