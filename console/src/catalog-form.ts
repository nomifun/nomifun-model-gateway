// SPDX-License-Identifier: Apache-2.0
import { integer } from './api.ts';
import type { Row } from './api.ts';

export type CatalogIssue = { path: string; code: string };
export type MappingRow = { public_id: string; upstream_id: string };
export const TASK_ENDPOINTS: Record<string, string[]> = {
  chat: ['openai', 'openai-response', 'anthropic', 'gemini'],
  image_generation: ['image-generation'], image_edit: ['image-generation'],
  embedding: ['embeddings'], rerank: ['jina-rerank']
};
export const ENDPOINTS = [...new Set(Object.values(TASK_ENDPOINTS).flat())];
export const PRICE_METERS = ['requests', 'input_tokens', 'output_tokens', 'cached_input_tokens', 'cache_read_input_tokens', 'cache_creation_input_tokens', 'reasoning_tokens', 'images'];
export const CHANNEL_KINDS = ['openai', 'anthropic', 'gemini', 'compatible', 'azure'];
export function record(value: unknown): Row { return value && typeof value === 'object' && !Array.isArray(value) ? value as Row : {}; }
export function stringList(value: unknown): string[] {
  if (Array.isArray(value)) return value.map(String);
  return typeof value === 'string' ? value.split(',').map(item => item.trim()).filter(Boolean) : [];
}
export function mappingRows(value: unknown): MappingRow[] {
  if (Array.isArray(value)) return value.map(item => ({ public_id: String(record(item).public_id ?? ''), upstream_id: String(record(item).upstream_id ?? '') }));
  return Object.entries(record(value)).map(([public_id, upstream_id]) => ({ public_id, upstream_id: String(upstream_id ?? '') }));
}
function cleanDraft(input: Row): Row { return Object.fromEntries(Object.entries(input).filter(([key]) => !key.startsWith('__'))); }
function nonempty(value: unknown): string { return String(value ?? '').trim(); }
function normalizeList(value: unknown, path: string, issues: CatalogIssue[]): string[] {
  const list = stringList(value);
  if (value !== undefined && !Array.isArray(value) && typeof value !== 'string') issues.push({ path, code: 'arrayRequired' });
  const seen = new Set<string>();
  list.forEach((item, index) => {
    if (!item.trim() || seen.has(item)) issues.push({ path: `${path}.${index}`, code: 'emptyOrDuplicate' });
    seen.add(item);
  });
  return list;
}
function normalizeInteger(value: unknown, path: string, issues: CatalogIssue[], minimum: bigint, optional = false, maximum?: bigint): bigint | null | unknown {
  if (value === undefined || value === null || value === '') {
    if (!optional) issues.push({ path, code: 'required' });
    return null;
  }
  // Reject already-rounded JavaScript numbers; JSONBig and string fields preserve the original value.
  if (typeof value === 'number' && !Number.isSafeInteger(value)) { issues.push({ path, code: 'integer_range' }); return value; }
  try { return integer(String(value).trim(), minimum, maximum); }
  catch (error) { issues.push({ path, code: error instanceof Error ? error.message : 'integer_required' }); return value; }
}

// Strings from visible numeric inputs become exact native int64 JSON numbers only at submission.
// Unknown fields survive visual edits, including metadata added through the advanced JSON editor.
export function normalizeModelDraft(input: Row): { value: Row; issues: CatalogIssue[] } {
  const value = cleanDraft(input); const issues: CatalogIssue[] = [];
  if (input.__advanced_pending) issues.push({ path: 'advanced', code: 'advancedPending' });
  for (const key of ['id', 'display_name', 'vendor']) { value[key] = nonempty(value[key]); if (!value[key]) issues.push({ path: key, code: 'required' }); }
  if (String(value.id).length > 200) issues.push({ path: 'id', code: 'idTooLong' });
  const tasks = normalizeList(value.tasks, 'tasks', issues); value.tasks = tasks;
  if (!tasks.length) issues.push({ path: 'tasks', code: 'required' });
  const routes = record(value.task_endpoints); const normalizedRoutes: Row = {};
  tasks.forEach(task => {
    const allowed = Object.hasOwn(TASK_ENDPOINTS, task) ? TASK_ENDPOINTS[task] : [];
    if (!allowed.length) issues.push({ path: 'tasks', code: 'unknownTask' });
    const route = record(routes[task]); const endpoints = normalizeList(route.endpoints, `task_endpoints.${task}.endpoints`, issues);
    if (!endpoints.length) issues.push({ path: `task_endpoints.${task}.endpoints`, code: 'required' });
    endpoints.forEach((endpoint, index) => { if (!allowed.includes(endpoint)) issues.push({ path: `task_endpoints.${task}.endpoints.${index}`, code: 'endpointForTask' }); });
    const preferred = nonempty(route.preferred_endpoint);
    if (!preferred || !endpoints.includes(preferred)) issues.push({ path: `task_endpoints.${task}.preferred_endpoint`, code: 'preferredIncluded' });
    normalizedRoutes[task] = { ...route, endpoints, preferred_endpoint: preferred };
    if (preferred === 'anthropic' && (value.max_output_tokens === undefined || value.max_output_tokens === null || value.max_output_tokens === '')) issues.push({ path: 'max_output_tokens', code: 'anthropicLimit' });
  });
  for (const task of Object.keys(routes)) if (!tasks.includes(task)) issues.push({ path: `task_endpoints.${task}`, code: 'taskNotSelected' });
  // Retain mismatched routes so correcting a task selection never destroys existing configuration.
  value.task_endpoints = { ...routes, ...normalizedRoutes };
  value.context_window = normalizeInteger(value.context_window, 'context_window', issues, 1n, true);
  value.max_output_tokens = normalizeInteger(value.max_output_tokens, 'max_output_tokens', issues, 1n, true);
  value.input_modalities = normalizeList(value.input_modalities, 'input_modalities', issues);
  value.traits = normalizeList(value.traits, 'traits', issues);
  if (!['available', 'degraded', 'unavailable'].includes(String(value.status))) issues.push({ path: 'status', code: 'statusRequired' });
  if (!Array.isArray(value.pricing)) issues.push({ path: 'pricing', code: 'arrayRequired' });
  const prices = Array.isArray(value.pricing) ? value.pricing.map(record) : [];
  const seenPrices = new Set<string>(); const currencies = new Set<string>();
  value.pricing = prices.map((row, index) => {
    const price = { ...row }; const prefix = `pricing.${index}`;
    price.task = nonempty(price.task); price.meter = nonempty(price.meter); price.currency = nonempty(price.currency);
    if (!tasks.includes(String(price.task))) issues.push({ path: `${prefix}.task`, code: 'priceTask' });
    if (!price.meter) issues.push({ path: `${prefix}.meter`, code: 'required' });
    else if (value.enabled && !PRICE_METERS.includes(String(price.meter))) issues.push({ path: `${prefix}.meter`, code: 'priceUnsupportedMeter' });
    if (!/^[A-Z]{3}$/.test(String(price.currency))) issues.push({ path: `${prefix}.currency`, code: 'currencyRequired' });
    const key = `${String(price.task)}\0${String(price.meter)}\0${String(price.currency)}`;
    if (seenPrices.has(key)) issues.push({ path: `${prefix}.meter`, code: 'duplicatePrice' });
    seenPrices.add(key); currencies.add(String(price.currency));
    price.unit_size = normalizeInteger(price.unit_size, `${prefix}.unit_size`, issues, 1n);
    price.amount = normalizeInteger(price.amount, `${prefix}.amount`, issues, 0n);
    return price;
  });
  if (currencies.size > 1) issues.push({ path: 'pricing', code: 'singleCurrency' });
  tasks.forEach(task => {
    const taskPrices = (value.pricing as Row[]).filter(price => price.task === task);
    if (value.enabled && !taskPrices.length) issues.push({ path: 'pricing', code: 'taskUnpriced' });
    if (taskPrices.some(price => price.meter === 'cached_input_tokens') && taskPrices.some(price => price.meter === 'cache_read_input_tokens')) issues.push({ path: 'pricing', code: 'cacheAliasOverlap' });
  });
  return { value, issues };
}

export function normalizeChannelDraft(input: Row, editing = false): { value: Row; issues: CatalogIssue[] } {
  const value = cleanDraft(input); const issues: CatalogIssue[] = [];
  if (input.__advanced_pending) issues.push({ path: 'advanced', code: 'advancedPending' });
  value.name = nonempty(value.name); if (!value.name || String(value.name).length > 200) issues.push({ path: 'name', code: 'required' });
  if (!CHANNEL_KINDS.includes(String(value.kind))) issues.push({ path: 'kind', code: 'required' });
  value.base_url = nonempty(value.base_url);
  try {
    const url = new URL(String(value.base_url));
    if (!['http:', 'https:'].includes(url.protocol) || !url.hostname || url.username || url.password || url.search || url.hash) throw new Error();
    if (/(?:^|\.)your[-_]/i.test(url.hostname)) issues.push({ path: 'base_url', code: 'baseURLTemplate' });
  }
  catch { issues.push({ path: 'base_url', code: 'baseURL' }); }
  if (!editing && !String(value.api_key ?? '')) issues.push({ path: 'api_key', code: 'required' });
  if (editing && String(value.api_key ?? '')) issues.push({ path: 'api_key', code: 'immutableIdentity' });
  if (editing) delete value.api_key;
  const rows = mappingRows(input.__mapping_rows ?? value.model_ids); const models: Record<string, string> = Object.create(null); const seen = new Set<string>();
  if (!rows.length) issues.push({ path: 'model_ids', code: 'required' });
  rows.forEach((row, index) => {
    const publicID = row.public_id.trim(); const upstreamID = row.upstream_id.trim();
    if (!publicID) issues.push({ path: `model_ids.${index}.public_id`, code: 'required' });
    if (!upstreamID) issues.push({ path: `model_ids.${index}.upstream_id`, code: 'required' });
    if (seen.has(publicID)) issues.push({ path: `model_ids.${index}.public_id`, code: 'duplicatePublicID' });
    seen.add(publicID); if (publicID && upstreamID) models[publicID] = upstreamID;
  });
  value.model_ids = models;
  const endpoints = normalizeList(value.endpoints, 'endpoints', issues); value.endpoints = endpoints;
  if (!endpoints.length) issues.push({ path: 'endpoints', code: 'required' });
  endpoints.forEach((endpoint, index) => { if (!ENDPOINTS.includes(endpoint)) issues.push({ path: `endpoints.${index}`, code: 'unknownEndpoint' }); });
  value.priority = normalizeInteger(value.priority ?? '0', 'priority', issues, -9223372036854775808n);
  value.weight = normalizeInteger(value.weight ?? '1', 'weight', issues, 1n, false, 100000n);
  return { value, issues };
}
