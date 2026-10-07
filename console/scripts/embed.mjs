// SPDX-License-Identifier: Apache-2.0
import { cp, mkdir, readdir, readFile, rm, writeFile } from 'node:fs/promises';
import { dirname, resolve, sep } from 'node:path';
import { fileURLToPath } from 'node:url';
const root = resolve(dirname(fileURLToPath(import.meta.url)), '../..');
const source = resolve(root, 'console/dist');
const target = resolve(root, 'internal/console/assets');
// Only this explicitly named generated directory may be replaced.
if (target !== resolve(root, 'internal/console/assets') || !target.startsWith(root + sep)) throw new Error('Unsafe embed path');
await readdir(source); // A failed frontend build must preserve the last embedded build.
// Retain notices for every installed runtime package. Build/test-only packages
// are audited in the lockfile but do not ship inside the browser bundle.
const lock = JSON.parse(await readFile(resolve(root, 'console/package-lock.json'), 'utf8'));
const notices = ['NomiFun Model Gateway console — third-party runtime notices\nProject: Apache-2.0\n'];
notices.push(await readFile(resolve(root, 'console/notices/codevisual.txt'), 'utf8'));
for (const [name, metadata] of Object.entries(lock.packages)) {
  if (!name || metadata.dev) continue;
  const directory = resolve(root, 'console', name);
  if (!directory.startsWith(resolve(root, 'console/node_modules') + sep)) throw new Error('Unsafe package path');
  let entries;
  try { entries = await readdir(directory, { withFileTypes: true }); } catch { continue; } // Other-platform optional archive.
  const files = entries.filter(entry => entry.isFile() && /^(licen[cs]e|copying|copyright|notice)([._-]|$)/i.test(entry.name));
  if (!files.length) {
    if (name !== 'node_modules/number-precision' || metadata.version !== '1.6.0' || metadata.license !== 'MIT') throw new Error(`Runtime dependency ${name} has no distributable license text`);
    notices.push(await readFile(resolve(root, 'console/notices/number-precision-1.6.0.txt'), 'utf8'));
    continue;
  }
  notices.push(`\n${name.replace(/^node_modules\//, '')} ${metadata.version} (${metadata.license})\n`);
  for (const file of files) notices.push(await readFile(resolve(directory, file.name), 'utf8'));
}
await writeFile(resolve(source, 'THIRD_PARTY_LICENSES.txt'), notices.join('\n'), 'utf8');
await mkdir(target, { recursive: true });
for (const name of await readdir(target)) await rm(resolve(target, name), { recursive: true, force: true });
await cp(source, target, { recursive: true, errorOnExist: true });
console.log('Console build copied to internal/console/assets for go:embed.');
