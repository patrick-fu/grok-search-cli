package grok

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/patrick-fu/grok-search-cli/internal/search"
)

const stableVersionURL = "https://x.ai/cli/stable"

type discoveryTransport struct {
	t             *testing.T
	version       string
	catalog       string
	versionStatus int
	versionHeader string
	urls          []string
	model         string
}

func (rt *discoveryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	rt.urls = append(rt.urls, req.URL.String())
	status, contentType, body := http.StatusOK, "application/json", ""
	switch req.URL.String() {
	case stableVersionURL:
		if req.Header.Get("Authorization") != "" {
			rt.t.Fatal("the public version request received a credential")
		}
		body = rt.version
		if rt.versionStatus != 0 {
			status = rt.versionStatus
		}
	case modelsEndpointURL:
		body = rt.catalog
	case endpointURL:
		wantVersion := rt.versionHeader
		if wantVersion == "" {
			wantVersion = "9.8.7"
		}
		if req.Header.Get("X-Grok-Client-Version") != wantVersion {
			status = http.StatusUpgradeRequired
			break
		}
		var value struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(req.Body).Decode(&value); err != nil {
			rt.t.Fatal(err)
		}
		rt.model = value.Model
		contentType, body = "text/event-stream", validSSE("answer")
	default:
		rt.t.Fatalf("unexpected URL %s", req.URL)
	}
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(body))}, nil
}

func TestDiscoveryFollowsProviderModelOrderAndFreshVersion(t *testing.T) {
	rt := &discoveryTransport{t: t, version: "9.8.7\n", catalog: `{"data":[
		{"id":"chat-only","model":"chat-only","api_backend":"responses","supports_backend_search":false},
		{"id":"other-backend","model":"other-backend","api_backend":"messages","supports_backend_search":true},
		{"id":"future-search","model":"future-search-wire","api_backend":"responses","supports_backend_search":true},
		{"id":"another-search","api_backend":"responses","supports_backend_search":true}
	]}`}
	response, err := NewClient("test-input", "dev", rt).Search(context.Background(), search.Request{Type: search.TypeWeb, Query: "q"})
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if rt.model != "future-search-wire" || response.Model != "future-search-wire" {
		t.Fatalf("sent=%q returned=%q, want provider's first eligible wire model", rt.model, response.Model)
	}
	if strings.Join(rt.urls, "\n") != strings.Join([]string{stableVersionURL, modelsEndpointURL, endpointURL}, "\n") {
		t.Fatalf("requests = %v", rt.urls)
	}
}

func TestDiscoveryExplicitModelSkipsCatalog(t *testing.T) {
	rt := &discoveryTransport{t: t, version: "9.8.7"}
	response, err := NewClient("test-input", "dev", rt).Search(context.Background(), search.Request{Type: search.TypeWeb, Query: "q", Model: "explicit-model"})
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if rt.model != "explicit-model" || response.Model != "explicit-model" || len(rt.urls) != 2 {
		t.Fatalf("model=%q returned=%q requests=%v", rt.model, response.Model, rt.urls)
	}
}

func TestDiscoveryFailsWithoutGuessingOrStartingInference(t *testing.T) {
	for _, test := range []struct {
		name          string
		version       string
		catalog       string
		versionStatus int
		code          search.ErrorCode
	}{
		{name: "invalid version", version: "not-a-version", code: search.ErrorInvalidProviderResponse},
		{name: "header injection", version: "9.8.7\r\nAuthorization: unexpected", code: search.ErrorInvalidProviderResponse},
		{name: "public auth failure", version: "9.8.7", versionStatus: 401, code: search.ErrorProvider},
		{name: "invalid catalog", version: "9.8.7", catalog: "not-json", code: search.ErrorInvalidProviderResponse},
		{name: "oversized version", version: strings.Repeat("1", 257), code: search.ErrorOutputTooLarge},
		{name: "oversized catalog", version: "9.8.7", catalog: strings.Repeat(" ", (1<<20)+1), code: search.ErrorOutputTooLarge},
		{name: "empty catalog", version: "9.8.7", catalog: `{"data":[]}`, code: search.ErrorCode("search_model_unavailable")},
		{name: "invalid wire ID", version: "9.8.7", catalog: `{"data":[{"id":"bad model","api_backend":"responses","supports_backend_search":true}]}`, code: search.ErrorCode("search_model_unavailable")},
		{name: "no search models", version: "9.8.7", catalog: `{"data":[{"id":"chat","api_backend":"responses"}]}`, code: search.ErrorCode("search_model_unavailable")},
	} {
		t.Run(test.name, func(t *testing.T) {
			rt := &discoveryTransport{t: t, version: test.version, catalog: test.catalog, versionStatus: test.versionStatus}
			_, err := NewClient("test-input", "dev", rt).Search(context.Background(), search.Request{Type: search.TypeWeb, Query: "q"})
			if err == nil || err.Code != test.code {
				t.Fatalf("error = %v, want %s", err, test.code)
			}
			for _, url := range rt.urls {
				if url == endpointURL {
					t.Fatal("discovery failure started inference")
				}
			}
		})
	}
}

func TestDiscoveryUsesVersionPointerVerbatimWithBuildMetadata(t *testing.T) {
	rt := &discoveryTransport{t: t, version: "9.8.7+provider", versionHeader: "9.8.7+provider"}
	_, err := NewClient("test-input", "dev", rt).Search(context.Background(), search.Request{Type: search.TypeWeb, Query: "q", Model: "explicit-model"})
	if err != nil {
		t.Fatal(err)
	}
}
