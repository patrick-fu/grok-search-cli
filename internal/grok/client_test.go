package grok

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

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

func TestSearch_RejectsNonWebWithoutNetwork(t *testing.T) {
	rt := &fakeRoundTripper{status: 200, header: http.Header{"Content-Type": {"text/event-stream"}}}
	c := NewClient("tok", "1.0", rt)
	_, err := c.Search(context.Background(), search.Request{Type: search.TypeX, Query: "test"})
	if err == nil {
		t.Fatal("expected error for non-web")
	}
	if err.Code != search.ErrorInternal {
		t.Fatalf("expected internal_error, got %s", err.Code)
	}
	if atomic.LoadInt32(&rt.called) != 0 {
		t.Fatal("non-web must not make network request")
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

func TestSearch_Non200ReturnsInternalError(t *testing.T) {
	rt := &fakeRoundTripper{status: 500}
	c := NewClient("tok", "1.0", rt)
	_, err := c.Search(context.Background(), search.Request{Type: search.TypeWeb, Query: "q"})
	if err == nil {
		t.Fatal("expected error for 500")
	}
	if err.Code != search.ErrorInternal {
		t.Errorf("code = %s, want internal_error", err.Code)
	}
}

func TestSearch_WrongContentTypeReturnsInternalError(t *testing.T) {
	rt := &fakeRoundTripper{
		status: 200,
		header: http.Header{"Content-Type": {"application/json"}},
		text:   "{}",
	}
	c := NewClient("tok", "1.0", rt)
	_, err := c.Search(context.Background(), search.Request{Type: search.TypeWeb, Query: "q"})
	if err == nil {
		t.Fatal("expected error for wrong content-type")
	}
	if err.Code != search.ErrorInternal {
		t.Errorf("code = %s, want internal_error", err.Code)
	}
}

func TestSearch_TransportErrorReturnsInternalError(t *testing.T) {
	rt := &errRoundTripper{}
	c := NewClient("tok", "1.0", rt)
	_, err := c.Search(context.Background(), search.Request{Type: search.TypeWeb, Query: "q"})
	if err == nil {
		t.Fatal("expected error for transport failure")
	}
	if err.Code != search.ErrorInternal {
		t.Errorf("code = %s, want internal_error", err.Code)
	}
}

type errRoundTripper struct{}

func (e *errRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, io.ErrUnexpectedEOF
}
