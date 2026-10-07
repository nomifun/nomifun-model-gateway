// SPDX-License-Identifier: Apache-2.0
// Retain dependency license/NOTICE texts alongside a distributed binary/image.
import { readFile, readdir, realpath, writeFile } from 'node:fs/promises';
import { dirname, resolve, sep } from 'node:path';
import { fileURLToPath } from 'node:url';
import { auditPackageLock } from './check-frontend-licenses.mjs';
const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const consoleRoot = resolve(process.argv[2] ?? resolve(root, 'console'));
const output = resolve(process.argv[3] ?? resolve(root, '.tools/frontend-license-notices.txt'));
const manifest = JSON.parse(await readFile(resolve(consoleRoot, 'package.json'), 'utf8'));
const lock = JSON.parse(await readFile(resolve(consoleRoot, 'package-lock.json'), 'utf8'));
auditPackageLock(manifest, lock, 'console');
const parts = ['NomiFun Model Gateway frontend dependency notices\n', await readFile(resolve(root, 'LICENSE'), 'utf8')];
const nodeRoot = await realpath(resolve(consoleRoot, 'node_modules'));
for (const path of Object.keys(lock.packages).filter(path => path !== '').sort()) {
  if (!path.startsWith('node_modules/')) throw new Error('Unsupported locked package path');
  const entry = lock.packages[path];
  parts.push(`\n===== ${path} @ ${entry.version} | ${entry.license} =====\n`);
  let directory;
  try { directory = await realpath(resolve(consoleRoot, path)); }
  catch (error) {
    if (error.code === 'ENOENT' && entry.optional === true) {
      parts.push('Optional platform dependency is not installed or distributed in this build. Locked license metadata retained.\n');
      continue;
    }
    throw error;
  }
  if (!directory.startsWith(nodeRoot + sep)) throw new Error('Package source is outside node_modules');
  const names = (await readdir(directory)).filter(name => /^(?:licen[cs]e|copying|notice)(?:$|[.\-_])/i.test(name)).sort();
  if (names.length === 0) {
    const packageName = path.replace(/^node_modules\//, '');
    if ((packageName === 'unocss' || packageName.startsWith('@unocss/')) && entry.version === '66.10.5') {
      parts.push('Pinned publisher monorepo license (see supplemental provenance):\n');
      parts.push(await readFile(resolve(root, 'licenses/frontend/unocss-66.10.5.LICENSE'), 'utf8'));
      continue;
    }
    // Retain the publisher's own declared licensing and attribution metadata;
    // missing standalone license documents are explicitly visible for review.
    parts.push('No standalone license file shipped. Publisher package metadata follows:\n');
    parts.push(await readFile(resolve(directory, 'package.json'), 'utf8'));
  }
  for (const name of names) {
    const file = await realpath(resolve(directory, name));
    if (!file.startsWith(nodeRoot + sep)) throw new Error('License source is outside node_modules');
    parts.push(`\n--- ${name} ---\n`, await readFile(file, 'utf8'));
  }
}
await writeFile(output, parts.join('\n'), 'utf8');
console.log('Frontend publisher license and notice texts collected.');
