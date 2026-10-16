// SPDX-License-Identifier: Apache-2.0
import assert from 'node:assert/strict';
import { mkdir, rm, writeFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import { pathToFileURL } from 'node:url';
import test from 'node:test';
import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { createInstance } from 'i18next';
import { I18nextProvider } from 'react-i18next';
import { build } from 'esbuild';

const directory = resolve('node_modules/.cache/catalog-editors-test-' + process.pid);
await mkdir(directory, { recursive: true });
const result = await build({
  stdin: { contents: "export { ModelFieldsEditor } from './CatalogEditors';", resolveDir: resolve('src'), loader: 'ts' },
  bundle: true, packages: 'external', platform: 'node', format: 'esm', jsx: 'automatic', target: 'es2023', write: false, loader: { '.css': 'empty' }
});
const bundle = resolve(directory, 'catalog-editors.mjs');
await writeFile(bundle, result.outputFiles[0].text);
const { ModelFieldsEditor } = await import(pathToFileURL(bundle).href);
test.after(() => rm(directory, { recursive: true, force: true }));

test('Chat advisory controls preserve unknown and legacy metadata without presenting technical capability switches', async () => {
  const i18n = createInstance(); await i18n.init({ lng: 'en', resources: { en: { translation: {} } } });
  const markup = renderToStaticMarkup(React.createElement(I18nextProvider, { i18n }, React.createElement(ModelFieldsEditor, {
    value: { id: 'test', tasks: [], task_endpoints: {}, traits: ['vision_input', 'function_calling', 'reasoning', 'vendor.future'], pricing: [], status: 'available' }, onChange: () => undefined
  })));
  assert.match(markup, /Chat advisory traits/); assert.match(markup, /Vision input/); assert.match(markup, /Audio input/); assert.match(markup, /Video input/); assert.match(markup, /Web search/);
  assert.match(markup, /value="function_calling, reasoning, vendor.future"/); assert.match(markup, /not capability switches/);
  assert.doesNotMatch(markup, /type="checkbox"[^>]*\/?>[^<]*<span>(function_calling|reasoning|streaming|realtime|audio_output)<\/span>/);
  assert.match(markup, /9007199254740991/); assert.match(markup, /Loading protocol manifest/);
});
