// SPDX-License-Identifier: Apache-2.0
package conformance

import (
	"encoding/json"
	v1 "github.com/nomifun/nomifun-model-gateway/contract/v1"
	"testing"
)

func TestProtocolManifestChecksExactNativeConfigurationAndIgnoresAdditions(t *testing.T) {
	caps := []v1.Endpoint{v1.OpenAI, v1.Gemini}
	valid, _ := json.Marshal(v1.Manifest(caps))
	var root map[string]any
	_ = json.Unmarshal(valid, &root)
	root["future_control_field"] = true
	withFuture, _ := json.Marshal(root)
	if err := validateManifest(withFuture, caps); err != nil {
		t.Fatal(err)
	}
	for _, mutation := range []func(map[string]any){
		func(r map[string]any) { r["manifest_version"] = "2" },
		func(r map[string]any) { r["protocols"].([]any)[0].(map[string]any)["protocol_id"] = "guessed.chat" },
		func(r map[string]any) { r["protocols"].([]any)[0].(map[string]any)["auth_scheme"] = "query_key:key" },
		func(r map[string]any) { delete(r["protocols"].([]any)[0].(map[string]any), "requires_stream_usage") },
		func(r map[string]any) { r["protocols"].([]any)[1].(map[string]any)["requires_stream_usage"] = nil },
		func(r map[string]any) { r["protocols"].([]any)[1].(map[string]any)["base_url_suffix"] = nil },
		func(r map[string]any) { r["traits_are_advisory"] = false },
	} {
		_ = json.Unmarshal(valid, &root)
		mutation(root)
		data, _ := json.Marshal(root)
		if validateManifest(data, caps) == nil {
			t.Fatal("invalid manifest was accepted")
		}
	}
	if validateManifest(valid, []v1.Endpoint{v1.OpenAI}) == nil {
		t.Fatal("manifest advertised a disabled protocol")
	}
}
