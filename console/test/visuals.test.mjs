// SPDX-License-Identifier: Apache-2.0
import assert from 'node:assert/strict';
import { mkdir, rm, writeFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import { pathToFileURL } from 'node:url';
import test from 'node:test';
import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { build } from 'esbuild';

const directory = resolve('node_modules/.cache/visuals-test-' + process.pid);
await mkdir(directory, { recursive: true });
const result = await build({
  stdin: {
    contents: "export { StatCard } from './stat-card'; export { UsageMeter } from './usage-meter'; export { StateEmpty } from './state-empty'; export { StateError } from './state-error'; export { Filters } from './filters'; export { ConfigurationChecklist } from './configuration-checklist';",
    resolveDir: resolve('src/visuals'), loader: 'ts',
  }, bundle: true, packages: 'external', platform: 'node', format: 'esm', jsx: 'automatic', target: 'es2023', write: false,
});
const bundle = resolve(directory, 'visuals.mjs');
await writeFile(bundle, result.outputFiles[0].text);
const { StatCard, UsageMeter, StateEmpty, StateError, Filters, ConfigurationChecklist } = await import(pathToFileURL(bundle).href);
test.after(() => rm(directory, { recursive: true, force: true }));
const render = (component, props) => renderToStaticMarkup(React.createElement(component, props));

test('account components preserve exact int64 text and never add demonstration revenue or trends', () => {
  const markup = render(StatCard, { label: 'Balance', value: 'USD 90,071,992,547,409.93', footer: 'Reserved USD 0.00' });
  assert.match(markup, /USD 90,071,992,547,409\.93/);
  assert.doesNotMatch(markup, /48,213|12\.4%|Revenue|aria-hidden="true"[^>]*>USD/);
});

test('an undisclosed or invalid quota does not manufacture a progress percentage', () => {
  for (const percent of [undefined, null, Number.NaN]) {
    const markup = render(UsageMeter, { title: 'Token quota', value: '9,007,199,254,740,993 / —', percent, hint: 'Not disclosed' });
    assert.match(markup, /9,007,199,254,740,993/);
    assert.doesNotMatch(markup, /role="progressbar"|1\.24M|248\.60|62%/);
  }
  assert.match(render(UsageMeter, { title: 'Token quota', value: '0 / 100', percent: 0 }), /aria-valuenow="0"/);
});

test('empty and failure components retain a real action and an accessible error', () => {
  const empty = render(StateEmpty, { title: 'No keys', description: 'Create your first key', actionLabel: 'Create key', onAction: () => undefined });
  assert.match(empty, /<button[^>]*aria-label="Create key"/);
  assert.match(empty, /Create your first key/);
  const error = render(StateError, { title: 'Request failed', message: 'Actual API failure', retryLabel: 'Retry', onRetry: () => undefined });
  assert.match(error, /role="alert"/); assert.match(error, /Actual API failure/); assert.match(error, /<button[^>]*>.*Retry/s);
});

test('filter counts and credential presence remain actual supplied values', () => {
  const filter = render(Filters, { title: 'Models', query: 'text', onQuery: () => undefined, placeholder: 'Find models', clearLabel: 'Clear', removeLabel: 'Remove', count: '0 of 2 records' });
  assert.match(filter, /value="text"/); assert.match(filter, /0 of 2 records/); assert.doesNotMatch(filter, /100%/);
  const credentials = render(ConfigurationChecklist, { title: 'Credential status', summary: '0 of 1 configured', items: [{ id: 'api_key', label: 'API key', present: false }], presentLabel: 'Configured', missingLabel: 'Missing' });
  assert.match(credentials, /Missing/); assert.doesNotMatch(credentials, /42 ms|Operational|us-east/);
});
