// SPDX-License-Identifier: Apache-2.0
package relay

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/nomifun/nomifun-model-gateway/internal/core"
)

// DiscoveryInput is an ephemeral account preview. It is never persisted or
// used for inference, and its credential must not enter logs or error messages.
type DiscoveryInput struct {
	Kind       string `json:"kind"`
	BaseURL    string `json:"base_url"`
	APIKey     string `json:"api_key"`
	APIVersion string `json:"api_version"`
}

// DiscoveredModel contains only metadata present in the native list response.
// It is deliberately separate from the public catalog: discovery establishes
// neither supported gateway tasks nor prices, access policy or actual calls.
type DiscoveredModel struct {
	ID                         string   `json:"id"`
	NativeName                 string   `json:"native_name,omitempty"`
	DisplayName                string   `json:"display_name,omitempty"`
	Description                string   `json:"description,omitempty"`
	OwnedBy                    string   `json:"owned_by,omitempty"`
	Created                    *int64   `json:"created,omitempty"`
	CreatedAt                  string   `json:"created_at,omitempty"`
	InputTokenLimit            *int64   `json:"input_token_limit,omitempty"`
	OutputTokenLimit           *int64   `json:"output_token_limit,omitempty"`
	SupportedGenerationMethods []string `json:"supported_generation_methods,omitempty"`
}

type DiscoveryResult struct {
	Models   []DiscoveredModel `json:"models"`
	Complete bool              `json:"complete"`
	Pages    int               `json:"pages"`
	Warnings []string          `json:"warnings"`
}

type discoveryLimits struct {
	pages, models       int
	pageBytes, allBytes int64
	timeout             time.Duration
}

var defaultDiscoveryLimits = discoveryLimits{pages: 10, models: 1000, pageBytes: 2 << 20, allBytes: 8 << 20, timeout: 20 * time.Second}

func invalidDiscovery(message string) error {
	return &Error{Status: http.StatusBadRequest, Code: "invalid_discovery_input", Message: message}
}

func validateDiscovery(in DiscoveryInput) error {
	switch in.Kind {
	case "openai", "compatible", "anthropic", "gemini", "azure":
	default:
		return invalidDiscovery("Choose a supported channel kind.")
	}
	u, err := url.Parse(in.BaseURL)
	if err != nil || len(in.BaseURL) > 4096 || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return invalidDiscovery("Provide an absolute HTTP(S) base URL without credentials, query or fragment.")
	}
	if strings.Contains(strings.ToLower(u.Hostname()), "your-") {
		return invalidDiscovery("Replace the provider hostname placeholder with the account's actual endpoint.")
	}
	if in.APIKey == "" || len(in.APIKey) > 8192 || invalidDiscoveryHeader(in.APIKey) {
		return invalidDiscovery("Provide a valid upstream credential.")
	}
	if len(in.APIVersion) > 100 || invalidDiscoveryHeader(in.APIVersion) {
		return invalidDiscovery("Provide a valid upstream API version.")
	}
	return nil
}

func invalidDiscoveryHeader(value string) bool {
	for _, ch := range []byte(value) {
		if ch < 32 || ch == 127 {
			return true
		}
	}
	return false
}

// DiscoverChannel reads a saved, possibly disabled account without modifying
// its routing health, model mapping, catalog, billing or upstream affinity.
func (s *Service) DiscoverChannel(ctx context.Context, id int64) (DiscoveryResult, error) {
	var c core.Channel
	if err := s.store.DB().WithContext(ctx).First(&c, id).Error; err != nil {
		return DiscoveryResult{}, err
	}
	key, err := s.store.ChannelKey(c)
	if err != nil {
		return DiscoveryResult{}, err
	}
	return s.DiscoverPreview(ctx, DiscoveryInput{Kind: c.Kind, BaseURL: c.BaseURL, APIKey: key, APIVersion: c.APIVersion})
}

// DiscoverPreview lists native models with the relay's existing no-redirect
// HTTP transport. The operation is bounded and makes only read-only GETs.
func (s *Service) DiscoverPreview(ctx context.Context, in DiscoveryInput) (DiscoveryResult, error) {
	if err := validateDiscovery(in); err != nil {
		return DiscoveryResult{}, err
	}
	return s.discoverModels(ctx, in, defaultDiscoveryLimits)
}

func (s *Service) discoverModels(ctx context.Context, in DiscoveryInput, limits discoveryLimits) (DiscoveryResult, error) {
	out := DiscoveryResult{Models: []DiscoveredModel{}, Complete: true, Warnings: []string{}}
	warn := func(message string) {
		out.Complete = false
		for _, existing := range out.Warnings {
			if existing == message {
				return
			}
		}
		out.Warnings = append(out.Warnings, message)
	}
	if in.Kind == "azure" {
		warn("Azure model offerings are not deployment identifiers. Enter the deployment names manually and confirm them with the operator.")
		return out, nil
	}
	ctx, cancel := context.WithTimeout(ctx, limits.timeout)
	defer cancel()
	seenIDs := map[string]bool{}
	seenCursors := map[string]bool{}
	cursor := ""
	var totalBytes int64
	fail := func(message string) (DiscoveryResult, error) {
		if out.Pages == 0 {
			return DiscoveryResult{}, &Error{Status: http.StatusBadGateway, Code: "discovery_failed", Message: message}
		}
		warn(message + " The returned list is partial; retry or enter remaining models manually.")
		return out, nil
	}
	for out.Pages < limits.pages {
		address, err := discoveryURL(in, cursor)
		if err != nil {
			return fail("The upstream model list address could not be constructed.")
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, address.String(), nil)
		if err != nil {
			return fail("The upstream model list request could not be constructed.")
		}
		req.Header.Set("Accept", "application/json")
		switch in.Kind {
		case "anthropic":
			req.Header.Set("x-api-key", in.APIKey)
			version := in.APIVersion
			if version == "" {
				version = s.Config.DefaultAnthropicVersion
				if version == "" {
					version = "2023-06-01"
				}
			}
			req.Header.Set("anthropic-version", version)
		case "gemini":
			req.Header.Set("x-goog-api-key", in.APIKey)
		default:
			req.Header.Set("Authorization", "Bearer "+in.APIKey)
		}
		resp, err := s.client.Do(req)
		if err != nil {
			return fail("The upstream model list could not be read within the discovery deadline.")
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			resp.Body.Close()
			// Never return an upstream error body or a redirect Location: either
			// can contain the account credential or unrelated private data.
			return fail("The upstream rejected model discovery. Check the account or use manual model entry.")
		}
		remaining := limits.allBytes - totalBytes
		bound := min(limits.pageBytes, remaining)
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, bound+1))
		resp.Body.Close()
		if readErr != nil {
			return fail("The upstream model list response could not be fully read.")
		}
		if int64(len(body)) > bound {
			return fail("The upstream model list exceeded the discovery response size limit.")
		}
		totalBytes += int64(len(body))
		models, next, more, limited, err := decodeDiscoveryPage(in.Kind, body, limits.models+1)
		if err != nil {
			return fail("The upstream model list has an unsupported or invalid response format.")
		}
		out.Pages++
		for _, model := range models {
			if !validDiscoveredModel(model, in.APIKey) {
				warn("Some upstream model entries contain invalid metadata and were omitted. Confirm them manually.")
				continue
			}
			if seenIDs[model.ID] {
				continue
			}
			if len(out.Models) >= limits.models {
				warn("The discovery model count limit was reached. Enter remaining models manually.")
				return out, nil
			}
			seenIDs[model.ID] = true
			out.Models = append(out.Models, model)
		}
		if limited {
			warn("The discovery model count limit was reached. Enter remaining models manually.")
			return out, nil
		}
		if !more {
			return out, nil
		}
		if in.Kind != "anthropic" && in.Kind != "gemini" {
			warn("This compatible upstream reports more models but its pagination is unsupported. Enter remaining models manually.")
			return out, nil
		}
		if next == "" || len(next) > 4096 || strings.Contains(next, in.APIKey) || seenCursors[next] {
			warn("The upstream returned a missing, repeated or invalid pagination cursor. Enter remaining models manually.")
			return out, nil
		}
		seenCursors[next] = true
		cursor = next
		if totalBytes >= limits.allBytes {
			warn("The total discovery response size limit was reached. Enter remaining models manually.")
			return out, nil
		}
	}
	warn("The discovery page limit was reached. Enter remaining models manually.")
	return out, nil
}

func discoveryURL(in DiscoveryInput, cursor string) (*url.URL, error) {
	u, err := url.Parse(in.BaseURL)
	if err != nil {
		return nil, err
	}
	root := strings.TrimRight(u.Path, "/")
	query := url.Values{}
	if in.Kind == "gemini" {
		root = strings.TrimSuffix(root, "/v1beta")
		u.Path = root + "/v1beta/models"
		query.Set("pageSize", "100")
		if cursor != "" {
			query.Set("pageToken", cursor)
		}
	} else {
		root = strings.TrimSuffix(root, "/v1")
		u.Path = root + "/v1/models"
		if in.Kind == "anthropic" {
			query.Set("limit", "100")
			if cursor != "" {
				query.Set("after_id", cursor)
			}
		}
	}
	u.RawPath = ""
	u.RawQuery = query.Encode()
	return u, nil
}

type discoveryPage struct {
	Data          json.RawMessage `json:"data"`
	Models        json.RawMessage `json:"models"`
	HasMore       *bool           `json:"has_more"`
	LastID        string          `json:"last_id"`
	NextPageToken string          `json:"nextPageToken"`
	Error         json.RawMessage `json:"error"`
}

type discoveryNativeModel struct {
	ID                         string   `json:"id"`
	Name                       string   `json:"name"`
	DisplayName                string   `json:"display_name"`
	GeminiDisplayName          string   `json:"displayName"`
	Description                string   `json:"description"`
	OwnedBy                    string   `json:"owned_by"`
	Created                    *int64   `json:"created"`
	CreatedAt                  string   `json:"created_at"`
	MaxInputTokens             *int64   `json:"max_input_tokens"`
	MaxTokens                  *int64   `json:"max_tokens"`
	InputTokenLimit            *int64   `json:"inputTokenLimit"`
	OutputTokenLimit           *int64   `json:"outputTokenLimit"`
	SupportedGenerationMethods []string `json:"supportedGenerationMethods"`
}

func decodeDiscoveryPage(kind string, body []byte, recordLimit int) ([]DiscoveredModel, string, bool, bool, error) {
	var page discoveryPage
	if err := json.Unmarshal(body, &page); err != nil || strings.TrimSpace(string(body)) == "null" || len(page.Error) != 0 {
		return nil, "", false, false, io.ErrUnexpectedEOF
	}
	data := page.Data
	next, more := page.LastID, page.HasMore != nil && *page.HasMore
	if kind == "anthropic" && page.HasMore == nil {
		return nil, "", false, false, io.ErrUnexpectedEOF
	}
	if kind == "gemini" {
		data = page.Models
		next, more = page.NextPageToken, page.NextPageToken != ""
		// Protobuf JSON can omit an empty repeated models field.
		if len(data) == 0 && len(page.Data) == 0 {
			data = json.RawMessage("[]")
		}
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('[') {
		return nil, "", false, false, io.ErrUnexpectedEOF
	}
	out := []DiscoveredModel{}
	for decoder.More() {
		if len(out) >= recordLimit {
			return out, next, more, true, nil
		}
		var model discoveryNativeModel
		if err = decoder.Decode(&model); err != nil {
			return nil, "", false, false, err
		}
		record := DiscoveredModel{ID: model.ID, DisplayName: model.DisplayName, Description: model.Description, OwnedBy: model.OwnedBy, Created: model.Created}
		if kind == "anthropic" {
			record.CreatedAt = model.CreatedAt
			record.InputTokenLimit = model.MaxInputTokens
			record.OutputTokenLimit = model.MaxTokens
		}
		if kind == "gemini" {
			record.ID = strings.TrimPrefix(model.Name, "models/")
			record.NativeName = model.Name
			record.DisplayName = model.GeminiDisplayName
			record.InputTokenLimit = model.InputTokenLimit
			record.OutputTokenLimit = model.OutputTokenLimit
			record.SupportedGenerationMethods = model.SupportedGenerationMethods
		}
		out = append(out, record)
	}
	if _, err = decoder.Token(); err != nil {
		return nil, "", false, false, err
	}
	return out, next, more, false, nil
}

func validDiscoveredModel(model DiscoveredModel, key string) bool {
	if strings.TrimSpace(model.ID) == "" || len(model.ID) > 1024 || len(model.NativeName) > 2048 || len(model.DisplayName) > 4096 || len(model.Description) > 16384 || len(model.OwnedBy) > 1024 || len(model.CreatedAt) > 100 || len(model.SupportedGenerationMethods) > 64 {
		return false
	}
	if strings.ContainsAny(model.ID, "\r\n\t") || (model.NativeName != "" && (!strings.HasPrefix(model.NativeName, "models/") || strings.ContainsAny(model.ID, "/\\?#"))) {
		return false
	}
	for _, value := range append([]string{model.ID, model.NativeName, model.DisplayName, model.Description, model.OwnedBy, model.CreatedAt}, model.SupportedGenerationMethods...) {
		if strings.Contains(value, key) || strings.ContainsAny(value, "\x00\r") {
			return false
		}
	}
	for _, method := range model.SupportedGenerationMethods {
		if len(method) > 256 {
			return false
		}
	}
	for _, value := range []*int64{model.Created, model.InputTokenLimit, model.OutputTokenLimit} {
		if value != nil && *value < 0 {
			return false
		}
	}
	return true
}
