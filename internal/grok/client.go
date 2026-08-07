// Package grok implements the fixed Grok Responses request profile and
// production-safe HTTP transport for the search CLI.
package grok

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/patrick-fu/grok-search-cli/internal/normalize"
	"github.com/patrick-fu/grok-search-cli/internal/search"
)

const (
	endpointURL  = "https://cli-chat-proxy.grok.com/v1/responses"
	defaultModel = "grok-4.5"
	toolVersion  = "0.2.117"
)

// Client is the transport boundary for Grok search requests.
type Client interface {
	Search(ctx context.Context, request search.Request) (search.Response, *search.Error)
}

type client struct {
	accessToken string
	cliVersion  string
	httpClient  *http.Client
}

// NewClient creates a Client with the given credentials. When rt is non-nil
// it replaces the production HTTP transport (for testing).
func NewClient(accessToken, cliVersion string, rt http.RoundTripper) Client {
	if cliVersion == "" {
		cliVersion = "dev"
	}
	c := &client{
		accessToken: accessToken,
		cliVersion:  cliVersion,
	}
	var transport http.RoundTripper
	if rt != nil {
		transport = rt
	} else {
		transport = &http.Transport{
			Proxy: nil,
			TLSClientConfig: &tls.Config{
				MinVersion: tls.VersionTLS12,
			},
		}
	}
	c.httpClient = &http.Client{
		Transport:     transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return c
}

func (c *client) Search(ctx context.Context, request search.Request) (search.Response, *search.Error) {
	if request.Type != search.TypeWeb {
		return search.Response{}, search.NewError(search.ErrorInternal)
	}

	body, err := c.buildBody(request)
	if err != nil {
		return search.Response{}, search.NewError(search.ErrorInternal)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpointURL, bytes.NewReader(body))
	if err != nil {
		return search.Response{}, search.NewError(search.ErrorInternal)
	}
	c.setHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return search.Response{}, search.NewError(search.ErrorRequestTimeout)
		}
		return search.Response{}, search.NewError(search.ErrorTransport)
	}
	if resp == nil {
		return search.Response{}, search.NewError(search.ErrorTransport)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return search.Response{}, classifyHTTPError(resp)
	}

	ct := headerValue(resp.Header, "Content-Type")
	if !strings.HasPrefix(ct, "text/event-stream") {
		errorValue := search.NewError(search.ErrorInvalidProviderResponse).WithHTTPStatus(resp.StatusCode)
		if requestID := safeRequestID(headerValue(resp.Header, "X-Request-Id")); requestID != "" {
			errorValue.WithRequestID(requestID)
		}
		return search.Response{}, errorValue
	}

	response, normalizedErr := normalize.Normalize(ctx, request, resp.Body)
	if normalizedErr != nil {
		normalizedErr.WithHTTPStatus(resp.StatusCode)
		if requestID := safeRequestID(headerValue(resp.Header, "X-Request-Id")); requestID != "" {
			normalizedErr.WithRequestID(requestID)
		}
	}
	return response, normalizedErr
}

// classifyHTTPError maps only the trusted HTTP status and a small allowlist of
// response metadata into the stable search error domain. Provider body and
// mutable provider messages are deliberately never read.
func classifyHTTPError(resp *http.Response) *search.Error {
	status := resp.StatusCode
	code := search.ErrorProvider
	switch status {
	case http.StatusUnauthorized:
		code = search.ErrorAuthRejected
	case http.StatusForbidden:
		code = search.ErrorPermissionDenied
	case http.StatusTooManyRequests:
		code = search.ErrorRateLimited
	case http.StatusBadRequest, http.StatusConflict, http.StatusUnprocessableEntity:
		code = search.ErrorRequestRejected
	case http.StatusRequestEntityTooLarge:
		code = search.ErrorRequestTooLarge
	case http.StatusUpgradeRequired:
		code = search.ErrorClientIncompatible
	case http.StatusRequestTimeout:
		code = search.ErrorRequestTimeout
	default:
		if status >= 500 && status <= 599 {
			code = search.ErrorProviderUnavailable
		}
	}

	err := search.NewError(code).WithHTTPStatus(status)
	if code == search.ErrorRateLimited {
		if retryAfter, ok := parseRetryAfter(headerValue(resp.Header, "Retry-After")); ok {
			err = err.WithRetryAfterSeconds(retryAfter)
		}
	}
	if requestID := safeRequestID(headerValue(resp.Header, "X-Request-Id")); requestID != "" {
		err = err.WithRequestID(requestID)
	}
	return err
}

func headerValue(header http.Header, name string) string {
	for key, values := range header {
		if strings.EqualFold(key, name) && len(values) > 0 {
			return values[0]
		}
	}
	return ""
}

func parseRetryAfter(value string) (int, bool) {
	if value == "" {
		return 0, false
	}
	seconds, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0, false
	}
	if seconds < 0 {
		seconds = 0
	} else if seconds > 3600 {
		seconds = 3600
	}
	return seconds, true
}

func safeRequestID(value string) string {
	if value == "" || len(value) > 128 {
		return ""
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') || c == '.' || c == '_' || c == ':' || c == '-') {
			return ""
		}
	}
	return value
}

type requestBody struct {
	Model           string        `json:"model"`
	Input           string        `json:"input"`
	Tools           []searchTool  `json:"tools"`
	ToolChoice      string        `json:"tool_choice"`
	Stream          bool          `json:"stream"`
	Store           bool          `json:"store"`
	MaxOutputTokens int           `json:"max_output_tokens"`
	Reasoning       reasoningSpec `json:"reasoning"`
}

type searchTool struct {
	Type string `json:"type"`
}

type reasoningSpec struct {
	Effort string `json:"effort"`
}

func (c *client) buildBody(request search.Request) ([]byte, error) {
	model := request.Model
	if model == "" {
		model = defaultModel
	}
	return json.Marshal(requestBody{
		Model:           model,
		Input:           request.Query,
		Tools:           []searchTool{{Type: "web_search"}},
		ToolChoice:      "required",
		Stream:          true,
		Store:           false,
		MaxOutputTokens: 1024,
		Reasoning:       reasoningSpec{Effort: "low"},
	})
}

func (c *client) setHeaders(req *http.Request) {
	version := fmt.Sprintf("%s+grok-search-cli.%s", toolVersion, c.cliVersion)
	ua := fmt.Sprintf("grok-search-cli/%s", c.cliVersion)

	req.Header.Set("Authorization", "Bearer "+c.accessToken)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-grok-client-mode", "headless")
	req.Header.Set("x-grok-client-identifier", "grok-search-cli")
	req.Header.Set("x-grok-client-version", version)
	req.Header.Set("User-Agent", ua)
}
