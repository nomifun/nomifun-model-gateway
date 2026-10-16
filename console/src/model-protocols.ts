// SPDX-License-Identifier: Apache-2.0
import { record } from './catalog-form.ts';

export type ProtocolEndpoint = { task: string; field: string; purpose: string; method: string; default_value: string; root_shape: string };
export type ProtocolDescriptor = {
  protocol_id: string; endpoint: string; supported_tasks: string[]; executor: string; transport: string; connection_role: string;
  auth_scheme: string; root_shape: string; base_url_suffix: string; requires_output_ceiling: boolean; requires_stream_usage: boolean; endpoints: ProtocolEndpoint[];
};
export type ProtocolManifest = {
  contract_version: string; manifest_version: string; protocols: ProtocolDescriptor[]; unsupported_tasks: string[];
  catalog_traits: string[]; technical_capabilities: string[]; traits_are_advisory: boolean; max_token_limit: number;
};
function strings(value: unknown): value is string[] { return Array.isArray(value) && value.every(item => typeof item === 'string'); }
export function parseProtocolManifest(value: unknown): ProtocolManifest {
  const manifest = record(value);
  if (manifest.contract_version !== '1.0' || manifest.manifest_version !== '1' || !Array.isArray(manifest.protocols) ||
      !strings(manifest.unsupported_tasks) || !strings(manifest.catalog_traits) || !strings(manifest.technical_capabilities) ||
      manifest.traits_are_advisory !== true || manifest.max_token_limit !== Number.MAX_SAFE_INTEGER) throw new Error('invalid_protocol_manifest');
  for (const raw of manifest.protocols) {
    const protocol = record(raw);
    if (!['protocol_id', 'endpoint', 'executor', 'transport', 'connection_role', 'auth_scheme', 'root_shape', 'base_url_suffix'].every(key => typeof protocol[key] === 'string') ||
        !strings(protocol.supported_tasks) || typeof protocol.requires_output_ceiling !== 'boolean' || typeof protocol.requires_stream_usage !== 'boolean' || !Array.isArray(protocol.endpoints)) throw new Error('invalid_protocol_manifest');
    for (const rawEndpoint of protocol.endpoints) {
      const endpoint = record(rawEndpoint);
      if (!['task', 'field', 'purpose', 'method', 'default_value', 'root_shape'].every(key => typeof endpoint[key] === 'string')) throw new Error('invalid_protocol_manifest');
    }
  }
  return manifest as ProtocolManifest;
}
let pending: Promise<ProtocolManifest> | undefined;
export function loadProtocolManifest(): Promise<ProtocolManifest> {
  if (!pending) {
    const controller = new AbortController(); const timeout = setTimeout(() => controller.abort(), 8000);
    pending = fetch('/nomifun/v1/protocols', { headers: { Accept: 'application/json' }, credentials: 'same-origin', cache: 'no-store', signal: controller.signal }).then(async response => {
      if (!response.ok) throw new Error('protocol_manifest_unavailable');
      return parseProtocolManifest(await response.json());
    }).finally(() => { pending = undefined; clearTimeout(timeout); });
  }
  return pending;
}
export function protocolsForRoute(manifest: ProtocolManifest, task: string, endpoints: string[]): ProtocolDescriptor[] {
  return manifest.protocols.filter(protocol => protocol.supported_tasks.includes(task) && endpoints.includes(protocol.endpoint));
}
