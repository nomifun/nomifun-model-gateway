// SPDX-License-Identifier: Apache-2.0
package server

import (
	"encoding/json"
	"errors"
	"math"
	"strings"

	"github.com/gin-gonic/gin"
	v1 "github.com/nomifun/nomifun-model-gateway/contract/v1"
	"github.com/nomifun/nomifun-model-gateway/internal/billing"
	"github.com/nomifun/nomifun-model-gateway/internal/channel"
	"github.com/nomifun/nomifun-model-gateway/internal/core"
	"github.com/nomifun/nomifun-model-gateway/internal/store"
	"gorm.io/gorm"
)

type publicationIssue struct {
	ModelID  string `json:"model_id,omitempty"`
	Field    string `json:"field"`
	Message  string `json:"message"`
	blocking bool
}

type publicationCheck struct {
	Ready  bool               `json:"ready"`
	Issues []publicationIssue `json:"issues"`
}

type publicationError struct {
	Issues   []publicationIssue
	Conflict bool
}

func (*publicationError) Error() string { return "model publication checks failed" }

type onboardingInput struct {
	Channel channelInput `json:"channel"`
	Models  []modelDTO   `json:"models"`
}

func issue(modelID, field, message string, blocking bool) publicationIssue {
	return publicationIssue{ModelID: modelID, Field: field, Message: message, blocking: blocking}
}

func checkResult(issues []publicationIssue) publicationCheck {
	return publicationCheck{Ready: len(issues) == 0, Issues: issues}
}

func publicationFailure(c *gin.Context, err error) bool {
	var failed *publicationError
	if !errors.As(err, &failed) {
		return false
	}
	status, code := 422, "publication_not_ready"
	if failed.Conflict {
		status, code = 409, "conflict"
	}
	c.JSON(status, gin.H{"ready": false, "issues": failed.Issues, "error": gin.H{"code": code, "message": "Resolve the reported fields before saving or publishing."}})
	c.Abort()
	return true
}

// publicationIssuesTx reads the same durable state used by the write. It never
// calls upstreams: channel configuration is evidence of routing, not fidelity.
func (s *Server) publicationIssuesTx(tx *gorm.DB, model modelDTO, extra *core.Channel) ([]publicationIssue, error) {
	issues := []publicationIssue{}
	if err := store.ValidateModel(model.Model); err != nil {
		return append(issues, issue(model.ID, structuralModelField(err), err.Error(), true)), nil
	}
	var channels []core.Channel
	if err := tx.Where("enabled = ?", true).Find(&channels).Error; err != nil {
		return nil, err
	}
	if extra != nil && extra.Enabled {
		channels = append(channels, *extra)
	}
	settings := defaultSettings()
	var row core.Setting
	err := tx.Where("name = ?", "instance").First(&row).Error
	if err == nil {
		if json.Unmarshal([]byte(row.ValueJSON), &settings) != nil || validateSettings(settings) != nil {
			return nil, errors.New("invalid stored instance settings")
		}
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	declared := map[v1.Endpoint]bool{}
	for _, endpoint := range settings.Capabilities {
		declared[endpoint] = true
	}
	chat := false
	for _, task := range model.Tasks {
		route := model.TaskEndpoints[task]
		for _, endpoint := range route.Endpoints {
			field := "task_endpoints." + string(task) + ".endpoints"
			if !declared[endpoint] {
				issues = append(issues, issue(model.ID, field, "Endpoint is not enabled in instance capabilities: "+string(endpoint), false))
			}
			supported := false
			for _, candidate := range channels {
				if candidate.EncryptedKey != "" {
					if _, ok := channel.Supports(candidate, model.ID, string(endpoint)); ok {
						supported = true
						break
					}
				}
			}
			if !supported {
				issues = append(issues, issue(model.ID, field, "No enabled credentialed channel maps this public ID to the native endpoint: "+string(endpoint), false))
			}
		}
		bounds := core.Usage{Requests: 1}
		switch task {
		case v1.Chat:
			chat = true
			bounds.InputTokens, bounds.OutputTokens = 1, 1
			if model.ContextWindow != nil {
				bounds.InputTokens = *model.ContextWindow
			}
			if model.MaxOutputTokens != nil {
				bounds.OutputTokens = *model.MaxOutputTokens
				for _, endpoint := range route.Endpoints {
					if endpoint == v1.OpenAI {
						if bounds.OutputTokens > math.MaxInt64/16 {
							issues = append(issues, issue(model.ID, "max_output_tokens", "Output ceiling exceeds the supported aggregate integer range.", false))
						} else {
							bounds.OutputTokens *= 16
						}
					}
				}
			}
		case v1.Embedding, v1.Reranking:
			bounds.InputTokens = 1
		case v1.ImageGeneration, v1.ImageEdit:
			bounds.Images = 16
		}
		_, currency, priceErr := billing.QuoteUpperBound(model.Model, task, bounds)
		if priceErr != nil {
			issues = append(issues, issue(model.ID, "pricing."+string(task), priceErr.Error(), false))
		} else if currency != settings.Currency {
			issues = append(issues, issue(model.ID, "pricing."+string(task)+".currency", "Price currency must match the instance wallet currency: "+settings.Currency, false))
		}
	}
	if chat && model.ContextWindow == nil {
		issues = append(issues, issue(model.ID, "context_window", "Publishing chat requires a positive context ceiling for opaque or stored native input reservation.", false))
	}
	if chat && model.MaxOutputTokens == nil {
		issues = append(issues, issue(model.ID, "max_output_tokens", "Publishing chat requires a positive output ceiling.", false))
	}
	if model.SubscriptionOnly {
		var plans []core.Plan
		if err := tx.Where("enabled = ? AND currency = ?", true, settings.Currency).Find(&plans).Error; err != nil {
			return nil, err
		}
		covered := false
		for _, plan := range plans {
			var ids []string
			if json.Unmarshal([]byte(plan.ModelIDsJSON), &ids) != nil || ids == nil {
				continue
			}
			if len(ids) == 0 {
				covered = true
			}
			for _, id := range ids {
				covered = covered || id == model.ID
			}
		}
		if !covered {
			issues = append(issues, issue(model.ID, "subscription_only", "Subscription-only publication requires an enabled plan covering this model in the instance currency.", false))
		}
	}
	return issues, nil
}

func structuralModelField(err error) string {
	text := err.Error()
	switch {
	case strings.Contains(text, "max_output"):
		return "max_output_tokens"
	case strings.Contains(text, "pricing"), strings.Contains(text, "prices"):
		return "pricing"
	case strings.Contains(text, "endpoint"), strings.Contains(text, "task"):
		return "task_endpoints"
	case strings.Contains(text, "ceilings"):
		return "context_window"
	case strings.Contains(text, "status"):
		return "status"
	default:
		return "model"
	}
}

func (s *Server) onboardingIssuesTx(tx *gorm.DB, in onboardingInput, candidate core.Channel, extra bool) ([]publicationIssue, error) {
	issues := []publicationIssue{}
	if err := store.ValidateChannel(candidate); err != nil {
		issues = append(issues, issue("", structuralChannelField(err), err.Error(), true))
	}
	if strings.TrimSpace(in.Channel.APIKey) == "" {
		issues = append(issues, issue("", "channel.api_key", "Upstream credential is required.", true))
	}
	if len(in.Models) == 0 || len(in.Models) > 200 {
		issues = append(issues, issue("", "models", "Provide 1 to 200 models.", true))
	}
	seen := map[string]bool{}
	for _, model := range in.Models {
		if strings.TrimSpace(in.Channel.ModelIDs[model.ID]) == "" {
			issues = append(issues, issue(model.ID, "channel.model_ids", "Map this public model ID to its upstream ID before publication.", false))
		}
		if seen[model.ID] {
			issues = append(issues, issue(model.ID, "id", "Public model ID is repeated in this submission.", true))
		}
		seen[model.ID] = true
		var count int64
		if err := tx.Model(&core.Model{}).Where("id = ?", model.ID).Count(&count).Error; err != nil {
			return nil, err
		}
		if count != 0 {
			issues = append(issues, issue(model.ID, "id", "Public model ID already exists; edit it explicitly instead of importing over it.", true))
		}
		var additional *core.Channel
		if extra {
			additional = &candidate
		}
		modelIssues, err := s.publicationIssuesTx(tx, model, additional)
		if err != nil {
			return nil, err
		}
		issues = append(issues, modelIssues...)
	}
	return issues, nil
}

func (s *Server) checkOnboarding(c *gin.Context) {
	var in onboardingInput
	if !s.decode(c, &in) {
		return
	}
	candidate := in.Channel.channel(0)
	if strings.TrimSpace(in.Channel.APIKey) != "" {
		candidate.EncryptedKey = "credential-preview"
	}
	issues, err := s.onboardingIssuesTx(s.Store.DB().WithContext(c.Request.Context()), in, candidate, true)
	if s.databaseError(c, err) {
		return
	}
	c.JSON(200, checkResult(issues))
}

func (s *Server) createOnboarding(c *gin.Context) {
	var in onboardingInput
	if !s.decode(c, &in) {
		return
	}
	models := make([]store.OnboardingModel, 0, len(in.Models))
	enabled := map[string]bool{}
	for _, model := range in.Models {
		models = append(models, store.OnboardingModel{Definition: model.Model, Enabled: model.Enabled, SubscriptionOnly: model.SubscriptionOnly})
		enabled[model.ID] = model.Enabled
	}
	for i := range in.Models {
		in.Models[i].IncludedInPlan = false
	}
	created, err := s.Store.CreateOnboarding(c.Request.Context(), in.Channel.channel(0), in.Channel.APIKey, models, func(tx *gorm.DB, candidate core.Channel) error {
		issues, err := s.onboardingIssuesTx(tx, in, candidate, false)
		if err != nil {
			return err
		}
		blocking := []publicationIssue{}
		conflict := false
		for _, item := range issues {
			if item.blocking || enabled[item.ModelID] {
				blocking = append(blocking, item)
				conflict = conflict || item.Field == "id"
			}
		}
		if len(blocking) > 0 {
			return &publicationError{Issues: blocking, Conflict: conflict}
		}
		return tx.Create(&core.AuditLog{UserID: user(c).ID, Action: "onboarding.create", Resource: strconvID(candidate.ID)}).Error
	})
	if err != nil {
		if !publicationFailure(c, err) {
			s.fail(c, err)
		}
		return
	}
	c.JSON(201, gin.H{"channel": channelView(created), "models": in.Models})
}

func (s *Server) checkModelPublication(c *gin.Context) {
	var model modelDTO
	if !s.decode(c, &model) {
		return
	}
	issues, err := s.publicationIssuesTx(s.Store.DB().WithContext(c.Request.Context()), model, nil)
	if s.databaseError(c, err) {
		return
	}
	c.JSON(200, checkResult(issues))
}

func (s *Server) checkedModelAudit(c *gin.Context, model modelDTO, action string) func(*gorm.DB) error {
	return func(tx *gorm.DB) error {
		if model.Enabled {
			issues, err := s.publicationIssuesTx(tx, model, nil)
			if err != nil {
				return err
			}
			if len(issues) > 0 {
				return &publicationError{Issues: issues}
			}
		}
		return tx.Create(&core.AuditLog{UserID: user(c).ID, Action: action, Resource: model.ID}).Error
	}
}

// Operational channel edits cannot leave an enabled model without its last
// advertised native route. Existing unrelated publication gaps are unaffected.
func (s *Server) checkedChannelAudit(c *gin.Context, action string) func(*gorm.DB, core.Channel, core.Channel) error {
	return func(tx *gorm.DB, old, updated core.Channel) error {
		var channels []core.Channel
		if err := tx.Where("enabled = ? AND id <> ?", true, old.ID).Find(&channels).Error; err != nil {
			return err
		}
		if updated.Enabled {
			channels = append(channels, updated)
		}
		var models []core.Model
		if err := tx.Where("enabled = ?", true).Find(&models).Error; err != nil {
			return err
		}
		issues := []publicationIssue{}
		for _, row := range models {
			var model v1.Model
			if json.Unmarshal([]byte(row.DefinitionJSON), &model) != nil {
				return errors.New("stored model definition is malformed")
			}
			for _, task := range model.Tasks {
				for _, endpoint := range model.TaskEndpoints[task].Endpoints {
					if !old.Enabled {
						continue
					}
					if _, supported := channel.Supports(old, model.ID, string(endpoint)); !supported {
						continue
					}
					remaining := false
					for _, candidate := range channels {
						if candidate.EncryptedKey != "" {
							if _, supported := channel.Supports(candidate, model.ID, string(endpoint)); supported {
								remaining = true
							}
						}
					}
					if !remaining {
						issues = append(issues, issue(model.ID, "channel.model_ids", "This edit removes the last enabled native route for an enabled model. Disable the model or add another channel first: "+string(endpoint), true))
					}
				}
			}
		}
		if len(issues) > 0 {
			return &publicationError{Issues: issues}
		}
		return tx.Create(&core.AuditLog{UserID: user(c).ID, Action: action, Resource: strconvID(updated.ID)}).Error
	}
}

func structuralChannelField(err error) string {
	text := err.Error()
	switch {
	case strings.Contains(text, "URL"), strings.Contains(text, "placeholder"):
		return "channel.base_url"
	case strings.Contains(text, "kind"):
		return "channel.kind"
	case strings.Contains(text, "name"):
		return "channel.name"
	case strings.Contains(text, "model"):
		return "channel.model_ids"
	case strings.Contains(text, "endpoint"):
		return "channel.endpoints"
	case strings.Contains(text, "weight"):
		return "channel.weight"
	default:
		return "channel"
	}
}
