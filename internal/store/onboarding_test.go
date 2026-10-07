// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"errors"
	"testing"

	v1 "github.com/nomifun/nomifun-model-gateway/contract/v1"
	"github.com/nomifun/nomifun-model-gateway/internal/core"
	"gorm.io/gorm"
)

func onboardingModel(id string) v1.Model {
	return v1.Model{ID: id, DisplayName: "Synthetic", Vendor: "synthetic", Tasks: []v1.Task{v1.Chat}, TaskEndpoints: map[v1.Task]v1.TaskEndpoints{v1.Chat: {Endpoints: []v1.Endpoint{v1.OpenAI}, PreferredEndpoint: v1.OpenAI}}, InputModalities: []string{"text"}, Traits: []string{}, Pricing: []v1.Price{}, Status: "unavailable"}
}

func TestOnboardingAtomicConflictValidationAndCredential(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	candidate := core.Channel{Name: "Synthetic", Kind: "openai", BaseURL: "https://synthetic.example", ModelsJSON: `{"new":"private"}`, EndpointsJSON: `["openai"]`, Weight: 1}
	model := OnboardingModel{Definition: onboardingModel("new")}
	created, err := s.CreateOnboarding(ctx, candidate, "synthetic-key", []OnboardingModel{model}, nil)
	if err != nil {
		t.Fatal(err)
	}
	key, err := s.ChannelKey(created)
	if err != nil || key != "synthetic-key" {
		t.Fatal("onboarding did not seal credential with the assigned immutable identity")
	}
	assertCount := func(channels, models int64) {
		t.Helper()
		var channelCount, modelCount int64
		if err := s.DB().Model(&core.Channel{}).Count(&channelCount).Error; err != nil {
			t.Fatal(err)
		}
		if err := s.DB().Model(&core.Model{}).Count(&modelCount).Error; err != nil {
			t.Fatal(err)
		}
		if channelCount != channels || modelCount != models {
			t.Fatalf("partial onboarding: channels=%d models=%d", channelCount, modelCount)
		}
	}
	assertCount(1, 1)
	if _, err := s.CreateOnboarding(ctx, candidate, "synthetic-key", []OnboardingModel{model}, nil); !errors.Is(err, ErrConflict) {
		t.Fatalf("existing public ID was not a conflict: %v", err)
	}
	assertCount(1, 1)
	other := OnboardingModel{Definition: onboardingModel("other")}
	if _, err := s.CreateOnboarding(ctx, candidate, "synthetic-key", []OnboardingModel{other}, func(tx *gorm.DB, c core.Channel) error {
		if c.ID == 0 || c.EncryptedKey == "" {
			t.Fatal("validator did not observe the newly sealed channel")
		}
		return errors.New("synthetic publication failure")
	}); err == nil {
		t.Fatal("failed publication validator committed")
	}
	assertCount(1, 1)
	if _, err := s.CreateOnboarding(ctx, candidate, "synthetic-key", []OnboardingModel{other, model}, nil); !errors.Is(err, ErrConflict) {
		t.Fatalf("late conflict was not returned: %v", err)
	}
	assertCount(1, 1)
	if _, err := s.CreateOnboarding(ctx, candidate, "synthetic-key", []OnboardingModel{other, other}, nil); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate submitted public ID was not a conflict: %v", err)
	}
	assertCount(1, 1)
}

func TestExplicitModelCreateUpdateAndPricingRollback(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	model := onboardingModel("model")
	if err := s.UpdateModelAccess(ctx, model, false, false, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("editing absent model created it: %v", err)
	}
	if err := s.CreateModelAccess(ctx, model, false, false, nil); err != nil {
		t.Fatal(err)
	}
	model.DisplayName = "Changed"
	if err := s.CreateModelAccess(ctx, model, false, false, nil); !errors.Is(err, ErrConflict) {
		t.Fatalf("creating same ID overwrote it: %v", err)
	}
	stored, err := s.Model(ctx, model.ID)
	if err != nil || stored.DisplayName != "Synthetic" {
		t.Fatal("conflicting create changed the catalog")
	}
	if err := s.UpdateModelAccess(ctx, model, false, true, nil); err != nil {
		t.Fatal(err)
	}
	prices := []v1.Price{{Task: v1.Chat, Meter: "requests", UnitSize: 1, Amount: 9007199254740993, Currency: "USD"}}
	if err := s.UpdateModelPricing(ctx, model.ID, prices, func(*gorm.DB, OnboardingModel) error { return errors.New("synthetic rejection") }); err == nil {
		t.Fatal("rejected price update committed")
	}
	stored, _ = s.Model(ctx, model.ID)
	if len(stored.Pricing) != 0 {
		t.Fatal("failed price validation changed pricing")
	}
	if err := s.UpdateModelPricing(ctx, model.ID, prices, nil); err != nil {
		t.Fatal(err)
	}
	stored, _ = s.Model(ctx, model.ID)
	record, _ := s.ModelRecord(ctx, model.ID)
	if stored.DisplayName != "Changed" || !record.SubscriptionOnly || record.Enabled || stored.Pricing[0].Amount != prices[0].Amount {
		t.Fatal("price update changed access/catalog settings or int64 precision")
	}
}
