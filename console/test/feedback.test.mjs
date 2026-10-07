// SPDX-License-Identifier: Apache-2.0
import assert from 'node:assert/strict';
import { mkdir, readFile, readdir, rm } from 'node:fs/promises';
import test from 'node:test';
import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import i18n from 'i18next';
import { initReactI18next } from 'react-i18next';
import { build } from 'esbuild';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { resolve } from 'node:path';
import { runMutation } from '../src/mutation.ts';
import { request } from '../src/api.ts';

// Render the actual declarative feedback with its local component imports.
// Keep dependencies external so React 19 and the renderer share one instance.
await i18n.use(initReactI18next).init({ lng: 'en-US', resources: { 'en-US': { translation: { done: 'Done' } } }, interpolation: { escapeValue: false } });
const directory = resolve('node_modules/.cache/feedback-test-' + process.pid);
await mkdir(directory, { recursive: true });
await build({ entryPoints: [fileURLToPath(new URL('../src/feedback.tsx', import.meta.url))], outfile: resolve(directory, 'feedback.mjs'), bundle: true, packages: 'external', platform: 'node', jsx: 'automatic', format: 'esm', target: 'es2023' });
const { FeedbackNotice } = await import(pathToFileURL(resolve(directory, 'feedback.mjs')).href);
test.after(async () => { await rm(directory, { recursive: true, force: true }); });

test('a successful real API transport renders success feedback without becoming an API failure', async () => {
  globalThis.sessionStorage = { getItem: () => 'synthetic-session' };
  const original = globalThis.fetch; let writes = 0; let markup = '';
  globalThis.fetch = async (url, options) => { assert.equal(url, '/api/console/v1/redeem'); assert.equal(options.method, 'POST'); writes++; return new Response('{"ok":true,"redemption":{"id":7}}', { status: 200 }); };
  try {
    const result = await runMutation(() => request('/redeem', 'POST', { code: 'synthetic-code' }), () => { markup = renderToStaticMarkup(React.createElement(FeedbackNotice, { notice: { type: 'success', message: '操作已成功完成' } })); });
    assert.equal(result.ok, true); assert.equal(result.feedbackError, undefined); assert.equal(writes, 1);
    assert.match(markup, /操作已成功完成/); assert.match(markup, /role="status"/); assert.doesNotMatch(markup, /请求失败/);
  } finally { globalThis.fetch = original; }
});
test('feedback failure after a completed write does not invite another mutation', async () => {
  let writes = 0;
  const result = await runMutation(async () => { writes++; return { ok: true }; }, () => { throw new Error('feedback-render-failed'); });
  assert.equal(result.ok, true); assert.equal(result.feedbackError.message, 'feedback-render-failed'); assert.equal(writes, 1);
});
test('a failed mutation does not render success feedback', async () => {
  let notices = 0; const result = await runMutation(async () => { throw new Error('invalid-code'); }, () => { notices++; });
  assert.equal(result.ok, false); assert.equal(result.error.message, 'invalid-code'); assert.equal(notices, 0);
});
test('all console feedback and confirmations are owned by the existing React root', async () => {
  for (const file of await readdir(new URL('../src', import.meta.url))) {
    if (!file.endsWith('.tsx')) continue;
    const source = await readFile(new URL('../src/' + file, import.meta.url), 'utf8');
    assert.doesNotMatch(source, /\b(?:Message|Modal|Notification)\.(?:success|warning|error|info|confirm)\s*\(/, file);
  }
});
