// SPDX-License-Identifier: Apache-2.0
import JSONBig from 'json-bigint';
export type Integer = number | bigint;
export type Row = Record<string, unknown>;
export const exactJSON = JSONBig({ useNativeBigInt: true, strict: true, protoAction: 'error', constructorAction: 'error' });
const tokenName = 'nmg.console.session';
export const session = {
  get: () => sessionStorage.getItem(tokenName),
  set: (token: string) => sessionStorage.setItem(tokenName, token),
  clear: () => sessionStorage.removeItem(tokenName)
};
export class APIError extends Error {
  status: number;
  constructor(message: string, status: number) { super(message); this.name = 'APIError'; this.status = status; }
}
export async function request<T = Row>(path: string, method = 'GET', body?: unknown): Promise<T> {
  if (!path.startsWith('/') || path.startsWith('//')) throw new Error('Invalid API path');
  const token = session.get();
  const response = await fetch('/api/console/v1' + path, {
    method, headers: { Accept: 'application/json', ...(token ? { Authorization: `Bearer ${token}` } : {}), ...(body === undefined ? {} : { 'Content-Type': 'application/json' }) },
    body: body === undefined ? undefined : exactJSON.stringify(body), credentials: 'same-origin', cache: 'no-store'
  });
  const raw = await response.text();
  let value: Row;
  try { value = raw ? exactJSON.parse(raw) : {}; } catch { throw new APIError('invalid_response', response.status); }
  if (!response.ok) {
    if (response.status === 401) { session.clear(); globalThis.dispatchEvent?.(new Event('nmg-session-expired')); }
    const error = value.error as Row | string | undefined;
    const message = typeof error === 'string' ? error : String(error?.message ?? value.message ?? 'request_failed');
    throw new APIError(message, response.status);
  }
  return value as T;
}
export async function signOut(): Promise<void> {
  // Revoke the authenticated server session before removing its local bearer.
  try { await request('/logout', 'POST'); } finally { session.clear(); }
}
export function integer(value: string, min = 0n, max = 9223372036854775807n): bigint {
  if (!/^-?\d+$/.test(value)) throw new Error('integer_required');
  const result = BigInt(value);
  if (result < min || result > max) throw new Error('integer_range');
  return result;
}
export function money(value: unknown, currency = 'USD'): string {
  const amount = BigInt(String(value ?? 0));
  const digits = new Intl.NumberFormat('en', { style: 'currency', currency }).resolvedOptions().maximumFractionDigits ?? 2;
  const divisor = 10n ** BigInt(digits);
  const negative = amount < 0n;
  const absolute = negative ? -amount : amount;
  return `${currency} ${negative ? '-' : ''}${(absolute / divisor).toLocaleString()}${digits ? '.' + String(absolute % divisor).padStart(digits, '0') : ''}`;
}
export function safeHTTPS(value: unknown): string | undefined {
  if (typeof value !== 'string') return undefined;
  try { const url = new URL(value); return url.protocol === 'https:' && !url.username && !url.password ? url.href : undefined; } catch { return undefined; }
}
export function text(value: unknown): string {
  if (value === null || value === undefined) return '—';
  if (typeof value === 'object') return exactJSON.stringify(value, null, 2);
  return String(value);
}
export function items(data: Row): Row[] { return Array.isArray(data.items) ? data.items as Row[] : []; }
