// SPDX-License-Identifier: Apache-2.0
import assert from 'node:assert/strict';
import test from 'node:test';
import { loadProtocolManifest, parseProtocolManifest, protocolsForRoute } from '../src/model-protocols.ts';

const descriptor = overrides => ({
  protocol_id: 'openai.chat_text', endpoint: 'openai', supported_tasks: ['chat'], executor: 'openai_chat', transport: 'http', connection_role: 'default',
  auth_scheme: 'bearer', root_shape: 'api_root', base_url_suffix: '/v1', requires_output_ceiling: false, requires_stream_usage: true,
  endpoints: [{ task: 'chat', field: 'endpoint', purpose: 'submit', method: 'POST', default_value: '/chat/completions', root_shape: 'api_root' }], ...overrides
});
const manifest = overrides => ({
  contract_version: '1.0', manifest_version: '1', protocols: [descriptor()], unsupported_tasks: ['realtime_conversation', 'video_generation'],
  catalog_traits: ['vision_input', 'audio_input', 'video_input', 'web_search'], technical_capabilities: ['function_calling', 'reasoning', 'streaming'], traits_are_advisory: true,
  max_token_limit: Number.MAX_SAFE_INTEGER, ...overrides
});

test('protocol presentation uses manifest route identity, authentication, roots and task paths', () => {
  const value = parseProtocolManifest(manifest({ protocols: [descriptor(), descriptor({ protocol_id: 'vendor.future', endpoint: 'future', supported_tasks: ['embedding'] })] }));
  const routes = protocolsForRoute(value, 'chat', ['openai', 'future']);
  assert.equal(routes.length, 1); assert.equal(routes[0].protocol_id, 'openai.chat_text'); assert.equal(routes[0].auth_scheme, 'bearer');
  assert.equal(routes[0].root_shape, 'api_root'); assert.equal(routes[0].endpoints[0].default_value, '/chat/completions');
  assert.equal(routes[0].requires_stream_usage, true); assert.deepEqual(protocolsForRoute(value, 'embedding', ['openai']), []);
});

test('manifest parser fails closed for unsupported contracts and malformed policy or endpoint metadata', () => {
  for (const bad of [
    null, manifest({ contract_version: '2.0' }), manifest({ manifest_version: 1 }), manifest({ manifest_version: '2' }), manifest({ protocols: {} }),
    manifest({ unsupported_tasks: ['chat', null] }), manifest({ catalog_traits: 'vision_input' }), manifest({ technical_capabilities: null }),
    manifest({ traits_are_advisory: false }), manifest({ max_token_limit: Number.MAX_SAFE_INTEGER + 1 }),
    manifest({ protocols: [descriptor({ supported_tasks: null })] }), manifest({ protocols: [descriptor({ auth_scheme: null })] }),
    manifest({ protocols: [descriptor({ requires_stream_usage: 'yes' })] }), manifest({ protocols: [descriptor({ endpoints: [{}] })] })
  ]) assert.throws(() => parseProtocolManifest(bad), /invalid_protocol_manifest/);
});

test('anonymous same-origin manifest load retries failures, bounds fetch and shares concurrent successful reads', async () => {
  const original = globalThis.fetch; const calls = [];
  globalThis.fetch = async (url, options) => { calls.push({ url, options }); return { ok: calls.length > 1, json: async () => manifest() }; };
  try {
    await assert.rejects(loadProtocolManifest(), /protocol_manifest_unavailable/);
    const first = loadProtocolManifest(); const second = loadProtocolManifest();
    assert.equal(first, second); assert.equal((await first).contract_version, '1.0');
    assert.equal(calls.length, 2); assert.equal(calls[1].url, '/nomifun/v1/protocols');
    assert.equal(calls[1].options.credentials, 'same-origin'); assert.equal(calls[1].options.cache, 'no-store');
    assert.ok(calls[1].options.signal instanceof AbortSignal); assert.deepEqual(calls[1].options.headers, { Accept: 'application/json' });
    await loadProtocolManifest();
    assert.equal(calls.length, 3, 'a later editor must observe changed instance capabilities');
  } finally { globalThis.fetch = original; }
});
