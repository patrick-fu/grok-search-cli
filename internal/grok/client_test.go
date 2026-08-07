package grok

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/patrick-fu/grok-search-cli/internal/search"
)

// fakeRoundTripper captures the request and returns a canned response.
type fakeRoundTripper struct {
	req    *http.Request
	body   []byte
	status int
	header http.Header
	text   string // SSE body when status == 200
	called int32
}

func (f *fakeRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	atomic.AddInt32(&f.called, 1)
	f.req = req
	if req.Body != nil {
		f.body, _ = io.ReadAll(req.Body)
	}
	h := f.header
	if h == nil {
		h = http.Header{}
	}
	return &http.Response{
		StatusCode: f.status,
		Header:     h,
		Body:       io.NopCloser(strings.NewReader(f.text)),
	}, nil
}

// validSSE returns an SSE stream with web_search_call.completed and response.completed.
func validSSE(answer string) string {
	return "data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"web_search_call\",\"status\":\"completed\"}}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"model\":\"grok-4.5\",\"output\":[{\"content\":[{\"type\":\"output_text\",\"text\":\"" + answer + "\",\"annotations\":[{\"type\":\"url_citation\",\"url\":\"https://example.com\",\"title\":\"Ex\"}]}]}],\"citations\":[\"https://example.com\"]}}\n\n" +
		"data: [DONE]\n\n"
}

// validSSEX returns an SSE stream with a completed x_search custom tool call and response.completed.
func validSSEX(answer string) string {
	return "data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"custom_tool_call\",\"name\":\"x_search\",\"status\":\"completed\"}}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"model\":\"grok-4.5\",\"output\":[{\"content\":[{\"type\":\"output_text\",\"text\":\"" + answer + "\"}]}]}}\n\n" +
		"data: [DONE]\n\n"
}

func TestSearch_RejectsUnsupportedTypeWithoutNetwork(t *testing.T) {
	rt := &fakeRoundTripper{status: 200, header: http.Header{"Content-Type": {"text/event-stream"}}}
	c := NewClient("tok", "1.0", rt)
	_, err := c.Search(context.Background(), search.Request{Type: search.Type("unknown"), Query: "test"})
	if err == nil {
		t.Fatal("expected error for unsupported type")
	}
	if err.Code != search.ErrorInternal {
		t.Fatalf("expected internal_error, got %s", err.Code)
	}
	if atomic.LoadInt32(&rt.called) != 0 {
		t.Fatal("unsupported type must not make network request")
	}
}

func TestSearch_ExactRequestForX(t *testing.T) {
	rt := &fakeRoundTripper{
		status: 200,
		header: http.Header{"Content-Type": {"text/event-stream"}},
		text:   validSSEX("answer"),
	}
	c := NewClient("mytoken", "0.1.0", rt)
	resp, err := c.Search(context.Background(), search.Request{Type: search.TypeX, Query: "hello world", Model: "grok-4.5"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Answer != "answer" {
		t.Fatalf("expected answer 'answer', got %q", resp.Answer)
	}
	if resp.Type != search.TypeX {
		t.Fatalf("response type = %q, want x", resp.Type)
	}

	// Verify exact method and URL
	if rt.req.Method != http.MethodPost {
		t.Fatalf("method = %s, want POST", rt.req.Method)
	}
	if rt.req.URL.String() != endpointURL {
		t.Fatalf("url = %s, want %s", rt.req.URL, endpointURL)
	}

	// Headers must be identical to the web profile
	expectHeaders := map[string]string{
		"Authorization":            "Bearer mytoken",
		"Accept":                   "text/event-stream",
		"Content-Type":             "application/json",
		"X-Grok-Client-Mode":       "headless",
		"X-Grok-Client-Identifier": "grok-search-cli",
		"X-Grok-Client-Version":    "0.2.117+grok-search-cli.0.1.0",
		"User-Agent":               "grok-search-cli/0.1.0",
	}
	h := rt.req.Header
	for k, want := range expectHeaders {
		got := h.Get(k)
		if got != want {
			t.Errorf("header %s = %q, want %q", k, got, want)
		}
	}
	allowedTransport := map[string]bool{"Host": true, "Content-Length": true}
	for k := range h {
		if _, ok := expectHeaders[k]; !ok {
			if allowedTransport[k] {
				continue
			}
			t.Errorf("unexpected header: %s = %q", k, h.Get(k))
		}
	}

	// Body shape: identical to web except the single tool is x_search
	var body requestBody
	if err := json.Unmarshal(rt.body, &body); err != nil {
		t.Fatalf("body unmarshal: %v", err)
	}
	if body.Model != "grok-4.5" {
		t.Errorf("model = %s, want grok-4.5", body.Model)
	}
	if body.Input != "hello world" {
		t.Errorf("input = %s, want hello world", body.Input)
	}
	if body.ToolChoice != "required" {
		t.Errorf("tool_choice = %s, want required", body.ToolChoice)
	}
	if !body.Stream {
		t.Error("stream should be true")
	}
	if body.Store {
		t.Error("store should be false")
	}
	if body.MaxOutputTokens != 1024 {
		t.Errorf("max_output_tokens = %d, want 1024", body.MaxOutputTokens)
	}
	if body.Reasoning.Effort != "low" {
		t.Errorf("reasoning.effort = %s, want low", body.Reasoning.Effort)
	}
	if len(body.Tools) != 1 || body.Tools[0].Type != "x_search" {
		t.Errorf("tools = %v, want [{x_search}]", body.Tools)
	}
}

func TestSearch_ExactRequestForWeb(t *testing.T) {
	rt := &fakeRoundTripper{
		status: 200,
		header: http.Header{"Content-Type": {"text/event-stream"}},
		text:   validSSE("answer"),
	}
	c := NewClient("mytoken", "0.1.0", rt)
	resp, err := c.Search(context.Background(), search.Request{Type: search.TypeWeb, Query: "hello world", Model: "grok-4.5"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Answer != "answer" {
		t.Fatalf("expected answer 'answer', got %q", resp.Answer)
	}

	// Verify exact method and URL
	if rt.req.Method != http.MethodPost {
		t.Fatalf("method = %s, want POST", rt.req.Method)
	}
	if rt.req.URL.String() != endpointURL {
		t.Fatalf("url = %s, want %s", rt.req.URL, endpointURL)
	}

	// Verify exact 7 application headers (canonical form)
	expectHeaders := map[string]string{
		"Authorization":            "Bearer mytoken",
		"Accept":                   "text/event-stream",
		"Content-Type":             "application/json",
		"X-Grok-Client-Mode":       "headless",
		"X-Grok-Client-Identifier": "grok-search-cli",
		"X-Grok-Client-Version":    "0.2.117+grok-search-cli.0.1.0",
		"User-Agent":               "grok-search-cli/0.1.0",
	}
	h := rt.req.Header
	for k, want := range expectHeaders {
		got := h.Get(k)
		if got != want {
			t.Errorf("header %s = %q, want %q", k, got, want)
		}
	}

	// Verify no unexpected application headers
	allowedTransport := map[string]bool{"Host": true, "Content-Length": true}
	for k := range h {
		if _, ok := expectHeaders[k]; !ok {
			if allowedTransport[k] {
				continue
			}
			t.Errorf("unexpected header: %s = %q", k, h.Get(k))
		}
	}

	// Verify body shape
	var body requestBody
	if err := json.Unmarshal(rt.body, &body); err != nil {
		t.Fatalf("body unmarshal: %v", err)
	}
	if body.Model != "grok-4.5" {
		t.Errorf("model = %s, want grok-4.5", body.Model)
	}
	if body.Input != "hello world" {
		t.Errorf("input = %s, want hello world", body.Input)
	}
	if body.ToolChoice != "required" {
		t.Errorf("tool_choice = %s, want required", body.ToolChoice)
	}
	if !body.Stream {
		t.Error("stream should be true")
	}
	if body.Store {
		t.Error("store should be false")
	}
	if body.MaxOutputTokens != 1024 {
		t.Errorf("max_output_tokens = %d, want 1024", body.MaxOutputTokens)
	}
	if body.Reasoning.Effort != "low" {
		t.Errorf("reasoning.effort = %s, want low", body.Reasoning.Effort)
	}
	if len(body.Tools) != 1 || body.Tools[0].Type != "web_search" {
		t.Errorf("tools = %v, want [{web_search}]", body.Tools)
	}
}

func TestSearch_FilterToolShapesAndOmission(t *testing.T) {
	tests := []struct {
		name    string
		request search.Request
		check   func(t *testing.T, tool searchTool)
	}{
		{
			name:    "web allowed domains",
			request: search.Request{Type: search.TypeWeb, Query: "q", WebFilters: &search.WebFilters{AllowedDomains: []string{"example.com", "docs.example.com"}}},
			check: func(t *testing.T, tool searchTool) {
				if tool.Filters == nil || !reflect.DeepEqual(tool.Filters.AllowedDomains, []string{"example.com", "docs.example.com"}) || len(tool.Filters.ExcludedDomains) != 0 {
					t.Fatalf("tool = %#v", tool)
				}
			},
		},
		{
			name:    "web excluded domains",
			request: search.Request{Type: search.TypeWeb, Query: "q", WebFilters: &search.WebFilters{ExcludedDomains: []string{"example.com"}}},
			check: func(t *testing.T, tool searchTool) {
				if tool.Filters == nil || !reflect.DeepEqual(tool.Filters.ExcludedDomains, []string{"example.com"}) || len(tool.Filters.AllowedDomains) != 0 {
					t.Fatalf("tool = %#v", tool)
				}
			},
		},
		{
			name:    "x direct fields",
			request: search.Request{Type: search.TypeX, Query: "q", XFilters: &search.XFilters{AllowedHandles: []string{"one"}, FromDate: "2026-02-03", ToDate: "2026-02-04"}},
			check: func(t *testing.T, tool searchTool) {
				if tool.Filters != nil || !reflect.DeepEqual(tool.AllowedXHandles, []string{"one"}) || len(tool.ExcludedXHandles) != 0 || tool.FromDate != "2026-02-03" || tool.ToDate != "2026-02-04" {
					t.Fatalf("tool = %#v", tool)
				}
			},
		},
		{
			name:    "unused filters omitted",
			request: search.Request{Type: search.TypeX, Query: "q"},
			check: func(t *testing.T, tool searchTool) {
				if tool.Filters != nil || len(tool.AllowedXHandles) != 0 || len(tool.ExcludedXHandles) != 0 || tool.FromDate != "" || tool.ToDate != "" {
					t.Fatalf("tool = %#v", tool)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			text := validSSE("ok")
			if test.request.Type == search.TypeX {
				text = validSSEX("ok")
			}
			rt := &fakeRoundTripper{status: http.StatusOK, header: http.Header{"Content-Type": {"text/event-stream"}}, text: text}
			_, err := NewClient("tok", "1.0", rt).Search(context.Background(), test.request)
			if err != nil {
				t.Fatal(err)
			}
			var body requestBody
			if err := json.Unmarshal(rt.body, &body); err != nil {
				t.Fatal(err)
			}
			if len(body.Tools) != 1 {
				t.Fatalf("tools = %#v", body.Tools)
			}
			test.check(t, body.Tools[0])
		})
	}
}

func TestSearch_FilterRejectedWithoutFallbackOrRetry(t *testing.T) {
	rt := &scriptedRoundTripper{results: []roundTripResult{
		{status: http.StatusBadRequest, header: http.Header{}},
		{status: http.StatusOK, header: http.Header{"Content-Type": {"text/event-stream"}}, text: validSSE("must not run")},
	}}
	c := NewClient("tok", "1.0", rt).(*client)
	c.waitRetry = func(context.Context, time.Duration) bool {
		t.Fatal("provider rejection must not retry")
		return false
	}
	_, err := c.Search(context.Background(), search.Request{
		Type: search.TypeWeb, Query: "q", WebFilters: &search.WebFilters{AllowedDomains: []string{"example.com"}},
	})
	if err == nil || err.Code != search.ErrorRequestRejected || len(rt.bodies) != 1 {
		t.Fatalf("error=%#v attempts=%d", err, len(rt.bodies))
	}
	var body requestBody
	if err := json.Unmarshal(rt.bodies[0], &body); err != nil || body.Tools[0].Filters == nil || !reflect.DeepEqual(body.Tools[0].Filters.AllowedDomains, []string{"example.com"}) {
		t.Fatalf("body=%q decoded=%#v err=%v", rt.bodies[0], body, err)
	}
}

func TestSearch_DefaultModel(t *testing.T) {
	rt := &fakeRoundTripper{
		status: 200,
		header: http.Header{"Content-Type": {"text/event-stream"}},
		text:   validSSE("ok"),
	}
	c := NewClient("tok", "1.0", rt)
	_, err := c.Search(context.Background(), search.Request{Type: search.TypeWeb, Query: "q"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var body requestBody
	json.Unmarshal(rt.body, &body)
	if body.Model != "grok-4.5" {
		t.Errorf("model = %s, want default grok-4.5", body.Model)
	}
}

func TestSearch_AcceptsAnySuccessfulSSEStatus(t *testing.T) {
	rt := &fakeRoundTripper{
		status: http.StatusCreated,
		header: http.Header{"Content-Type": {"text/event-stream"}},
		text:   validSSE("ok"),
	}
	c := NewClient("tok", "1.0", rt)
	response, err := c.Search(context.Background(), search.Request{Type: search.TypeWeb, Query: "q"})
	if err != nil || response.Answer != "ok" {
		t.Fatalf("Search() response=%#v error=%v", response, err)
	}
}

func TestSearch_Non200MapsProviderUnavailable(t *testing.T) {
	rt := &fakeRoundTripper{status: 500}
	c := NewClient("tok", "1.0", rt)
	_, err := c.Search(context.Background(), search.Request{Type: search.TypeWeb, Query: "q"})
	if err == nil {
		t.Fatal("expected error for 500")
	}
	if err.Code != search.ErrorProviderUnavailable {
		t.Errorf("code = %s, want provider_unavailable", err.Code)
	}
}

func TestSearch_404CatalogDiagnosesOnlyExplicitModelOmission(t *testing.T) {
	tests := []struct {
		name          string
		requestType   search.Type
		catalogStatus int
		catalogBody   string
		wantCode      search.ErrorCode
	}{
		{
			name:          "web omission",
			requestType:   search.TypeWeb,
			catalogStatus: http.StatusOK,
			catalogBody:   `{"data":[{"id":"another-model"}]}`,
			wantCode:      search.ErrorUnsupportedModel,
		},
		{
			name:          "x omission",
			requestType:   search.TypeX,
			catalogStatus: http.StatusOK,
			catalogBody:   `{"data":[{"id":"another-model"}]}`,
			wantCode:      search.ErrorUnsupportedModel,
		},
		{
			name:          "exact presence",
			requestType:   search.TypeWeb,
			catalogStatus: http.StatusOK,
			catalogBody:   `{"data":[{"id":"future-model"}]}`,
			wantCode:      search.ErrorProvider,
		},
		{
			name:          "malformed",
			requestType:   search.TypeWeb,
			catalogStatus: http.StatusOK,
			catalogBody:   `{`,
			wantCode:      search.ErrorProvider,
		},
		{
			name:          "empty",
			requestType:   search.TypeWeb,
			catalogStatus: http.StatusOK,
			catalogBody:   `{"data":[]}`,
			wantCode:      search.ErrorProvider,
		},
		{
			name:          "non 2xx",
			requestType:   search.TypeWeb,
			catalogStatus: http.StatusServiceUnavailable,
			catalogBody:   `provider secret`,
			wantCode:      search.ErrorProvider,
		},
		{
			name:          "over limit",
			requestType:   search.TypeWeb,
			catalogStatus: http.StatusOK,
			catalogBody:   strings.Repeat("x", catalogLimit+1),
			wantCode:      search.ErrorProvider,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rt := &scriptedRoundTripper{results: []roundTripResult{
				{status: http.StatusNotFound, header: http.Header{"X-Request-Id": {"safe-404"}}},
				{status: test.catalogStatus, header: http.Header{"Content-Type": {"application/json"}}, text: test.catalogBody},
			}}
			_, err := NewClient("tok", "1.0", rt).Search(context.Background(), search.Request{
				Type: test.requestType, Query: "q", Model: "future-model",
			})
			if err == nil || err.Code != test.wantCode {
				t.Fatalf("error = %#v, want %s", err, test.wantCode)
			}
			if err.HTTPStatus != http.StatusNotFound || err.RequestID != "safe-404" {
				t.Fatalf("metadata = %#v, want original 404 metadata", err)
			}
			if len(rt.methods) != 2 || rt.methods[0] != http.MethodPost || rt.methods[1] != http.MethodGet {
				t.Fatalf("methods = %v, want POST then GET", rt.methods)
			}
			if rt.urls[0] != endpointURL || rt.urls[1] != modelsEndpointURL {
				t.Fatalf("routes = %v", rt.urls)
			}
			var body requestBody
			if err := json.Unmarshal(rt.bodies[0], &body); err != nil || body.Model != "future-model" {
				t.Fatalf("inference body=%q model=%q err=%v", rt.bodies[0], body.Model, err)
			}
			assertCatalogHeaders(t, rt.headers[1])
		})
	}
}

func TestSearch_NeverPreflightsCatalog(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusBadRequest, http.StatusServiceUnavailable} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			rt := &scriptedRoundTripper{results: []roundTripResult{{
				status: status, header: http.Header{"Content-Type": {"text/event-stream"}}, text: validSSE("ok"),
			}}}
			_, _ = NewClient("tok", "1.0", rt).Search(context.Background(), search.Request{Type: search.TypeWeb, Query: "q", Model: "future-model"})
			if len(rt.methods) != 1 || rt.urls[0] != endpointURL {
				t.Fatalf("unexpected catalog request: methods=%v routes=%v", rt.methods, rt.urls)
			}
		})
	}
}

func assertCatalogHeaders(t *testing.T, headers http.Header) {
	t.Helper()
	want := map[string]string{
		"Authorization":            "Bearer tok",
		"Accept":                   "application/json",
		"X-Grok-Client-Mode":       "headless",
		"X-Grok-Client-Identifier": "grok-search-cli",
		"X-Grok-Client-Version":    "0.2.117+grok-search-cli.1.0",
		"User-Agent":               "grok-search-cli/1.0",
	}
	for name, value := range want {
		if got := headers.Get(name); got != value {
			t.Errorf("header %s = %q, want %q", name, got, value)
		}
	}
	if got := headers.Get("Content-Type"); got != "" {
		t.Errorf("Content-Type = %q, want absent", got)
	}
}

func TestSearch_WrongContentTypeReturnsInvalidProviderResponse(t *testing.T) {
	rt := &fakeRoundTripper{
		status: 200,
		header: http.Header{
			"Content-Type": {"application/json"},
			"X-Request-Id": {"safe-request-id"},
		},
		text: "{}",
	}
	c := NewClient("tok", "1.0", rt)
	_, err := c.Search(context.Background(), search.Request{Type: search.TypeWeb, Query: "q"})
	if err == nil {
		t.Fatal("expected error for wrong content-type")
	}
	if err.Code != search.ErrorInvalidProviderResponse {
		t.Errorf("code = %s, want invalid_provider_response", err.Code)
	}
	if err.HTTPStatus != http.StatusOK || err.RequestID != "safe-request-id" {
		t.Errorf("metadata = %#v, want trusted status and request ID", err)
	}
}

func TestSearch_TransportErrorReturnsTransportError(t *testing.T) {
	rt := &errRoundTripper{}
	c := NewClient("tok", "1.0", rt).(*client)
	c.waitRetry = func(context.Context, time.Duration) bool { return true }
	_, err := c.Search(context.Background(), search.Request{Type: search.TypeWeb, Query: "q"})
	if err == nil {
		t.Fatal("expected error for transport failure")
	}
	if err.Code != search.ErrorTransport {
		t.Errorf("code = %s, want transport_error", err.Code)
	}
}

func TestSearch_UsesRequestedMaxOutputTokens(t *testing.T) {
	rt := &fakeRoundTripper{
		status: 200,
		header: http.Header{"Content-Type": {"text/event-stream"}},
		text:   validSSE("ok"),
	}
	_, err := NewClient("tok", "1.0", rt).Search(context.Background(), search.Request{
		Type: search.TypeWeb, Query: "q", MaxOutputTokens: 256,
	})
	if err != nil {
		t.Fatal(err)
	}
	var body requestBody
	if err := json.Unmarshal(rt.body, &body); err != nil {
		t.Fatal(err)
	}
	if body.MaxOutputTokens != 256 {
		t.Errorf("max_output_tokens = %d, want 256", body.MaxOutputTokens)
	}
}

func TestSearch_RetriesOnePreResponseTransportFailureWithExactRequest(t *testing.T) {
	rt := &scriptedRoundTripper{results: []roundTripResult{
		{err: errors.New("connection refused")},
		{status: http.StatusOK, header: http.Header{"Content-Type": {"text/event-stream"}}, text: validSSEX("ok")},
	}}
	c := NewClient("tok", "1.0", rt).(*client)
	var waited time.Duration
	c.waitRetry = func(_ context.Context, delay time.Duration) bool {
		waited = delay
		return true
	}
	response, err := c.Search(context.Background(), search.Request{
		Type: search.TypeX, Query: "same query", Model: "future-model", MaxOutputTokens: 256,
	})
	if err != nil || response.Answer != "ok" {
		t.Fatalf("Search() response=%#v error=%v", response, err)
	}
	if waited != retryDelay || len(rt.bodies) != 2 || len(rt.headers) != 2 {
		t.Fatalf("retry state waited=%s attempts=%d", waited, len(rt.bodies))
	}
	if !reflect.DeepEqual(rt.bodies[0], rt.bodies[1]) || !reflect.DeepEqual(rt.headers[0], rt.headers[1]) {
		t.Fatal("retry changed request body or headers")
	}
	var body requestBody
	if err := json.Unmarshal(rt.bodies[0], &body); err != nil {
		t.Fatal(err)
	}
	if body.Model != "future-model" || body.MaxOutputTokens != 256 || len(body.Tools) != 1 || body.Tools[0].Type != "x_search" {
		t.Fatalf("retry body = %#v", body)
	}
}

func TestSearch_NeverRetriesStatusCancellationOrPartialStream(t *testing.T) {
	tests := []struct {
		name    string
		results []roundTripResult
		want    search.ErrorCode
	}{
		{
			name: "HTTP status",
			results: []roundTripResult{
				{status: http.StatusServiceUnavailable, header: http.Header{}},
				{status: http.StatusOK, header: http.Header{"Content-Type": {"text/event-stream"}}, text: validSSE("must not run")},
			},
			want: search.ErrorProviderUnavailable,
		},
		{
			name: "deadline",
			results: []roundTripResult{
				{err: context.DeadlineExceeded},
				{status: http.StatusOK, header: http.Header{"Content-Type": {"text/event-stream"}}, text: validSSE("must not run")},
			},
			want: search.ErrorRequestTimeout,
		},
		{
			name: "cancellation",
			results: []roundTripResult{
				{err: context.Canceled},
				{status: http.StatusOK, header: http.Header{"Content-Type": {"text/event-stream"}}, text: validSSE("must not run")},
			},
			want: search.ErrorTransport,
		},
		{
			name: "partial stream",
			results: []roundTripResult{
				{status: http.StatusOK, header: http.Header{"Content-Type": {"text/event-stream"}}, reader: &prefixErrorReadCloser{prefix: []byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}"), err: io.ErrUnexpectedEOF}},
				{status: http.StatusOK, header: http.Header{"Content-Type": {"text/event-stream"}}, text: validSSE("must not run")},
			},
			want: search.ErrorTransport,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rt := &scriptedRoundTripper{results: test.results}
			c := NewClient("tok", "1.0", rt).(*client)
			c.waitRetry = func(context.Context, time.Duration) bool {
				t.Fatal("unexpected retry")
				return false
			}
			_, err := c.Search(context.Background(), search.Request{Type: search.TypeWeb, Query: "q"})
			if err == nil || err.Code != test.want {
				t.Fatalf("error = %#v, want %s", err, test.want)
			}
			if len(rt.bodies) != 1 {
				t.Fatalf("attempts = %d, want 1", len(rt.bodies))
			}
		})
	}
}

func TestSearch_HTTPStatusMappingAndMetadata(t *testing.T) {
	cases := []struct {
		status int
		code   search.ErrorCode
	}{
		{http.StatusUnauthorized, search.ErrorAuthRejected},
		{http.StatusForbidden, search.ErrorPermissionDenied},
		{http.StatusTooManyRequests, search.ErrorRateLimited},
		{http.StatusBadRequest, search.ErrorRequestRejected},
		{http.StatusConflict, search.ErrorRequestRejected},
		{http.StatusUnprocessableEntity, search.ErrorRequestRejected},
		{http.StatusRequestEntityTooLarge, search.ErrorRequestTooLarge},
		{http.StatusUpgradeRequired, search.ErrorClientIncompatible},
		{http.StatusRequestTimeout, search.ErrorRequestTimeout},
		{http.StatusNotFound, search.ErrorProvider},
		{http.StatusInternalServerError, search.ErrorProviderUnavailable},
		{http.StatusBadGateway, search.ErrorProviderUnavailable},
		{http.StatusTeapot, search.ErrorProvider},
	}
	for _, tc := range cases {
		t.Run(strconv.Itoa(tc.status), func(t *testing.T) {
			body := "provider secret body"
			header := http.Header{"Content-Type": {"text/plain"}, "X-REQUEST-ID": {"safe.id-1"}}
			if tc.status == http.StatusTooManyRequests {
				header.Set("Retry-After", "4001")
			}
			rt := &fakeRoundTripper{status: tc.status, header: header, text: body}
			_, err := NewClient("tok", "1.0", rt).Search(context.Background(), search.Request{Type: search.TypeWeb, Query: "q"})
			if err == nil || err.Code != tc.code {
				t.Fatalf("error = %#v, want %s", err, tc.code)
			}
			if err.HTTPStatus != tc.status {
				t.Errorf("http_status = %d, want %d", err.HTTPStatus, tc.status)
			}
			if err.RequestID != "safe.id-1" {
				t.Errorf("request_id = %q, want safe.id-1", err.RequestID)
			}
			if strings.Contains(err.Message, body) {
				t.Error("provider body leaked into client-owned error message")
			}
			if tc.status == http.StatusTooManyRequests {
				if err.RetryAfterSeconds == nil || *err.RetryAfterSeconds != 3600 {
					t.Errorf("retry_after_seconds = %v, want 3600", err.RetryAfterSeconds)
				}
			}
		})
	}
}

func TestSearch_RequestIDAndRetryAfterSanitized(t *testing.T) {
	longID := strings.Repeat("a", 129)
	for _, id := range []string{"bad id", "中文", longID} {
		rt := &fakeRoundTripper{status: http.StatusTooManyRequests, header: http.Header{
			"X-Request-ID": {id}, "Retry-After": {"not-an-integer"},
		}}
		_, err := NewClient("tok", "1.0", rt).Search(context.Background(), search.Request{Type: search.TypeWeb, Query: "q"})
		if err == nil || err.RequestID != "" || err.RetryAfterSeconds != nil {
			t.Fatalf("unsafe metadata retained: %#v", err)
		}
	}
}

type errRoundTripper struct{}

func (e *errRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, io.ErrUnexpectedEOF
}

type roundTripResult struct {
	status int
	header http.Header
	text   string
	reader io.ReadCloser
	err    error
}

type scriptedRoundTripper struct {
	results []roundTripResult
	bodies  [][]byte
	headers []http.Header
	methods []string
	urls    []string
}

func (transport *scriptedRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	var body []byte
	if req.Body != nil {
		body, _ = io.ReadAll(req.Body)
	}
	transport.bodies = append(transport.bodies, body)
	transport.headers = append(transport.headers, req.Header.Clone())
	transport.methods = append(transport.methods, req.Method)
	transport.urls = append(transport.urls, req.URL.String())
	result := transport.results[len(transport.bodies)-1]
	if result.err != nil {
		return nil, result.err
	}
	reader := result.reader
	if reader == nil {
		reader = io.NopCloser(strings.NewReader(result.text))
	}
	return &http.Response{StatusCode: result.status, Header: result.header, Body: reader}, nil
}

type prefixErrorReadCloser struct {
	prefix []byte
	err    error
}

func (reader *prefixErrorReadCloser) Read(buffer []byte) (int, error) {
	if len(reader.prefix) == 0 {
		return 0, reader.err
	}
	count := copy(buffer, reader.prefix)
	reader.prefix = reader.prefix[count:]
	return count, reader.err
}

func (reader *prefixErrorReadCloser) Close() error { return nil }
