// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"

	v1 "github.com/nomifun/nomifun-model-gateway/contract/v1"
	"github.com/nomifun/nomifun-model-gateway/internal/core"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func ValidateChannel(c core.Channel) error {
	if strings.TrimSpace(c.Name) == "" || len(c.Name) > 200 {
		return errors.New("channel name is required")
	}
	switch c.Kind {
	case "openai", "anthropic", "gemini", "compatible", "azure":
	default:
		return errors.New("unsupported channel kind")
	}
	u, err := url.Parse(c.BaseURL)
	if err != nil || u.Hostname() == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("channel base URL must be an absolute HTTP(S) address without credentials, query or fragment")
	}
	if strings.Contains(strings.ToLower(u.Hostname()), "your-") {
		return errors.New("replace the resource or workspace placeholder in the channel base URL")
	}
	var models map[string]string
	if json.Unmarshal([]byte(c.ModelsJSON), &models) != nil || len(models) == 0 {
		return errors.New("channel models must map public ids to upstream ids")
	}
	for k, v := range models {
		if strings.TrimSpace(k) == "" || strings.TrimSpace(v) == "" {
			return errors.New("channel model ids cannot be empty")
		}
	}
	var endpoints []v1.Endpoint
	if json.Unmarshal([]byte(c.EndpointsJSON), &endpoints) != nil || len(endpoints) == 0 {
		return errors.New("channel endpoints must be a nonempty array")
	}
	valid := map[v1.Endpoint]bool{v1.OpenAI: true, v1.Responses: true, v1.Anthropic: true, v1.Gemini: true, v1.Images: true, v1.Embeddings: true, v1.Rerank: true}
	seen := map[v1.Endpoint]bool{}
	for _, endpoint := range endpoints {
		if !valid[endpoint] || seen[endpoint] {
			return errors.New("channel endpoints must be recognized and unique")
		}
		seen[endpoint] = true
	}
	if c.Weight < 1 || c.Weight > 100000 {
		return errors.New("channel weight must be between 1 and 100000")
	}
	return nil
}

func (s *Store) CreateChannel(ctx context.Context, c core.Channel, plainKey string) (core.Channel, error) {
	if c.ID != 0 {
		return core.Channel{}, errors.New("new channel cannot have an id")
	}
	if plainKey == "" {
		return core.Channel{}, errors.New("upstream key is required")
	}
	if err := ValidateChannel(c); err != nil {
		return core.Channel{}, err
	}
	// Insert and seal with row identity in a transaction. Ciphertext cannot be
	// copied to another channel to silently rebind response affinity.
	err := s.Transaction(ctx, func(tx *gorm.DB) error {
		c.EncryptedKey = ""
		if err := tx.Create(&c).Error; err != nil {
			return err
		}
		sealed, err := s.cipher.Encrypt(plainKey, channelPurpose(c.ID))
		if err != nil {
			return err
		}
		c.EncryptedKey = sealed
		return tx.Model(&core.Channel{}).Where("id = ?", c.ID).Update("encrypted_key", sealed).Error
	})
	return c, err
}
func channelPurpose(id int64) string { return "upstream-channel:" + strconv.FormatInt(id, 10) }
func (s *Store) ChannelKey(channel core.Channel) (string, error) {
	return s.cipher.Decrypt(channel.EncryptedKey, channelPurpose(channel.ID))
}
func (s *Store) Channel(ctx context.Context, id int64) (core.Channel, error) {
	var c core.Channel
	err := s.db.WithContext(ctx).First(&c, id).Error
	return c, dbError(err)
}
func (s *Store) ListChannels(ctx context.Context) ([]core.Channel, error) {
	out := []core.Channel{}
	err := s.db.WithContext(ctx).Order("priority desc, id").Find(&out).Error
	return out, err
}

// UpdateChannel changes operational routing controls only. Upstream key and
// identity are immutable; changing an account always creates a new channel.
func (s *Store) UpdateChannel(ctx context.Context, c core.Channel, plainKey string, validators ...func(*gorm.DB, core.Channel, core.Channel) error) error {
	return dbError(s.catalogTransaction(ctx, func(tx *gorm.DB) error {
		var old core.Channel
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&old, c.ID).Error; err != nil {
			return err
		}
		if c.Kind != old.Kind || c.BaseURL != old.BaseURL || c.APIVersion != old.APIVersion || plainKey != "" {
			return ErrImmutableChannel
		}
		if err := ValidateChannel(c); err != nil {
			return err
		}
		c.EncryptedKey = old.EncryptedKey
		for _, validate := range validators {
			if err := validate(tx, old, c); err != nil {
				return err
			}
		}
		return tx.Model(&core.Channel{}).Where("id = ?", c.ID).Updates(map[string]any{"name": c.Name, "models_json": c.ModelsJSON, "endpoints_json": c.EndpointsJSON, "priority": c.Priority, "weight": c.Weight, "enabled": c.Enabled}).Error
	}))
}
func (s *Store) DeleteChannel(ctx context.Context, id int64, validators ...func(*gorm.DB, core.Channel) error) error {
	return dbError(s.catalogTransaction(ctx, func(tx *gorm.DB) error {
		var old core.Channel
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&old, id).Error; err != nil {
			return err
		}
		var count int64
		if err := tx.Model(&core.ResponseAffinity{}).Where("channel_id = ?", id).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return errors.New("channel has response affinity; disable it instead")
		}
		if err := tx.Model(&core.SessionAffinity{}).Where("channel_id = ?", id).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return errors.New("channel has session affinity; disable it instead")
		}
		for _, validate := range validators {
			if err := validate(tx, old); err != nil {
				return err
			}
		}
		r := tx.Delete(&core.Channel{}, id)
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected == 0 {
			return ErrNotFound
		}
		return nil
	}))
}
func (s *Store) ChannelFailure(ctx context.Context, id int64, cooldown time.Duration) error {
	until := time.Now().UTC().Add(cooldown)
	return s.db.WithContext(ctx).Model(&core.Channel{}).Where("id = ?", id).Updates(map[string]any{"failures": gorm.Expr("failures + 1"), "cooldown_until": until}).Error
}
func (s *Store) ChannelSuccess(ctx context.Context, id int64) error {
	return s.db.WithContext(ctx).Model(&core.Channel{}).Where("id = ?", id).Updates(map[string]any{"failures": 0, "cooldown_until": nil}).Error
}
