package main

import (
	"encoding/json"
	"net/url"
	"strings"
	"unicode"
)

const schemaVersion = 1

type SearchRequest struct {
	Type  string
	Query string
	Model string
}

type SearchResponse struct {
	SchemaVersion int      `json:"schema_version"`
	Type          string   `json:"type"`
	Query         string   `json:"query"`
	Model         string   `json:"model"`
	ProviderModel string   `json:"provider_model,omitempty"`
	Answer        string   `json:"answer"`
	Sources       []Source `json:"sources"`
	Warnings      []string `json:"warnings"`
}

type Source struct {
	URL   string `json:"url"`
	Title string `json:"title,omitempty"`
}

type ErrorResponse struct {
	SchemaVersion int         `json:"schema_version"`
	Error         ErrorDetail `json:"error"`
}

type ErrorDetail struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
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
	Type   string     `json:"type"`
	Name   string     `json:"name"`
	Status string     `json:"status"`
	Action wireAction `json:"action"`
}

type wireAction struct {
	Sources []json.RawMessage `json:"sources"`
}

type wireResponse struct {
	Model     string            `json:"model"`
	Citations []json.RawMessage `json:"citations"`
	Output    []wireOutput      `json:"output"`
}

type wireOutput struct {
	Type    string        `json:"type"`
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

type sourceCollector struct {
	sources      []Source
	indexByURL   map[string]int
	invalidCount int
}

func NormalizeSearch(request SearchRequest, rawEvents []json.RawMessage) (SearchResponse, *ErrorResponse) {
	collector := sourceCollector{
		sources:    make([]Source, 0),
		indexByURL: make(map[string]int),
	}
	var events []wireEvent
	var completed *wireResponse
	var completedIndex = -1
	var completedCount int
	var textDeltas strings.Builder
	var doneText string
	var toolCompleted bool

	for i, raw := range rawEvents {
		var event wireEvent
		if err := json.Unmarshal(raw, &event); err != nil || event.Type == "" {
			return SearchResponse{}, protocolError("Provider returned an undecodable search event.")
		}
		events = append(events, event)

		switch event.Type {
		case "response.error", "response.failed":
			return SearchResponse{}, providerError()
		case "response.completed":
			completedCount++
			completed = event.Response
			completedIndex = i
		case "response.output_text.delta":
			textDeltas.WriteString(event.Delta)
		case "response.output_text.done":
			doneText = event.Text
		case "response.output_text.annotation.added":
			if event.Annotation != nil {
				collector.addAnnotation(*event.Annotation)
			}
		}

		if request.Type == "web" && isCompletedWebTool(event) {
			toolCompleted = true
		}
		if request.Type == "x" && isCompletedXTool(event) {
			toolCompleted = true
		}
	}

	if completedCount != 1 || completed == nil || completedIndex != len(events)-1 {
		return SearchResponse{}, protocolError("Provider did not return one terminal completed response.")
	}
	if !toolCompleted {
		return SearchResponse{}, &ErrorResponse{
			SchemaVersion: schemaVersion,
			Error: ErrorDetail{
				Code:      "search_not_performed",
				Message:   "Provider completed without performing the requested search tool.",
				Retryable: false,
			},
		}
	}

	answer := completedAnswer(*completed)
	if strings.TrimSpace(answer) == "" {
		answer = doneText
	}
	if strings.TrimSpace(answer) == "" {
		answer = textDeltas.String()
	}
	if strings.TrimSpace(answer) == "" {
		return SearchResponse{}, protocolError("Provider completed without an answer.")
	}

	for _, output := range completed.Output {
		for _, content := range output.Content {
			for _, annotation := range content.Annotations {
				collector.addAnnotation(annotation)
			}
		}
	}
	for _, citation := range completed.Citations {
		collector.addRawCitation(citation)
	}

	warnings := make([]string, 0, 2)
	if collector.invalidCount > 0 {
		warnings = append(warnings, "invalid_citations_ignored")
	}
	if len(collector.sources) == 0 {
		warnings = append(warnings, "missing_citations")
	}

	providerModel := strings.TrimSpace(completed.Model)
	if providerModel == request.Model {
		providerModel = ""
	}

	return SearchResponse{
		SchemaVersion: schemaVersion,
		Type:          request.Type,
		Query:         request.Query,
		Model:         request.Model,
		ProviderModel: providerModel,
		Answer:        answer,
		Sources:       collector.sources,
		Warnings:      warnings,
	}, nil
}

func completedAnswer(response wireResponse) string {
	var parts []string
	for _, output := range response.Output {
		for _, content := range output.Content {
			if content.Type == "output_text" && content.Text != "" {
				parts = append(parts, content.Text)
			}
		}
	}
	return strings.Join(parts, "\n")
}

func isCompletedWebTool(event wireEvent) bool {
	if event.Type == "response.web_search_call.completed" {
		return true
	}
	return event.Type == "response.output_item.done" && event.Item != nil &&
		event.Item.Type == "web_search_call" && event.Item.Status == "completed"
}

func isCompletedXTool(event wireEvent) bool {
	return event.Type == "response.output_item.done" && event.Item != nil &&
		event.Item.Type == "custom_tool_call" && event.Item.Status == "completed" &&
		strings.HasPrefix(event.Item.Name, "x_")
}

func (collector *sourceCollector) addAnnotation(annotation wireAnnotation) {
	if annotation.Type != "url_citation" {
		return
	}
	collector.add(annotation.URL, annotation.Title)
}

func (collector *sourceCollector) addRawCitation(raw json.RawMessage) {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		collector.add(text, "")
		return
	}

	var annotation wireAnnotation
	if json.Unmarshal(raw, &annotation) == nil {
		collector.add(annotation.URL, annotation.Title)
	}
}

func (collector *sourceCollector) add(rawURL string, rawTitle string) {
	parsed, err := url.Parse(rawURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		collector.invalidCount++
		return
	}

	title := meaningfulTitle(rawTitle, rawURL)
	if index, exists := collector.indexByURL[rawURL]; exists {
		if collector.sources[index].Title == "" && title != "" {
			collector.sources[index].Title = title
		}
		return
	}

	collector.indexByURL[rawURL] = len(collector.sources)
	collector.sources = append(collector.sources, Source{URL: rawURL, Title: title})
}

func meaningfulTitle(rawTitle string, rawURL string) string {
	title := strings.TrimSpace(rawTitle)
	if title == "" || title == rawURL {
		return ""
	}

	marker := strings.Trim(title, "[]()")
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

func protocolError(message string) *ErrorResponse {
	return &ErrorResponse{
		SchemaVersion: schemaVersion,
		Error: ErrorDetail{
			Code:      "invalid_provider_response",
			Message:   message,
			Retryable: false,
		},
	}
}

func providerError() *ErrorResponse {
	return &ErrorResponse{
		SchemaVersion: schemaVersion,
		Error: ErrorDetail{
			Code:      "provider_error",
			Message:   "Provider reported that the search failed.",
			Retryable: false,
		},
	}
}
