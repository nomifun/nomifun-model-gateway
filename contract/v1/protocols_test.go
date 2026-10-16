// SPDX-License-Identifier: Apache-2.0
package v1_test

import (
	"encoding/json"
	"reflect"
	"testing"

	v1 "github.com/nomifun/nomifun-model-gateway/contract/v1"
)

func TestProtocolManifestPreservesDesktopV1ConnectionsAndNativePaths(t *testing.T) {
	wantEndpoints := []v1.Endpoint{v1.OpenAI, v1.Responses, v1.Anthropic, v1.Gemini, v1.Images, v1.Embeddings, v1.Rerank}
	if !reflect.DeepEqual(v1.Endpoints(), wantEndpoints) {
		t.Fatal("registry changed the frozen v1 endpoint order")
	}
	want := map[v1.Endpoint]struct {
		protocol, role, auth, shape, suffix string
		paths                               []string
		tasks                               []v1.Task
	}{
		v1.OpenAI:     {"openai.chat_text", "default", "bearer", "versioned_root", "/v1", []string{"/chat/completions"}, []v1.Task{v1.Chat}},
		v1.Responses:  {"openai.responses", "default", "bearer", "versioned_root", "/v1", []string{"/responses"}, []v1.Task{v1.Chat}},
		v1.Anthropic:  {"anthropic.messages", "anthropic", "header_key:x-api-key", "origin_root", "", []string{"/v1/messages"}, []v1.Task{v1.Chat}},
		v1.Gemini:     {"gemini.generate_text", "gemini", "header_key:x-goog-api-key", "origin_root", "", []string{"/v1beta/models/{model}:generateContent", "/v1beta/models/{model}:streamGenerateContent"}, []v1.Task{v1.Chat}},
		v1.Images:     {"openai.images", "default", "bearer", "versioned_root", "/v1", []string{"/images/generations", "/images/edits"}, []v1.Task{v1.ImageGeneration, v1.ImageEdit}},
		v1.Embeddings: {"openai.embeddings", "default", "bearer", "versioned_root", "/v1", []string{"/embeddings"}, []v1.Task{v1.Embedding}},
		v1.Rerank:     {"generic.rerank", "default", "bearer", "versioned_root", "/v1", []string{"/rerank"}, []v1.Task{v1.Reranking}},
	}
	manifest := v1.Manifest(wantEndpoints)
	if manifest.ContractVersion != "1.0" || manifest.ManifestVersion != "1" || len(manifest.Protocols) != len(want) {
		t.Fatal("unexpected manifest identity or protocol count")
	}
	for _, protocol := range manifest.Protocols {
		expected, ok := want[protocol.Endpoint]
		if !ok {
			t.Fatalf("unexpected registry endpoint %q", protocol.Endpoint)
		}
		if protocol.ProtocolID != expected.protocol || protocol.ConnectionRole != expected.role || protocol.AuthScheme != expected.auth || protocol.RootShape != expected.shape || protocol.BaseURLSuffix != expected.suffix {
			t.Fatalf("protocol connection changed for %q: %+v", protocol.Endpoint, protocol)
		}
		chat := protocol.SupportedTasks[0] == v1.Chat
		wantExecutor := "model_invoke"
		if chat {
			wantExecutor = "agent"
		}
		if protocol.Executor != wantExecutor || protocol.Transport != "http" || protocol.RequiresOutputCeiling != chat || protocol.RequiresStreamUsage != (protocol.Endpoint == v1.OpenAI) || !reflect.DeepEqual(protocol.SupportedTasks, expected.tasks) {
			t.Fatalf("protocol task/transport policy changed for %q", protocol.Endpoint)
		}
		var paths []string
		for index, endpoint := range protocol.Endpoints {
			paths = append(paths, endpoint.DefaultValue)
			purpose := "submit"
			if protocol.Endpoint == v1.Gemini && index == 1 {
				purpose = "stream"
			}
			if endpoint.Method != "POST" || endpoint.Field != "endpoint" || endpoint.Purpose != purpose || endpoint.RootShape != protocol.RootShape || !v1.SupportsTaskEndpoint(endpoint.Task, protocol.Endpoint) {
				t.Fatalf("invalid native operation for %q: %+v", protocol.Endpoint, endpoint)
			}
		}
		if !reflect.DeepEqual(paths, expected.paths) {
			t.Fatalf("native paths changed for %q: %v", protocol.Endpoint, paths)
		}
	}
	if manifest.MaxTokenLimit != 9_007_199_254_740_991 || !manifest.TraitsAreAdvisory {
		t.Fatal("Desktop integer or advisory metadata policy changed")
	}
	if manifest.MaxTokenLimit != v1.MaxTokenLimit {
		t.Fatal("manifest does not expose the producer token ceiling")
	}
	if !reflect.DeepEqual(manifest.UnsupportedTasks, []string{"realtime_conversation", "video_generation", "music_generation", "speech_synthesis", "speech_recognition"}) || !reflect.DeepEqual(manifest.CatalogTraits, []string{"vision_input", "audio_input", "video_input", "web_search"}) || !reflect.DeepEqual(manifest.TechnicalCapabilities, []string{"function_calling", "reasoning", "streaming"}) {
		t.Fatal("task and feature boundaries changed")
	}
}

func TestProtocolManifestFiltersAndDoesNotShareMutableRegistryState(t *testing.T) {
	first := v1.Manifest([]v1.Endpoint{v1.Gemini, v1.OpenAI, v1.Gemini, "future"})
	if len(first.Protocols) != 2 || first.Protocols[0].Endpoint != v1.OpenAI || first.Protocols[1].Endpoint != v1.Gemini {
		t.Fatal("manifest must filter and deduplicate without changing registry order")
	}
	first.Protocols[0].SupportedTasks[0] = "future"
	first.Protocols[0].Endpoints[0].DefaultValue = "/changed"
	first.CatalogTraits[0] = "function_calling"
	first.UnsupportedTasks[0] = "chat"
	first.TechnicalCapabilities[0] = "vision_input"
	all := v1.Protocols()
	all[0].SupportedTasks[0] = "changed"
	all[0].Endpoints[0].DefaultValue = "/changed-again"
	endpoints := v1.Endpoints()
	endpoints[0] = "changed"
	again := v1.Manifest([]v1.Endpoint{v1.OpenAI})
	if len(again.Protocols) != 1 || again.Protocols[0].SupportedTasks[0] != v1.Chat || again.Protocols[0].Endpoints[0].DefaultValue != "/chat/completions" || again.CatalogTraits[0] != "vision_input" || again.UnsupportedTasks[0] != "realtime_conversation" || again.TechnicalCapabilities[0] != "function_calling" || v1.Endpoints()[0] != v1.OpenAI {
		t.Fatal("caller mutations changed registry or other manifest responses")
	}
	for _, capabilities := range [][]v1.Endpoint{nil, {}, {"future"}} {
		empty := v1.Manifest(capabilities)
		if empty.Protocols == nil || len(empty.Protocols) != 0 {
			t.Fatal("an empty capability set must publish an empty JSON array")
		}
		encoded, err := json.Marshal(empty)
		if err != nil {
			t.Fatal(err)
		}
		var decoded map[string]any
		if json.Unmarshal(encoded, &decoded) != nil || !reflect.DeepEqual(decoded["protocols"], []any{}) {
			t.Fatal("empty protocols did not retain its wire array shape")
		}
	}
}

func TestTaskEndpointSupportHasNoCrossTaskOrUnknownFallback(t *testing.T) {
	allTasks := []v1.Task{v1.Chat, v1.ImageGeneration, v1.ImageEdit, v1.Embedding, v1.Reranking, "speech_synthesis", "realtime_conversation", "future", ""}
	for _, endpoint := range v1.Endpoints() {
		if !v1.KnownEndpoint(endpoint) {
			t.Fatalf("registered endpoint %q was unknown", endpoint)
		}
		for _, task := range allTasks {
			want := task == v1.Chat && (endpoint == v1.OpenAI || endpoint == v1.Responses || endpoint == v1.Anthropic || endpoint == v1.Gemini) || (task == v1.ImageGeneration || task == v1.ImageEdit) && endpoint == v1.Images || task == v1.Embedding && endpoint == v1.Embeddings || task == v1.Reranking && endpoint == v1.Rerank
			if v1.SupportsTaskEndpoint(task, endpoint) != want {
				t.Fatalf("unexpected support for task %q endpoint %q", task, endpoint)
			}
		}
	}
	for _, endpoint := range []v1.Endpoint{"", "future", "openai.chat_text"} {
		if v1.KnownEndpoint(endpoint) || v1.SupportsTaskEndpoint(v1.Chat, endpoint) {
			t.Fatalf("unknown endpoint %q acquired protocol support", endpoint)
		}
	}
}
