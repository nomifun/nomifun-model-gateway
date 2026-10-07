// SPDX-License-Identifier: Apache-2.0
import { useCallback, useEffect, useState } from 'react';
import { Alert, Button, Select, Space, Spin, Tag } from '@arco-design/web-react';
import { IconEdit, IconPlus, IconRefresh } from '@arco-design/web-react/icon';
import { useTranslation } from 'react-i18next';
import { exactJSON, items, request, safeHTTPS, text } from './api';
import type { Row } from './api';
import { channelFields, Editor, modelFields, planFields, settingFields } from './forms';
import type { Field } from './forms';
import { Details, ErrorBox, RecordTable, Secret } from './components';
import { useFeedback } from './feedback';
import { ConfigurationChecklist } from './visuals/configuration-checklist';
import { Filters } from './visuals/filters';
import { CatalogEditorModal, ProviderOnboarding, SavedDiscoveryModal } from './CatalogWorkflow';
const resources: Record<string, { path: string; columns: string[]; fields?: Field[]; hint?: string }> = {
  channels: { path: 'channels', columns: ['id', 'name', 'kind', 'base_url', 'priority', 'weight', 'enabled'], fields: channelFields },
  models: { path: 'models', columns: ['id', 'display_name', 'vendor', 'tasks', 'status', 'enabled', 'subscription_only'], fields: modelFields, hint: 'modelJSONHint' },
  users: { path: 'users', columns: ['id', 'email', 'name', 'balance', 'reserved_balance', 'admin', 'disabled'], fields: [{ key: 'admin', kind: 'boolean' }, { key: 'disabled', kind: 'boolean' }] },
  adminplans: { path: 'plans', columns: ['id', 'name', 'price', 'currency', 'period_days', 'token_quota', 'enabled'], fields: planFields },
  adminorders: { path: 'orders', columns: ['id', 'user_id', 'provider', 'amount', 'state', 'created_at'] },
  redeemcodes: { path: 'redeemcodes', columns: ['id', 'prefix', 'amount', 'currency', 'plan_id', 'redeemed_by', 'expires_at'], fields: [
    { key: 'amount', kind: 'int', required: true, initial: 0 }, { key: 'currency', required: true, initial: 'USD' }, { key: 'plan_id', kind: 'int', hint: 'unlimitedHint' }, { key: 'count', kind: 'int', min: 1n, initial: 1, required: true }, { key: 'expires_at', kind: 'date', hint: 'expiryHint' }
  ] },
  logs: { path: 'logs', columns: ['id', 'user_id', 'action', 'resource', 'created_at'], hint: 'auditDescription' },
  ledger: { path: 'ledger', columns: ['id', 'user_id', 'request_id', 'kind', 'amount', 'balance_after', 'created_at'], hint: 'ledgerDescription' },
  reservations: { path: 'reservations', columns: ['request_id', 'user_id', 'model_id', 'reserved_amount', 'actual_amount', 'state', 'created_at'], hint: 'reservationsDescription' }
};
const paymentFields = (provider: string): Field[] => [
  { key: 'enabled', kind: 'boolean' }, { key: 'sandbox', kind: 'boolean' },
  ...((provider === 'stripe' ? ['api_key', 'webhook_secret', 'stripe_account_id', 'return_url', 'cancel_url'] : provider === 'alipay' ? ['app_id', 'merchant_id', 'private_key', 'platform_public_key', 'notify_url', 'return_url'] : ['app_id', 'merchant_id', 'private_key', 'certificate_serial', 'api_v3_key', 'platform_public_key', 'platform_key_id', 'notify_url']).map(key => ({
    key, label: key === 'api_key' ? 'secret_key' : key === 'platform_public_key' ? 'public_key' : key,
    kind: (key === 'private_key' || key === 'platform_public_key' ? 'secret' : key.endsWith('_url') ? 'https' : key.includes('key') || key.includes('secret') ? 'password' : 'text') as Field['kind'],
    hint: key.includes('key') || key.includes('secret') ? 'secretHint' : key.endsWith('_url') ? 'httpsHint' : undefined, wide: key === 'private_key' || key === 'platform_public_key'
  })))
];
export function AdminPages({ page, onConfigChanged }: { page: string; onConfigChanged: () => Promise<void> }) {
  const { t } = useTranslation(); const feedback = useFeedback();
  const [data, setData] = useState<Row>({}); const [busy, setBusy] = useState(true); const [error, setError] = useState(''); const [filter, setFilter] = useState('');
  const [statusFilters, setStatusFilters] = useState<Record<string, string>>({});
  const [actionBusy, setActionBusy] = useState('');
  const [editor, setEditor] = useState<{ title: string; fields: Field[]; value: Row; hint?: string; save: (value: Row) => Promise<void> }>();
  const [detail, setDetail] = useState<Row>(); const [secret, setSecret] = useState('');
  const [catalogEditor, setCatalogEditor] = useState<{ kind: 'channel' | 'model'; value: Row; editing: boolean }>();
  const [onboardingCatalog, setOnboardingCatalog] = useState<Row[]>();
  const [discoveryEditor, setDiscoveryEditor] = useState<{ channel: Row; catalog: Row[] }>();
  const definition = resources[page]; const path = '/admin/' + (definition?.path ?? page);
  const load = useCallback(async () => { setBusy(true); setError(''); setData({}); try { setData(await request(path)); } catch (error) { setError(error instanceof Error ? error.message : 'failed'); } finally { setBusy(false); } }, [path]);
  useEffect(() => { void load(); setFilter(''); setStatusFilters({}); }, [load]);
  const act = async (action: () => Promise<void>, key = 'action') => { if (actionBusy) return; setActionBusy(key); setError(''); try { await action(); } catch (error) { setError(error instanceof Error ? error.message : 'failed'); } finally { setActionBusy(''); } };
  const allRows = items(data);
  const statusKeys = definition?.columns.filter(key => ['status', 'state', 'enabled', 'disabled', 'redeemed_by'].includes(key)) ?? [];
  const statusValue = (row: Row, key: string) => key === 'redeemed_by' ? Boolean(row[key]) ? 'redeemed' : 'unredeemed' : String(row[key] ?? '');
  const statusLabel = (key: string, value: string) => key === 'enabled' ? t(value === 'true' ? 'enabled' : 'disabled') : key === 'disabled' ? t(value === 'true' ? 'disabled' : 'enabled') : t(({ reserved: 'reservedState', failed: 'failedState' } as Record<string, string>)[value] ?? value);
  const rows = allRows.filter(row => (!filter.trim() || exactJSON.stringify(row).toLowerCase().includes(filter.trim().toLowerCase())) && statusKeys.every(key => !statusFilters[key] || statusValue(row, key) === statusFilters[key]));
  const hasFilters = !!filter.trim() || Object.values(statusFilters).some(Boolean);
  const clearFilters = () => { setFilter(''); setStatusFilters({}); };
  const operator = (data.operator ?? (data.meta as Row | undefined)?.operator ?? {}) as Row;
  const editResource = (row?: Row) => {
    if (!definition?.fields) return;
    const value = { ...(row ?? {}) };
    if (page === 'channels') { if (!value.model_ids && value.models_json) value.model_ids = exactJSON.parse(String(value.models_json)); if (!value.endpoints && value.endpoints_json) value.endpoints = exactJSON.parse(String(value.endpoints_json)); }
    if (page === 'adminplans' && !value.model_ids && value.model_ids_json) value.model_ids = exactJSON.parse(String(value.model_ids_json));
    if (page === 'channels' && !row) { void act(async () => { setOnboardingCatalog(items(await request('/admin/models'))); }, 'onboarding'); return; }
    if (page === 'channels' || page === 'models') {
      if (!row) Object.assign(value, { id: '', display_name: '', vendor: '', tasks: [], task_endpoints: {}, pricing: [], input_modalities: [], traits: [], status: 'available', enabled: false, subscription_only: false });
      setCatalogEditor({ kind: page === 'channels' ? 'channel' : 'model', value, editing: !!row }); return;
    }
    setEditor({ title: t(row ? 'edit' : 'create') + ' · ' + t(page), fields: definition.fields, value, hint: definition.hint, save: async output => {
      const response = await request(path + (row && page !== 'models' ? '/' + encodeURIComponent(String(row.id)) : ''), row && page !== 'models' ? 'PATCH' : 'POST', output);
      if (page === 'redeemcodes') { const codes = response.codes; if (Array.isArray(codes)) setSecret(codes.map(code => typeof code === 'string' ? code : String((code as Row).code)).join('\n')); }
      setEditor(undefined); await load();
    } });
  };
  const credit = (row: Row) => setEditor({ title: t('credit') + ' · ' + text(row.email), value: { currency: row.currency }, hint: 'creditHint', fields: [{ key: 'amount', kind: 'int', min: 1n, required: true }, { key: 'currency', required: true }, { key: 'reason', required: true, wide: true }], save: async output => { await request('/admin/users/' + encodeURIComponent(String(row.id)) + '/credit', 'POST', output); setEditor(undefined); await load(); } });
  const reservation = (row: Row) => setEditor({ title: t('reconcile'), value: {}, hint: 'reservationWarning', fields: [
    { key: 'action', label: 'reconcileAction', kind: 'select', required: true, initial: 'settle', options: [{ value: 'settle', label: 'settleAction' }, { value: 'release', label: 'releaseAction' }] }, { key: 'reason', required: true }, { key: 'usage', label: 'usageJSON', kind: 'json', wide: true }
  ], save: async output => { if (output.action === 'settle' && !output.usage) throw new Error('required'); await request('/admin/reservations/' + encodeURIComponent(String(row.request_id)) + '/reconcile', 'POST', output); setEditor(undefined); await load(); } });
  const editSettings = () => {
    const operator = data.operator as Row | undefined ?? (data.meta as Row | undefined)?.operator as Row | undefined ?? {};
    setEditor({ title: t('settings'), fields: settingFields, value: { ...data, ...operator }, save: async output => {
      const operator: Row = {}; for (const key of ['name', 'homepage_url', 'console_url', 'purchase_url', 'terms_url', 'privacy_url']) operator[key] = output[key];
      await request('/admin/settings', 'PUT', { operator, currency: output.currency, registration_enabled: output.registration_enabled, capabilities: output.capabilities, optional_endpoints: output.optional_endpoints }); setEditor(undefined); await load(); await onConfigChanged();
    } });
  };
  const editPayment = (row: Row) => {
    const provider = String(row.provider ?? row.id);
    setEditor({ title: t('paymentconfig') + ' · ' + provider, fields: paymentFields(provider), value: row, hint: 'paymentSettingsDescription', save: async output => { await request('/admin/paymentconfig/' + encodeURIComponent(provider), 'PUT', output); setEditor(undefined); await load(); await onConfigChanged(); } });
  };
  return <><div className="page-heading"><div><h1>{t(page)}</h1><div className="muted">{t(definition?.hint ?? (page === 'paymentconfig' ? 'paymentSettingsDescription' : 'adminDescription'))}</div></div><Space className="page-actions"><Button icon={<IconRefresh />} loading={busy} onClick={load}>{t('refresh')}</Button>{definition?.fields && page !== 'users' && <Button type="primary" icon={<IconPlus />} disabled={busy} onClick={() => editResource()}>{t('create')}</Button>}{page === 'settings' && <Button type="primary" icon={<IconEdit />} disabled={busy} onClick={editSettings}>{t('edit')}</Button>}</Space></div>
    <ErrorBox error={error} retry={load} />
    {error && !Object.keys(data).length ? null : definition ? <div className="panel table-panel"><Filters title={t(page)} query={filter} onQuery={setFilter} queryLabel={t('search')} placeholder={t('search')} clearLabel={t('clearFilters')} removeLabel={t('removeFilter')} onClear={clearFilters} count={hasFilters ? t('filteredCount', { visible: rows.length, total: allRows.length }) : t('recordsCount', { count: allRows.length })} activeFilters={statusKeys.filter(key => statusFilters[key]).map(key => ({ id: key, label: `${t(key === 'redeemed_by' || key === 'disabled' ? 'status' : key)}: ${statusLabel(key, statusFilters[key])}`, onRemove: () => setStatusFilters(old => ({ ...old, [key]: '' })) }))} slots={statusKeys.length > 0 ? statusKeys.map(key => <Select className="status-filter" key={key} aria-label={t(key === 'redeemed_by' ? 'status' : key)} value={statusFilters[key] ?? ''} options={[{ label: `${t(key === 'redeemed_by' || key === 'disabled' ? 'status' : key)} · ${t('allStatuses')}`, value: '' }, ...[...new Set(allRows.map(row => statusValue(row, key)))].filter(Boolean).map(value => ({ label: `${statusLabel(key, value)} (${allRows.filter(row => statusValue(row, key) === value).length})`, value }))]} onChange={value => setStatusFilters(old => ({ ...old, [key]: String(value) }))} />) : undefined} /><RecordTable rows={rows} busy={busy} columns={definition.columns} filtered={hasFilters} onReset={clearFilters} actions={row => <>
      {definition.fields && page !== 'redeemcodes' && <Button size="small" disabled={!!actionBusy} onClick={() => editResource(row)}>{t('edit')}</Button>}
      {page === 'channels' && <Button size="small" disabled={!!actionBusy} loading={actionBusy === `probe:${String(row.id)}`} onClick={() => act(async () => { const result = await request('/admin/channels/' + encodeURIComponent(String(row.id)) + '/probe', 'POST'); setDetail(result); await load(); }, `probe:${String(row.id)}`)}>{t('probe')}</Button>}
      {page === 'channels' && <Button size="small" disabled={!!actionBusy} loading={actionBusy === `discover:${String(row.id)}`} onClick={() => act(async () => { setDiscoveryEditor({ channel: row, catalog: items(await request('/admin/models')) }); }, `discover:${String(row.id)}`)}>{t('discoverModels')}</Button>}
      {page === 'channels' && <Button size="small" status="danger" disabled={!!actionBusy} onClick={() => feedback.confirm({ title: t('confirmDeleteChannel'), onOk: () => act(async () => { await request('/admin/channels/' + encodeURIComponent(String(row.id)), 'DELETE'); await load(); }) })}>{t('delete')}</Button>}
      {page === 'redeemcodes' && <Button size="small" status="danger" disabled={!!actionBusy || Boolean(row.redeemed_by)} onClick={() => feedback.confirm({ title: t('confirmDeleteCode'), onOk: () => act(async () => { await request('/admin/redeemcodes/' + encodeURIComponent(String(row.id)), 'DELETE'); await load(); }) })}>{t('delete')}</Button>}
      {page === 'models' && <Button size="small" status="danger" disabled={!!actionBusy} onClick={() => feedback.confirm({ title: t('confirmDelete'), onOk: () => act(async () => { await request('/admin/models/' + encodeURIComponent(String(row.id)), 'DELETE'); await load(); }) })}>{t('disabled')}</Button>}
      {page === 'users' && <Button size="small" disabled={!!actionBusy} onClick={() => credit(row)}>{t('credit')}</Button>}
      {page === 'adminorders' && row.state === 'pending' && <Button size="small" disabled={!!actionBusy} loading={actionBusy === `reconcile:${String(row.id)}`} onClick={() => act(async () => { await request('/admin/orders/' + encodeURIComponent(String(row.id)) + '/reconcile', 'POST'); await load(); }, `reconcile:${String(row.id)}`)}>{t('reconcile')}</Button>}
      {page === 'reservations' && ['reserved', 'reconciliation'].includes(String(row.state)) && <Button size="small" disabled={!!actionBusy} onClick={() => reservation(row)}>{t('reconcile')}</Button>}
      {!definition.fields && <Button size="small" onClick={() => setDetail(row)}>{t('details')}</Button>}
    </>} /></div> : busy ? <div className="panel page-loading"><Spin /></div> : page === 'settings' ? <><div className="settings-layout">
      <div className="panel settings-card"><div className="section-heading"><h2>{t('operator')}</h2><p className="muted">{t('operatorDescription')}</p></div><div className="operator-identity"><span className="provider-icon">{String(operator.name ?? 'O').slice(0, 1).toUpperCase()}</span><div><strong>{text(operator.name)}</strong><p className="muted">{t('subtitle')}</p></div></div></div>
      <div className="panel settings-card"><div className="section-heading"><h2>{t('gatewayPreferences')}</h2><p className="muted">{t('gatewayPreferencesDescription')}</p></div><dl className="detail-grid settings-values"><dt>{t('currency')}</dt><dd>{text(data.currency)}</dd><dt>{t('registration')}</dt><dd><Tag className={`status-tag ${data.registration_enabled ? 'status-success' : 'status-neutral'}`} color={data.registration_enabled ? 'green' : 'gray'}>{t(data.registration_enabled ? 'enabled' : 'disabled')}</Tag></dd><dt>{t('capabilities')}</dt><dd><div className="table-tags">{Array.isArray(data.capabilities) && data.capabilities.length ? data.capabilities.map(value => <Tag key={String(value)}>{text(value)}</Tag>) : '—'}</div></dd><dt>{t('optional_endpoints')}</dt><dd>{Array.isArray(data.optional_endpoints) && data.optional_endpoints.length ? data.optional_endpoints.map(text).join(', ') : '—'}</dd></dl></div>
      <div className="panel settings-card settings-links"><div className="section-heading"><h2>{t('publicLinks')}</h2><p className="muted">{t('publicLinksDescription')}</p></div><dl className="detail-grid settings-values">{['homepage_url', 'console_url', 'purchase_url', 'terms_url', 'privacy_url'].map(key => <div key={key} style={{ display: 'contents' }}><dt>{t(key)}</dt><dd>{safeHTTPS(operator[key]) ? <a href={safeHTTPS(operator[key])} target="_blank" rel="noopener noreferrer">{text(operator[key])}</a> : <span className="muted">{text(operator[key])}</span>}</dd></div>)}</dl></div>
    </div><Alert className="community-note" type="info" content={t('community')} /></>
      : page === 'paymentconfig' ? <div className="plan-grid provider-grid">{allRows.map(row => {
        const provider = String(row.provider ?? row.id);
        const providerNames: Record<string, string> = { stripe: 'Stripe', alipay: 'Alipay', wechat: 'WeChat Pay' };
        const credentials: Record<string, string[]> = { stripe: ['api_key', 'webhook_secret'], alipay: ['private_key', 'platform_public_key'], wechat: ['private_key', 'platform_public_key', 'api_v3_key'] };
        const presence = row.credential_presence as Row | undefined ?? {};
        const fields = credentials[provider] ?? Object.keys(presence);
        return <div className="panel provider-card" key={provider}><div className="provider-card-header"><span className={`provider-icon provider-${provider}`}>{(providerNames[provider] ?? provider).slice(0, 1)}</span><div><h2>{providerNames[provider] ?? provider}</h2><span className="muted">{t(row.sandbox ? 'sandbox' : 'productionMode')}</span></div><Tag className={`status-tag ${row.enabled ? 'status-success' : 'status-neutral'}`} color={row.enabled ? 'green' : 'gray'}>{t(row.enabled ? 'enabled' : 'disabled')}</Tag></div>
          <div className="provider-readiness"><Tag className={`status-tag ${row.configured ? 'status-success' : 'status-warning'}`} color={row.configured ? 'green' : 'orange'}>{t(row.configured ? 'configured' : 'unconfigured')}</Tag></div>
          <ConfigurationChecklist title={t('credentialStatus')} summary={t('paymentReadiness', { configured: fields.filter(key => presence[key]).length, total: fields.length })} items={fields.map(key => ({ id: key, label: t(key === 'api_key' ? 'secret_key' : key === 'platform_public_key' ? 'public_key' : key), present: Boolean(presence[key]) }))} presentLabel={t('configured')} missingLabel={t('unconfigured')} />
          <div className="provider-card-footer"><Button type={row.configured ? 'secondary' : 'primary'} long icon={<IconEdit />} onClick={() => editPayment(row)}>{t(row.configured ? 'edit' : 'configureProvider')}</Button></div></div>;
      })}</div> : null}
    {editor && <Editor {...editor} onCancel={() => setEditor(undefined)} onSave={editor.save} />}
    {catalogEditor && <CatalogEditorModal {...catalogEditor} onCancel={() => setCatalogEditor(undefined)} onSaved={load} />}
    {onboardingCatalog && <ProviderOnboarding catalog={onboardingCatalog} onCancel={() => setOnboardingCatalog(undefined)} onSaved={load} />}
    {discoveryEditor && <SavedDiscoveryModal {...discoveryEditor} onCancel={() => setDiscoveryEditor(undefined)} onSaved={load} />}
    {secret && <Secret kind="codes" value={secret} close={() => setSecret('')} />}{detail && <Details row={detail} close={() => setDetail(undefined)} />}
  </>;
}
