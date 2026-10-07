// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	v1 "github.com/nomifun/nomifun-model-gateway/contract/v1"
	"github.com/nomifun/nomifun-model-gateway/internal/core"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ValidateModel enforces frozen v1 cross-field semantics before durable writes.
func ValidateModel(m v1.Model) error {
	if strings.TrimSpace(m.ID) == "" || len(m.ID) > 200 || strings.TrimSpace(m.DisplayName) == "" || strings.TrimSpace(m.Vendor) == "" {
		return errors.New("model id, display name and vendor are required")
	}
	if len(m.Tasks) == 0 || len(m.Tasks) != len(m.TaskEndpoints) {
		return errors.New("tasks and task endpoints must match exactly")
	}
	if m.InputModalities == nil || m.Traits == nil || m.Pricing == nil {
		return errors.New("input_modalities, traits and pricing must be arrays")
	}
	if err := validateStringList(m.InputModalities); err != nil {
		return err
	}
	if err := validateStringList(m.Traits); err != nil {
		return err
	}
	if m.ContextWindow != nil && *m.ContextWindow <= 0 || m.MaxOutputTokens != nil && *m.MaxOutputTokens <= 0 {
		return errors.New("token ceilings must be positive when provided")
	}
	allowed := map[v1.Task]map[v1.Endpoint]bool{
		v1.Chat:            {v1.OpenAI: true, v1.Responses: true, v1.Anthropic: true, v1.Gemini: true},
		v1.ImageGeneration: {v1.Images: true}, v1.ImageEdit: {v1.Images: true}, v1.Embedding: {v1.Embeddings: true}, v1.Reranking: {v1.Rerank: true},
	}
	seenTasks := map[v1.Task]bool{}
	for _, task := range m.Tasks {
		if allowed[task] == nil || seenTasks[task] {
			return errors.New("unknown or duplicate model task")
		}
		seenTasks[task] = true
		route, ok := m.TaskEndpoints[task]
		if !ok || len(route.Endpoints) == 0 || !allowed[task][route.PreferredEndpoint] {
			return errors.New("task requires a recognized preferred endpoint")
		}
		seenEndpoints := map[v1.Endpoint]bool{}
		for _, endpoint := range route.Endpoints {
			if !allowed[task][endpoint] || seenEndpoints[endpoint] {
				return errors.New("unknown or duplicate task endpoint")
			}
			seenEndpoints[endpoint] = true
		}
		if !seenEndpoints[route.PreferredEndpoint] {
			return errors.New("preferred endpoint must occur in task endpoints")
		}
		if route.PreferredEndpoint == v1.Anthropic && m.MaxOutputTokens == nil {
			return errors.New("Anthropic preferred endpoint requires max_output_tokens")
		}
	}
	seenPrices := map[string]bool{}
	currency := ""
	for _, p := range m.Pricing {
		key := string(p.Task) + "\x00" + p.Meter + "\x00" + p.Currency
		if !seenTasks[p.Task] || strings.TrimSpace(p.Meter) == "" || p.UnitSize <= 0 || p.Amount < 0 || !validCurrency(p.Currency) || seenPrices[key] {
			return errors.New("invalid or duplicate task pricing")
		}
		if currency != "" && currency != p.Currency {
			return errors.New("model prices must use a single currency")
		}
		currency = p.Currency
		seenPrices[key] = true
	}
	if m.Status != "available" && m.Status != "degraded" && m.Status != "unavailable" {
		return errors.New("invalid model status")
	}
	return nil
}

func (s *Store) SaveModel(ctx context.Context, definition v1.Model, enabled bool) error {
	if err := ValidateModel(definition); err != nil {
		return err
	}
	b, err := json.Marshal(definition)
	if err != nil {
		return err
	}
	m := core.Model{ID: definition.ID, DefinitionJSON: string(b), Enabled: enabled}
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}}, DoUpdates: clause.AssignmentColumns([]string{"definition_json", "enabled", "updated_at"})}).Create(&m).Error
}

// SaveModelAccess updates the wire definition and durable admission policy
// together. IncludedInPlan is computed per account at catalog read time.
func (s *Store) SaveModelAccess(ctx context.Context, definition v1.Model, enabled, subscriptionOnly bool) error {
	if err := ValidateModel(definition); err != nil {
		return err
	}
	definition.IncludedInPlan = false
	b, err := json.Marshal(definition)
	if err != nil {
		return err
	}
	m := core.Model{ID: definition.ID, DefinitionJSON: string(b), Enabled: enabled, SubscriptionOnly: subscriptionOnly}
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}}, DoUpdates: clause.AssignmentColumns([]string{"definition_json", "enabled", "subscription_only", "updated_at"})}).Create(&m).Error
}

func (s *Store) ModelRecord(ctx context.Context, id string) (core.Model, error) {
	var row core.Model
	err := s.db.WithContext(ctx).Where("id = ?", id).First(&row).Error
	return row, dbError(err)
}
func (s *Store) ListModelRecords(ctx context.Context) ([]core.Model, error) {
	rows := []core.Model{}
	err := s.db.WithContext(ctx).Order("id").Find(&rows).Error
	return rows, err
}
func (s *Store) Model(ctx context.Context, id string) (v1.Model, error) {
	var row core.Model
	if err := s.db.WithContext(ctx).Where("id = ?", id).First(&row).Error; err != nil {
		return v1.Model{}, dbError(err)
	}
	var model v1.Model
	if err := json.Unmarshal([]byte(row.DefinitionJSON), &model); err != nil {
		return v1.Model{}, errors.New("stored model definition is malformed")
	}
	if err := ValidateModel(model); err != nil {
		return v1.Model{}, errors.New("stored model definition violates protocol")
	}
	return model, nil
}
func (s *Store) ListModels(ctx context.Context, enabledOnly bool) ([]v1.Model, error) {
	var rows []core.Model
	q := s.db.WithContext(ctx).Order("id")
	if enabledOnly {
		q = q.Where("enabled = ?", true)
	}
	if err := q.Find(&rows).Error; err != nil {
		return nil, err
	}
	out := []v1.Model{}
	for _, row := range rows {
		var model v1.Model
		if json.Unmarshal([]byte(row.DefinitionJSON), &model) != nil || ValidateModel(model) != nil {
			return nil, errors.New("stored model definition violates protocol")
		}
		out = append(out, model)
	}
	return out, nil
}
func (s *Store) DeleteModel(ctx context.Context, id string) error {
	r := s.db.WithContext(ctx).Where("id = ?", id).Delete(&core.Model{})
	if r.Error != nil {
		return r.Error
	}
	if r.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) CatalogForKey(ctx context.Context, key core.APIKey) (v1.Catalog, error) {
	var rows []core.Model
	if err := s.db.WithContext(ctx).Where("enabled = ?", true).Order("id").Find(&rows).Error; err != nil {
		return v1.Catalog{}, err
	}
	var allowed []string
	if json.Unmarshal([]byte(key.ModelIDsJSON), &allowed) != nil || allowed == nil {
		return v1.Catalog{}, ErrUnauthorized
	}
	permitted := map[string]bool{}
	for _, id := range allowed {
		permitted[id] = true
	}
	var subscription core.Subscription
	now := time.Now().UTC()
	subErr := s.db.WithContext(ctx).Where("user_id = ?", key.UserID).Order("id desc").First(&subscription).Error
	if subErr != nil && !errors.Is(subErr, gorm.ErrRecordNotFound) {
		return v1.Catalog{}, subErr
	}
	coverage := map[string]bool{}
	allCovered := false
	if subErr == nil && !subscription.PeriodStart.After(now) && subscription.PeriodEnd.After(now) {
		var ids []string
		if json.Unmarshal([]byte(subscription.ModelIDsJSON), &ids) != nil || ids == nil {
			return v1.Catalog{}, errors.New("stored subscription model permissions are malformed")
		}
		if len(ids) == 0 {
			allCovered = true
		}
		for _, id := range ids {
			coverage[id] = true
		}
	}
	out := v1.Catalog{ContractVersion: v1.Version, Models: []v1.Model{}}
	for _, row := range rows {
		var model v1.Model
		if json.Unmarshal([]byte(row.DefinitionJSON), &model) != nil || ValidateModel(model) != nil {
			return v1.Catalog{}, errors.New("stored model definition violates protocol")
		}
		model.IncludedInPlan = allCovered || coverage[model.ID]
		if row.SubscriptionOnly && !model.IncludedInPlan {
			continue
		}
		if len(allowed) == 0 || permitted[model.ID] {
			out.Models = append(out.Models, model)
		}
	}
	return out, nil
}
