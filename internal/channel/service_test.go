// SPDX-License-Identifier: Apache-2.0
package channel

import (
	"context"
	"errors"
	"github.com/glebarez/sqlite"
	"github.com/nomifun/nomifun-model-gateway/internal/core"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"path/filepath"
	"testing"
	"time"
)

func database(t *testing.T) *gorm.DB {
	t.Helper()
	db, e := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "affinity.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if e != nil {
		t.Fatal(e)
	}
	sql, _ := db.DB()
	sql.SetMaxOpenConns(1)
	t.Cleanup(func() { sql.Close() })
	if e = db.AutoMigrate(&core.Channel{}, &core.ResponseAffinity{}, &core.SessionAffinity{}); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 2; i++ {
		if e = db.Create(&core.Channel{ID: int64(i + 1), Kind: "anthropic", ModelsJSON: `{"public":"private"}`, EndpointsJSON: `["anthropic"]`, Enabled: true, Weight: 1, Priority: 2 - i}).Error; e != nil {
			t.Fatal(e)
		}
	}
	return db
}
func TestSessionExpiryTombstoneSurvivesServiceReopen(t *testing.T) {
	db := database(t)
	s := New(db, 0)
	now := time.Now()
	s.Now = func() time.Time { return now }
	ctx := context.Background()
	first, bound, e := s.Candidates(ctx, 1, "public", "anthropic", "", "session")
	if e != nil || !bound || first[0].ID != 1 {
		t.Fatalf("initial binding %+v %v", first, e)
	}
	if _, _, e = s.Candidates(ctx, 2, "public", "anthropic", "", "session"); e != nil {
		t.Fatal("separate identity prevented new session")
	}
	now = now.Add(24 * time.Hour)
	if _, _, e = s.Candidates(ctx, 1, "public", "anthropic", "", "session"); !errors.Is(e, ErrAffinity) {
		t.Fatal("expired session silently rebound")
	}
	reopened := New(db, 0)
	reopened.Now = func() time.Time { return now }
	if _, _, e = reopened.Candidates(ctx, 1, "public", "anthropic", "", "session"); !errors.Is(e, ErrAffinity) {
		t.Fatal("expiry tombstone lost on service reopen")
	}
	var record core.SessionAffinity
	db.Where("api_key_id = ? AND session_id = ?", 1, "session").First(&record)
	if !record.Expired || record.ChannelID != 1 {
		t.Fatal("expiry not durably preserved")
	}
	newSession, _, e := reopened.Candidates(ctx, 1, "public", "anthropic", "", "explicit-new-session")
	if e != nil || newSession[0].ID != 1 {
		t.Fatal("new explicit session blocked")
	}
}
func TestBoundAccountNeverFailsOverDuringCooldownOrDeletion(t *testing.T) {
	db := database(t)
	s := New(db, 0)
	ctx := context.Background()
	s.BindResponse(ctx, 1, "response", 1)
	until := time.Now().Add(time.Hour)
	db.Model(&core.Channel{}).Where("id = ?", 1).Update("cooldown_until", until)
	if _, _, e := s.Candidates(ctx, 1, "public", "anthropic", "response", ""); !errors.Is(e, ErrAffinity) {
		t.Fatal("response affinity ignored cooldown failure")
	}
	unbound, bound, e := s.Candidates(ctx, 1, "public", "anthropic", "", "")
	if e != nil || bound || unbound[0].ID != 2 {
		t.Fatal("unbound request did not use healthy account")
	}
	db.Delete(&core.Channel{}, 1)
	if _, _, e = s.Candidates(ctx, 1, "public", "anthropic", "response", ""); !errors.Is(e, ErrAffinity) {
		t.Fatal("deleted bound account silently rebound")
	}
	if e = s.BindResponse(ctx, 1, "response", 2); !errors.Is(e, ErrAffinity) {
		t.Fatal("immutable response binding was replaced")
	}
}
func TestSuccessRefreshesSessionOnlyForBoundAccount(t *testing.T) {
	db := database(t)
	s := New(db, 0)
	ctx := context.Background()
	now := time.Now()
	s.Now = func() time.Time { return now }
	s.Candidates(ctx, 1, "public", "anthropic", "", "session")
	now = now.Add(23 * time.Hour)
	s.Success(ctx, 2, 1, "session")
	now = now.Add(2 * time.Hour)
	if _, _, e := s.Candidates(ctx, 1, "public", "anthropic", "", "session"); !errors.Is(e, ErrAffinity) {
		t.Fatal("other account refreshed session")
	}
}
