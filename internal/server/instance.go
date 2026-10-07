// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	v1 "github.com/nomifun/nomifun-model-gateway/contract/v1"
	"github.com/nomifun/nomifun-model-gateway/internal/channel"
	"github.com/nomifun/nomifun-model-gateway/internal/relay"
	"github.com/nomifun/nomifun-model-gateway/internal/store"
)

type InstanceSettings struct {
	Operator            v1.Operator   `json:"operator"`
	RegistrationEnabled bool          `json:"registration_enabled"`
	Capabilities        []v1.Endpoint `json:"capabilities"`
	OptionalEndpoints   []string      `json:"optional_endpoints"`
	Currency            string        `json:"currency"`
}

func defaultSettings() InstanceSettings {
	return InstanceSettings{Operator: v1.Operator{Name: "Unconfigured Community Operator"}, Capabilities: []v1.Endpoint{v1.OpenAI, v1.Responses, v1.Anthropic, v1.Gemini, v1.Images, v1.Embeddings, v1.Rerank}, OptionalEndpoints: []string{}, Currency: "USD"}
}
func validateSettings(settings InstanceSettings) error {
	if strings.TrimSpace(settings.Operator.Name) == "" || len(settings.Operator.Name) > 200 {
		return errors.New("operator name is required")
	}
	if len(settings.Currency) != 3 || settings.Currency[0] < 'A' || settings.Currency[0] > 'Z' || settings.Currency[1] < 'A' || settings.Currency[1] > 'Z' || settings.Currency[2] < 'A' || settings.Currency[2] > 'Z' {
		return errors.New("currency must contain three uppercase letters")
	}
	for _, field := range []*string{settings.Operator.HomepageURL, settings.Operator.ConsoleURL, settings.Operator.PurchaseURL, settings.Operator.TermsURL, settings.Operator.PrivacyURL} {
		if field == nil {
			continue
		}
		u, err := url.Parse(*field)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" || u.Query().Has("key") {
			return errors.New("operator URLs must be HTTPS without credentials")
		}
	}
	if settings.Capabilities == nil || settings.OptionalEndpoints == nil {
		return errors.New("capabilities and optional endpoints must be arrays")
	}
	known := map[v1.Endpoint]bool{v1.OpenAI: true, v1.Responses: true, v1.Anthropic: true, v1.Gemini: true, v1.Images: true, v1.Embeddings: true, v1.Rerank: true}
	seen := map[v1.Endpoint]bool{}
	for _, endpoint := range settings.Capabilities {
		if !known[endpoint] || seen[endpoint] {
			return errors.New("invalid or duplicate capability")
		}
		seen[endpoint] = true
	}
	// count_tokens has no billable terminal usage and is kept undeclared until
	// a separate non-inference metering policy is available.
	if len(settings.OptionalEndpoints) != 0 {
		return errors.New("no optional endpoint is currently declared")
	}
	return nil
}
func (s *Server) instance(ctx context.Context) (InstanceSettings, error) {
	b, err := s.Store.GetSetting(ctx, "instance")
	if errors.Is(err, store.ErrNotFound) {
		return defaultSettings(), nil
	}
	if err != nil {
		return InstanceSettings{}, err
	}
	var settings InstanceSettings
	if json.Unmarshal(b, &settings) != nil {
		return InstanceSettings{}, errors.New("invalid stored instance settings")
	}
	if settings.Currency == "" {
		settings.Currency = "USD"
	}
	if validateSettings(settings) != nil {
		return InstanceSettings{}, errors.New("invalid stored instance settings")
	}
	return settings, nil
}
func (settings InstanceSettings) meta() v1.Meta {
	return v1.Meta{ContractVersion: v1.Version, Operator: settings.Operator, Capabilities: settings.Capabilities, OptionalEndpoints: settings.OptionalEndpoints}
}
func (s *Server) meta(c *gin.Context) {
	settings, err := s.instance(c.Request.Context())
	if s.databaseError(c, err) {
		return
	}
	c.JSON(200, settings.meta())
}
func (s *Server) catalog(c *gin.Context) {
	catalog, err := s.Store.CatalogForKey(c.Request.Context(), *key(c))
	if s.databaseError(c, err) {
		return
	}
	settings, err := s.instance(c.Request.Context())
	if s.databaseError(c, err) {
		return
	}
	catalog.Models = filterCatalog(catalog.Models, settings.Capabilities)
	c.JSON(200, catalog)
}
func filterCatalog(models []v1.Model, capabilities []v1.Endpoint) []v1.Model {
	enabled := map[v1.Endpoint]bool{}
	for _, capability := range capabilities {
		enabled[capability] = true
	}
	out := []v1.Model{}
	for _, model := range models {
		valid := true
		for _, route := range model.TaskEndpoints {
			for _, endpoint := range route.Endpoints {
				if !enabled[endpoint] {
					valid = false
				}
			}
		}
		if valid {
			out = append(out, model)
		}
	}
	return out
}
func (s *Server) account(c *gin.Context) {
	account, err := s.Billing.Account(c.Request.Context(), user(c).ID, key(c).ID)
	if s.databaseError(c, err) {
		return
	}
	c.JSON(200, account)
}
func (s *Server) models(c *gin.Context) {
	catalog, err := s.Store.CatalogForKey(c.Request.Context(), *key(c))
	if s.databaseError(c, err) {
		return
	}
	settings, err := s.instance(c.Request.Context())
	if s.databaseError(c, err) {
		return
	}
	data := []gin.H{}
	for _, model := range filterCatalog(catalog.Models, settings.Capabilities) {
		data = append(data, gin.H{"id": model.ID, "object": "model", "owned_by": model.Vendor, "created": 0})
	}
	c.JSON(200, gin.H{"object": "list", "data": data})
}
func (s *Server) native(c *gin.Context) {
	if geminiCredentialRoute(c.Request.URL.Path) {
		if values, present := c.Request.URL.Query()["alt"]; present && (len(values) != 1 || values[0] != "sse") {
			s.fail(c, &relay.Error{Status: 400, Code: "invalid_request_error", Message: "Gemini supports one alt=sse value."})
			return
		}
	}
	controller := http.NewResponseController(c.Writer)
	_ = controller.SetReadDeadline(time.Now().Add(60 * time.Second))
	native, err := relay.InspectRequest(c.Request, s.Relay.Config.RequestBodyLimit)
	_ = controller.SetReadDeadline(time.Time{})
	if err != nil {
		s.fail(c, err)
		return
	}
	if c.Request.URL.Path == "/v1/messages/count_tokens" {
		s.fail(c, &relay.Error{Status: 501, Code: "unsupported_endpoint", Message: "This optional endpoint is not declared by this instance."})
		return
	}
	settings, err := s.instance(c.Request.Context())
	if s.databaseError(c, err) {
		return
	}
	supported := false
	for _, endpoint := range settings.Capabilities {
		supported = supported || endpoint == native.Endpoint
	}
	if !supported {
		s.fail(c, &relay.Error{Status: 501, Code: "unsupported_endpoint", Message: "This native endpoint is not declared by this instance."})
		return
	}
	catalog, err := s.Store.CatalogForKey(c.Request.Context(), *key(c))
	if s.databaseError(c, err) {
		return
	}
	// Key scope is independent of subscription entitlement. A known configured
	// subscription-only model returns the billing business error even when the
	// current catalog hides it; other keys cannot discover its identity.
	record, err := s.Store.ModelRecord(c.Request.Context(), native.Model)
	if err != nil || !record.Enabled {
		s.fail(c, &relay.Error{Status: 404, Code: "model_not_found", Message: "Model is not in this key's visible catalog."})
		return
	}
	var permitted []string
	if json.Unmarshal([]byte(key(c).ModelIDsJSON), &permitted) != nil {
		s.fail(c, store.ErrUnauthorized)
		return
	}
	allowed := len(permitted) == 0
	for _, id := range permitted {
		allowed = allowed || id == native.Model
	}
	if !allowed {
		s.fail(c, &relay.Error{Status: 404, Code: "model_not_found", Message: "Model is not in this key's visible catalog."})
		return
	}
	model, err := s.Store.Model(c.Request.Context(), native.Model)
	if s.databaseError(c, err) {
		return
	}
	model.IncludedInPlan = false
	for _, visible := range catalog.Models {
		if visible.ID == native.Model {
			model.IncludedInPlan = visible.IncludedInPlan
		}
	}
	if len(filterCatalog([]v1.Model{model}, settings.Capabilities)) == 0 {
		s.fail(c, &relay.Error{Status: 400, Code: "unsupported_endpoint", Message: "This model references an endpoint disabled by the operator."})
		return
	}
	// Validate the durable Responses binding before a retained-history estimate
	// can disclose whether a previous ID exists. Candidates takes the fixed,
	// read-only binding path here; it never creates an Anthropic session.
	if native.PreviousResponseID != "" {
		if _, _, err := s.Relay.Channels.Candidates(c.Request.Context(), key(c).ID, native.Model, string(native.Endpoint), native.PreviousResponseID, ""); err != nil {
			if errors.Is(err, channel.ErrAffinity) {
				s.fail(c, &relay.Error{Status: 400, Code: "invalid_previous_response_id", Message: "The requested upstream binding is unavailable."})
			} else {
				s.fail(c, &relay.Error{Status: 503, Code: "upstream_unavailable", Message: "The gateway could not complete this request."})
			}
			return
		}
	}
	estimate, err := relay.EstimateUsage(native, model)
	if err != nil {
		s.fail(c, err)
		return
	}
	release, retry, ok := s.limits.acquire("key:"+strconvID(key(c).ID), estimate.TotalTokens, key(c).RequestsPerMinute, key(c).TokensPerMinute, key(c).ConcurrentRequests)
	if !ok {
		c.Header("Retry-After", retrySeconds(retry))
		relay.WriteError(c.Writer, c.Request, &relay.Error{Status: 429, Code: "rate_limited", Message: "This API key's request limit is temporarily exhausted."}, purchase(settings))
		c.Abort()
		return
	}
	defer release()
	s.Relay.Serve(c.Writer, c.Request, relay.Request{Key: *key(c), Model: model, RequireSubscription: record.SubscriptionOnly, RequestID: c.Writer.Header().Get("x-request-id"), Native: &native, PurchaseURL: purchase(settings)})
}
func purchase(settings InstanceSettings) string {
	if settings.Operator.PurchaseURL != nil {
		return *settings.Operator.PurchaseURL
	}
	return ""
}
