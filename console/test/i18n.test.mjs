// SPDX-License-Identifier: Apache-2.0
import assert from 'node:assert/strict';
import { readFile, readdir } from 'node:fs/promises';
import test from 'node:test';
globalThis.localStorage = { getItem: () => null };
const { resources } = await import('../src/i18n.ts');
test('English and Chinese have complete matching translation keys', () => {
  const en = resources['en-US'].translation; const zh = resources['zh-CN'].translation;
  assert.deepEqual(Object.keys(en).sort(), Object.keys(zh).sort());
  for (const [key, value] of Object.entries(zh)) assert.ok(value.length > 0, `${key} is empty`);
});
test('literal UI translations resolve in both languages', async () => {
  for (const file of await readdir(new URL('../src', import.meta.url))) {
    if (!file.endsWith('.tsx')) continue;
    const source = await readFile(new URL('../src/' + file, import.meta.url), 'utf8');
    for (const match of source.matchAll(/\bt\('([^']+)'\)/g)) for (const language of Object.keys(resources)) assert.ok(match[1] in resources[language].translation, `${file}: missing ${language} key ${match[1]}`);
  }
});
