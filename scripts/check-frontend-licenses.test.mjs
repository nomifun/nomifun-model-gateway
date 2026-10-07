// Copyright 2026 NomiFun Model Gateway contributors
// SPDX-License-Identifier: Apache-2.0
import assert from 'node:assert/strict';
import test from 'node:test';
import { auditPackageLock, checkLicense } from './check-frontend-licenses.mjs';

function fixture() {
  const manifest = { license: 'Apache-2.0', dependencies: { direct: '^1.0.0' } };
  const lockedPackage = { version: '1.0.0', resolved: 'https://registry.npmjs.org/example/-/example-1.0.0.tgz', integrity: `sha512-${Buffer.alloc(64, 1).toString('base64')}`, license: 'MIT' };
  return { manifest, lock: { lockfileVersion: 3, packages: {
    '': { license: 'Apache-2.0', dependencies: { direct: '^1.0.0' } },
    'node_modules/direct': { ...lockedPackage, dependencies: { transitive: '^1.0.0' } },
    'node_modules/transitive': { ...lockedPackage, dev: true },
  } } };
}

test('permits an entirely permissive lockfile including transitive dev packages', () => {
  const { manifest, lock } = fixture();
  assert.equal(auditPackageLock(manifest, lock, 'fixture'), 2);
  checkLicense('(MIT OR Apache-2.0)', 'dual permissive');
  checkLicense('0BSD', 'zero-clause BSD');
  lock.packages['node_modules/transitive'].license = '0BSD';
  assert.equal(auditPackageLock(manifest, lock, 'fixture with zero-clause BSD'), 2);
});

test('rejects a forbidden transitive license and a copyleft OR alternative', () => {
  const { manifest, lock } = fixture();
  lock.packages['node_modules/transitive'].license = 'AGPL-3.0-only';
  assert.throws(() => auditPackageLock(manifest, lock, 'fixture'), /non-permissive or unknown license AGPL/);
  assert.throws(() => checkLicense('(MIT OR GPL-3.0-only)', 'dual'), /non-permissive or unknown license GPL/);
});

test('rejects missing metadata and unknown license expressions', () => {
  const { manifest, lock } = fixture();
  delete lock.packages['node_modules/transitive'].license;
  assert.throws(() => auditPackageLock(manifest, lock, 'fixture'), /missing license metadata/);
  assert.throws(() => checkLicense('UNKNOWN', 'unknown'), /non-permissive or unknown/);
  assert.throws(() => checkLicense('MIT WITH unknown-exception', 'exception'), /unsupported SPDX expression/);
});

test('rejects omitted transitive dependencies and accepts optional absent peers', () => {
  const missing = fixture();
  delete missing.lock.packages['node_modules/transitive'];
  assert.throws(() => auditPackageLock(missing.manifest, missing.lock, 'fixture'), /transitive transitive missing from lockfile/);
  const optionalPeer = fixture();
  optionalPeer.lock.packages['node_modules/direct'].peerDependencies = { peer: '^1.0.0' };
  optionalPeer.lock.packages['node_modules/direct'].peerDependenciesMeta = { peer: { optional: true } };
  assert.equal(auditPackageLock(optionalPeer.manifest, optionalPeer.lock, 'fixture'), 2);
  optionalPeer.lock.packages['node_modules/direct'].peerDependenciesMeta.peer.optional = false;
  assert.throws(() => auditPackageLock(optionalPeer.manifest, optionalPeer.lock, 'fixture'), /transitive peer missing from lockfile/);
});

test('rejects unpinned archives, stale manifests and missing direct packages', () => {
  const { manifest, lock } = fixture();
  delete lock.packages['node_modules/transitive'].integrity;
  assert.throws(() => auditPackageLock(manifest, lock, 'fixture'), /missing archive integrity/);
  const invalidDigest = fixture();
  invalidDigest.lock.packages['node_modules/transitive'].integrity = 'sha512-YWJj';
  assert.throws(() => auditPackageLock(invalidDigest.manifest, invalidDigest.lock, 'fixture'), /invalid archive integrity digest/);
  const stale = fixture();
  stale.manifest.dependencies.direct = '^2.0.0';
  assert.throws(() => auditPackageLock(stale.manifest, stale.lock, 'fixture'), /lockfile dependencies differ/);
  const missing = fixture();
  delete missing.lock.packages['node_modules/direct'];
  assert.throws(() => auditPackageLock(missing.manifest, missing.lock, 'fixture'), /direct missing from lockfile/);
});
