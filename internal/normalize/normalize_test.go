package normalize_test

import (
	"context"
	"strings"
	"testing"

	"github.com/patrick-fu/grok-search-cli/internal/normalize"
	"github.com/patrick-fu/grok-search-cli/internal/search"
)

func TestNormalizeWebUsesOnlyAnswerCitationsAndStableWarnings(t *testing.T) {
	body := strings.NewReader(`: provider keepalive

data: {"type":"response.output_item.done","item":{"type":"web_search_call","status":"completed","action":{"sources":[{"url":"https://candidate.example.test"}]}}}

data: {"type":"response.output_text.annotation.added","annotation":{"type":"url_citation","url":"javascript:alert(1)","title":"unsafe"}}

data: {"type":"response.output_text.annotation.added","annotation":{"type":"url_citation","url":" https://whitespace.example.test/ ","title":"not an absolute URL"}}

data: {"type":"response.completed","response":{"model":"grok-4.5-build","output":[{"content":[{"type":"output_text","text":"Answer.","annotations":[{"type":"url_citation","url":"https://docs.example.test/web","title":"[1]"},{"type":"url_citation","url":"https://docs.example.test/web","title":"Documentation"}]}]}]}}

data: [DONE]

`)

	response, searchErr := normalize.Normalize(context.Background(), search.Request{
		Type: search.TypeWeb, Query: "find docs", Model: "grok-4.5",
	}, body)
	if searchErr != nil {
		t.Fatalf("Normalize() error = %v", searchErr)
	}
	if response.SchemaVersion != search.SchemaVersion || response.Type != search.TypeWeb || response.Query != "find docs" || response.Model != "grok-4.5" || response.ProviderModel != "grok-4.5-build" || response.Answer != "Answer." {
		t.Fatalf("response = %#v", response)
	}
	if len(response.Sources) != 1 || response.Sources[0] != (search.Source{URL: "https://docs.example.test/web", Title: "Documentation"}) {
		t.Fatalf("sources = %#v", response.Sources)
	}
	wantWarnings := []search.Warning{search.WarningInvalidCitationsIgnored}
	if !sameWarnings(response.Warnings, wantWarnings) {
		t.Fatalf("warnings = %#v, want %#v", response.Warnings, wantWarnings)
	}
}

func TestNormalizeSupportsMultilineDataAndDeltaFallback(t *testing.T) {
	body := strings.NewReader(`data: {"type":"response.web_search_call.completed"}

data: {"type":"response.output_text.delta","delta":"first "}

data: {"type":"response.output_text.delta",
data: "delta":"second"}

data: {"type":"response.completed","response":{"model":"grok-4.5","output":[]}}

`)

	response, searchErr := normalize.Normalize(context.Background(), search.Request{Type: search.TypeWeb, Query: "q", Model: "grok-4.5"}, body)
	if searchErr != nil {
		t.Fatalf("Normalize() error = %v", searchErr)
	}
	if response.Answer != "first second" {
		t.Fatalf("answer = %q", response.Answer)
	}
	if !sameWarnings(response.Warnings, []search.Warning{search.WarningMissingCitations}) {
		t.Fatalf("warnings = %#v", response.Warnings)
	}
}

func TestNormalizeRejectsNonTerminalCompletion(t *testing.T) {
	body := strings.NewReader(`data: {"type":"response.web_search_call.completed"}

data: {"type":"response.completed","response":{"output":[{"content":[{"type":"output_text","text":"answer"}]}]}}

data: {"type":"response.output_text.delta","delta":"late"}

`)

	_, searchErr := normalize.Normalize(context.Background(), search.Request{Type: search.TypeWeb}, body)
	if searchErr == nil || searchErr.Code != search.ErrorInternal {
		t.Fatalf("error = %#v", searchErr)
	}
}

func TestNormalizeRejectsMissingMatchingTool(t *testing.T) {
	body := strings.NewReader(`data: {"type":"response.completed","response":{"output":[{"content":[{"type":"output_text","text":"answer"}]}]}}

`)

	_, searchErr := normalize.Normalize(context.Background(), search.Request{Type: search.TypeWeb}, body)
	if searchErr == nil || searchErr.Code != search.ErrorInternal {
		t.Fatalf("error = %#v", searchErr)
	}
}

func sameWarnings(got, want []search.Warning) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}
