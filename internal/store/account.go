// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"encoding/json"
	"errors"
	"net/mail"
	"net/netip"
	"strings"
	"time"

	"github.com/nomifun/nomifun-model-gateway/internal/core"
	"github.com/nomifun/nomifun-model-gateway/internal/secret"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func normalizeEmail(email string) (string, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	address, err := mail.ParseAddress(email)
	if err != nil || address.Address != email || len(email) > 254 {
		return "", errors.New("invalid email")
	}
	return email, nil
}
func validCurrency(currency string) bool {
	return len(currency) == 3 && currency[0] >= 'A' && currency[0] <= 'Z' && currency[1] >= 'A' && currency[1] <= 'Z' && currency[2] >= 'A' && currency[2] <= 'Z'
}
func (s *Store) Register(ctx context.Context, email, name, password, currency string) (*core.User, error) {
	email, err := normalizeEmail(email)
	if err != nil {
		return nil, err
	}
	if len(password) < 12 || len(password) > 72 {
		return nil, errors.New("password must contain 12 to 72 bytes")
	}
	if currency == "" {
		currency = "USD"
	}
	if !validCurrency(currency) {
		return nil, errors.New("currency must be three uppercase letters")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = email
	}
	if len(name) > 200 {
		return nil, errors.New("name is too long")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), s.passwordCost)
	if err != nil {
		return nil, errors.New("password hashing failed")
	}
	u := core.User{Email: email, Name: name, PasswordHash: string(hash), Currency: currency}
	if err = s.db.WithContext(ctx).Create(&u).Error; err != nil {
		return nil, dbError(err)
	}
	return &u, nil
}

// BootstrapAdmin creates the deployment owner once. Existing accounts are
// never promoted, overwritten or have their password reset by environment.
func (s *Store) BootstrapAdmin(ctx context.Context, email, password string) error {
	if email == "" && password == "" {
		return nil
	}
	if email == "" || password == "" {
		return errors.New("bootstrap email and password must both be set")
	}
	email, err := normalizeEmail(email)
	if err != nil {
		return err
	}
	if len(password) < 12 || len(password) > 72 {
		return errors.New("bootstrap password must contain 12 to 72 bytes")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), s.passwordCost)
	if err != nil {
		return errors.New("password hashing failed")
	}
	return s.Transaction(ctx, func(tx *gorm.DB) error {
		// The marker serializes bootstrap and survives changes to the owner email.
		marker := core.Setting{Name: "internal.bootstrap_admin", ValueJSON: "true"}
		result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&marker)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return nil
		}
		var count int64
		if err := tx.Model(&core.User{}).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return nil
		}
		return dbError(tx.Create(&core.User{Email: email, Name: "Administrator", PasswordHash: string(hash), Currency: "USD", Admin: true}).Error)
	})
}

func (s *Store) Login(ctx context.Context, email, password string) (*core.User, string, error) {
	email, err := normalizeEmail(email)
	if err != nil {
		return nil, "", ErrUnauthorized
	}
	var u core.User
	if err = s.db.WithContext(ctx).Where("email = ?", email).First(&u).Error; err != nil || u.Disabled {
		return nil, "", ErrUnauthorized
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)) != nil {
		return nil, "", ErrUnauthorized
	}
	token, err := secret.Token("nms_")
	if err != nil {
		return nil, "", err
	}
	session := core.LoginSession{Hash: secret.Hash(token), UserID: u.ID, ExpiresAt: time.Now().UTC().Add(s.sessionTTL)}
	if err = s.db.WithContext(ctx).Create(&session).Error; err != nil {
		return nil, "", err
	}
	return &u, token, nil
}
func (s *Store) SessionUser(ctx context.Context, token string) (*core.User, error) {
	if len(token) < 20 || len(token) > 200 {
		return nil, ErrUnauthorized
	}
	var session core.LoginSession
	if err := s.db.WithContext(ctx).Where("hash = ? AND expires_at > ?", secret.Hash(token), time.Now().UTC()).First(&session).Error; err != nil {
		return nil, ErrUnauthorized
	}
	var u core.User
	if err := s.db.WithContext(ctx).First(&u, session.UserID).Error; err != nil || u.Disabled {
		return nil, ErrUnauthorized
	}
	return &u, nil
}
func (s *Store) RevokeSession(ctx context.Context, token string) error {
	return s.db.WithContext(ctx).Where("hash = ?", secret.Hash(token)).Delete(&core.LoginSession{}).Error
}

type KeyInput struct {
	Name               string     `json:"name"`
	ExpiresAt          *time.Time `json:"expires_at"`
	QuotaLimit         *int64     `json:"quota_limit"`
	ModelIDs           []string   `json:"model_ids"`
	AllowedIPs         []string   `json:"allowed_ips"`
	RequestsPerMinute  *int64     `json:"requests_per_minute"`
	TokensPerMinute    *int64     `json:"tokens_per_minute"`
	ConcurrentRequests *int64     `json:"concurrent_requests"`
}

func (s *Store) CreateAPIKey(ctx context.Context, userID int64, in KeyInput) (core.APIKey, string, error) {
	if strings.TrimSpace(in.Name) == "" || len(in.Name) > 200 {
		return core.APIKey{}, "", errors.New("key name is required and must contain at most 200 bytes")
	}
	for _, n := range []*int64{in.QuotaLimit, in.RequestsPerMinute, in.TokensPerMinute, in.ConcurrentRequests} {
		if n != nil && *n < 0 {
			return core.APIKey{}, "", errors.New("key limits cannot be negative")
		}
	}
	if in.ExpiresAt != nil && !in.ExpiresAt.After(time.Now().UTC()) {
		return core.APIKey{}, "", errors.New("key expiration must be in the future")
	}
	if err := validateStringList(in.ModelIDs); err != nil {
		return core.APIKey{}, "", err
	}
	for _, ip := range in.AllowedIPs {
		if _, err := netip.ParseAddr(ip); err != nil {
			if _, err = netip.ParsePrefix(ip); err != nil {
				return core.APIKey{}, "", errors.New("allowed IP must be an address or CIDR")
			}
		}
	}
	var u core.User
	if err := s.db.WithContext(ctx).Where("id = ? AND disabled = ?", userID, false).First(&u).Error; err != nil {
		return core.APIKey{}, "", ErrUnauthorized
	}
	plain, err := secret.Token("nmg_")
	if err != nil {
		return core.APIKey{}, "", err
	}
	if in.ModelIDs == nil {
		in.ModelIDs = []string{}
	}
	if in.AllowedIPs == nil {
		in.AllowedIPs = []string{}
	}
	models, _ := json.Marshal(in.ModelIDs)
	ips, _ := json.Marshal(in.AllowedIPs)
	k := core.APIKey{UserID: userID, Name: in.Name, Prefix: plain[:12], Hash: secret.Hash(plain), ExpiresAt: in.ExpiresAt, QuotaLimit: in.QuotaLimit, ModelIDsJSON: string(models), AllowedIPsJSON: string(ips), RequestsPerMinute: in.RequestsPerMinute, TokensPerMinute: in.TokensPerMinute, ConcurrentRequests: in.ConcurrentRequests}
	if err = s.db.WithContext(ctx).Create(&k).Error; err != nil {
		return core.APIKey{}, "", err
	}
	return k, plain, nil
}
func (s *Store) ListAPIKeys(ctx context.Context, userID int64) ([]core.APIKey, error) {
	out := []core.APIKey{}
	err := s.db.WithContext(ctx).Where("user_id = ?", userID).Order("id desc").Find(&out).Error
	return out, err
}
func (s *Store) RevokeAPIKey(ctx context.Context, userID, keyID int64) error {
	r := s.db.WithContext(ctx).Model(&core.APIKey{}).Where("id = ? AND user_id = ?", keyID, userID).Update("revoked", true)
	if r.Error != nil {
		return r.Error
	}
	if r.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) AuthenticateKey(ctx context.Context, plain, ip, model string) (*core.User, *core.APIKey, error) {
	if len(plain) < 20 || len(plain) > 200 {
		return nil, nil, ErrUnauthorized
	}
	var k core.APIKey
	if err := s.db.WithContext(ctx).Where("hash = ?", secret.Hash(plain)).First(&k).Error; err != nil || k.Revoked {
		return nil, nil, ErrUnauthorized
	}
	if k.ExpiresAt != nil && !k.ExpiresAt.After(time.Now().UTC()) {
		return nil, nil, ErrKeyExpired
	}
	var u core.User
	if err := s.db.WithContext(ctx).First(&u, k.UserID).Error; err != nil || u.Disabled {
		return nil, nil, ErrUnauthorized
	}
	var ips, models []string
	if json.Unmarshal([]byte(k.AllowedIPsJSON), &ips) != nil || ips == nil || json.Unmarshal([]byte(k.ModelIDsJSON), &models) != nil || models == nil {
		return nil, nil, ErrUnauthorized
	}
	if len(ips) > 0 {
		addr, err := netip.ParseAddr(ip)
		if err != nil {
			return nil, nil, ErrUnauthorized
		}
		addr = addr.Unmap()
		allowed := false
		for _, entry := range ips {
			if prefix, err := netip.ParsePrefix(entry); err == nil {
				allowed = allowed || prefix.Contains(addr)
			} else if exact, err := netip.ParseAddr(entry); err == nil {
				allowed = allowed || exact.Unmap() == addr
			}
		}
		if !allowed {
			return nil, nil, ErrUnauthorized
		}
	}
	if model != "" && len(models) > 0 {
		allowed := false
		for _, id := range models {
			allowed = allowed || id == model
		}
		if !allowed {
			return nil, nil, ErrModelNotAllowed
		}
	}
	return &u, &k, nil
}

func validateStringList(values []string) error {
	seen := map[string]bool{}
	for _, v := range values {
		if strings.TrimSpace(v) == "" || seen[v] {
			return errors.New("list values must be nonempty and unique")
		}
		seen[v] = true
	}
	return nil
}
