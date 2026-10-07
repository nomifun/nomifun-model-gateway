// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"encoding/json"
	"errors"

	v1 "github.com/nomifun/nomifun-model-gateway/contract/v1"
	"github.com/nomifun/nomifun-model-gateway/internal/core"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// OnboardingModel retains the existing catalog and admission policy together.
type OnboardingModel struct {
	Definition       v1.Model
	Enabled          bool
	SubscriptionOnly bool
}

// catalogTransaction serializes admin publication and routing changes across
// gateway processes sharing PostgreSQL. Individual model/channel row locks
// alone cannot prevent a publication check racing removal of its final route.
// The advisory lock belongs to this transaction and releases on commit/rollback;
// it is acquired before any catalog or routing snapshot is read. SQLite already
// serializes this store's transactions through its single writer connection.
func (s *Store) catalogTransaction(ctx context.Context, fn func(*gorm.DB) error) error {
	return s.Transaction(ctx, func(tx *gorm.DB) error {
		if tx.Dialector.Name() == "postgres" {
			const gatewayCatalogLock int64 = 0x4e4d470000000001 // NMG catalog mutation namespace.
			if err := tx.Exec("SELECT pg_advisory_xact_lock(?)", gatewayCatalogLock).Error; err != nil {
				return err
			}
		}
		return fn(tx)
	})
}

func modelAccessRecord(definition v1.Model, enabled, subscriptionOnly bool) (core.Model, error) {
	if err := ValidateModel(definition); err != nil {
		return core.Model{}, err
	}
	definition.IncludedInPlan = false
	b, err := json.Marshal(definition)
	return core.Model{ID: definition.ID, DefinitionJSON: string(b), Enabled: enabled, SubscriptionOnly: subscriptionOnly}, err
}

// CreateOnboarding saves a complete channel and new model set in one transaction.
// validate runs after assigning the channel identity, before catalog writes; any
// validation, catalog conflict or audit failure rolls back the entire operation.
func (s *Store) CreateOnboarding(ctx context.Context, c core.Channel, plainKey string, models []OnboardingModel, validate func(*gorm.DB, core.Channel) error) (core.Channel, error) {
	if c.ID != 0 || plainKey == "" || len(models) == 0 || len(models) > 200 {
		return core.Channel{}, errors.New("onboarding requires a new channel, credential and 1 to 200 models")
	}
	if err := ValidateChannel(c); err != nil {
		return core.Channel{}, err
	}
	rows := make([]core.Model, 0, len(models))
	seen := map[string]bool{}
	for _, model := range models {
		row, err := modelAccessRecord(model.Definition, model.Enabled, model.SubscriptionOnly)
		if err != nil {
			return core.Channel{}, err
		}
		if seen[row.ID] {
			return core.Channel{}, ErrConflict
		}
		seen[row.ID] = true
		rows = append(rows, row)
	}
	err := s.catalogTransaction(ctx, func(tx *gorm.DB) error {
		c.EncryptedKey = ""
		if err := tx.Create(&c).Error; err != nil {
			return err
		}
		sealed, err := s.cipher.Encrypt(plainKey, channelPurpose(c.ID))
		if err != nil {
			return err
		}
		c.EncryptedKey = sealed
		if err := tx.Model(&core.Channel{}).Where("id = ?", c.ID).Update("encrypted_key", sealed).Error; err != nil {
			return err
		}
		if validate != nil {
			if err := validate(tx, c); err != nil {
				return err
			}
		}
		// No upsert: an already present public ID must never be overwritten.
		return tx.Create(&rows).Error
	})
	if err != nil {
		return core.Channel{}, dbError(err)
	}
	return c, nil
}

// CreateModelAccess and UpdateModelAccess distinguish creation from editing.
// The validator shares the write transaction for readiness and audit checks.
func (s *Store) CreateModelAccess(ctx context.Context, definition v1.Model, enabled, subscriptionOnly bool, validate func(*gorm.DB) error) error {
	return s.writeModelAccess(ctx, definition, enabled, subscriptionOnly, true, validate)
}

func (s *Store) UpdateModelAccess(ctx context.Context, definition v1.Model, enabled, subscriptionOnly bool, validate func(*gorm.DB) error) error {
	return s.writeModelAccess(ctx, definition, enabled, subscriptionOnly, false, validate)
}

func (s *Store) writeModelAccess(ctx context.Context, definition v1.Model, enabled, subscriptionOnly, create bool, validate func(*gorm.DB) error) error {
	row, err := modelAccessRecord(definition, enabled, subscriptionOnly)
	if err != nil {
		return err
	}
	return dbError(s.catalogTransaction(ctx, func(tx *gorm.DB) error {
		var existing core.Model
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", row.ID).First(&existing).Error
		if create && err == nil {
			return ErrConflict
		}
		if !create && errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrNotFound
		}
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if validate != nil {
			if err := validate(tx); err != nil {
				return err
			}
		}
		if create {
			return tx.Create(&row).Error
		}
		result := tx.Model(&core.Model{}).Where("id = ?", row.ID).Updates(map[string]any{"definition_json": row.DefinitionJSON, "enabled": row.Enabled, "subscription_only": row.SubscriptionOnly})
		if result.Error == nil && result.RowsAffected == 0 {
			return ErrNotFound
		}
		return result.Error
	}))
}

// UpdateModelPricing changes only pricing under the row lock, preserving the
// latest catalog and access settings when another administrator edits them.
func (s *Store) UpdateModelPricing(ctx context.Context, id string, pricing []v1.Price, validate func(*gorm.DB, OnboardingModel) error) error {
	return dbError(s.catalogTransaction(ctx, func(tx *gorm.DB) error {
		var row core.Model
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", id).First(&row).Error; err != nil {
			return err
		}
		var definition v1.Model
		if err := json.Unmarshal([]byte(row.DefinitionJSON), &definition); err != nil {
			return errors.New("stored model definition is malformed")
		}
		definition.Pricing = pricing
		updated, err := modelAccessRecord(definition, row.Enabled, row.SubscriptionOnly)
		if err != nil {
			return err
		}
		if validate != nil {
			if err := validate(tx, OnboardingModel{Definition: definition, Enabled: row.Enabled, SubscriptionOnly: row.SubscriptionOnly}); err != nil {
				return err
			}
		}
		return tx.Model(&core.Model{}).Where("id = ?", id).Update("definition_json", updated.DefinitionJSON).Error
	}))
}
