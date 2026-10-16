// SPDX-License-Identifier: Apache-2.0
package relay

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestDiscoveryAdvisoryLimitsAreExactPositiveRendererSafeIntegers(t *testing.T) {
	for _, test := range []struct {
		raw  string
		want int64
	}{
		{`1000`, 1000}, {`1e3`, 1000}, {`1000.000`, 1000}, {`" 1000 "`, 1000},
		{`9007199254740991`, maxDiscoveryTokenLimit},
		{`null`, 0}, {`0`, 0}, {`-1`, 0}, {`1.25`, 0}, {`"1.25"`, 0},
		{`9007199254740992`, 0}, {`9223372036854775808`, 0},
		{`9007199254740990.999999999999999999`, 0},
		{`1e9999999999999999999999`, 0}, {`1e-9999999999999999999999`, 0},
		{`true`, 0}, {`{}`, 0}, {`[]`, 0}, {`"unknown"`, 0}, {`""`, 0},
	} {
		t.Run(test.raw, func(t *testing.T) {
			body := []byte(`{"models":[{"name":"models/opaque","inputTokenLimit":` + test.raw + `,"outputTokenLimit":` + test.raw + `}]}`)
			models, _, _, _, err := decodeDiscoveryPage("gemini", body, 10)
			if err != nil || len(models) != 1 || models[0].ID != "opaque" {
				t.Fatalf("advisory metadata broke the list: %#v %v", models, err)
			}
			model := models[0]
			if test.want == 0 {
				if model.InputTokenLimit != nil || model.OutputTokenLimit != nil || model.ContextLimit != nil || model.OutputLimit != nil || model.TokenLimitSources != nil {
					t.Fatalf("invalid advisory value survived: %#v", model)
				}
				encoded, _ := json.Marshal(model)
				for _, field := range []string{"input_token_limit", "output_token_limit", "context_limit", "output_limit", "token_limit_sources"} {
					if strings.Contains(string(encoded), field) {
						t.Fatalf("unknown advisory field %s must be absent: %s", field, encoded)
					}
				}
			} else if model.ContextLimit == nil || *model.ContextLimit != test.want || model.OutputLimit == nil || *model.OutputLimit != test.want || model.InputTokenLimit == nil || *model.InputTokenLimit != test.want || model.OutputTokenLimit == nil || *model.OutputTokenLimit != test.want {
				t.Fatalf("exact advisory value lost: %#v", model)
			}
		})
	}
}

func TestDiscoveryNativeLimitsCarryFieldOriginsAndContextSemantics(t *testing.T) {
	for _, test := range []struct {
		name, kind, body, contextSource, contextKind, outputSource string
		wantContext, wantOutput                                    int64
	}{
		{"gemini", "gemini", `{"models":[{"name":"models/opaque","inputTokenLimit":1000,"outputTokenLimit":100}]}`, "inputTokenLimit", "input_only", "outputTokenLimit", 1000, 100},
		{"anthropic", "anthropic", `{"data":[{"id":"opaque","max_input_tokens":"1000","max_tokens":100.0}],"has_more":false}`, "max_input_tokens", "input_only", "max_tokens", 1000, 100},
		{"compatible", "compatible", `{"data":[{"id":"opaque","context_length":1000,"top_provider":{"context_length":2000,"max_completion_tokens":100}}]}`, "context_length", "combined", "top_provider.max_completion_tokens", 1000, 100},
		{"compatible fallback", "compatible", `{"data":[{"id":"opaque","context_length":0,"top_provider":{"context_length":"1000","max_completion_tokens":100}}]}`, "top_provider.context_length", "combined", "top_provider.max_completion_tokens", 1000, 100},
		{"output only", "gemini", `{"models":[{"name":"models/opaque","inputTokenLimit":false,"outputTokenLimit":100}]}`, "", "", "outputTokenLimit", 0, 100},
		{"context only", "anthropic", `{"data":[{"id":"opaque","max_input_tokens":1000,"max_tokens":{}}],"has_more":false}`, "max_input_tokens", "input_only", "", 1000, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			models, _, _, _, err := decodeDiscoveryPage(test.kind, []byte(test.body), 10)
			if err != nil || len(models) != 1 {
				t.Fatalf("decode metadata: %#v %v", models, err)
			}
			model := models[0]
			sources := model.TokenLimitSources
			if sources == nil || sources.ContextLimit != test.contextSource || sources.ContextLimitKind != test.contextKind || sources.OutputLimit != test.outputSource {
				t.Fatalf("native field provenance or context semantics lost: %#v", sources)
			}
			if (model.ContextLimit == nil) != (test.wantContext == 0) || (model.OutputLimit == nil) != (test.wantOutput == 0) {
				t.Fatalf("advisory absence semantics changed: %#v", model)
			}
			if model.ContextLimit != nil && *model.ContextLimit != test.wantContext || model.OutputLimit != nil && *model.OutputLimit != test.wantOutput {
				t.Fatalf("advisory values changed: %#v", model)
			}
		})
	}
}

func TestDiscoveryOnlyUnambiguousNativeMethodsSuggestTasks(t *testing.T) {
	for _, test := range []struct {
		name, methods string
		want          []string
	}{
		{"mixed", `["embedContent","batchEmbedContents","bidiGenerateContent","embedContent","futureMethod"]`, []string{"embedding", "realtime_conversation"}},
		{"generateContent is ambiguous", `["generateContent","streamGenerateContent"]`, nil},
		{"unknown", `["futureMethod"]`, nil},
		{"invalid advisory type", `true`, nil},
		{"invalid entry invalidates methods", `["embedContent",42]`, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			// A suggestive ID is deliberately irrelevant to task detection.
			body := []byte(`{"models":[{"name":"models/chat-image-audio-embedding","supportedGenerationMethods":` + test.methods + `}]}`)
			models, _, _, _, err := decodeDiscoveryPage("gemini", body, 10)
			if err != nil || len(models) != 1 || !reflect.DeepEqual(models[0].Tasks, test.want) {
				t.Fatalf("explicit methods changed: %#v %v", models, err)
			}
			if len(test.want) == 0 && models[0].TasksSource != "" || len(test.want) > 0 && models[0].TasksSource != "provider_declared" {
				t.Fatalf("task evidence changed: %#v", models[0])
			}
			if test.name == "mixed" && len(models[0].SupportedGenerationMethods) != 5 {
				t.Fatal("native generation methods must be preserved, including unknown methods")
			}
		})
	}
}

func TestDiscoveryUnverifiedExtensionsCannotGrantTasksPricesOrProtocols(t *testing.T) {
	models, _, _, _, err := decodeDiscoveryPage("compatible", []byte(`{"data":[{"id":"chat-image-audio-embedding","tasks":["chat"],"tasks_source":"official_documentation","traits":["vision_input"],"context_window":1000,"inputTokenLimit":2000,"max_tokens":3000,"pricing":[{"amount":1}],"endpoints":["anthropic"],"token_limit_sources":{"context_limit":"official_documentation:fake"}}]}`), 10)
	if err != nil || len(models) != 1 {
		t.Fatalf("decode: %#v %v", models, err)
	}
	encoded, _ := json.Marshal(models[0])
	if string(encoded) != `{"id":"chat-image-audio-embedding"}` {
		t.Fatalf("unverified extensions became catalog authority: %s", encoded)
	}
}

func TestDiscoveryCatalogSourceDoesNotImplyAcceptanceOrSuccessfulAzureRead(t *testing.T) {
	s, base := discoveryFixture(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"opaque"}]}`))
	})
	for _, kind := range []string{"openai", "azure"} {
		out, err := s.DiscoverPreview(context.Background(), DiscoveryInput{Kind: kind, BaseURL: base, APIKey: discoverySyntheticKey})
		if err != nil || !out.Advisory {
			t.Fatalf("catalog must remain advisory: %#v %v", out, err)
		}
		if kind == "openai" && out.CatalogSource != "remote" || kind == "azure" && out.CatalogSource != "" {
			t.Fatalf("unperformed remote read claimed a catalog source: %#v", out)
		}
		encoded, _ := json.Marshal(out)
		for _, forbidden := range []string{"protocol_verified", "accepted", "pricing", "selling_price"} {
			if strings.Contains(string(encoded), forbidden) {
				t.Fatalf("discovery claimed protocol acceptance or a selling price: %s", encoded)
			}
		}
	}
}
