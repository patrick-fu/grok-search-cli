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
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/patrick-fu/grok-search-cli/internal/normalize"
	"github.com/patrick-fu/grok-search-cli/internal/search"
)

const (
	endpointURL       = "https://cli-chat-proxy.grok.com/v1/responses"
	modelsEndpointURL = "https://cli-chat-proxy.grok.com/v1/models"
	defaultModel      = "grok-4.5"
	toolVersion       = "0.2.117"
	retryDelay        = 250 * time.Millisecond
	connectLimit      = 10 * time.Second
	catalogLimit      = 1 << 20
	catalogTimeout    = 15 * time.Second
)

// Client is the transport boundary for Grok search requests.
type Client interface {
	Search(ctx context.Context, request search.Request) (search.Response, *search.Error)
}

type client struct {
	accessToken string
	cliVersion  string
	httpClient  *http.Client
	waitRetry   func(context.Context, time.Duration) bool
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
		transport = newProductionTransport()
	}
	c.httpClient = &http.Client{
		Transport:     transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	c.waitRetry = waitForRetry
	return c
}

func (c *client) Search(ctx context.Context, request search.Request) (search.Response, *search.Error) {
	if request.Type != search.TypeWeb && request.Type != search.TypeX {
		return search.Response{}, search.NewError(search.ErrorInternal)
	}

	body, err := c.buildBody(request)
	if err != nil {
		return search.Response{}, search.NewError(search.ErrorInternal)
	}

	resp, transportErr := c.doRequest(ctx, body)
	if transportErr != nil {
		return search.Response{}, transportErr
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		if resp.StatusCode == http.StatusNotFound && c.catalogOmitsModel(ctx, modelFor(request)) {
			return search.Response{}, classifyHTTPError(resp, search.ErrorUnsupportedModel)
		}
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

// catalogOmitsModel performs the sole model-catalog lookup. It is deliberately
// reachable only after the fixed Responses route returned 404: the catalog is
// diagnostic evidence, never model selection or a request preflight.
func (c *client) catalogOmitsModel(ctx context.Context, requestedModel string) bool {
	catalogCtx, cancel := context.WithTimeout(ctx, catalogTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(catalogCtx, http.MethodGet, modelsEndpointURL, nil)
	if err != nil {
		return false
	}
	c.setCatalogHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil || resp == nil {
		if resp != nil && resp.Body != nil {
			resp.Body.Close()
		}
		return false
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices || resp.Body == nil {
		return false
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, catalogLimit+1))
	if err != nil || len(body) > catalogLimit {
		return false
	}

	var catalog modelCatalog
	if err := json.Unmarshal(body, &catalog); err != nil || len(catalog.Data) == 0 {
		return false
	}
	for _, model := range catalog.Data {
		if model.ID == nil || *model.ID == "" {
			return false
		}
		if *model.ID == requestedModel {
			return false
		}
	}
	return true
}

// doRequest makes at most two attempts. The second is permitted only after a
// RoundTrip failure that returned no response, which is the only point at
// which this client can establish that no response header or body was seen.
func (c *client) doRequest(ctx context.Context, body []byte) (*http.Response, *search.Error) {
	for attempt := 0; attempt < 2; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpointURL, bytes.NewReader(body))
		if err != nil {
			return nil, search.NewError(search.ErrorInternal)
		}
		c.setHeaders(req)

		resp, err := c.httpClient.Do(req)
		if err == nil && resp != nil {
			return resp, nil
		}
		if resp != nil {
			resp.Body.Close()
			return nil, search.NewError(search.ErrorTransport)
		}
		if isDeadlineError(ctx, err) {
			return nil, search.NewError(search.ErrorRequestTimeout)
		}
		if !canRetry(ctx, err) || attempt == 1 || !c.waitRetry(ctx, retryDelay) {
			return nil, search.NewError(search.ErrorTransport)
		}
	}
	return nil, search.NewError(search.ErrorTransport)
}

func isDeadlineError(ctx context.Context, err error) bool {
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded)
}

func canRetry(ctx context.Context, err error) bool {
	return err != nil && ctx.Err() == nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
}

func waitForRetry(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func newProductionTransport() *http.Transport {
	return &http.Transport{
		Proxy:               nil,
		DialContext:         dialContextWithLimit,
		TLSHandshakeTimeout: connectLimit,
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
		},
	}
}

func dialContextWithLimit(ctx context.Context, network, address string) (net.Conn, error) {
	timeout, ok := connectTimeout(ctx)
	if !ok {
		return nil, ctx.Err()
	}
	dialer := net.Dialer{Timeout: timeout}
	return dialer.DialContext(ctx, network, address)
}

func connectTimeout(ctx context.Context) (time.Duration, bool) {
	timeout := connectLimit
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return 0, false
		}
		if remaining < timeout {
			timeout = remaining
		}
	}
	return timeout, true
}

// classifyHTTPError maps only the trusted HTTP status and a small allowlist of
// response metadata into the stable search error domain. Provider body and
// mutable provider messages are deliberately never read.
func classifyHTTPError(resp *http.Response, override ...search.ErrorCode) *search.Error {
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
	if len(override) == 1 {
		code = override[0]
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

type modelCatalog struct {
	Data []catalogModel `json:"data"`
}

type catalogModel struct {
	ID *string `json:"id"`
}

func (c *client) buildBody(request search.Request) ([]byte, error) {
	maxOutputTokens := request.MaxOutputTokens
	if maxOutputTokens == 0 {
		maxOutputTokens = 1024
	}
	return json.Marshal(requestBody{
		Model:           modelFor(request),
		Input:           request.Query,
		Tools:           []searchTool{{Type: toolTypeFor(request.Type)}},
		ToolChoice:      "required",
		Stream:          true,
		Store:           false,
		MaxOutputTokens: maxOutputTokens,
		Reasoning:       reasoningSpec{Effort: "low"},
	})
}

func modelFor(request search.Request) string {
	if request.Model == "" {
		return defaultModel
	}
	return request.Model
}

// toolTypeFor maps a request search surface to the single provider tool. Each
// surface emits exactly one tool with no web fallback.
func toolTypeFor(t search.Type) string {
	if t == search.TypeX {
		return "x_search"
	}
	return "web_search"
}

func (c *client) setHeaders(req *http.Request) {
	c.setClientIdentityHeaders(req)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Content-Type", "application/json")
}

func (c *client) setCatalogHeaders(req *http.Request) {
	c.setClientIdentityHeaders(req)
	req.Header.Set("Accept", "application/json")
}

func (c *client) setClientIdentityHeaders(req *http.Request) {
	version := fmt.Sprintf("%s+grok-search-cli.%s", toolVersion, c.cliVersion)
	ua := fmt.Sprintf("grok-search-cli/%s", c.cliVersion)

	req.Header.Set("Authorization", "Bearer "+c.accessToken)
	req.Header.Set("x-grok-client-mode", "headless")
	req.Header.Set("x-grok-client-identifier", "grok-search-cli")
	req.Header.Set("x-grok-client-version", version)
	req.Header.Set("User-Agent", ua)
}
