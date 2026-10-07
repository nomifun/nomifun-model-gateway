// SPDX-License-Identifier: Apache-2.0
import assert from 'node:assert/strict';
import test from 'node:test';
import { findProviderPreset, providerPresets } from '../src/provider-presets.ts';

test('editing a preset draft cannot change a later draft or regional template', () => {
  const first = findProviderPreset('bailian-cn');
  const expected = findProviderPreset('bailian-cn');
  first.endpoints.push('embeddings');
  first.notes[0] = 'changed';
  first.official_docs[0].url = 'https://example.invalid/changed';
  assert.deepEqual(findProviderPreset('bailian-cn'), expected);
  assert.notEqual(findProviderPreset('bailian-sg').notes[0], 'changed');
  assert.equal(findProviderPreset('unknown-provider'), undefined);
});

test('templates carry only current channel types, known native endpoints and credential-free addresses', () => {
  const ids = new Set();
  const kinds = new Set(['openai', 'anthropic', 'gemini', 'compatible', 'azure']);
  const endpoints = new Set(['openai', 'openai-response', 'anthropic', 'gemini', 'image-generation', 'embeddings', 'jina-rerank']);
  for (const preset of providerPresets) {
    assert.equal(ids.has(preset.id), false, `duplicate preset ${preset.id}`); ids.add(preset.id);
    assert.ok(kinds.has(preset.kind));
    assert.ok(preset.endpoints.length > 0 && preset.endpoints.every(item => endpoints.has(item)));
    assert.equal(Object.hasOwn(preset, 'api_key'), false);
    assert.equal(Object.hasOwn(preset, 'model_ids'), false);
    assert.equal(Object.hasOwn(preset, 'pricing'), false);
    if (preset.id === 'custom') {
      assert.equal(preset.base_url, ''); assert.equal(preset.discovery, 'manual');
    } else {
      const address = new URL(preset.base_url);
      assert.equal(address.protocol, 'https:');
      assert.equal(address.username + address.password + address.search + address.hash, '');
      assert.ok(preset.official_docs.length > 0);
    }
    for (const doc of preset.official_docs) {
      const url = new URL(doc.url);
      assert.equal(url.protocol, 'https:');
      assert.equal(url.username + url.password, '');
    }
  }
});

test('native APIs stay separate and undeclared catalog paths retain manual entry', () => {
  assert.deepEqual(findProviderPreset('anthropic').endpoints, ['anthropic']);
  assert.deepEqual(findProviderPreset('gemini').endpoints, ['gemini']);
  assert.equal(findProviderPreset('anthropic').api_version, '2023-06-01');
  assert.equal(findProviderPreset('azure').api_version, undefined);
  assert.equal(findProviderPreset('azure').discovery, 'manual');
  for (const preset of providerPresets.filter(item => item.id.startsWith('bailian-'))) {
    assert.equal(preset.discovery, 'manual');
    assert.equal(new URL(preset.base_url).pathname, '/compatible-mode');
  }
  assert.equal(new URL(findProviderPreset('openrouter').base_url).pathname, '/api');
  assert.equal(findProviderPreset('zhipu'), undefined, 'a /v4 path cannot be silently advertised as a /v1 channel');
});
