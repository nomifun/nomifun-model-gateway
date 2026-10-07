// Copyright 2026 NomiFun Model Gateway contributors
// SPDX-License-Identifier: Apache-2.0
import assert from 'node:assert/strict';
import { readFile, readdir } from 'node:fs/promises';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const allowed = new Set(['Apache-2.0', 'MIT', '0BSD', 'BSD-2-Clause', 'BSD-3-Clause', 'ISC', 'Unlicense', 'CC0-1.0']);
const dependencyFields = ['dependencies', 'devDependencies', 'optionalDependencies', 'peerDependencies'];
const excludedDirectories = new Set(['.git', '.tools', 'node_modules']);

// Every identifier in a dual-license expression must be permissive. Even an
// OR expression containing a copyleft alternative requires an explicit review.
export function checkLicense(expression, label) {
  assert.equal(typeof expression, 'string', `${label}: missing license metadata`);
  const tokens = expression.match(/\(|\)|[A-Za-z0-9.+-]+/g) ?? [];
  assert.equal(tokens.join(''), expression.replace(/\s/g, ''), `${label}: invalid SPDX expression`);
  let position = 0;
  function atom() {
    const token = tokens[position++];
    if (token === '(') {
      sequence();
      assert.equal(tokens[position++], ')', `${label}: unbalanced SPDX expression`);
    } else {
      assert.ok(allowed.has(token), `${label}: non-permissive or unknown license ${token ?? '(empty)'}`);
    }
  }
  function sequence() {
    atom();
    while (tokens[position] === 'AND' || tokens[position] === 'OR') {
      position++;
      atom();
    }
  }
  sequence();
  assert.equal(position, tokens.length, `${label}: unsupported SPDX expression`);
}

export function auditPackageLock(manifest, lock, label) {
  assert.ok([2, 3].includes(lock.lockfileVersion), `${label}: require npm package-lock.json v2 or v3`);
  assert.ok(lock.packages && typeof lock.packages === 'object' && !Array.isArray(lock.packages), `${label}: incomplete lockfile packages`);
  const root = lock.packages[''];
  assert.ok(root && typeof root === 'object', `${label}: missing root lockfile entry`);
  checkLicense(manifest.license, `${label}: package.json`);
  for (const field of dependencyFields) {
    assert.deepEqual(root[field] ?? {}, manifest[field] ?? {}, `${label}: package.json and lockfile ${field} differ`);
  }
  let count = 0;
  for (const [path, entry] of Object.entries(lock.packages)) {
    const packageLabel = `${label}: ${path || '(root)'}`;
    assert.ok(entry && typeof entry === 'object' && !Array.isArray(entry), `${packageLabel}: invalid package metadata`);
    checkLicense(entry.license, packageLabel);
    if (path === '') continue;
    assert.ok(!entry.link, `${packageLabel}: local workspace links require a separate source-license audit`);
    assert.match(entry.version ?? '', /^\d+\.\d+\.\d+(?:[-+][A-Za-z0-9.+-]+)?$/, `${packageLabel}: version is not pinned`);
    assert.match(entry.resolved ?? '', /^https:\/\//, `${packageLabel}: require a pinned HTTPS archive`);
    const integrity = /^(sha256|sha384|sha512)-([A-Za-z0-9+/]+={0,2})$/.exec(entry.integrity ?? '');
    assert.ok(integrity, `${packageLabel}: missing archive integrity`);
    const digest = Buffer.from(integrity[2], 'base64');
    const digestBytes = { sha256: 32, sha384: 48, sha512: 64 };
    assert.ok(digest.length === digestBytes[integrity[1]] && digest.toString('base64') === integrity[2], `${packageLabel}: invalid archive integrity digest`);
    count++;
  }
  function resolveDependency(path, name) {
    let scope = path;
    for (;;) {
      const candidate = `${scope ? `${scope}/` : ''}node_modules/${name}`;
      if (lock.packages[candidate]) return true;
      if (scope === '') return false;
      const slash = scope.lastIndexOf('/');
      scope = slash < 0 ? '' : scope.slice(0, slash);
    }
  }
  for (const [path, entry] of Object.entries(lock.packages)) {
    for (const field of ['dependencies', 'optionalDependencies', 'peerDependencies']) {
      for (const name of Object.keys(entry[field] ?? {})) {
        if (field === 'peerDependencies' && entry.peerDependenciesMeta?.[name]?.optional) continue;
        assert.ok(resolveDependency(path, name), `${label}: ${path || '(root)'} transitive ${name} missing from lockfile`);
      }
    }
  }
  // Direct references must also be represented at the root.
  for (const field of dependencyFields) {
    for (const name of Object.keys(manifest[field] ?? {})) {
      assert.ok(lock.packages[`node_modules/${name}`], `${label}: ${name} missing from lockfile`);
    }
  }
  return count;
}

async function manifestsIn(directory) {
  const result = [];
  for (const entry of await readdir(directory, { withFileTypes: true })) {
    if (entry.isSymbolicLink()) {
      // Dependency trees may use symlinks, but source manifests must be explicit.
      if (!excludedDirectories.has(entry.name)) throw new Error(`Source symlink requires a license audit: ${join(directory, entry.name)}`);
    } else if (entry.isDirectory() && !excludedDirectories.has(entry.name)) {
      result.push(...await manifestsIn(join(directory, entry.name)));
    } else if (entry.isFile() && entry.name === 'package.json') {
      result.push(join(directory, entry.name));
    }
  }
  return result;
}

export async function auditFrontend(repositoryRoot) {
  const manifests = await manifestsIn(repositoryRoot);
  if (manifests.length === 0) {
    console.log('Frontend license gate: no frontend package manifests in M0.');
    return;
  }
  for (const manifestPath of manifests) {
    const lockPath = join(dirname(manifestPath), 'package-lock.json');
    const manifest = JSON.parse(await readFile(manifestPath, 'utf8'));
    const lock = JSON.parse(await readFile(lockPath, 'utf8'));
    const count = auditPackageLock(manifest, lock, manifestPath);
    console.log(`Frontend license gate passed: ${manifestPath}; ${count} locked dependencies (including dev, optional and transitive).`);
  }
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const repositoryRoot = resolve(process.argv[2] ?? join(dirname(fileURLToPath(import.meta.url)), '..'));
  try {
    await auditFrontend(repositoryRoot);
  } catch (error) {
    console.error(`Frontend license check failed: ${error.message}`);
    process.exitCode = 1;
  }
}
