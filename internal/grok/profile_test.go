package grok

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/patrick-fu/grok-search-cli/internal/search"
)

type profileTransport struct {
	t       *testing.T
	headers []http.Header
}

func (rt *profileTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	body, contentType := "9.8.7", "text/plain"
	if req.URL.String() == endpointURL {
		rt.headers = append(rt.headers, req.Header.Clone())
		for name, want := range map[string]string{
			"X-Grok-Client-Version":  "9.8.7",
			"X-XAI-Token-Auth":       "xai-grok-cli",
			"X-Authenticateresponse": "authenticate-response",
			"X-Grok-Model-Override":  "future-model",
			"X-Grok-Turn-Idx":        "1",
		} {
			if got := req.Header.Get(name); got != want {
				rt.t.Errorf("%s = %q, want %q", name, got, want)
			}
		}
		var value struct {
			Input []struct{ Type, Role, Content string }
		}
		if err := json.NewDecoder(req.Body).Decode(&value); err != nil || len(value.Input) != 1 || value.Input[0].Type != "message" || value.Input[0].Role != "user" || value.Input[0].Content != "preserved query\nwith a newline" {
			rt.t.Errorf("input must preserve the query as one user message: %v", err)
		}
		body, contentType = validSSE("answer"), "text/event-stream"
	} else if req.URL.String() != versionEndpointURL {
		rt.t.Fatalf("unexpected discovery request %s", req.URL)
	} else if req.Header.Get("Authorization") != "" || req.Header.Get("X-XAI-Token-Auth") != "" {
		rt.t.Error("public version lookup received an auth header")
	}
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(body))}, nil
}

func TestProfileHasFreshSearchIdentitiesAndReadonlyExistingAgentID(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GROK_HOME", home)
	cache := filepath.Join(home, "agent_id")
	agentID := "111fc242-925b-5a7d-826e-2974daad239f"
	if err := os.WriteFile(cache, []byte(agentID+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	before, _ := os.Stat(cache)
	rt := &profileTransport{t: t}
	c := NewClient("test-input", "dev", rt)
	for i := 0; i < 2; i++ {
		response, err := c.Search(context.Background(), search.Request{Type: search.TypeWeb, Model: "future-model", Query: "preserved query\nwith a newline"})
		if err != nil || response.Model != "future-model" {
			t.Fatalf("search: response=%v error=%v", response, err)
		}
	}
	if len(rt.headers) != 2 {
		t.Fatalf("inference requests = %d", len(rt.headers))
	}
	for _, headers := range rt.headers {
		assertUUIDVersion(t, headers.Get("X-Grok-Session-Id"), 7)
		assertUUIDVersion(t, headers.Get("X-Grok-Req-Id"), 4)
		assertUUIDVersion(t, headers.Get("X-Grok-Conv-Group-Id"), 5)
		if headers.Get("X-Grok-Conv-Id") != headers.Get("X-Grok-Session-Id") {
			t.Error("conversation must belong to this search session")
		}
		if headers.Get("X-Grok-Agent-Id") != agentID {
			t.Error("existing machine agent ID was not reused")
		}
	}
	for _, name := range []string{"X-Grok-Session-Id", "X-Grok-Req-Id", "X-Grok-Conv-Group-Id"} {
		if rt.headers[0].Get(name) == rt.headers[1].Get(name) {
			t.Errorf("%s leaked across searches", name)
		}
	}
	data, _ := os.ReadFile(cache)
	after, _ := os.Stat(cache)
	if string(data) != agentID+"\n" || !os.SameFile(before, after) || before.ModTime() != after.ModTime() || before.Mode() != after.Mode() {
		t.Error("agent ID cache was changed")
	}
}

func assertUUIDVersion(t *testing.T, value string, version byte) {
	t.Helper()
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		t.Fatalf("invalid UUID shape: %q", value)
	}
	b, err := hex.DecodeString(strings.ReplaceAll(value, "-", ""))
	if err != nil || len(b) != 16 || b[6]>>4 != version || b[8]>>6 != 2 {
		t.Errorf("invalid UUID version/variant: %q", value)
	}
}

func TestProfileMissingOrUnusableAgentCacheIsOptional(t *testing.T) {
	for _, value := range []string{"", "not-an-id", strings.Repeat("a", 65)} {
		t.Run(value[:min(len(value), 12)], func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("GROK_HOME", home)
			if value != "" {
				if err := os.WriteFile(filepath.Join(home, "agent_id"), []byte(value), 0600); err != nil {
					t.Fatal(err)
				}
			}
			rt := &profileTransport{t: t}
			_, err := NewClient("test-input", "dev", rt).Search(context.Background(), search.Request{Type: search.TypeWeb, Model: "future-model", Query: "preserved query\nwith a newline"})
			if err != nil || len(rt.headers) != 1 {
				t.Fatalf("search depends on an agent cache: %v", err)
			}
			if rt.headers[0].Get("X-Grok-Agent-Id") != "" {
				t.Error("unusable agent cache was sent")
			}
		})
	}
}
