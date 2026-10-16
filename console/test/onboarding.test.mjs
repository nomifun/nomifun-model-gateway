// SPDX-License-Identifier: Apache-2.0
import assert from 'node:assert/strict';
import test from 'node:test';
import { addDiscoveredMappings, applyDiscoveredMetadata, channelSubmission, discoveryMetadataPreview, mappingPreview, synchronizeModelDrafts, writeOutcomeUnknown } from '../src/onboarding.ts';
import { APIError } from '../src/api.ts';
import { normalizeChannelDraft } from '../src/catalog-form.ts';
test('unconfirmed writes include malformed success and gateway failures; explicit rejections allow correction', () => {
  for (const error of [new TypeError('fetch failed'), new APIError('invalid_response', 201), new APIError('invalid_response', 200), new APIError('bad gateway', 502)]) assert.equal(writeOutcomeUnknown(error), true);
  for (const status of [400, 409, 422]) assert.equal(writeOutcomeUnknown(new APIError('rejected', status)), false);
});
test('channel submissions contain only accepted admin input, never row metadata or draft artifacts', () => {
  const submission = channelSubmission({ name: 'test', model_ids: { test: 'native' }, id: 2, created_at: 'now', credential_configured: true, models_json: '{}', __mapping_rows: [] });
  assert.deepEqual(Object.keys(submission), ['name', 'model_ids']);
});
test('discovery preview identifies new unchanged changed and catalog conflicts without overwriting', () => {
  const models = ['new', 'existing', 'changed', 'conflict'].map(id => ({ id }));
  const channel = { model_ids: { existing: 'existing', changed: 'original' }, priority: 7 };
  assert.deepEqual(mappingPreview(channel.model_ids, models, [{ id: 'conflict' }]).map(row => row.state), ['new', 'unchanged', 'changed', 'conflict']);
  const next = addDiscoveredMappings(channel, models);
  assert.equal(next.model_ids.changed, 'original'); assert.equal(next.model_ids.new, 'new'); assert.equal(next.priority, 7); assert.equal(next.__mapping_rows.length, 4);
  assert.equal(channel.model_ids.new, undefined);
});
test('import preserves incomplete and conflicting mapping rows until an operator corrects them', () => {
  const rows = [{ public_id: 'a', upstream_id: 'old' }, { public_id: 'a', upstream_id: 'new' }, { public_id: 'pending', upstream_id: '' }];
  const next = addDiscoveredMappings({ model_ids: { a: 'new' }, __mapping_rows: rows }, [{ id: 'a' }, { id: 'b' }]);
  assert.deepEqual(next.__mapping_rows.slice(0, 3), rows); assert.equal(next.__mapping_rows.length, 4); assert.equal(rows.length, 3);
  const normalized = normalizeChannelDraft({ name: 'Synthetic', kind: 'compatible', base_url: 'http://127.0.0.1:18892', api_key: 'synthetic-only', endpoints: ['openai'], ...next });
  assert.ok(normalized.issues.some(issue => issue.code === 'duplicatePublicID')); assert.ok(normalized.issues.some(issue => issue.path === 'model_ids.2.upstream_id'));
});
test('mapping changes preserve explicit prices and operations while new models remain disabled and unknown', () => {
  const prior = { id: 'kept', pricing: [{ amount: 9007199254740993n }], enabled: true, tasks: ['chat'] };
  const drafts = synchronizeModelDrafts({ model_ids: { kept: 'upstream', added: 'new' } }, [prior, { id: 'removed' }], 'Provider', [{ id: 'new', display_name: 'Actual name' }]);
  assert.equal(drafts[0], prior); assert.equal(drafts.length, 2); assert.equal(drafts[1].enabled, false); assert.deepEqual(drafts[1].pricing, []); assert.deepEqual(drafts[1].tasks, []); assert.equal(drafts[1].context_window, null); assert.equal(drafts[1].display_name, 'Actual name');
});

test('remote metadata stays unapplied until review and never infers tasks or prices from names', () => {
  const remote = { id: 'vision-reasoning-image-video-chat', display_name: 'Best embedding rerank', context_limit: 128000, output_limit: 8192, tasks: ['chat', 'video_generation'], tasks_source: 'supportedGenerationMethods' };
  const [draft] = synchronizeModelDrafts({ model_ids: { public: remote.id } }, [], 'Provider', [remote]);
  assert.deepEqual(draft.tasks, []); assert.equal(draft.context_window, null); assert.deepEqual(draft.pricing, []);
  assert.deepEqual(discoveryMetadataPreview({ id: remote.id, display_name: remote.display_name, tasks: ['chat'] }).tasks, []);
  const preview = discoveryMetadataPreview(remote);
  assert.deepEqual(preview.tasks, ['chat']); assert.deepEqual(preview.unsupportedTasks, ['video_generation']);
  const next = applyDiscoveredMetadata(draft, remote);
  assert.deepEqual(next.tasks, ['chat']); assert.deepEqual(next.task_endpoints, { chat: { endpoints: [], preferred_endpoint: '' } });
  assert.equal(next.context_window, 128000n); assert.equal(next.max_output_tokens, 8192n); assert.equal(next.enabled, false); assert.deepEqual(next.pricing, []);
  assert.deepEqual(draft.tasks, []); assert.equal(draft.context_window, null);
});

test('applying remote metadata preserves existing edits, routes, operations and exact prices', () => {
  const route = { endpoints: ['anthropic'], preferred_endpoint: 'anthropic', vendor_hint: true };
  const draft = { id: 'public', tasks: ['chat'], task_endpoints: { chat: route }, context_window: '4096', max_output_tokens: 1024n, traits: ['vendor.future'], enabled: true, pricing: [{ amount: 9007199254740993n }] };
  const before = structuredClone(draft);
  const next = applyDiscoveredMetadata(draft, { id: 'native', tasks: ['embedding'], tasks_source: 'native', context_limit: 128000, output_limit: 8192 });
  assert.deepEqual(next, before); assert.deepEqual(draft, before);
  const preservedRoute = applyDiscoveredMetadata({ tasks: [], task_endpoints: { chat: route }, context_window: null }, { id: 'native', tasks: ['chat'], tasks_source: 'native' });
  assert.deepEqual(preservedRoute.task_endpoints.chat, route);
});

test('invalid or unsafe discovered limits and unsupported tasks never enter v1 model fields', () => {
  for (const context_limit of [0, -1, 1.5, 9007199254740992n, Number.MAX_SAFE_INTEGER + 1, '8192']) {
    const remote = { id: 'native', context_limit, tasks: ['speech_synthesis'], tasks_source: 'native' };
    const preview = discoveryMetadataPreview(remote);
    assert.deepEqual(preview.invalidLimits, ['context_limit']); assert.deepEqual(preview.unsupportedTasks, ['speech_synthesis']);
    const draft = { tasks: [], task_endpoints: {}, context_window: null, pricing: [] };
    assert.deepEqual(applyDiscoveredMetadata(draft, remote), draft);
  }
  const result = applyDiscoveredMetadata({ context_window: null }, { id: 'native', context_limit: 9007199254740991n });
  assert.equal(result.context_window, 9007199254740991n);
});
