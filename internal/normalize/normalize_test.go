package normalize_test

import (
	"context"
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
