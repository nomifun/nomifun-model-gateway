// SPDX-License-Identifier: Apache-2.0
// Package channel selects native upstream accounts and persists affinity.
package channel

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"math/big"
	"sort"
	"time"

	"github.com/nomifun/nomifun-model-gateway/internal/core"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrAffinity = errors.New("affinity unavailable")
var ErrNoChannel = errors.New("no available native upstream channel")

type Service struct {
	DB          *gorm.DB
	ResponseTTL time.Duration
	Now         func() time.Time
}

func New(db *gorm.DB, ttl time.Duration) *Service {
	if ttl <= 0 {
		ttl = 30 * 24 * time.Hour
	}
	return &Service{DB: db, ResponseTTL: ttl, Now: time.Now}
}

func Supports(c core.Channel, model, endpoint string) (string, bool) {
	var models map[string]string
	var endpoints []string
	if json.Unmarshal([]byte(c.ModelsJSON), &models) != nil || json.Unmarshal([]byte(c.EndpointsJSON), &endpoints) != nil {
		return "", false
	}
	upstream, ok := models[model]
	if !ok || upstream == "" {
		return "", false
	}
	compatible := false
	switch endpoint {
	case "anthropic":
		compatible = c.Kind == "anthropic"
	case "gemini":
		compatible = c.Kind == "gemini"
	default:
		compatible = c.Kind == "openai" || c.Kind == "compatible" || c.Kind == "azure"
	}
	if !compatible {
		return "", false
	}
	for _, e := range endpoints {
		if e == endpoint {
			return upstream, true
		}
	}
	return "", false
}

// Candidates produces a weighted permutation within each priority. A bound
// account is never substituted, even while it is cooling down.
func (s *Service) Candidates(ctx context.Context, key int64, model, endpoint, previous, session string) ([]core.Channel, bool, error) {
	now := s.Now()
	var fixed int64
	if previous != "" {
		var binding core.ResponseAffinity
		err := s.DB.WithContext(ctx).Where("api_key_id = ? AND response_id = ?", key, previous).First(&binding).Error
		if err != nil || !binding.ExpiresAt.After(now) {
			return nil, true, ErrAffinity
		}
		fixed = binding.ChannelID
	}
	if session != "" {
		var binding core.SessionAffinity
		err := s.DB.WithContext(ctx).Where("api_key_id = ? AND session_id = ?", key, session).First(&binding).Error
		if err == nil {
			if binding.Expired || now.Sub(binding.LastUsedAt) >= 24*time.Hour {
				s.DB.WithContext(ctx).Model(&binding).Update("expired", true)
				return nil, true, ErrAffinity
			}
			fixed = binding.ChannelID
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, true, err
		}
	}
	var rows []core.Channel
	if fixed != 0 {
		var c core.Channel
		if err := s.DB.WithContext(ctx).First(&c, fixed).Error; err != nil || !c.Enabled {
			return nil, true, ErrAffinity
		}
		if _, ok := Supports(c, model, endpoint); !ok {
			return nil, true, ErrAffinity
		}
		if c.CooldownUntil != nil && c.CooldownUntil.After(now) {
			return nil, true, ErrAffinity
		}
		return []core.Channel{c}, true, nil
	}
	if err := s.DB.WithContext(ctx).Where("enabled = ?", true).Find(&rows).Error; err != nil {
		return nil, false, err
	}
	available := rows[:0]
	for _, c := range rows {
		if _, ok := Supports(c, model, endpoint); ok && (c.CooldownUntil == nil || !c.CooldownUntil.After(now)) {
			available = append(available, c)
		}
	}
	if len(available) == 0 {
		return nil, false, ErrNoChannel
	}
	sort.SliceStable(available, func(i, j int) bool { return available[i].Priority > available[j].Priority })
	result := make([]core.Channel, 0, len(available))
	for len(available) > 0 {
		end := 1
		for end < len(available) && available[end].Priority == available[0].Priority {
			end++
		}
		group := append([]core.Channel(nil), available[:end]...)
		available = available[end:]
		for len(group) > 0 {
			total := int64(0)
			for _, c := range group {
				w := c.Weight
				if w <= 0 {
					w = 1
				}
				if w > 1000000 {
					w = 1000000
				}
				total += int64(w)
			}
			draw, e := rand.Int(rand.Reader, big.NewInt(total))
			if e != nil {
				return nil, false, e
			}
			n := draw.Int64()
			selected := 0
			for i, c := range group {
				w := c.Weight
				if w <= 0 {
					w = 1
				}
				if w > 1000000 {
					w = 1000000
				}
				n -= int64(w)
				if n < 0 {
					selected = i
					break
				}
			}
			result = append(result, group[selected])
			group = append(group[:selected], group[selected+1:]...)
		}
	}
	if session != "" {
		binding := core.SessionAffinity{APIKeyID: key, SessionID: session, ChannelID: result[0].ID, LastUsedAt: now, CreatedAt: now}
		if err := s.DB.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&binding).Error; err != nil {
			return nil, true, err
		}
		var actual core.SessionAffinity
		if err := s.DB.WithContext(ctx).Where("api_key_id = ? AND session_id = ?", key, session).First(&actual).Error; err != nil {
			return nil, true, err
		}
		if actual.Expired || now.Sub(actual.LastUsedAt) >= 24*time.Hour {
			return nil, true, ErrAffinity
		}
		if actual.ChannelID != result[0].ID {
			return s.Candidates(ctx, key, model, endpoint, "", session)
		}
		return result[:1], true, nil
	}
	return result, false, nil
}

// BindResponse commits before the response.created event can reach a client.
func (s *Service) BindResponse(ctx context.Context, key int64, id string, account int64) error {
	if id == "" || len(id) > 300 {
		return ErrAffinity
	}
	now := s.Now()
	binding := core.ResponseAffinity{APIKeyID: key, ResponseID: id, ChannelID: account, ExpiresAt: now.Add(s.ResponseTTL), CreatedAt: now}
	if err := s.DB.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&binding).Error; err != nil {
		return err
	}
	var stored core.ResponseAffinity
	if err := s.DB.WithContext(ctx).Where("api_key_id = ? AND response_id = ?", key, id).First(&stored).Error; err != nil {
		return err
	}
	if stored.ChannelID != account || !stored.ExpiresAt.After(now) {
		return ErrAffinity
	}
	return nil
}
func (s *Service) Success(ctx context.Context, id, key int64, session string) error {
	if err := s.DB.WithContext(ctx).Model(&core.Channel{}).Where("id = ?", id).Updates(map[string]any{"failures": 0, "cooldown_until": nil}).Error; err != nil {
		return err
	}
	if session != "" {
		return s.DB.WithContext(ctx).Model(&core.SessionAffinity{}).Where("api_key_id = ? AND session_id = ? AND channel_id = ? AND expired = ?", key, session, id, false).Update("last_used_at", s.Now()).Error
	}
	return nil
}
func (s *Service) Failure(ctx context.Context, id int64) error {
	until := s.Now().Add(30 * time.Second)
	return s.DB.WithContext(ctx).Model(&core.Channel{}).Where("id = ?", id).Updates(map[string]any{"failures": gorm.Expr("failures + 1"), "cooldown_until": until}).Error
}
