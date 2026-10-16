// SPDX-License-Identifier: Apache-2.0
package conformance

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strings"

	v1 "github.com/nomifun/nomifun-model-gateway/contract/v1"
)

func normalizeManifest(m *v1.ProtocolManifest) {
	sort.Strings(m.UnsupportedTasks)
	sort.Strings(m.CatalogTraits)
	sort.Strings(m.TechnicalCapabilities)
	sort.Slice(m.Protocols, func(i, j int) bool { return m.Protocols[i].Endpoint < m.Protocols[j].Endpoint })
	for i := range m.Protocols {
		p := &m.Protocols[i]
		sort.Slice(p.SupportedTasks, func(i, j int) bool { return p.SupportedTasks[i] < p.SupportedTasks[j] })
		sort.Slice(p.Endpoints, func(i, j int) bool {
			return string(p.Endpoints[i].Task)+p.Endpoints[i].Purpose < string(p.Endpoints[j].Task)+p.Endpoints[j].Purpose
		})
	}
}

func validateManifest(data []byte, capabilities []v1.Endpoint) error {
	root, err := object(data)
	if err != nil {
		return err
	}
	if err = required(root, "contract_version", "manifest_version", "protocols", "unsupported_tasks", "catalog_traits", "technical_capabilities", "traits_are_advisory", "max_token_limit"); err != nil {
		return err
	}
	var protocols []map[string]json.RawMessage
	if json.Unmarshal(root["protocols"], &protocols) != nil {
		return errors.New("invalid protocol descriptor array")
	}
	for _, p := range protocols {
		if err = required(p, "protocol_id", "endpoint", "supported_tasks", "executor", "transport", "connection_role", "auth_scheme", "root_shape", "base_url_suffix", "requires_output_ceiling", "requires_stream_usage", "endpoints"); err != nil {
			return err
		}
		for _, name := range []string{"requires_output_ceiling", "requires_stream_usage"} {
			var value *bool
			if json.Unmarshal(p[name], &value) != nil || value == nil {
				return errors.New("protocol policy flags must be JSON booleans")
			}
		}
		var suffix *string
		if json.Unmarshal(p["base_url_suffix"], &suffix) != nil || suffix == nil {
			return errors.New("protocol root suffix must be a JSON string")
		}
		var operations []map[string]json.RawMessage
		if json.Unmarshal(p["endpoints"], &operations) != nil {
			return errors.New("invalid protocol operation array")
		}
		for _, operation := range operations {
			if err = required(operation, "task", "field", "purpose", "method", "default_value", "root_shape"); err != nil {
				return err
			}
		}
	}
	var actual v1.ProtocolManifest
	if json.Unmarshal(data, &actual) != nil {
		return errors.New("invalid protocol manifest field types")
	}
	expected := v1.Manifest(capabilities)
	normalizeManifest(&actual)
	normalizeManifest(&expected)
	if !reflect.DeepEqual(actual, expected) {
		return errors.New("protocol manifest does not match enabled v1 interfaces or Desktop mappings")
	}
	return nil
}

func (r *runner) protocolManifest(ctx context.Context, meta v1.Meta) {
	if meta.ProtocolManifest == "" {
		r.skip("contract/protocols", "optional protocol manifest not advertised")
		return
	}
	res, err := r.call(ctx, probe{path: meta.ProtocolManifest}, "", "", nil, false)
	r.check("contract/protocols", func() error {
		if err != nil {
			return err
		}
		if res.status != 200 || !strings.HasPrefix(strings.ToLower(res.header.Get("Content-Type")), "application/json") {
			return errors.New("anonymous manifest must return HTTP 200 JSON")
		}
		return validateManifest(res.body, meta.Capabilities)
	})
}
