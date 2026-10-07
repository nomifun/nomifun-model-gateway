// SPDX-License-Identifier: Apache-2.0
import assert from 'node:assert/strict';
import test from 'node:test';
import { exactJSON, integer, money, request, safeHTTPS, session, signOut } from '../src/api.ts';

test('int64 money and transport preserve exact minor units above the JS safe limit', () => {
  const value = 9223372036854775807n;
  assert.equal(exactJSON.parse('{"amount":9223372036854775807}').amount, value);
  assert.equal(exactJSON.stringify({ amount: value }), '{"amount":9223372036854775807}');
  assert.equal(money(value, 'USD'), 'USD 92,233,720,368,547,758.07');
  assert.equal(money(-101, 'CNY'), 'CNY -1.01');
  assert.equal(money(100, 'JPY'), 'JPY 100');
  assert.equal(money(1001, 'BHD'), 'BHD 1.001');
});
test('money inputs reject fractions, overflow, negative amounts and exponent notation', () => {
  for (const value of ['1.01', '1e3', '9223372036854775808', '-1', 'NaN']) assert.throws(() => integer(value));
  assert.equal(integer('9007199254740993'), 9007199254740993n);
});
test('external links only accept credential-free HTTPS', () => {
  for (const value of ['javascript:alert(1)', 'http://shop.test', 'https://key@shop.test', 'https://key:secret@shop.test', 'weixin://wxpay/123', '//shop.test']) assert.equal(safeHTTPS(value), undefined);
  assert.equal(safeHTTPS('https://shop.test/checkout'), 'https://shop.test/checkout');
});
test('parser rejects duplicate and prototype fields instead of ambiguous money', () => {
  assert.throws(() => exactJSON.parse('{"amount":1,"amount":100}'));
  assert.throws(() => exactJSON.parse('{"__proto__":{"admin":true}}'));
});
test('API uses a session-only bearer and keeps secret values out of URLs', async () => {
  const values = new Map(); globalThis.sessionStorage = { getItem: key => values.get(key) ?? null, setItem: (key, value) => values.set(key, value), removeItem: key => values.delete(key) };
  session.set('test-session');
  const original = globalThis.fetch; const calls = [];
  globalThis.fetch = async (...args) => { calls.push(args); return new Response('{"amount":9007199254740993}', { status: 200 }); };
  try {
    const result = await request('/orders', 'POST', { amount: integer('9007199254740993'), api_key: 'one-time' });
    assert.equal(result.amount, 9007199254740993n); assert.equal(calls[0][0], '/api/console/v1/orders');
    assert.equal(calls[0][1].headers.Authorization, 'Bearer test-session');
    assert.match(calls[0][1].body, /"amount":9007199254740993/);
    assert.equal(calls[0][1].cache, 'no-store'); assert.equal(values.size, 1);
  } finally { globalThis.fetch = original; session.clear(); }
});
test('unauthorized API clears expired sessions and never substitutes mock data', async () => {
  const values = new Map(); globalThis.sessionStorage = { getItem: key => values.get(key) ?? null, setItem: (key, value) => values.set(key, value), removeItem: key => values.delete(key) };
  session.set('expired'); const original = globalThis.fetch;
  globalThis.fetch = async () => new Response('{"error":{"message":"session_expired"}}', { status: 401 });
  try { await assert.rejects(request('/me'), { message: 'session_expired', status: 401 }); assert.equal(session.get(), null); } finally { globalThis.fetch = original; }
});
test('sign out revokes with the current bearer before clearing the local session', async () => {
  const values = new Map(); globalThis.sessionStorage = { getItem: key => values.get(key) ?? null, setItem: (key, value) => values.set(key, value), removeItem: key => values.delete(key) };
  const original = globalThis.fetch; session.set('revoke-me');
  globalThis.fetch = async (url, options) => { assert.equal(url, '/api/console/v1/logout'); assert.equal(options.method, 'POST'); assert.equal(options.headers.Authorization, 'Bearer revoke-me'); assert.equal(session.get(), 'revoke-me'); return new Response('{}'); };
  try { await signOut(); assert.equal(session.get(), null); } finally { globalThis.fetch = original; }
});
test('sign out clears local credentials even if revocation transport fails', async () => {
  const values = new Map(); globalThis.sessionStorage = { getItem: key => values.get(key) ?? null, setItem: (key, value) => values.set(key, value), removeItem: key => values.delete(key) };
  const original = globalThis.fetch; session.set('network-failure');
  globalThis.fetch = async () => { throw new Error('offline'); };
  try { await assert.rejects(signOut(), /offline/); assert.equal(session.get(), null); } finally { globalThis.fetch = original; }
});
