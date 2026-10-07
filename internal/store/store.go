// SPDX-License-Identifier: Apache-2.0
// Package store is the versioned durable boundary for gateway administration.
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/go-gormigrate/gormigrate/v2"
	"github.com/nomifun/nomifun-model-gateway/internal/core"
	"github.com/nomifun/nomifun-model-gateway/internal/secret"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

type Config struct {
	Driver       string
	DSN          string
	MasterKey    string
	SessionTTL   time.Duration
	PasswordCost int // zero uses bcrypt cost 12; lower values are rejected
}

type Store struct {
	db           *gorm.DB
	cipher       *secret.Cipher
	sessionTTL   time.Duration
	passwordCost int
}

var (
	ErrNotFound         = errors.New("record not found")
	ErrUnauthorized     = errors.New("invalid or disabled credentials")
	ErrConflict         = errors.New("record conflicts with an existing record")
	ErrKeyExpired       = errors.New("key_expired")
	ErrModelNotAllowed  = errors.New("model_not_in_plan")
	ErrImmutableChannel = errors.New("channel account identity is immutable; create a new channel")
)

func Open(cfg Config) (*Store, error) {
	cipher, err := secret.New(cfg.MasterKey)
	if err != nil {
		return nil, err
	}
	if cfg.SessionTTL == 0 {
		cfg.SessionTTL = 24 * time.Hour
	}
	if cfg.SessionTTL < time.Minute || cfg.SessionTTL > 30*24*time.Hour {
		return nil, errors.New("invalid session lifetime")
	}
	if cfg.PasswordCost == 0 {
		cfg.PasswordCost = 12
	}
	if cfg.PasswordCost < 12 || cfg.PasswordCost > 16 {
		return nil, errors.New("password cost must be between 12 and 16")
	}
	var dialector gorm.Dialector
	switch cfg.Driver {
	case "", "sqlite":
		if cfg.DSN == "" {
			return nil, errors.New("database DSN is required")
		}
		dialector = sqlite.Open(cfg.DSN)
	case "postgres", "postgresql":
		if cfg.DSN == "" {
			return nil, errors.New("database DSN is required")
		}
		dialector = postgres.Open(cfg.DSN)
	default:
		return nil, errors.New("database driver must be sqlite or postgres")
	}
	// GORM SQL logging is disabled: SQL variables include password hashes,
	// session hashes, merchant configuration and encrypted upstream credentials.
	db, err := gorm.Open(dialector, &gorm.Config{Logger: logger.Default.LogMode(logger.Silent), TranslateError: true})
	if err != nil {
		return nil, errors.New("database connection failed")
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	failed := true
	defer func() {
		if failed {
			_ = sqlDB.Close()
		}
	}()
	if db.Dialector.Name() == "sqlite" {
		// One writer connection avoids SQLite deferred-transaction lock upgrades.
		// Billing keeps every reservation and settlement in a single transaction.
		sqlDB.SetMaxOpenConns(1)
		sqlDB.SetMaxIdleConns(1)
		for _, q := range []string{"PRAGMA busy_timeout = 10000", "PRAGMA foreign_keys = ON", "PRAGMA journal_mode = WAL"} {
			if err = db.Exec(q).Error; err != nil {
				return nil, errors.New("SQLite configuration failed")
			}
		}
	} else {
		sqlDB.SetMaxOpenConns(20)
		sqlDB.SetMaxIdleConns(5)
		sqlDB.SetConnMaxLifetime(30 * time.Minute)
	}
	if err = migrate(db); err != nil {
		return nil, fmt.Errorf("database migration failed: %w", err)
	}
	s := &Store{db: db, cipher: cipher, sessionTTL: cfg.SessionTTL, passwordCost: cfg.PasswordCost}
	if err = s.checkMasterKey(); err != nil {
		return nil, err
	}
	failed = false
	return s, nil
}

func migrate(db *gorm.DB) error {
	options := *gormigrate.DefaultOptions
	options.UseTransaction = true
	options.ValidateUnknownMigrations = true
	return gormigrate.New(db, &options, []*gormigrate.Migration{{ID: "202610070001_gateway_core", Migrate: func(tx *gorm.DB) error {
		return tx.AutoMigrate(&core.User{}, &core.APIKey{}, &core.LoginSession{}, &core.Model{}, &core.Channel{}, &core.ResponseAffinity{}, &core.SessionAffinity{}, &core.Plan{}, &core.Subscription{}, &core.Reservation{}, &core.LedgerEntry{}, &core.PaymentOrder{}, &core.PaymentEvent{}, &core.RedeemCode{}, &core.Setting{}, &core.AuditLog{})
	}}, {ID: "202610070002_access_billing_payment", Migrate: func(tx *gorm.DB) error {
		return tx.AutoMigrate(&core.Model{}, &core.Reservation{}, &core.PaymentOrder{})
	}}}).Migrate()
}

func (s *Store) checkMasterKey() error {
	const purpose = "gateway-master-key-verifier"
	return s.db.Transaction(func(tx *gorm.DB) error {
		sealed, err := s.cipher.Encrypt("nomifun-model-gateway", purpose)
		if err != nil {
			return err
		}
		initial := core.Setting{Name: "internal.master_key_verifier", ValueJSON: sealed}
		if err = tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&initial).Error; err != nil {
			return err
		}
		var setting core.Setting
		if err = tx.Where("name = ?", initial.Name).First(&setting).Error; err != nil {
			return err
		}
		plain, err := s.cipher.Decrypt(setting.ValueJSON, purpose)
		if err != nil || plain != "nomifun-model-gateway" {
			return secret.ErrDecrypt
		}
		return nil
	})
}

func (s *Store) DB() *gorm.DB { return s.db }
func (s *Store) Close() error {
	db, err := s.db.DB()
	if err != nil {
		return err
	}
	return db.Close()
}
func (s *Store) Transaction(ctx context.Context, fn func(*gorm.DB) error) error {
	return s.db.WithContext(ctx).Transaction(fn)
}
func dbError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrNotFound
	}
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return ErrConflict
	}
	return err
}
