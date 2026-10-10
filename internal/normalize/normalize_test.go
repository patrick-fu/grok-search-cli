package normalize_test

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/patrick-fu/grok-search-cli/internal/normalize"
	"github.com/patrick-fu/grok-search-cli/internal/search"
)

// --- success path tests ---

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

func TestNormalizeXToolEvidence(t *testing.T) {
	body := strings.NewReader(`data: {"type":"response.output_item.done","item":{"type":"custom_tool_call","status":"completed","name":"x_search"}}

data: {"type":"response.completed","response":{"output":[{"content":[{"type":"output_text","text":"X answer."}]}]}}

data: [DONE]

`)

	response, searchErr := normalize.Normalize(context.Background(), search.Request{Type: search.TypeX, Query: "q", Model: "m"}, body)
	if searchErr != nil {
		t.Fatalf("Normalize() error = %v", searchErr)
	}
	if response.Answer != "X answer." {
		t.Fatalf("answer = %q", response.Answer)
	}
}

func TestNormalizeXSuccessPreservesRequestAndNormalizesCitations(t *testing.T) {
	body := strings.NewReader(`data: {"type":"response.output_item.done","item":{"type":"custom_tool_call","status":"completed","name":"x_keyword_search"}}

data: {"type":"response.output_text.annotation.added","annotation":{"type":"url_citation","url":"https://x.example.test/i/status/111","title":"[1]"}}

data: {"type":"response.output_text.annotation.added","annotation":{"type":"url_citation","url":"https://x.example.test/i/status/111","title":"First public post"}}

data: {"type":"response.output_text.annotation.added","annotation":{"type":"url_citation","url":"https://x.example.test/i/status/222","title":"https://x.example.test/i/status/222"}}

data: {"type":"response.completed","response":{"model":"grok-4.5-build","output":[{"content":[{"type":"output_text","text":"Two public posts match.","annotations":[{"type":"url_citation","url":"https://x.example.test/i/status/111","title":"First public post"},{"type":"url_citation","url":"https://x.example.test/i/status/222","title":"https://x.example.test/i/status/222"}]}]}],"citations":["https://x.example.test/i/status/111"]}}

data: [DONE]

`)

	response, searchErr := normalize.Normalize(context.Background(), search.Request{
		Type: search.TypeX, Query: "find posts", Model: "grok-4.5",
	}, body)
	if searchErr != nil {
		t.Fatalf("Normalize() error = %v", searchErr)
	}
	if response.Type != search.TypeX || response.Query != "find posts" || response.Model != "grok-4.5" || response.ProviderModel != "grok-4.5-build" || response.Answer != "Two public posts match." {
		t.Fatalf("response = %#v", response)
	}
	wantSources := []search.Source{
		{URL: "https://x.example.test/i/status/111", Title: "First public post"},
		{URL: "https://x.example.test/i/status/222"},
	}
	if !sameSources(response.Sources, wantSources) {
		t.Fatalf("sources = %#v, want %#v", response.Sources, wantSources)
	}
	if len(response.Warnings) != 0 {
		t.Fatalf("warnings = %#v, want none", response.Warnings)
	}
}

func TestNormalizeXCitationWarningsMatchWeb(t *testing.T) {
	tests := []struct {
		name               string
		citationData       string
		completedCitations string
		wantWarnings       []search.Warning
	}{
		{
			name:               "missing",
			completedCitations: `[]`,
			wantWarnings:       []search.Warning{search.WarningMissingCitations},
		},
		{
			name: "invalid",
			citationData: `data: {"type":"response.output_text.annotation.added","annotation":{"type":"url_citation","url":"javascript:alert(1)","title":"unsafe"}}

data: {"type":"response.output_text.annotation.added","annotation":{"type":"url_citation","url":" https://whitespace.example.test/ ","title":"not absolute"}}

`,
			completedCitations: `[42]`,
			wantWarnings: []search.Warning{
				search.WarningInvalidCitationsIgnored,
				search.WarningMissingCitations,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := strings.NewReader(`data: {"type":"response.output_item.done","item":{"type":"custom_tool_call","status":"completed","name":"x_keyword_search"}}

` + test.citationData + `data: {"type":"response.completed","response":{"output":[{"content":[{"type":"output_text","text":"X answer."}]}],"citations":` + test.completedCitations + `}}

data: [DONE]

`)

			response, searchErr := normalize.Normalize(context.Background(), search.Request{Type: search.TypeX, Model: "m"}, body)
			if searchErr != nil {
				t.Fatalf("Normalize() error = %v", searchErr)
			}
			if len(response.Sources) != 0 {
				t.Fatalf("sources = %#v, want none", response.Sources)
			}
			encoded, err := json.Marshal(response)
			if err != nil || !strings.Contains(string(encoded), `"sources":[]`) {
				t.Fatalf("empty sources must remain a JSON array: %s (%v)", encoded, err)
			}
			if !sameWarnings(response.Warnings, test.wantWarnings) {
				t.Fatalf("warnings = %#v, want %#v", response.Warnings, test.wantWarnings)
			}
		})
	}
}

func TestNormalizeXRequiresOneTerminalCompletionAndMatchingEvidence(t *testing.T) {
	tests := []struct {
		name string
		body string
		code search.ErrorCode
	}{
		{
			name: "absent evidence",
			body: `data: {"type":"response.completed","response":{"output":[{"content":[{"type":"output_text","text":"answer"}]}]}}

data: [DONE]

`,
			code: search.ErrorSearchNotPerformed,
		},
		{
			name: "wrong tool type",
			body: `data: {"type":"response.output_item.done","item":{"type":"web_search_call","status":"completed"}}

data: {"type":"response.completed","response":{"output":[{"content":[{"type":"output_text","text":"answer"}]}]}}

data: [DONE]

`,
			code: search.ErrorSearchNotPerformed,
		},
		{
			name: "custom tool outside x namespace",
			body: `data: {"type":"response.output_item.done","item":{"type":"custom_tool_call","status":"completed","name":"web_search"}}

data: {"type":"response.completed","response":{"output":[{"content":[{"type":"output_text","text":"answer"}]}]}}

data: [DONE]

`,
			code: search.ErrorSearchNotPerformed,
		},
		{
			name: "custom tool incomplete",
			body: `data: {"type":"response.output_item.done","item":{"type":"custom_tool_call","status":"in_progress","name":"x_keyword_search"}}

data: {"type":"response.completed","response":{"output":[{"content":[{"type":"output_text","text":"answer"}]}]}}

data: [DONE]

`,
			code: search.ErrorSearchNotPerformed,
		},
		{
			name: "duplicate completion",
			body: `data: {"type":"response.output_item.done","item":{"type":"custom_tool_call","status":"completed","name":"x_keyword_search"}}

data: {"type":"response.completed","response":{"output":[{"content":[{"type":"output_text","text":"first"}]}]}}

data: {"type":"response.completed","response":{"output":[{"content":[{"type":"output_text","text":"second"}]}]}}

`,
			code: search.ErrorInvalidProviderResponse,
		},
		{
			name: "completion is not terminal",
			body: `data: {"type":"response.output_item.done","item":{"type":"custom_tool_call","status":"completed","name":"x_keyword_search"}}

data: {"type":"response.completed","response":{"output":[{"content":[{"type":"output_text","text":"answer"}]}]}}

data: {"type":"response.output_text.delta","delta":"late"}

`,
			code: search.ErrorInvalidProviderResponse,
		},
		{
			name: "empty answer",
			body: `data: {"type":"response.output_item.done","item":{"type":"custom_tool_call","status":"completed","name":"x_keyword_search"}}

data: {"type":"response.completed","response":{"model":"grok-4.5-build","output":[]}}

data: [DONE]

`,
			code: search.ErrorInvalidProviderResponse,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, searchErr := normalize.Normalize(context.Background(), search.Request{Type: search.TypeX, Model: "m"}, strings.NewReader(test.body))
			if searchErr == nil {
				t.Fatal("expected error")
			}
			if searchErr.Code != test.code {
				t.Fatalf("code = %q, want %q", searchErr.Code, test.code)
			}
		})
	}
}

func TestNormalizeXOmitsProviderModelWhenNotDifferent(t *testing.T) {
	body := strings.NewReader(`data: {"type":"response.output_item.done","item":{"type":"custom_tool_call","status":"completed","name":"x_keyword_search"}}

data: {"type":"response.completed","response":{"model":"grok-4.5","output":[{"content":[{"type":"output_text","text":"answer"}]}]}}

data: [DONE]

`)

	response, searchErr := normalize.Normalize(context.Background(), search.Request{Type: search.TypeX, Model: "grok-4.5"}, body)
	if searchErr != nil {
		t.Fatalf("Normalize() error = %v", searchErr)
	}
	if response.ProviderModel != "" {
		t.Fatalf("provider_model = %q, want empty", response.ProviderModel)
	}
}

func TestNormalizeDoneTextFallback(t *testing.T) {
	body := strings.NewReader(`data: {"type":"response.web_search_call.completed"}

data: {"type":"response.output_text.done","text":"done text"}

data: {"type":"response.completed","response":{"output":[]}}

data: [DONE]

`)

	response, searchErr := normalize.Normalize(context.Background(), search.Request{Type: search.TypeWeb, Query: "q", Model: "m"}, body)
	if searchErr != nil {
		t.Fatalf("Normalize() error = %v", searchErr)
	}
	if response.Answer != "done text" {
		t.Fatalf("answer = %q", response.Answer)
	}
}

func TestNormalizeProviderModelOmittedWhenEqual(t *testing.T) {
	body := strings.NewReader(`data: {"type":"response.web_search_call.completed"}

data: {"type":"response.completed","response":{"model":"grok-4.5","output":[{"content":[{"type":"output_text","text":"ok"}]}]}}

data: [DONE]

`)

	response, searchErr := normalize.Normalize(context.Background(), search.Request{Type: search.TypeWeb, Query: "q", Model: "grok-4.5"}, body)
	if searchErr != nil {
		t.Fatalf("Normalize() error = %v", searchErr)
	}
	if response.ProviderModel != "" {
		t.Fatalf("provider_model = %q, want empty", response.ProviderModel)
	}
}

// --- provider_error tests ---

func TestNormalizeProviderErrorResponseBodyEvent(t *testing.T) {
	body := strings.NewReader(`data: {"type":"response.error"}

`)

	_, searchErr := normalize.Normalize(context.Background(), search.Request{Type: search.TypeWeb, Model: "m"}, body)
	if searchErr == nil {
		t.Fatal("expected error")
	}
	if searchErr.Code != search.ErrorProvider {
		t.Fatalf("code = %q, want %q", searchErr.Code, search.ErrorProvider)
	}
}

func TestNormalizeProviderErrorResponseFailedEvent(t *testing.T) {
	body := strings.NewReader(`data: {"type":"response.failed"}

`)

	_, searchErr := normalize.Normalize(context.Background(), search.Request{Type: search.TypeWeb, Model: "m"}, body)
	if searchErr == nil {
		t.Fatal("expected error")
	}
	if searchErr.Code != search.ErrorProvider {
		t.Fatalf("code = %q, want %q", searchErr.Code, search.ErrorProvider)
	}
}

func TestNormalizeOutputTruncatedForTypedLengthStops(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{
			name: "incomplete event max output tokens",
			body: `data: {"type":"response.incomplete","response":{"incomplete_details":{"reason":"max_output_tokens"}}}

`,
		},
		{
			name: "completed event max tokens",
			body: `data: {"type":"response.web_search_call.completed"}

data: {"type":"response.completed","response":{"status":"incomplete","incomplete_details":{"reason":"max_tokens"}}}

`,
		},
		{
			name: "length finish reason",
			body: `data: {"type":"response.completed","response":{"finish_reason":"length"}}

`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, searchErr := normalize.Normalize(context.Background(), search.Request{Type: search.TypeWeb, Model: "m"}, strings.NewReader(test.body))
			if searchErr == nil {
				t.Fatal("expected error")
			}
			if searchErr.Code != search.ErrorOutputTruncated {
				t.Fatalf("code = %q, want %q", searchErr.Code, search.ErrorOutputTruncated)
			}
		})
	}
}

// --- invalid_provider_response tests ---

func TestNormalizeInvalidProviderResponseMalformedJSON(t *testing.T) {
	body := strings.NewReader(`data: {not json}

`)

	_, searchErr := normalize.Normalize(context.Background(), search.Request{Type: search.TypeWeb, Model: "m"}, body)
	if searchErr == nil {
		t.Fatal("expected error")
	}
	if searchErr.Code != search.ErrorInvalidProviderResponse {
		t.Fatalf("code = %q, want %q", searchErr.Code, search.ErrorInvalidProviderResponse)
	}
}

func TestNormalizeInvalidProviderResponseEmptyType(t *testing.T) {
	body := strings.NewReader(`data: {"type":""}

`)

	_, searchErr := normalize.Normalize(context.Background(), search.Request{Type: search.TypeWeb, Model: "m"}, body)
	if searchErr == nil {
		t.Fatal("expected error")
	}
	if searchErr.Code != search.ErrorInvalidProviderResponse {
		t.Fatalf("code = %q, want %q", searchErr.Code, search.ErrorInvalidProviderResponse)
	}
}

func TestNormalizeInvalidProviderResponseMissingType(t *testing.T) {
	body := strings.NewReader(`data: {"foo":"bar"}

`)

	_, searchErr := normalize.Normalize(context.Background(), search.Request{Type: search.TypeWeb, Model: "m"}, body)
	if searchErr == nil {
		t.Fatal("expected error")
	}
	if searchErr.Code != search.ErrorInvalidProviderResponse {
		t.Fatalf("code = %q, want %q", searchErr.Code, search.ErrorInvalidProviderResponse)
	}
}

func TestNormalizeInvalidProviderResponseNonObjectJSON(t *testing.T) {
	_, searchErr := normalize.Normalize(context.Background(), search.Request{Type: search.TypeWeb, Model: "m"}, strings.NewReader("data: []\n\n"))
	if searchErr == nil {
		t.Fatal("expected error")
	}
	if searchErr.Code != search.ErrorInvalidProviderResponse {
		t.Fatalf("code = %q, want %q", searchErr.Code, search.ErrorInvalidProviderResponse)
	}
}

// --- SSE framing and limits tests ---

func TestNormalizeSSEFramingIgnoresCommentsAndUnknownFields(t *testing.T) {
	body := strings.NewReader(`event: response.created
id: 1
retry: 1000
: keepalive

data: {"type":"response.web_search_call.completed"}

: completion follows
unknown: ignored
data: {"type":"response.completed","response":{"output":[{"content":[{"type":"output_text","text":"answer"}]}]}}

`)

	response, searchErr := normalize.Normalize(context.Background(), search.Request{Type: search.TypeWeb, Model: "m"}, body)
	if searchErr != nil {
		t.Fatalf("Normalize() error = %v", searchErr)
	}
	if response.Answer != "answer" {
		t.Fatalf("answer = %q", response.Answer)
	}
}

func TestNormalizeSSESizeLimits(t *testing.T) {
	const (
		maxBody  = 8 << 20
		maxEvent = 2 << 20
	)
	valid := `data: {"type":"response.web_search_call.completed"}

data: {"type":"response.completed","response":{"output":[{"content":[{"type":"output_text","text":"answer"}]}]}}

`

	tests := []struct {
		name    string
		body    string
		wantErr search.ErrorCode
	}{
		{
			name: "data event at limit",
			body: "data: " + sizedUnknownEvent(maxEvent) + "\n\n" + valid,
		},
		{
			name: "multiline data event over limit",
			body: "data: " + strings.Repeat("x", maxEvent/2+1) + "\n" +
				"data: " + strings.Repeat("x", maxEvent/2+1) + "\n\n",
			wantErr: search.ErrorOutputTooLarge,
		},
		{
			name: "complete body at limit",
			body: valid + ":" + strings.Repeat("x", maxBody-len(valid)-1),
		},
		{
			name:    "complete body over limit",
			body:    valid + ":" + strings.Repeat("x", maxBody-len(valid)),
			wantErr: search.ErrorOutputTooLarge,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response, searchErr := normalize.Normalize(context.Background(), search.Request{Type: search.TypeWeb, Model: "m"}, strings.NewReader(test.body))
			if test.wantErr == "" {
				if searchErr != nil {
					t.Fatalf("Normalize() error = %v", searchErr)
				}
				if response.Answer != "answer" {
					t.Fatalf("answer = %q", response.Answer)
				}
				return
			}
			if searchErr == nil {
				t.Fatal("expected error")
			}
			if searchErr.Code != test.wantErr {
				t.Fatalf("code = %q, want %q", searchErr.Code, test.wantErr)
			}
		})
	}
}

func TestNormalizeInvalidProviderResponseDuplicateCompleted(t *testing.T) {
	body := strings.NewReader(`data: {"type":"response.web_search_call.completed"}

data: {"type":"response.completed","response":{"output":[{"content":[{"type":"output_text","text":"a"}]}]}}

data: {"type":"response.completed","response":{"output":[{"content":[{"type":"output_text","text":"b"}]}]}}

`)

	_, searchErr := normalize.Normalize(context.Background(), search.Request{Type: search.TypeWeb, Model: "m"}, body)
	if searchErr == nil {
		t.Fatal("expected error")
	}
	if searchErr.Code != search.ErrorInvalidProviderResponse {
		t.Fatalf("code = %q, want %q", searchErr.Code, search.ErrorInvalidProviderResponse)
	}
}

func TestNormalizeInvalidProviderResponseNonTerminal(t *testing.T) {
	body := strings.NewReader(`data: {"type":"response.web_search_call.completed"}

data: {"type":"response.completed","response":{"output":[{"content":[{"type":"output_text","text":"answer"}]}]}}

data: {"type":"response.output_text.delta","delta":"late"}

`)

	_, searchErr := normalize.Normalize(context.Background(), search.Request{Type: search.TypeWeb, Model: "m"}, body)
	if searchErr == nil {
		t.Fatal("expected error")
	}
	if searchErr.Code != search.ErrorInvalidProviderResponse {
		t.Fatalf("code = %q, want %q", searchErr.Code, search.ErrorInvalidProviderResponse)
	}
}

func TestNormalizeInvalidProviderResponseMissingCompleted(t *testing.T) {
	body := strings.NewReader(`data: {"type":"response.web_search_call.completed"}

data: [DONE]

`)

	_, searchErr := normalize.Normalize(context.Background(), search.Request{Type: search.TypeWeb, Model: "m"}, body)
	if searchErr == nil {
		t.Fatal("expected error")
	}
	if searchErr.Code != search.ErrorInvalidProviderResponse {
		t.Fatalf("code = %q, want %q", searchErr.Code, search.ErrorInvalidProviderResponse)
	}
}

func TestNormalizeInvalidProviderResponseNilResponse(t *testing.T) {
	body := strings.NewReader(`data: {"type":"response.completed"}

`)

	_, searchErr := normalize.Normalize(context.Background(), search.Request{Type: search.TypeWeb, Model: "m"}, body)
	if searchErr == nil {
		t.Fatal("expected error")
	}
	if searchErr.Code != search.ErrorInvalidProviderResponse {
		t.Fatalf("code = %q, want %q", searchErr.Code, search.ErrorInvalidProviderResponse)
	}
}

func TestNormalizeInvalidProviderResponseEmptyAnswer(t *testing.T) {
	body := strings.NewReader(`data: {"type":"response.web_search_call.completed"}

data: {"type":"response.completed","response":{"output":[]}}

data: [DONE]

`)

	_, searchErr := normalize.Normalize(context.Background(), search.Request{Type: search.TypeWeb, Model: "m"}, body)
	if searchErr == nil {
		t.Fatal("expected error")
	}
	if searchErr.Code != search.ErrorInvalidProviderResponse {
		t.Fatalf("code = %q, want %q", searchErr.Code, search.ErrorInvalidProviderResponse)
	}
}

func TestNormalizeInvalidProviderResponseEmptyAnswerWhitespaceOnly(t *testing.T) {
	body := strings.NewReader(`data: {"type":"response.web_search_call.completed"}

data: {"type":"response.output_text.done","text":"   "}

data: {"type":"response.completed","response":{"output":[]}}

data: [DONE]

`)

	_, searchErr := normalize.Normalize(context.Background(), search.Request{Type: search.TypeWeb, Model: "m"}, body)
	if searchErr == nil {
		t.Fatal("expected error")
	}
	if searchErr.Code != search.ErrorInvalidProviderResponse {
		t.Fatalf("code = %q, want %q", searchErr.Code, search.ErrorInvalidProviderResponse)
	}
}

// --- search_not_performed tests ---

func TestNormalizeSearchNotPerformedWeb(t *testing.T) {
	body := strings.NewReader(`data: {"type":"response.completed","response":{"output":[{"content":[{"type":"output_text","text":"answer"}]}]}}

data: [DONE]

`)

	_, searchErr := normalize.Normalize(context.Background(), search.Request{Type: search.TypeWeb, Model: "m"}, body)
	if searchErr == nil {
		t.Fatal("expected error")
	}
	if searchErr.Code != search.ErrorSearchNotPerformed {
		t.Fatalf("code = %q, want %q", searchErr.Code, search.ErrorSearchNotPerformed)
	}
}

func TestNormalizeSearchNotPerformedX(t *testing.T) {
	body := strings.NewReader(`data: {"type":"response.completed","response":{"output":[{"content":[{"type":"output_text","text":"answer"}]}]}}

data: [DONE]

`)

	_, searchErr := normalize.Normalize(context.Background(), search.Request{Type: search.TypeX, Model: "m"}, body)
	if searchErr == nil {
		t.Fatal("expected error")
	}
	if searchErr.Code != search.ErrorSearchNotPerformed {
		t.Fatalf("code = %q, want %q", searchErr.Code, search.ErrorSearchNotPerformed)
	}
}

func TestNormalizeSearchNotPerformedWrongToolType(t *testing.T) {
	body := strings.NewReader(`data: {"type":"response.output_item.done","item":{"type":"web_search_call","status":"completed"}}

data: {"type":"response.completed","response":{"output":[{"content":[{"type":"output_text","text":"answer"}]}]}}

data: [DONE]

`)

	// Request is TypeX but only web_search_call evidence exists.
	_, searchErr := normalize.Normalize(context.Background(), search.Request{Type: search.TypeX, Model: "m"}, body)
	if searchErr == nil {
		t.Fatal("expected error")
	}
	if searchErr.Code != search.ErrorSearchNotPerformed {
		t.Fatalf("code = %q, want %q", searchErr.Code, search.ErrorSearchNotPerformed)
	}
}

// --- context / IO error tests ---

func TestNormalizeRequestTimeoutDeadlineExceeded(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 0)
	defer cancel()
	body := strings.NewReader("data: {}\n\n")

	_, searchErr := normalize.Normalize(ctx, search.Request{Type: search.TypeWeb, Model: "m"}, body)
	if searchErr == nil {
		t.Fatal("expected error")
	}
	if searchErr.Code != search.ErrorRequestTimeout {
		t.Fatalf("code = %q, want %q", searchErr.Code, search.ErrorRequestTimeout)
	}
}

func TestNormalizeTransportErrorContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	body := strings.NewReader("data: {}\n\n")

	_, searchErr := normalize.Normalize(ctx, search.Request{Type: search.TypeWeb, Model: "m"}, body)
	if searchErr == nil {
		t.Fatal("expected error")
	}
	if searchErr.Code != search.ErrorTransport {
		t.Fatalf("code = %q, want %q", searchErr.Code, search.ErrorTransport)
	}
}

func TestNormalizeTransportErrorBodyIOFailure(t *testing.T) {
	body := &errReader{err: io.ErrUnexpectedEOF}

	_, searchErr := normalize.Normalize(context.Background(), search.Request{Type: search.TypeWeb, Model: "m"}, body)
	if searchErr == nil {
		t.Fatal("expected error")
	}
	if searchErr.Code != search.ErrorTransport {
		t.Fatalf("code = %q, want %q", searchErr.Code, search.ErrorTransport)
	}
}

// --- helpers ---

type errReader struct{ err error }

func (r *errReader) Read([]byte) (int, error) { return 0, r.err }

func sizedUnknownEvent(size int) string {
	const prefix = `{"type":"unknown","padding":"`
	const suffix = `"}`
	return prefix + strings.Repeat("x", size-len(prefix)-len(suffix)) + suffix
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

func sameSources(got, want []search.Source) bool {
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
