// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	v1 "github.com/nomifun/nomifun-model-gateway/contract/v1"
	"github.com/nomifun/nomifun-model-gateway/internal/core"
)

func syntheticOnboarding(id string, enabled bool) onboardingInput {
	context, output := int64(8192), int64(256)
	return onboardingInput{Channel: channelInput{Name: "Synthetic onboarding", Kind: "compatible", BaseURL: "https://synthetic.example/v1", APIKey: "synthetic-onboarding-key", ModelIDs: map[string]string{id: "private"}, Endpoints: []v1.Endpoint{v1.OpenAI}, Weight: 1, Enabled: true}, Models: []modelDTO{{Model: v1.Model{ID: id, DisplayName: "Synthetic", Vendor: "synthetic", Tasks: []v1.Task{v1.Chat}, TaskEndpoints: map[v1.Task]v1.TaskEndpoints{v1.Chat: {Endpoints: []v1.Endpoint{v1.OpenAI}, PreferredEndpoint: v1.OpenAI}}, ContextWindow: &context, MaxOutputTokens: &output, InputModalities: []string{"text"}, Traits: []string{}, Pricing: []v1.Price{{Task: v1.Chat, Meter: "requests", UnitSize: 1, Amount: 9007199254740993, Currency: "USD"}}, Status: "available"}, Enabled: enabled}}}
}

func jsonBody(t *testing.T, input any) string {
	t.Helper()
	body, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func TestAdminOnboardingCheckAtomicSubmitAndSafeModelEditing(t *testing.T) {
	f := setupServer(t)
	base := "/api/console/v1/admin"
	draft := syntheticOnboarding("onboarded", true)
	checked := call(t, f.app, "POST", base+"/onboarding/check", jsonBody(t, draft), f.admin)
	if checked.Code != 200 || !strings.Contains(checked.Body.String(), `"ready":true`) {
		t.Fatalf("publication check: %d %s", checked.Code, checked.Body.String())
	}
	var channelsBefore int64
	f.st.DB().Model(&core.Channel{}).Count(&channelsBefore)
	created := call(t, f.app, "POST", base+"/onboarding", jsonBody(t, draft), f.admin)
	if created.Code != 201 {
		t.Fatalf("submit: %d %s", created.Code, created.Body.String())
	}
	if strings.Contains(created.Body.String(), draft.Channel.APIKey) {
		t.Fatal("onboarding response exposed its credential")
	}
	var createdChannel channelDTO
	if json.Unmarshal(document(t, created)["channel"], &createdChannel) != nil {
		t.Fatal("missing created channel")
	}
	stored, err := f.st.Model(context.Background(), "onboarded")
	if err != nil || stored.Pricing[0].Amount != 9007199254740993 {
		t.Fatal("integer price precision changed during onboarding")
	}
	duplicate := call(t, f.app, "POST", base+"/onboarding", jsonBody(t, draft), f.admin)
	if duplicate.Code != 409 || !strings.Contains(duplicate.Body.String(), `"field":"id"`) {
		t.Fatalf("repeat submission: %d %s", duplicate.Code, duplicate.Body.String())
	}
	var channelsAfter int64
	f.st.DB().Model(&core.Channel{}).Count(&channelsAfter)
	if channelsAfter != channelsBefore+1 {
		t.Fatal("conflicting submission left a partial channel")
	}
	draft.Models[0].DisplayName = "Unrequested overwrite"
	duplicateModel := call(t, f.app, "POST", base+"/models", jsonBody(t, draft.Models[0]), f.admin)
	if duplicateModel.Code != 409 {
		t.Fatalf("model create silently overwrote same ID: %d", duplicateModel.Code)
	}
	stored, _ = f.st.Model(context.Background(), "onboarded")
	if stored.DisplayName != "Synthetic" {
		t.Fatal("conflict overwrote model")
	}
	draft.Models[0].DisplayName = "Explicit edit"
	updated := call(t, f.app, "PATCH", base+"/models/onboarded", jsonBody(t, draft.Models[0]), f.admin)
	if updated.Code != 200 {
		t.Fatalf("explicit edit: %d %s", updated.Code, updated.Body.String())
	}
	wrongID := call(t, f.app, "PATCH", base+"/models/other", jsonBody(t, draft.Models[0]), f.admin)
	if wrongID.Code != 400 {
		t.Fatalf("model ID rename accepted: %d", wrongID.Code)
	}
	unpriced := call(t, f.app, "PUT", base+"/pricing/onboarded", `{"pricing":[]}`, f.admin)
	if unpriced.Code != 422 || !strings.Contains(unpriced.Body.String(), "pricing.chat") {
		t.Fatalf("enabled price removal accepted: %d %s", unpriced.Code, unpriced.Body.String())
	}
	stored, _ = f.st.Model(context.Background(), "onboarded")
	if len(stored.Pricing) != 1 || stored.DisplayName != "Explicit edit" {
		t.Fatal("failed price publication changed model state")
	}
	channelInput := draft.Channel
	channelInput.APIKey = ""
	channelInput.Enabled = false
	disabled := call(t, f.app, "PATCH", base+"/channels/"+strconvID(createdChannel.ID), jsonBody(t, channelInput), f.admin)
	if disabled.Code != 422 {
		t.Fatalf("removed last published route: %d %s", disabled.Code, disabled.Body.String())
	}
	if response := call(t, f.app, "DELETE", base+"/channels/"+strconvID(createdChannel.ID), "", f.admin); response.Code != 422 {
		t.Fatalf("deleted last published route: %d %s", response.Code, response.Body.String())
	}
	if response := call(t, f.app, "DELETE", base+"/models/onboarded", "", f.admin); response.Code != 200 {
		t.Fatal("could not disable model")
	}
	if response := call(t, f.app, "PATCH", base+"/channels/"+strconvID(createdChannel.ID), jsonBody(t, channelInput), f.admin); response.Code != 200 {
		t.Fatalf("disabled model prevented operational channel edit: %d %s", response.Code, response.Body.String())
	}
}

func TestAdminPublicationGapsRemainDisabledAndReportFields(t *testing.T) {
	f := setupServer(t)
	base := "/api/console/v1/admin"
	draft := syntheticOnboarding("pending", false)
	draft.Models[0].Pricing = []v1.Price{}
	draft.Models[0].ContextWindow = nil
	draft.Models[0].MaxOutputTokens = nil
	checked := call(t, f.app, "POST", base+"/onboarding/check", jsonBody(t, draft), f.admin)
	for _, field := range []string{`"ready":false`, "pricing.chat", "context_window", "max_output_tokens"} {
		if checked.Code != 200 || !strings.Contains(checked.Body.String(), field) {
			t.Fatalf("missing publication issue %s: %d %s", field, checked.Code, checked.Body.String())
		}
	}
	created := call(t, f.app, "POST", base+"/onboarding", jsonBody(t, draft), f.admin)
	if created.Code != 201 {
		t.Fatalf("pending disabled save: %d %s", created.Code, created.Body.String())
	}
	record, err := f.st.ModelRecord(context.Background(), "pending")
	model, modelErr := f.st.Model(context.Background(), "pending")
	if err != nil || modelErr != nil || record.Enabled || len(model.Pricing) != 0 {
		t.Fatal("pending model was published or given an invented free tariff")
	}
	standalone := syntheticOnboarding("not-created", false).Models[0]
	standalone.Enabled = true
	rejected := call(t, f.app, "POST", base+"/models", jsonBody(t, standalone), f.admin)
	if rejected.Code != 422 {
		t.Fatalf("unmapped enabled model accepted: %d %s", rejected.Code, rejected.Body.String())
	}
	standalone.Enabled = false
	if response := call(t, f.app, "POST", base+"/models", jsonBody(t, standalone), f.admin); response.Code != 201 {
		t.Fatalf("disabled standalone create failed: %d %s", response.Code, response.Body.String())
	}
	check := call(t, f.app, "POST", base+"/models/check", jsonBody(t, standalone), f.admin)
	if check.Code != 200 || !strings.Contains(check.Body.String(), `"ready":false`) || strings.Contains(check.Body.String(), "already exists") {
		t.Fatalf("edit checklist incorrectly conflicts with its own ID: %d %s", check.Code, check.Body.String())
	}
	// No new catalog/channel rows survive a rejected enabled combined submit.
	invalid := syntheticOnboarding("rejected", true)
	invalid.Models[0].Pricing = []v1.Price{}
	var before, after int64
	f.st.DB().Model(&core.Channel{}).Count(&before)
	response := call(t, f.app, "POST", base+"/onboarding", jsonBody(t, invalid), f.admin)
	f.st.DB().Model(&core.Channel{}).Count(&after)
	if response.Code != 422 || after != before {
		t.Fatalf("rejected submission left channel: %d before=%d after=%d", response.Code, before, after)
	}
	if response := call(t, f.app, "POST", base+"/onboarding/check", jsonBody(t, draft), ""); response.Code != 401 {
		t.Fatal("publication check bypassed admin authentication")
	}
}

func TestPublicationChecksNativeKindCurrenciesPricingAndPlan(t *testing.T) {
	f := setupServer(t)
	base := "/api/console/v1/admin/onboarding/check"
	for _, tc := range []struct {
		name   string
		mutate func(*onboardingInput)
		field  string
	}{
		{"native-kind", func(in *onboardingInput) { in.Channel.Kind = "anthropic" }, "task_endpoints.chat.endpoints"},
		{"unresolved-template", func(in *onboardingInput) { in.Channel.BaseURL = "https://YOUR-RESOURCE-NAME.openai.azure.com" }, "channel.base_url"},
		{"disabled-channel", func(in *onboardingInput) { in.Channel.Enabled = false }, "task_endpoints.chat.endpoints"},
		{"currency", func(in *onboardingInput) { in.Models[0].Pricing[0].Currency = "CNY" }, "pricing.chat.currency"},
		{"unsupported-meter", func(in *onboardingInput) { in.Models[0].Pricing[0].Meter = "unknown_meter" }, "pricing.chat"},
		{"missing-output-price", func(in *onboardingInput) {
			in.Models[0].Pricing[0].Meter = "input_tokens"
			in.Models[0].Pricing[0].Amount = 1
		}, "pricing.chat"},
		{"overlap-cache", func(in *onboardingInput) {
			in.Models[0].Pricing = []v1.Price{{Task: v1.Chat, Meter: "cached_input_tokens", UnitSize: 1, Amount: 1, Currency: "USD"}, {Task: v1.Chat, Meter: "cache_read_input_tokens", UnitSize: 1, Amount: 1, Currency: "USD"}}
		}, "pricing.chat"},
		{"subscription", func(in *onboardingInput) { in.Models[0].SubscriptionOnly = true }, "subscription_only"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := syntheticOnboarding("checked", true)
			tc.mutate(&in)
			response := call(t, f.app, "POST", base, jsonBody(t, in), f.admin)
			if response.Code != 200 || !strings.Contains(response.Body.String(), `"ready":false`) || !strings.Contains(response.Body.String(), tc.field) {
				t.Fatalf("missing %s issue: %d %s", tc.field, response.Code, response.Body.String())
			}
		})
	}
	plan := core.Plan{Name: "Synthetic plan", Price: 1, Currency: "USD", PeriodDays: 30, ModelIDsJSON: `["checked"]`, Enabled: true}
	if _, err := f.st.SavePlan(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	in := syntheticOnboarding("checked", true)
	in.Models[0].SubscriptionOnly = true
	response := call(t, f.app, "POST", base, jsonBody(t, in), f.admin)
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"ready":true`) {
		t.Fatalf("covering plan not recognized: %d %s", response.Code, response.Body.String())
	}
}

func TestConcurrentModelPublicationAndLastRouteMutation(t *testing.T) {
	f := setupServer(t)
	base := "/api/console/v1/admin"
	for _, mutation := range []string{"disable", "remap", "delete"} {
		t.Run(mutation, func(t *testing.T) {
			in := syntheticOnboarding("concurrent-"+mutation, false)
			created, err := f.st.CreateChannel(context.Background(), in.Channel.channel(0), in.Channel.APIKey)
			if err != nil {
				t.Fatal(err)
			}
			model := in.Models[0]
			if err := f.st.CreateModelAccess(context.Background(), model.Model, false, false, nil); err != nil {
				t.Fatal(err)
			}
			model.Enabled = true
			publishBody := jsonBody(t, model)
			operation := "PATCH"
			channel := in.Channel
			channel.APIKey = ""
			switch mutation {
			case "disable":
				channel.Enabled = false
			case "remap":
				channel.ModelIDs = map[string]string{"other-public-model": "private"}
			case "delete":
				operation = "DELETE"
			}
			mutationBody := jsonBody(t, channel)
			if operation == "DELETE" {
				mutationBody = ""
			}
			start := make(chan struct{})
			results := make(chan int, 2)
			go func() {
				<-start
				results <- call(t, f.app, "PATCH", base+"/models/"+model.ID, publishBody, f.admin).Code
			}()
			go func() {
				<-start
				results <- call(t, f.app, operation, base+"/channels/"+strconvID(created.ID), mutationBody, f.admin).Code
			}()
			close(start)
			first, second := <-results, <-results
			if !((first == 200 && second == 422) || (first == 422 && second == 200)) {
				t.Fatalf("publication and final-route mutation both succeeded or both failed: %d %d", first, second)
			}
			record, err := f.st.ModelRecord(context.Background(), model.ID)
			if err != nil {
				t.Fatal(err)
			}
			if record.Enabled {
				channel, err := f.st.Channel(context.Background(), created.ID)
				if err != nil || !channel.Enabled || channel.ModelsJSON != created.ModelsJSON {
					t.Fatal("enabled model lost its final native route")
				}
			}
		})
	}
}
