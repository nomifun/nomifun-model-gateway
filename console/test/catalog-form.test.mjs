// SPDX-License-Identifier: Apache-2.0
import assert from 'node:assert/strict';
import test from 'node:test';
import { exactJSON } from '../src/api.ts';
import { mappingRows, normalizeChannelDraft, normalizeModelDraft, TASK_ENDPOINTS } from '../src/catalog-form.ts';

const price = overrides => ({ task: 'chat', meter: 'requests', unit_size: '1', amount: '1', currency: 'USD', ...overrides });
const model = overrides => ({
  id: 'public-model', display_name: 'Public model', vendor: 'Synthetic vendor', status: 'available',
  tasks: ['chat'], task_endpoints: { chat: { endpoints: ['openai'], preferred_endpoint: 'openai' } },
  context_window: null, max_output_tokens: null, input_modalities: ['text'], traits: [],
  pricing: [price()], enabled: true, subscription_only: false, ...overrides
});
const channel = overrides => ({
  name: 'Synthetic channel', kind: 'openai', base_url: 'https://upstream.example/v1', api_key: 'synthetic-test-key',
  model_ids: { 'public-model': 'upstream-model' }, endpoints: ['openai'], priority: '0', weight: '1', enabled: true,
  ...overrides
});
const hasIssue = (result, path, code) => assert.ok(
  result.issues.some(issue => issue.path === path && (code === undefined || issue.code === code)),
  `Expected issue ${path}${code ? ` (${code})` : ''}; received ${JSON.stringify(result.issues)}`
);

test('task endpoint choices retain the frozen native protocol names', () => {
  assert.deepEqual(TASK_ENDPOINTS, {
    chat: ['openai', 'openai-response', 'anthropic', 'gemini'],
    image_generation: ['image-generation'], image_edit: ['image-generation'],
    embedding: ['embeddings'], rerank: ['jina-rerank']
  });
});

test('model draft preserves exact int64 prices and limits through transport', () => {
  const input = model({ context_window: '9007199254740993', max_output_tokens: '4096', pricing: [price({ amount: '9223372036854775807', unit_size: '9007199254740993' })] });
  const result = normalizeModelDraft(input);
  assert.deepEqual(result.issues, []);
  assert.equal(result.value.context_window, 9007199254740993n);
  assert.equal(result.value.max_output_tokens, 4096n);
  assert.equal(result.value.pricing[0].amount, 9223372036854775807n);
  assert.equal(result.value.pricing[0].unit_size, 9007199254740993n);
  const transported = exactJSON.parse(exactJSON.stringify(result.value));
  assert.equal(transported.pricing[0].amount, 9223372036854775807n);
  assert.equal(input.pricing[0].amount, '9223372036854775807');
});

test('explicit zero price is valid while an omitted amount remains unconfigured', () => {
  const free = normalizeModelDraft(model({ pricing: [price({ amount: '0' })] }));
  assert.deepEqual(free.issues, []);
  assert.equal(free.value.pricing[0].amount, 0n);
  for (const amount of [undefined, null, '']) {
    const missing = normalizeModelDraft(model({ pricing: [price({ amount })] }));
    hasIssue(missing, 'pricing.0.amount', 'required');
    assert.notEqual(missing.value.pricing[0].amount, 0n);
  }
});

test('price amounts reject fractions, exponent notation, negatives, overflow and rounded numbers', () => {
  for (const amount of ['1.01', '1e3', '-1', '9223372036854775808', Number.MAX_SAFE_INTEGER + 1, NaN, Infinity]) {
    hasIssue(normalizeModelDraft(model({ pricing: [price({ amount })] })), 'pricing.0.amount');
  }
  const safe = normalizeModelDraft(model({ pricing: [price({ amount: 123 })] }));
  assert.deepEqual(safe.issues, []);
  assert.equal(safe.value.pricing[0].amount, 123n);
});

test('price unit size must be a positive int64 and cannot be filled with an implicit zero', () => {
  for (const unit_size of [undefined, null, '', '0', '-1', '1.5', '1e6', '9223372036854775808']) {
    hasIssue(normalizeModelDraft(model({ pricing: [price({ unit_size })] })), 'pricing.0.unit_size');
  }
});

test('a disabled model may have empty prices but enabling requires explicit task prices', () => {
  const draft = normalizeModelDraft(model({ pricing: [], enabled: false }));
  assert.deepEqual(draft.issues, []);
  assert.deepEqual(draft.value.pricing, []);
  const enabled = normalizeModelDraft(model({ pricing: [], enabled: true }));
  hasIssue(enabled, 'pricing', 'taskUnpriced');
  assert.deepEqual(enabled.value.pricing, []);
});

test('every enabled task needs its own price and unknown meters remain visible as draft data', () => {
  const multi = model({
    tasks: ['chat', 'embedding'],
    task_endpoints: {
      chat: { endpoints: ['openai'], preferred_endpoint: 'openai' },
      embedding: { endpoints: ['embeddings'], preferred_endpoint: 'embeddings' }
    }
  });
  hasIssue(normalizeModelDraft(multi), 'pricing', 'taskUnpriced');
  const complete = normalizeModelDraft({ ...multi, pricing: [price(), price({ task: 'embedding' })] });
  assert.deepEqual(complete.issues, []);
  const unknown = price({ meter: 'unconfirmed_units' });
  hasIssue(normalizeModelDraft(model({ pricing: [unknown] })), 'pricing.0.meter', 'priceUnsupportedMeter');
  const draft = normalizeModelDraft(model({ pricing: [unknown], enabled: false }));
  assert.deepEqual(draft.issues, []);
  assert.equal(draft.value.pricing[0].meter, 'unconfirmed_units');
});

test('publication checks use normalized price identifiers from the same submitted draft', () => {
  const result = normalizeModelDraft(model({ pricing: [price({ task: ' chat ', meter: ' requests ', currency: ' USD ' })] }));
  assert.deepEqual(result.issues, []);
  assert.equal(result.value.pricing[0].task, 'chat');
  assert.equal(result.value.pricing[0].meter, 'requests');
  assert.equal(result.value.pricing[0].currency, 'USD');
  hasIssue(normalizeModelDraft(model({ pricing: [
    price({ task: ' chat ', meter: ' cached_input_tokens ' }),
    price({ task: ' chat ', meter: ' cache_read_input_tokens ' })
  ] })), 'pricing', 'cacheAliasOverlap');
});

test('prices reject duplicate meters, mixed currencies and overlapping cache aliases', () => {
  hasIssue(normalizeModelDraft(model({ pricing: [price(), price()] })), 'pricing.1.meter', 'duplicatePrice');
  hasIssue(normalizeModelDraft(model({ pricing: [price(), price({ meter: 'input_tokens', currency: 'CNY' })] })), 'pricing', 'singleCurrency');
  hasIssue(normalizeModelDraft(model({ pricing: [price({ meter: 'cached_input_tokens' }), price({ meter: 'cache_read_input_tokens' })] })), 'pricing', 'cacheAliasOverlap');
  hasIssue(normalizeModelDraft(model({ pricing: [price({ task: 'embedding' })] })), 'pricing.0.task', 'priceTask');
  hasIssue(normalizeModelDraft(model({ pricing: [price({ currency: 'usd' })] })), 'pricing.0.currency', 'currencyRequired');
});

test('task routes must match the selected tasks and never receive an implicit preferred endpoint', () => {
  hasIssue(normalizeModelDraft(model({ task_endpoints: {} })), 'task_endpoints.chat.endpoints', 'required');
  hasIssue(normalizeModelDraft(model({ task_endpoints: { chat: { endpoints: ['openai'] } } })), 'task_endpoints.chat.preferred_endpoint', 'preferredIncluded');
  const extra = { chat: { endpoints: ['openai'], preferred_endpoint: 'openai' }, embedding: { endpoints: ['embeddings'], preferred_endpoint: 'embeddings' } };
  const result = normalizeModelDraft(model({ task_endpoints: extra }));
  hasIssue(result, 'task_endpoints.embedding', 'taskNotSelected');
  assert.deepEqual(result.value.task_endpoints.embedding, extra.embedding);
});

test('task routes reject wrong native endpoints, duplicate endpoints and preferred endpoints outside the list', () => {
  hasIssue(normalizeModelDraft(model({ task_endpoints: { chat: { endpoints: ['embeddings'], preferred_endpoint: 'embeddings' } } })), 'task_endpoints.chat.endpoints.0', 'endpointForTask');
  hasIssue(normalizeModelDraft(model({ task_endpoints: { chat: { endpoints: ['openai', 'openai'], preferred_endpoint: 'openai' } } })), 'task_endpoints.chat.endpoints.1', 'emptyOrDuplicate');
  hasIssue(normalizeModelDraft(model({ task_endpoints: { chat: { endpoints: ['openai'], preferred_endpoint: 'gemini' } } })), 'task_endpoints.chat.preferred_endpoint', 'preferredIncluded');
  hasIssue(normalizeModelDraft(model({ tasks: ['chat', 'chat'] })), 'tasks.1', 'emptyOrDuplicate');
  hasIssue(normalizeModelDraft(model({ tasks: ['audio'] })), 'tasks', 'unknownTask');
});

test('unknown task names cannot resolve inherited object properties or crash error localization', () => {
  for (const task of ['constructor', 'toString', '__proto__']) {
    const task_endpoints = Object.fromEntries([[task, { endpoints: ['openai'], preferred_endpoint: 'openai' }]]);
    const result = normalizeModelDraft(model({ tasks: [task], task_endpoints, pricing: [price({ task })] }));
    hasIssue(result, 'tasks', 'unknownTask');
    hasIssue(result, `task_endpoints.${task}.endpoints.0`, 'endpointForTask');
  }
});

test('Anthropic preference requires a positive output ceiling while optional ceilings stay null', () => {
  const route = { chat: { endpoints: ['anthropic', 'openai'], preferred_endpoint: 'anthropic' } };
  hasIssue(normalizeModelDraft(model({ task_endpoints: route })), 'max_output_tokens', 'anthropicLimit');
  const valid = normalizeModelDraft(model({ task_endpoints: route, max_output_tokens: '8192' }));
  assert.deepEqual(valid.issues, []);
  assert.equal(valid.value.max_output_tokens, 8192n);
  for (const field of ['context_window', 'max_output_tokens']) {
    for (const limit of ['0', '-1', '1.5', '9223372036854775808']) hasIssue(normalizeModelDraft(model({ [field]: limit })), field);
  }
  const optional = normalizeModelDraft(model());
  assert.equal(optional.value.context_window, null);
  assert.equal(optional.value.max_output_tokens, null);
});

test('visual model normalization preserves modalities, traits and advanced unknown fields without mutating the input', () => {
  const input = model({
    input_modalities: ['text', 'image'], traits: ['reasoning', 'vendor.future_trait'],
    vendor_metadata: { revision: 'future', nested: [1, 2] }, __editor_step: 'prices',
    task_endpoints: { chat: { endpoints: ['openai', 'openai-response'], preferred_endpoint: 'openai-response', vendor_hint: { opaque: true } } },
    pricing: [price({ vendor_note: 'retain' })]
  });
  const before = structuredClone(input);
  const result = normalizeModelDraft(input);
  assert.deepEqual(result.issues, []);
  assert.deepEqual(result.value.input_modalities, ['text', 'image']);
  assert.deepEqual(result.value.traits, ['reasoning', 'vendor.future_trait']);
  assert.deepEqual(result.value.vendor_metadata, input.vendor_metadata);
  assert.deepEqual(result.value.task_endpoints.chat.vendor_hint, { opaque: true });
  assert.equal(result.value.pricing[0].vendor_note, 'retain');
  assert.equal('__editor_step' in result.value, false);
  assert.deepEqual(input, before);
});

test('malformed array fields produce field-level issues and are not mistaken for valid prices', () => {
  for (const field of ['input_modalities', 'traits']) hasIssue(normalizeModelDraft(model({ [field]: {} })), field, 'arrayRequired');
  hasIssue(normalizeModelDraft(model({ pricing: {} })), 'pricing', 'arrayRequired');
  hasIssue(normalizeModelDraft(model({ input_modalities: ['text', 'text'] })), 'input_modalities.1', 'emptyOrDuplicate');
});

test('mapping rows preserve incomplete and duplicate entries for correction', () => {
  const rows = [
    { public_id: 'public-model', upstream_id: 'upstream-1' },
    { public_id: 'public-model', upstream_id: 'upstream-2' },
    { public_id: '', upstream_id: 'upstream-3' },
    { public_id: 'pending', upstream_id: '' }
  ];
  assert.deepEqual(mappingRows(rows), rows);
  assert.deepEqual(mappingRows({ 'public-model': 'upstream-model', pending: '' }), [
    { public_id: 'public-model', upstream_id: 'upstream-model' }, { public_id: 'pending', upstream_id: '' }
  ]);
  assert.deepEqual(mappingRows(undefined), []);
  const before = structuredClone(rows);
  const result = normalizeChannelDraft(channel({ __mapping_rows: rows }));
  hasIssue(result, 'model_ids.1.public_id', 'duplicatePublicID');
  hasIssue(result, 'model_ids.2.public_id', 'required');
  hasIssue(result, 'model_ids.3.upstream_id', 'required');
  assert.deepEqual(rows, before);
});

test('channel submission converts the visible mapping table and removes all draft metadata', () => {
  const input = channel({
    __mapping_rows: [{ public_id: ' public-model ', upstream_id: ' upstream-model ' }, { public_id: 'second', upstream_id: 'upstream-2' }],
    __wizard_step: 'review', __discovery: { secret: 'synthetic-placeholder' }, vendor_metadata: { region: 'test' }
  });
  const before = structuredClone(input);
  const result = normalizeChannelDraft(input);
  assert.deepEqual(result.issues, []);
  assert.deepEqual(Object.entries(result.value.model_ids), [['public-model', 'upstream-model'], ['second', 'upstream-2']]);
  assert.equal(result.value.priority, 0n);
  assert.equal(result.value.weight, 1n);
  assert.deepEqual(result.value.vendor_metadata, { region: 'test' });
  assert.equal(Object.keys(result.value).some(key => key.startsWith('__')), false);
  assert.deepEqual(input, before);
});

test('unapplied advanced JSON blocks both editors and its pending marker never reaches the API payload', () => {
  for (const [normalize, input] of [[normalizeModelDraft, model()], [normalizeChannelDraft, channel()]]) {
    const pending = normalize({ ...input, __advanced_pending: true });
    hasIssue(pending, 'advanced', 'advancedPending');
    assert.equal('__advanced_pending' in pending.value, false);
    const applied = normalize({ ...input, __advanced_pending: false });
    assert.deepEqual(applied.issues, []);
    assert.equal('__advanced_pending' in applied.value, false);
  }
});

test('channel priorities preserve the full signed int64 range', () => {
  const result = normalizeChannelDraft(channel({ priority: '-9223372036854775808' }));
  assert.deepEqual(result.issues, []);
  assert.equal(result.value.priority, -9223372036854775808n);
  hasIssue(normalizeChannelDraft(channel({ priority: '-9223372036854775809' })), 'priority', 'integer_range');
});

test('channel creation requires a key while editing preserves immutable account credentials', () => {
  hasIssue(normalizeChannelDraft(channel({ api_key: undefined })), 'api_key', 'required');
  const editing = normalizeChannelDraft(channel({ api_key: undefined }), true);
  assert.deepEqual(editing.issues, []);
  assert.equal('api_key' in editing.value, false);
  const replacement = normalizeChannelDraft(channel({ api_key: 'synthetic-new-key' }), true);
  hasIssue(replacement, 'api_key', 'immutableIdentity');
  assert.equal('api_key' in replacement.value, false);
});

test('channel mapping and endpoint validation identifies empty, duplicate and unsupported entries', () => {
  hasIssue(normalizeChannelDraft(channel({ __mapping_rows: [] })), 'model_ids', 'required');
  hasIssue(normalizeChannelDraft(channel({ endpoints: [] })), 'endpoints', 'required');
  hasIssue(normalizeChannelDraft(channel({ endpoints: ['openai', 'openai'] })), 'endpoints.1', 'emptyOrDuplicate');
  hasIssue(normalizeChannelDraft(channel({ endpoints: ['realtime'] })), 'endpoints.0', 'unknownEndpoint');
  for (const weight of ['0', '100001', '1.5']) hasIssue(normalizeChannelDraft(channel({ weight })), 'weight');
});

test('channel URLs reject embedded credentials, query keys, fragments and non-HTTP protocols', () => {
  for (const base_url of ['https://key@upstream.example/v1', 'https://upstream.example/v1?key=synthetic', 'https://upstream.example/v1#fragment', 'file:///test', '/relative']) {
    hasIssue(normalizeChannelDraft(channel({ base_url })), 'base_url', 'baseURL');
  }
  assert.deepEqual(normalizeChannelDraft(channel({ base_url: 'http://127.0.0.1:18421/v1' })).issues, []);
});

test('preset template hosts remain unconfigured until the real account endpoint is entered', () => {
  hasIssue(normalizeChannelDraft(channel({ kind: 'azure', base_url: 'https://YOUR-RESOURCE.openai.azure.com' })), 'base_url', 'baseURLTemplate');
  const configured = normalizeChannelDraft(channel({ kind: 'azure', base_url: 'https://synthetic-resource.openai.azure.com' }));
  assert.deepEqual(configured.issues, []);
  assert.equal(configured.value.base_url, 'https://synthetic-resource.openai.azure.com');
});
