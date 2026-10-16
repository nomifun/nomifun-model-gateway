// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	v1 "github.com/nomifun/nomifun-model-gateway/contract/v1"
	"github.com/nomifun/nomifun-model-gateway/internal/relay"
)

func TestProtocolManifestIsAnonymousFilteredAndDoesNotProbe(t *testing.T) {
	f := setupServer(t)
	settings := defaultSettings()
	settings.Capabilities = []v1.Endpoint{v1.Gemini, v1.Images}
	encoded, _ := json.Marshal(settings)
	if err := f.st.SetSetting(context.Background(), "instance", encoded); err != nil {
		t.Fatal(err)
	}
	meta := call(t, f.app, http.MethodGet, "/nomifun/v1/meta", "", "")
	var declared v1.Meta
	if meta.Code != 200 || json.Unmarshal(meta.Body.Bytes(), &declared) != nil || declared.ProtocolManifest != "/nomifun/v1/protocols" || len(declared.OptionalEndpoints) != 0 {
		t.Fatalf("invalid additive manifest discovery: %d %s", meta.Code, meta.Body.String())
	}
	response := call(t, f.app, http.MethodGet, declared.ProtocolManifest, "", "")
	var manifest v1.ProtocolManifest
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &manifest) != nil || len(manifest.Protocols) != 2 || manifest.Protocols[0].Endpoint != v1.Gemini || manifest.Protocols[1].Endpoint != v1.Images {
		t.Fatalf("manifest must reflect instance capabilities: %d %s", response.Code, response.Body.String())
	}
	if response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("X-Request-ID") == "" || f.calls.Load() != 0 {
		t.Fatal("manifest lacks control-plane headers or contacted an upstream")
	}
	for _, protocol := range manifest.Protocols {
		for _, operation := range protocol.Endpoints {
			path := strings.ReplaceAll(protocol.BaseURLSuffix+operation.DefaultValue, "{model}", "synthetic-model")
			endpoint, task, found := relay.Route(path)
			if !found || endpoint != protocol.Endpoint || task != operation.Task {
				t.Fatalf("manifest operation is not served: %s", path)
			}
		}
	}
	if got := call(t, f.app, http.MethodGet, declared.ProtocolManifest+"?key=synthetic", "", ""); got.Code != 401 {
		t.Fatalf("query key accepted: %d", got.Code)
	}
	if got := call(t, f.app, http.MethodPost, declared.ProtocolManifest, "{}", ""); got.Code != 405 {
		t.Fatalf("manifest accepted a mutation: %d", got.Code)
	}
}

func TestAllManifestNativeOperationsMatchRelayRoutes(t *testing.T) {
	for _, protocol := range v1.Protocols() {
		for _, operation := range protocol.Endpoints {
			path := strings.ReplaceAll(protocol.BaseURLSuffix+operation.DefaultValue, "{model}", "synthetic-model")
			endpoint, task, found := relay.Route(path)
			if !found || endpoint != protocol.Endpoint || task != operation.Task {
				t.Fatalf("manifest route differs from relay: %s", path)
			}
		}
	}
}
