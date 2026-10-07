// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/nomifun/nomifun-model-gateway/internal/core"
	"gorm.io/gorm/clause"
)

func protectedSetting(name string) bool {
	return strings.HasPrefix(name, "internal.") || strings.HasPrefix(name, "payment.config.")
}
func (s *Store) SetSetting(ctx context.Context, name string, value []byte) error {
	if strings.TrimSpace(name) == "" || len(name) > 100 || protectedSetting(name) {
		return errors.New("invalid or reserved setting name")
	}
	if !json.Valid(value) {
		return errors.New("setting must contain valid JSON")
	}
	return s.saveSetting(ctx, name, string(value))
}
func (s *Store) GetSetting(ctx context.Context, name string) ([]byte, error) {
	if protectedSetting(name) {
		return nil, ErrNotFound
	}
	value, err := s.readSetting(ctx, name)
	if err != nil {
		return nil, err
	}
	if !json.Valid([]byte(value)) {
		return nil, errors.New("stored setting is malformed")
	}
	return []byte(value), nil
}
func (s *Store) ListSettings(ctx context.Context) ([]core.Setting, error) {
	out := []core.Setting{}
	err := s.db.WithContext(ctx).Where("name NOT LIKE ? AND name NOT LIKE ?", "internal.%", "payment.config.%").Order("name").Find(&out).Error
	return out, err
}
func (s *Store) SaveSettingSecret(ctx context.Context, name string, plain []byte) error {
	if !strings.HasPrefix(name, "payment.config.") || len(name) > 100 {
		return errors.New("secret setting must use payment.config prefix")
	}
	value, err := s.cipher.Encrypt(string(plain), "secret-setting:"+name)
	if err != nil {
		return err
	}
	return s.saveSetting(ctx, name, value)
}
func (s *Store) ReadSettingSecret(ctx context.Context, name string) ([]byte, error) {
	if !strings.HasPrefix(name, "payment.config.") {
		return nil, ErrNotFound
	}
	value, err := s.readSetting(ctx, name)
	if err != nil {
		return nil, err
	}
	plain, err := s.cipher.Decrypt(value, "secret-setting:"+name)
	if err != nil {
		return nil, err
	}
	return []byte(plain), nil
}
func (s *Store) saveSetting(ctx context.Context, name, value string) error {
	record := core.Setting{Name: name, ValueJSON: value}
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "name"}}, DoUpdates: clause.AssignmentColumns([]string{"value_json", "updated_at"})}).Create(&record).Error
}
func (s *Store) readSetting(ctx context.Context, name string) (string, error) {
	var row core.Setting
	err := s.db.WithContext(ctx).Where("name = ?", name).First(&row).Error
	return row.ValueJSON, dbError(err)
}
