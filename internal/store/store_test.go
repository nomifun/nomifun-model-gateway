// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	v1 "github.com/nomifun/nomifun-model-gateway/contract/v1"
	"github.com/nomifun/nomifun-model-gateway/internal/core"
	"github.com/nomifun/nomifun-model-gateway/internal/secret"
	"gorm.io/gorm"
)

func testConfig(t *testing.T) Config {
	t.Helper()
	return Config{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "gateway.sqlite"), MasterKey: base64.StdEncoding.EncodeToString([]byte(strings.Repeat("m", 32)))}
}
func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(testConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}
func createUser(t *testing.T, s *Store, email string) *core.User {
	t.Helper()
	u, err := s.Register(context.Background(), email, "Test user", "synthetic-password-123", "USD")
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestMigrationReopenWrongMasterAndRollback(t *testing.T) {
	ctx := context.Background()
	cfg := testConfig(t)
	s, err := Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	u := createUser(t, s, "persist@example.test")
	err = s.Transaction(ctx, func(tx *gorm.DB) error {
		if err := tx.Model(&core.User{}).Where("id = ?", u.ID).Update("balance", 100).Error; err != nil {
			return err
		}
		return errors.New("rollback intentionally")
	})
	if err == nil {
		t.Fatal("transaction committed")
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var got core.User
	if err = s.DB().First(&got, u.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.Balance != 0 || got.Email != u.Email {
		t.Fatal("rollback or reopen lost durable state")
	}
	var migrations int64
	if err = s.DB().Table("migrations").Count(&migrations).Error; err != nil || migrations != 2 {
		t.Fatalf("migration ledger: %d %v", migrations, err)
	}
	_ = s.Close()
	cfg.MasterKey = base64.StdEncoding.EncodeToString([]byte(strings.Repeat("x", 32)))
	if bad, err := Open(cfg); !errors.Is(err, secret.ErrDecrypt) {
		if bad != nil {
			_ = bad.Close()
		}
		t.Fatal("wrong master key did not fail closed")
	}
}

func TestAuthHashOnlyIsolationAndRestrictions(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	a := createUser(t, s, "a@example.test")
	b := createUser(t, s, "b@example.test")
	_, token, err := s.Login(ctx, "A@example.test", "synthetic-password-123")
	if err != nil {
		t.Fatal(err)
	}
	owner, err := s.SessionUser(ctx, token)
	if err != nil || owner.ID != a.ID {
		t.Fatal("session mismatch")
	}
	if _, _, err = s.Login(ctx, "a@example.test", "incorrect-password"); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("bad password accepted")
	}
	k, plain, err := s.CreateAPIKey(ctx, a.ID, KeyInput{Name: "restricted", ModelIDs: []string{"model-a"}, AllowedIPs: []string{"127.0.0.0/8"}})
	if err != nil {
		t.Fatal(err)
	}
	owner, key, err := s.AuthenticateKey(ctx, plain, "127.0.0.1", "model-a")
	if err != nil || owner.ID != a.ID || key.ID != k.ID {
		t.Fatal("key auth failed")
	}
	if _, _, err = s.AuthenticateKey(ctx, plain, "10.0.0.1", "model-a"); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("IP restriction bypassed")
	}
	if _, _, err = s.AuthenticateKey(ctx, plain, "127.0.0.1", "model-a-extra"); !errors.Is(err, ErrModelNotAllowed) {
		t.Fatal("model restriction was not exact")
	}
	if err = s.RevokeAPIKey(ctx, b.ID, k.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("cross-account key revoke accepted")
	}
	list, err := s.ListAPIKeys(ctx, b.ID)
	if err != nil || len(list) != 0 {
		t.Fatal("cross-account key leak")
	}
	encoded, _ := json.Marshal(k)
	if strings.Contains(string(encoded), k.Hash) || strings.Contains(string(encoded), plain) {
		t.Fatal("key serialized credential")
	}
	encoded, _ = json.Marshal(a)
	if strings.Contains(string(encoded), a.PasswordHash) {
		t.Fatal("password serialized")
	}
	var session core.LoginSession
	if err = s.DB().First(&session).Error; err != nil {
		t.Fatal(err)
	}
	if session.Hash == token || session.Hash != secret.Hash(token) || k.Hash != secret.Hash(plain) {
		t.Fatal("credentials persisted in clear")
	}
	if err = s.RevokeSession(ctx, token); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SessionUser(ctx, token); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("revoked login still authenticates")
	}
	expired := time.Now().UTC().Add(-time.Second)
	if err = s.DB().Model(&core.APIKey{}).Where("id = ?", k.ID).Update("expires_at", expired).Error; err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.AuthenticateKey(ctx, plain, "127.0.0.1", "model-a"); !errors.Is(err, ErrKeyExpired) {
		t.Fatal("expired key accepted")
	}
	if err = s.RevokeAPIKey(ctx, a.ID, k.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.AuthenticateKey(ctx, plain, "127.0.0.1", "model-a"); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("revoked key accepted")
	}
}

func TestBootstrapPreservesExistingAccounts(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	existing := createUser(t, s, "user@example.test")
	if err := s.BootstrapAdmin(ctx, existing.Email, "different-password-123"); err != nil {
		t.Fatal(err)
	}
	got, err := s.User(ctx, existing.ID)
	if err != nil || got.Admin || got.PasswordHash != existing.PasswordHash {
		t.Fatal("bootstrap altered existing user")
	}
	if err = s.BootstrapAdmin(ctx, "owner@example.test", "owner-password-123"); err != nil {
		t.Fatal(err)
	}
	users, _ := s.ListUsers(ctx)
	if len(users) != 1 {
		t.Fatal("bootstrap reran")
	}
	empty := openTest(t)
	if err = empty.BootstrapAdmin(ctx, "owner@example.test", "owner-password-123"); err != nil {
		t.Fatal(err)
	}
	users, _ = empty.ListUsers(ctx)
	if len(users) != 1 || !users[0].Admin {
		t.Fatal("owner not created")
	}
	if err = empty.BootstrapAdmin(ctx, "other@example.test", "other-password-123"); err != nil {
		t.Fatal(err)
	}
	users, _ = empty.ListUsers(ctx)
	if len(users) != 1 {
		t.Fatal("second owner created")
	}
}

func TestEncryptedChannelIdentitySettingsAndDatabaseLeak(t *testing.T) {
	ctx := context.Background()
	cfg := testConfig(t)
	s, err := Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	user := createUser(t, s, "db-secret@example.test")
	_, apiKey, err := s.CreateAPIKey(ctx, user.ID, KeyInput{Name: "database leak test"})
	if err != nil {
		t.Fatal(err)
	}
	_, loginToken, err := s.Login(ctx, user.Email, "synthetic-password-123")
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.CreateChannel(ctx, core.Channel{Name: "synthetic", Kind: "compatible", BaseURL: "http://127.0.0.1:12345", ModelsJSON: `{"public":"upstream"}`, EndpointsJSON: `["openai"]`, Weight: 1, Enabled: true}, "synthetic-channel-secret")
	if err != nil {
		t.Fatal(err)
	}
	plain, err := s.ChannelKey(c)
	if err != nil || plain != "synthetic-channel-secret" {
		t.Fatal("channel decrypt failed")
	}
	tampered := c
	tampered.ID++
	if _, err = s.ChannelKey(tampered); !errors.Is(err, secret.ErrDecrypt) {
		t.Fatal("channel identity substitution accepted")
	}
	c.BaseURL = "https://another.example.test"
	if err = s.UpdateChannel(ctx, c, ""); !errors.Is(err, ErrImmutableChannel) {
		t.Fatal("affinity account silently rebound")
	}
	c.BaseURL = "http://127.0.0.1:12345"
	if err = s.UpdateChannel(ctx, c, "rotated-secret"); !errors.Is(err, ErrImmutableChannel) {
		t.Fatal("immutable key changed")
	}
	if err = s.DB().Create(&core.ResponseAffinity{APIKeyID: 1, ResponseID: "resp_test", ChannelID: c.ID, ExpiresAt: time.Now().Add(time.Hour)}).Error; err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteChannel(ctx, c.ID); err == nil {
		t.Fatal("affinity channel deleted")
	}
	if err = s.SaveSettingSecret(ctx, "payment.config.stripe", []byte(`{"key":"synthetic-payment-secret"}`)); err != nil {
		t.Fatal(err)
	}
	value, err := s.ReadSettingSecret(ctx, "payment.config.stripe")
	if err != nil || string(value) != `{"key":"synthetic-payment-secret"}` {
		t.Fatal("secret setting failed")
	}
	if err = s.SetSetting(ctx, "payment.config.stripe", []byte(`{}`)); err == nil {
		t.Fatal("secret config bypass accepted")
	}
	if _, err = s.GetSetting(ctx, "payment.config.stripe"); !errors.Is(err, ErrNotFound) {
		t.Fatal("secret config exposed")
	}
	settings, _ := s.ListSettings(ctx)
	if len(settings) != 0 {
		t.Fatal("internal settings exposed")
	}
	if err = s.SetSetting(ctx, "brand", []byte(`{"name":"Synthetic partner"}`)); err != nil {
		t.Fatal(err)
	}
	if err = s.SetSetting(ctx, "brand", []byte(`invalid`)); err == nil {
		t.Fatal("malformed JSON persisted")
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	bytes, err := os.ReadFile(cfg.DSN)
	if err != nil {
		t.Fatal(err)
	}
	for _, credential := range []string{"synthetic-channel-secret", "synthetic-payment-secret", cfg.MasterKey, apiKey, loginToken, "synthetic-password-123"} {
		if strings.Contains(string(bytes), credential) {
			t.Fatal("database contains plaintext secret")
		}
	}
}

func TestCatalogValidationAndKeyFiltering(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	def := v1.Model{ID: "claude", DisplayName: "Claude", Vendor: "anthropic", Tasks: []v1.Task{v1.Chat}, TaskEndpoints: map[v1.Task]v1.TaskEndpoints{v1.Chat: {Endpoints: []v1.Endpoint{v1.Anthropic}, PreferredEndpoint: v1.Anthropic}}, InputModalities: []string{"text"}, Traits: []string{}, Pricing: []v1.Price{}, Status: "available"}
	if err := s.SaveModel(ctx, def, true); err == nil {
		t.Fatal("missing Anthropic max output persisted")
	}
	maxTokens := int64(1024)
	def.MaxOutputTokens = &maxTokens
	if err := s.SaveModel(ctx, def, true); err != nil {
		t.Fatal(err)
	}
	bad := def
	bad.Tasks = append([]v1.Task{}, def.Tasks...)
	bad.Tasks = append(bad.Tasks, v1.Chat)
	if err := ValidateModel(bad); err == nil {
		t.Fatal("duplicate task accepted")
	}
	bad = def
	bad.Pricing = []v1.Price{{Task: v1.Chat, Meter: "input_tokens", UnitSize: 1000000, Amount: 1, Currency: "USD"}, {Task: v1.Chat, Meter: "output_tokens", UnitSize: 1000000, Amount: 1, Currency: "CNY"}}
	if err := ValidateModel(bad); err == nil {
		t.Fatal("mixed currencies accepted")
	}
	catalog, err := s.CatalogForKey(ctx, core.APIKey{ModelIDsJSON: `["other"]`})
	if err != nil || len(catalog.Models) != 0 {
		t.Fatal("catalog key restriction ignored")
	}
	catalog, err = s.CatalogForKey(ctx, core.APIKey{ModelIDsJSON: `[]`})
	if err != nil || len(catalog.Models) != 1 {
		t.Fatal("unrestricted catalog empty")
	}
	def.TaskEndpoints[v1.Chat] = v1.TaskEndpoints{Endpoints: []v1.Endpoint{v1.Anthropic}, PreferredEndpoint: v1.OpenAI}
	if err := ValidateModel(def); err == nil {
		t.Fatal("preferred endpoint absent from list accepted")
	}
}

func TestCatalogActivePlanCoverageAndAdmissionPolicy(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	user := createUser(t, s, "plan@example.test")
	def := v1.Model{ID: "plan-model", DisplayName: "Plan model", Vendor: "compatible", Tasks: []v1.Task{v1.Chat}, TaskEndpoints: map[v1.Task]v1.TaskEndpoints{v1.Chat: {Endpoints: []v1.Endpoint{v1.OpenAI}, PreferredEndpoint: v1.OpenAI}}, InputModalities: []string{"text"}, Traits: []string{}, Pricing: []v1.Price{}, Status: "available"}
	if err := s.SaveModelAccess(ctx, def, true, true); err != nil {
		t.Fatal(err)
	}
	key := core.APIKey{UserID: user.ID, ModelIDsJSON: `[]`}
	before, err := s.CatalogForKey(ctx, key)
	if err != nil || len(before.Models) != 0 {
		t.Fatal("unsubscribed plan-only model visible")
	}
	if _, err = s.ModelRecord(ctx, def.ID); err != nil {
		t.Fatal("model unavailable for explicit business error")
	}
	now := time.Now().UTC()
	// The older plan stays temporally active after the new plan expires.
	older := core.Subscription{UserID: user.ID, PlanID: 1, PlanName: "older", PeriodStart: now.Add(-time.Hour), PeriodEnd: now.Add(time.Hour), ModelIDsJSON: `[]`}
	if err = s.DB().Create(&older).Error; err != nil {
		t.Fatal(err)
	}
	subscription := core.Subscription{UserID: user.ID, PlanID: 1, PlanName: "test plan", PeriodStart: now.Add(-time.Minute), PeriodEnd: now.Add(time.Hour), ModelIDsJSON: `["plan-model"]`}
	if err = s.DB().Create(&subscription).Error; err != nil {
		t.Fatal(err)
	}
	during, err := s.CatalogForKey(ctx, key)
	if err != nil || len(during.Models) != 1 || !during.Models[0].IncludedInPlan {
		t.Fatal("active plan coverage not projected")
	}
	def.ID = "paygo"
	if err = s.SaveModelAccess(ctx, def, true, false); err != nil {
		t.Fatal(err)
	}
	during, err = s.CatalogForKey(ctx, key)
	if err != nil || len(during.Models) != 2 {
		t.Fatal("paygo model removed by subscription")
	}
	for _, m := range during.Models {
		if m.ID == "paygo" && m.IncludedInPlan {
			t.Fatal("paygo incorrectly included")
		}
	}
	if err = s.DB().Model(&subscription).Update("period_end", now.Add(-time.Second)).Error; err != nil {
		t.Fatal(err)
	}
	after, err := s.CatalogForKey(ctx, key)
	if err != nil || len(after.Models) != 1 || after.Models[0].ID != "paygo" || after.Models[0].IncludedInPlan {
		t.Fatal("expired plan coverage remains")
	}
}

func TestMigrationForwardUpgradeAndUnknownVersion(t *testing.T) {
	cfg := testConfig(t)
	s, err := Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	user := createUser(t, s, "upgrade@example.test")
	if err = s.DB().Exec("DELETE FROM migrations WHERE id = ?", "202610070002_access_billing_payment").Error; err != nil {
		t.Fatal(err)
	}
	if err = s.DB().Migrator().DropColumn(&core.Model{}, "SubscriptionOnly"); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	s, err = Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !s.DB().Migrator().HasColumn(&core.Model{}, "SubscriptionOnly") {
		t.Fatal("forward migration missing column")
	}
	if _, err = s.User(context.Background(), user.ID); err != nil {
		t.Fatal("upgrade lost user")
	}
	if err = s.DB().Exec("INSERT INTO migrations (id) VALUES (?)", "209901010001_unknown_future").Error; err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	if reopened, err := Open(cfg); err == nil {
		_ = reopened.Close()
		t.Fatal("unknown migration lineage accepted")
	}
}

func TestAdministratorControls(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	if err := s.BootstrapAdmin(ctx, "owner@example.test", "owner-password-123"); err != nil {
		t.Fatal(err)
	}
	users, _ := s.ListUsers(ctx)
	owner := users[0]
	target := createUser(t, s, "target@example.test")
	yes, no := true, false
	if err := s.UpdateUserAdmin(ctx, target.ID, target.ID, nil, &yes); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("self promotion accepted")
	}
	if err := s.UpdateUserAdmin(ctx, owner.ID, owner.ID, &yes, nil); err == nil {
		t.Fatal("self disable accepted")
	}
	if err := s.UpdateUserAdmin(ctx, owner.ID, owner.ID, nil, &no); err == nil {
		t.Fatal("last admin demotion accepted")
	}
	if err := s.UpdateUserAdmin(ctx, owner.ID, target.ID, nil, &yes); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateUserAdmin(ctx, target.ID, owner.ID, nil, &no); err != nil {
		t.Fatal(err)
	}
	got, _ := s.User(ctx, owner.ID)
	if got.Admin {
		t.Fatal("authorized peer demotion ignored")
	}
	if err := s.UpdateUserAdmin(ctx, owner.ID, target.ID, nil, &no); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("demoted owner still authorizes")
	}
}

func TestRedeemBatchAuditRollbackAndHashOnly(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	if err := s.DB().Exec(`CREATE TRIGGER fail_audit BEFORE INSERT ON audit_logs BEGIN SELECT RAISE(ABORT, 'synthetic failure'); END`).Error; err != nil {
		t.Fatal(err)
	}
	template := core.RedeemCode{Amount: 100, Currency: "USD"}
	rows, plain, err := s.CreateRedeemCodes(ctx, 1, template, 3)
	if err == nil || rows != nil || plain != nil {
		t.Fatal("failed transaction returned one-time credentials")
	}
	var count int64
	if err = s.DB().Model(&core.RedeemCode{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatal("audit failure left orphan codes")
	}
	if err = s.DB().Exec("DROP TRIGGER fail_audit").Error; err != nil {
		t.Fatal(err)
	}
	rows, plain, err = s.CreateRedeemCodes(ctx, 1, template, 3)
	if err != nil || len(rows) != 3 || len(plain) != 3 {
		t.Fatal("batch failed")
	}
	seen := map[string]bool{}
	for i, row := range rows {
		if row.Hash != secret.Hash(plain[i]) || row.Hash == plain[i] || seen[row.Hash] {
			t.Fatal("redeem credential storage invalid")
		}
		seen[row.Hash] = true
	}
	logs, _ := s.ListAudit(ctx, 10)
	if len(logs) != 1 || logs[0].Action != "redeem.create" {
		t.Fatal("batch audit missing")
	}
}
