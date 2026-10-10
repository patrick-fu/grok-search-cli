// Package normalize converts the provider's SSE response into the stable
// search domain response. Provider event shapes deliberately do not escape
// this package.
package normalize

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"strings"
	"unicode"

	"github.com/patrick-fu/grok-search-cli/internal/search"
)

const (
	maxSSEBodyBytes  = 8 << 20
	maxSSEEventBytes = 2 << 20
)

// Normalize reads one provider SSE response and returns only the stable search
// domain result. It never includes raw provider events or messages in errors.
func Normalize(ctx context.Context, request search.Request, body io.Reader) (search.Response, *search.Error) {
	collector := sourceCollector{sources: make([]search.Source, 0), indexByURL: make(map[string]int)}
	state := normalizationState{}

	err := parseSSE(ctx, body, func(data string) error {
		if data == "[DONE]" {
			return nil
		}
		if state.completed != nil {
			return errInvalidProviderResponse
		}

		var event wireEvent
		if json.Unmarshal([]byte(data), &event) != nil || strings.TrimSpace(event.Type) == "" {
			return errInvalidProviderResponse
		}
		return state.accept(request, event, &collector)
	})
	if err != nil {
		return handleParseError(err)
	}

	// Missing completed response.
	if state.completed == nil {
		return search.Response{}, search.NewError(search.ErrorInvalidProviderResponse)
	}

	// Missing matching tool evidence.
	if !state.toolCompleted {
		return search.Response{}, search.NewError(search.ErrorSearchNotPerformed)
	}

	answer := completedAnswer(*state.completed)
	if strings.TrimSpace(answer) == "" {
		answer = state.doneText
	}
	if strings.TrimSpace(answer) == "" {
		answer = state.deltas.String()
	}
	if strings.TrimSpace(answer) == "" {
		return search.Response{}, search.NewError(search.ErrorInvalidProviderResponse)
	}

	for _, output := range state.completed.Output {
		for _, content := range output.Content {
			if content.Type != "output_text" {
				continue
			}
			for _, annotation := range content.Annotations {
				collector.addAnnotation(annotation)
			}
		}
	}
	for _, citation := range state.completed.Citations {
		collector.addRawCitation(citation)
	}

	warnings := make([]search.Warning, 0, 2)
	if collector.invalidCount > 0 {
		warnings = append(warnings, search.WarningInvalidCitationsIgnored)
	}
	if len(collector.sources) == 0 {
		warnings = append(warnings, search.WarningMissingCitations)
	}

	providerModel := strings.TrimSpace(state.completed.Model)
	if providerModel == request.Model {
		providerModel = ""
	}

	return search.Response{
		SchemaVersion: search.SchemaVersion,
		Type:          request.Type,
		Query:         request.Query,
		Model:         request.Model,
		ProviderModel: providerModel,
		Answer:        answer,
		Sources:       collector.sources,
		Warnings:      warnings,
	}, nil
}

// handleParseError translates a parseSSE failure into a typed search error.
func handleParseError(err error) (search.Response, *search.Error) {
	var ne normalizeError
	if errors.As(err, &ne) {
		return search.Response{}, search.NewError(ne.code)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return search.Response{}, search.NewError(search.ErrorRequestTimeout)
	}
	if errors.Is(err, context.Canceled) {
		return search.Response{}, search.NewError(search.ErrorTransport)
	}
	return search.Response{}, search.NewError(search.ErrorTransport)
}

// normalizeError carries a typed search error code through the parseSSE
// callback chain. It is not exposed outside this package.
type normalizeError struct {
	code search.ErrorCode
}

func (e normalizeError) Error() string { return string(e.code) }

var (
	errProviderError           = normalizeError{code: search.ErrorProvider}
	errOutputTooLarge          = normalizeError{code: search.ErrorOutputTooLarge}
	errOutputTruncated         = normalizeError{code: search.ErrorOutputTruncated}
	errInvalidProviderResponse = normalizeError{code: search.ErrorInvalidProviderResponse}
)

type normalizationState struct {
	completed     *wireResponse
	toolCompleted bool
	doneText      string
	deltas        strings.Builder
}

func (state *normalizationState) accept(request search.Request, event wireEvent, collector *sourceCollector) error {
	switch event.Type {
	case "response.error", "response.failed":
		return errProviderError
	case "response.incomplete":
		if isLengthStop(event.Type, event.Response) {
			return errOutputTruncated
		}
		return errInvalidProviderResponse
	case "response.completed":
		if event.Response == nil {
			return errInvalidProviderResponse
		}
		if isLengthStop(event.Type, event.Response) {
			return errOutputTruncated
		}
		state.completed = event.Response
	case "response.output_text.delta":
		state.deltas.WriteString(event.Delta)
	case "response.output_text.done":
		if state.doneText == "" {
			state.doneText = event.Text
		}
	case "response.output_text.annotation.added":
		if event.Annotation != nil {
			collector.addAnnotation(*event.Annotation)
		}
	}

	if request.Type == search.TypeWeb && isCompletedWebTool(event) {
		state.toolCompleted = true
	}
	if request.Type == search.TypeX && isCompletedXTool(event) {
		state.toolCompleted = true
	}
	return nil
}

func parseSSE(ctx context.Context, body io.Reader, accept func(string) error) error {
	reader := bufio.NewReader(&boundedReader{reader: body, remaining: maxSSEBodyBytes})
	dataLines := make([]string, 0, 1)
	eventBytes := 0

	dispatch := func() error {
		if len(dataLines) == 0 {
			return nil
		}
		data := strings.Join(dataLines, "\n")
		dataLines = dataLines[:0]
		eventBytes = 0
		return accept(data)
	}

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			line = strings.TrimSuffix(line, "\n")
			line = strings.TrimSuffix(line, "\r")
			switch {
			case line == "":
				if dispatchErr := dispatch(); dispatchErr != nil {
					return dispatchErr
				}
			case strings.HasPrefix(line, "data:"):
				value := strings.TrimPrefix(line, "data:")
				if strings.HasPrefix(value, " ") {
					value = value[1:]
				}
				if len(dataLines) > 0 {
					eventBytes++ // The SSE data field separator becomes a newline.
				}
				eventBytes += len(value)
				if eventBytes > maxSSEEventBytes {
					return errOutputTooLarge
				}
				dataLines = append(dataLines, value)
			}
		}
		if err == io.EOF {
			return dispatch()
		}
		if err != nil {
			return err
		}
	}
}

// boundedReader accepts a stream exactly at its limit, but rejects a following
// byte without allowing an unbounded line or buffer allocation.
type boundedReader struct {
	reader    io.Reader
	remaining int
}

func (reader *boundedReader) Read(buffer []byte) (int, error) {
	if reader.remaining == 0 {
		var probe [1]byte
		read, err := reader.reader.Read(probe[:])
		if read > 0 {
			return 0, errOutputTooLarge
		}
		return 0, err
	}
	if len(buffer) > reader.remaining {
		buffer = buffer[:reader.remaining]
	}
	read, err := reader.reader.Read(buffer)
	reader.remaining -= read
	return read, err
}

type wireEvent struct {
	Type       string          `json:"type"`
	Delta      string          `json:"delta"`
	Text       string          `json:"text"`
	Annotation *wireAnnotation `json:"annotation"`
	Item       *wireItem       `json:"item"`
	Response   *wireResponse   `json:"response"`
}

type wireItem struct {
	Type   string `json:"type"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

type wireResponse struct {
	Model             string                 `json:"model"`
	Status            string                 `json:"status"`
	FinishReason      string                 `json:"finish_reason"`
	IncompleteDetails *wireIncompleteDetails `json:"incomplete_details"`
	Citations         []json.RawMessage      `json:"citations"`
	Output            []wireOutput           `json:"output"`
}

type wireIncompleteDetails struct {
	Reason string `json:"reason"`
}

type wireOutput struct {
	Content []wireContent `json:"content"`
}

type wireContent struct {
	Type        string           `json:"type"`
	Text        string           `json:"text"`
	Annotations []wireAnnotation `json:"annotations"`
}

type wireAnnotation struct {
	Type  string `json:"type"`
	URL   string `json:"url"`
	Title string `json:"title"`
}

func completedAnswer(response wireResponse) string {
	parts := make([]string, 0)
	for _, output := range response.Output {
		for _, content := range output.Content {
			if content.Type == "output_text" && content.Text != "" {
				parts = append(parts, content.Text)
			}
		}
	}
	return strings.Join(parts, "\n")
}

func isLengthStop(eventType string, response *wireResponse) bool {
	if response == nil {
		return false
	}
	if eventType == "response.completed" && (response.FinishReason == "length" ||
		response.FinishReason == "max_tokens" || response.FinishReason == "max_output_tokens") {
		return true
	}
	return (eventType == "response.incomplete" || response.Status == "incomplete") && response.IncompleteDetails != nil &&
		(response.IncompleteDetails.Reason == "max_output_tokens" || response.IncompleteDetails.Reason == "max_tokens")
}

func isCompletedWebTool(event wireEvent) bool {
	return event.Type == "response.web_search_call.completed" ||
		(event.Type == "response.output_item.done" && event.Item != nil &&
			event.Item.Type == "web_search_call" && event.Item.Status == "completed")
}

func isCompletedXTool(event wireEvent) bool {
	return event.Type == "response.output_item.done" && event.Item != nil &&
		event.Item.Type == "custom_tool_call" && event.Item.Status == "completed" &&
		strings.HasPrefix(event.Item.Name, "x_")
}

type sourceCollector struct {
	sources      []search.Source
	indexByURL   map[string]int
	invalidCount int
}

func (collector *sourceCollector) addAnnotation(annotation wireAnnotation) {
	if annotation.Type == "url_citation" {
		collector.add(annotation.URL, annotation.Title)
	}
}

func (collector *sourceCollector) addRawCitation(raw json.RawMessage) {
	var value string
	if json.Unmarshal(raw, &value) == nil {
		collector.add(value, "")
		return
	}
	var annotation wireAnnotation
	if json.Unmarshal(raw, &annotation) == nil && annotation.Type == "url_citation" {
		collector.addAnnotation(annotation)
		return
	}
	collector.invalidCount++
}

func (collector *sourceCollector) add(rawURL, rawTitle string) {
	parsed, err := url.Parse(rawURL)
	if err != nil || !strings.EqualFold(parsed.Scheme, "http") && !strings.EqualFold(parsed.Scheme, "https") || parsed.Host == "" || parsed.User != nil {
		collector.invalidCount++
		return
	}
	title := meaningfulTitle(rawTitle, rawURL)
	if index, found := collector.indexByURL[rawURL]; found {
		if collector.sources[index].Title == "" && title != "" {
			collector.sources[index].Title = title
		}
		return
	}
	collector.indexByURL[rawURL] = len(collector.sources)
	collector.sources = append(collector.sources, search.Source{URL: rawURL, Title: title})
}

func meaningfulTitle(rawTitle, rawURL string) string {
	title := strings.TrimSpace(rawTitle)
	if title == "" || title == rawURL {
		return ""
	}
	marker := strings.TrimSpace(strings.Trim(title, "[]()"))
	if marker != "" && allDigits(marker) {
		return ""
	}
	return title
}

func allDigits(value string) bool {
	for _, character := range value {
		if !unicode.IsDigit(character) {
			return false
		}
	}
	return value != ""
}
