package grok

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/patrick-fu/grok-search-cli/internal/search"
)

const versionEndpointURL = "https://x.ai/cli/stable"

var versionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?(\+[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$`)

func (c *client) discoverVersion(ctx context.Context) (string, *search.Error) {
	data, err := c.metadata(ctx, versionEndpointURL, 256)
	if err != nil {
		return "", err
	}
	version := strings.TrimSpace(string(data))
	if !versionPattern.MatchString(version) {
		return "", search.NewError(search.ErrorInvalidProviderResponse)
	}
	return version, nil
}

func (c *client) loadCatalog(ctx context.Context) (*modelCatalog, *search.Error) {
	data, err := c.metadata(ctx, modelsEndpointURL, catalogLimit)
	if err != nil {
		return nil, err
	}
	var catalog modelCatalog
	if !utf8.Valid(data) || json.Unmarshal(data, &catalog) != nil || catalog.Data == nil {
		return nil, search.NewError(search.ErrorInvalidProviderResponse)
	}
	return &catalog, nil
}

func (catalog *modelCatalog) searchModel() string {
	for _, model := range catalog.Data {
		if !model.SupportsBackendSearch || model.APIBackend != "responses" {
			continue
		}
		id := model.Model
		if id == "" && model.ID != nil {
			id = *model.ID
		}
		if len(id) == 0 || len(id) > 256 || !utf8.ValidString(id) {
			continue
		}
		valid := true
		for _, character := range id {
			if unicode.IsControl(character) || unicode.IsSpace(character) {
				valid = false
				break
			}
		}
		if valid {
			return id
		}
	}
	return ""
}

func (c *client) metadata(ctx context.Context, endpoint string, limit int64) ([]byte, *search.Error) {
	metadataCtx, cancel := context.WithTimeout(ctx, catalogTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(metadataCtx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, search.NewError(search.ErrorInternal)
	}
	if endpoint == modelsEndpointURL {
		c.setCatalogHeaders(req)
	} else {
		req.Header.Set("Accept", "text/plain")
		req.Header.Set("User-Agent", "grok-search-cli/"+c.cliVersion)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil || resp == nil {
		if resp != nil && resp.Body != nil {
			resp.Body.Close()
		}
		if isDeadlineError(metadataCtx, err) {
			return nil, search.NewError(search.ErrorRequestTimeout)
		}
		return nil, search.NewError(search.ErrorTransport)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		if endpoint == versionEndpointURL && (resp.StatusCode == 401 || resp.StatusCode == 403 || resp.StatusCode == 426) {
			return nil, classifyHTTPError(resp, search.ErrorProvider)
		}
		return nil, classifyHTTPError(resp)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		if isDeadlineError(metadataCtx, err) {
			return nil, search.NewError(search.ErrorRequestTimeout)
		}
		return nil, search.NewError(search.ErrorTransport)
	}
	if int64(len(data)) > limit {
		return nil, search.NewError(search.ErrorOutputTooLarge)
	}
	return data, nil
}
