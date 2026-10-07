// SPDX-License-Identifier: Apache-2.0
package payment

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"reflect"
	"strings"
	"sync"

	"github.com/nomifun/nomifun-model-gateway/internal/core"
	"github.com/nomifun/nomifun-model-gateway/internal/store"
	"gorm.io/gorm"
)

type SecretStore interface {
	SaveSettingSecret(context.Context, string, []byte) error
	ReadSettingSecret(context.Context, string) ([]byte, error)
}

// Config is internal to the merchant adapters. REST handlers must exclusively
// use ConfigPatch and ConfigStatus: Config must never be serialized to a client.
type Config struct {
	Provider                                                                  string
	Enabled, Sandbox                                                          bool
	AppID, MerchantID                                                         string
	PrivateKey, PlatformPublicKey, PlatformKeyID, CertificateSerial, APIV3Key string
	NotifyURL, ReturnURL, CancelURL                                           string
	StripeAccountID, APIKey, WebhookSecret                                    string
}
type ConfigPatch struct {
	Enabled           *bool   `json:"enabled"`
	Sandbox           *bool   `json:"sandbox"`
	AppID             *string `json:"app_id"`
	MerchantID        *string `json:"merchant_id"`
	PrivateKey        *string `json:"private_key"`
	PlatformPublicKey *string `json:"platform_public_key"`
	PlatformKeyID     *string `json:"platform_key_id"`
	CertificateSerial *string `json:"certificate_serial"`
	APIV3Key          *string `json:"api_v3_key"`
	NotifyURL         *string `json:"notify_url"`
	ReturnURL         *string `json:"return_url"`
	CancelURL         *string `json:"cancel_url"`
	StripeAccountID   *string `json:"stripe_account_id"`
	APIKey            *string `json:"api_key"`
	WebhookSecret     *string `json:"webhook_secret"`
}
type ConfigStatus struct {
	Provider           string          `json:"provider"`
	Enabled            bool            `json:"enabled"`
	Sandbox            bool            `json:"sandbox"`
	Configured         bool            `json:"configured"`
	AppID              string          `json:"app_id,omitempty"`
	MerchantID         string          `json:"merchant_id,omitempty"`
	NotifyURL          string          `json:"notify_url,omitempty"`
	ReturnURL          string          `json:"return_url,omitempty"`
	CancelURL          string          `json:"cancel_url,omitempty"`
	StripeAccountID    string          `json:"stripe_account_id,omitempty"`
	PlatformKeyID      string          `json:"platform_key_id,omitempty"`
	CertificateSerial  string          `json:"certificate_serial,omitempty"`
	CredentialPresence map[string]bool `json:"credential_presence"`
}
type ConfigRepository struct {
	store SecretStore
	mu    sync.Mutex
	db    *gorm.DB
}

func NewConfigRepository(secrets SecretStore) *ConfigRepository {
	r := &ConfigRepository{store: secrets}
	if s, ok := secrets.(interface{ DB() *gorm.DB }); ok {
		r.db = s.DB()
	}
	return r
}
func validProvider(s string) bool { return s == "stripe" || s == "alipay" || s == "wechat" }
func (r *ConfigRepository) Load(ctx context.Context, name string) (Config, error) {
	if !validProvider(name) {
		return Config{}, ErrProviderUnavailable
	}
	b, err := r.store.ReadSettingSecret(ctx, "payment.config."+name)
	if errors.Is(err, store.ErrNotFound) {
		return Config{Provider: name}, nil
	}
	if err != nil {
		return Config{Provider: name}, err
	}
	if len(b) == 0 {
		return Config{Provider: name}, nil
	}
	var c Config
	if json.Unmarshal(b, &c) != nil || c.Provider != name {
		return Config{}, errors.New("invalid encrypted payment configuration")
	}
	return c, nil
}
func configured(c Config) bool {
	switch c.Provider {
	case "stripe":
		return c.APIKey != "" && c.WebhookSecret != "" && c.StripeAccountID != "" && c.ReturnURL != "" && c.CancelURL != ""
	case "alipay":
		return c.AppID != "" && c.MerchantID != "" && c.PrivateKey != "" && c.PlatformPublicKey != "" && c.NotifyURL != ""
	case "wechat":
		return c.AppID != "" && c.MerchantID != "" && c.PrivateKey != "" && c.PlatformPublicKey != "" && c.PlatformKeyID != "" && c.CertificateSerial != "" && len(c.APIV3Key) == 32 && c.NotifyURL != ""
	}
	return false
}
func status(c Config) ConfigStatus {
	return ConfigStatus{Provider: c.Provider, Enabled: c.Enabled, Sandbox: c.Sandbox, Configured: configured(c), AppID: c.AppID, MerchantID: c.MerchantID, NotifyURL: c.NotifyURL, ReturnURL: c.ReturnURL, CancelURL: c.CancelURL, StripeAccountID: c.StripeAccountID, PlatformKeyID: c.PlatformKeyID, CertificateSerial: c.CertificateSerial, CredentialPresence: map[string]bool{"api_key": c.APIKey != "", "webhook_secret": c.WebhookSecret != "", "private_key": c.PrivateKey != "", "platform_public_key": c.PlatformPublicKey != "", "api_v3_key": c.APIV3Key != ""}}
}
func validateConfig(c Config) error {
	for _, s := range []string{c.NotifyURL, c.ReturnURL, c.CancelURL} {
		if s == "" {
			continue
		}
		u, err := url.Parse(s)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" || u.RawQuery != "" || u.ForceQuery {
			return errors.New("payment callback and return URLs must be HTTPS")
		}
	}
	if c.Provider == "stripe" && c.APIKey != "" {
		prefix := "sk_live_"
		if c.Sandbox {
			prefix = "sk_test_"
		}
		if !strings.HasPrefix(c.APIKey, prefix) || !strings.HasPrefix(c.StripeAccountID, "acct_") {
			return errors.New("Stripe key mode and account are invalid")
		}
	}
	if c.Provider == "wechat" && c.Sandbox {
		return errors.New("WeChat API v3 has no merchant sandbox adapter; use signed local fixtures and operator merchant acceptance")
	}
	if c.Enabled && !configured(c) {
		return ErrProviderUnavailable
	}
	return nil
}
func (r *ConfigRepository) Update(ctx context.Context, name string, p ConfigPatch) (ConfigStatus, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, err := r.Load(ctx, name)
	if err != nil {
		return ConfigStatus{}, err
	}
	before := c
	if p.Enabled != nil {
		c.Enabled = *p.Enabled
	}
	if p.Sandbox != nil {
		c.Sandbox = *p.Sandbox
	}
	fields := []struct {
		dst *string
		src *string
	}{{&c.AppID, p.AppID}, {&c.MerchantID, p.MerchantID}, {&c.PrivateKey, p.PrivateKey}, {&c.PlatformPublicKey, p.PlatformPublicKey}, {&c.PlatformKeyID, p.PlatformKeyID}, {&c.CertificateSerial, p.CertificateSerial}, {&c.APIV3Key, p.APIV3Key}, {&c.NotifyURL, p.NotifyURL}, {&c.ReturnURL, p.ReturnURL}, {&c.CancelURL, p.CancelURL}, {&c.StripeAccountID, p.StripeAccountID}, {&c.APIKey, p.APIKey}, {&c.WebhookSecret, p.WebhookSecret}}
	for _, f := range fields {
		if f.src != nil {
			*f.dst = *f.src
		}
	}
	if err = validateConfig(c); err != nil {
		return ConfigStatus{}, err
	}
	if c.Enabled {
		client := newHTTPClient(nil)
		switch c.Provider {
		case "stripe":
			_, err = newStripe(c, client)
		case "alipay":
			_, err = newAlipay(c, client)
		case "wechat":
			_, err = newWechat(c, client)
		}
		if err != nil {
			return ConfigStatus{}, err
		}
	}
	before.Enabled = c.Enabled
	if r.db != nil && !reflect.DeepEqual(before, c) {
		var count int64
		if err = r.db.WithContext(ctx).Model(&core.PaymentOrder{}).Where("provider = ? AND state IN ?", name, []string{"creating", "pending", "expired", "failed"}).Count(&count).Error; err != nil {
			return ConfigStatus{}, err
		}
		if count > 0 {
			return ConfigStatus{}, errors.New("reconcile unresolved orders before changing merchant identity or credentials")
		}
	}
	b, err := json.Marshal(c)
	if err != nil {
		return ConfigStatus{}, err
	}
	if err = r.store.SaveSettingSecret(ctx, "payment.config."+name, b); err != nil {
		return ConfigStatus{}, err
	}
	return status(c), nil
}
func (r *ConfigRepository) Status(ctx context.Context) ([]ConfigStatus, error) {
	out := make([]ConfigStatus, 0, 3)
	for _, name := range []string{"stripe", "alipay", "wechat"} {
		c, err := r.Load(ctx, name)
		if err != nil {
			return nil, err
		}
		out = append(out, status(c))
	}
	return out, nil
}
