// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"encoding/json"
	v1 "github.com/nomifun/nomifun-model-gateway/contract/v1"
	"github.com/nomifun/nomifun-model-gateway/internal/core"
	"testing"
)

func TestModelWritesRespectDesktopLimitsWithoutNarrowingMoney(t *testing.T) {
	for _, field := range []string{"context", "output"} {
		for _, limit := range []int64{0, -1, v1.MaxTokenLimit + 1, 9223372036854775807} {
			model := v1.MockCatalog(false).Models[0]
			if field == "context" {
				model.ContextWindow = &limit
			} else {
				model.MaxOutputTokens = &limit
			}
			if ValidateDesktopModel(model) == nil {
				t.Fatalf("accepted incompatible %s limit %d", field, limit)
			}
		}
	}
	model := v1.MockCatalog(false).Models[0]
	limit := v1.MaxTokenLimit
	model.ContextWindow, model.MaxOutputTokens = &limit, &limit
	model.Pricing[0].Amount = 9223372036854775807
	if err := ValidateDesktopModel(model); err != nil {
		t.Fatalf("safe limits and int64 money rejected: %v", err)
	}
	// Advisory or historical technical traits do not authorize or deny a route.
	model.Traits = []string{"function_calling", "streaming", "future_advisory"}
	if err := ValidateDesktopModel(model); err != nil {
		t.Fatalf("v1 advisory vocabulary was narrowed: %v", err)
	}
	model.Tasks = []v1.Task{v1.Task("speech_synthesis")}
	model.TaskEndpoints = map[v1.Task]v1.TaskEndpoints{model.Tasks[0]: {Endpoints: []v1.Endpoint{v1.OpenAI}, PreferredEndpoint: v1.OpenAI}}
	if ValidateDesktopModel(model) == nil {
		t.Fatal("unsupported task acquired a Chat fallback")
	}
}

func TestHistoricalInt64LimitsStayReadableAndRequireExplicitCorrection(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	legacy := v1.MockCatalog(false).Models[0]
	limit := int64(9223372036854775807)
	legacy.ContextWindow = &limit
	if err := ValidateModel(legacy); err != nil {
		t.Fatalf("frozen v1 definition rejected: %v", err)
	}
	encoded, _ := json.Marshal(legacy)
	if err := s.DB().Create(&core.Model{ID: legacy.ID, DefinitionJSON: string(encoded), Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	read, err := s.Model(ctx, legacy.ID)
	if err != nil || read.ContextWindow == nil || *read.ContextWindow != limit {
		t.Fatalf("legacy limit was lost: %+v %v", read, err)
	}
	if _, err := s.ListModels(ctx, false); err != nil {
		t.Fatalf("admin cannot repair historical records: %v", err)
	}
	if err := s.SaveModelAccess(ctx, read, true, false); err == nil {
		t.Fatal("unsafe limit saved without correction")
	}
	corrected := int64(128000)
	read.ContextWindow = &corrected
	if err := s.SaveModelAccess(ctx, read, true, false); err != nil {
		t.Fatal(err)
	}
}
