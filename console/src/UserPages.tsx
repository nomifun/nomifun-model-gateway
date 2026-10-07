// SPDX-License-Identifier: Apache-2.0
import { useCallback, useEffect, useState } from 'react';
import type { ReactNode } from 'react';
import { Alert, Button, Form, Input, Modal, Select, Space, Spin, Tag } from '@arco-design/web-react';
import { IconApps, IconArrowRight, IconCalendar, IconCode, IconCopy, IconDashboard, IconDesktop, IconFile, IconGift, IconLock, IconPlus, IconRefresh, IconSafe, IconSearch, IconThunderbolt } from '@arco-design/web-react/icon';
import { useTranslation } from 'react-i18next';
import { integer, items, money, request, safeHTTPS, text } from './api';
import type { Row } from './api';
import { Editor, keyFields } from './forms';
import { Details, ErrorBox, PaymentQR, RecordTable, Secret } from './components';
import { useFeedback } from './feedback';
import { runMutation } from './mutation';
import { StateEmpty } from './visuals/state-empty';
import { Filters } from './visuals/filters';
import { StatCard } from './visuals/stat-card';
import { UsageMeter } from './visuals/usage-meter';

function integerText(value: unknown): string {
  if (value === null || value === undefined) return '—';
  try { return BigInt(String(value)).toLocaleString(); } catch { return text(value); }
}

function stringList(value: unknown): string[] {
  if (Array.isArray(value)) return value.map(String);
  if (typeof value !== 'string') return [];
  try { const parsed: unknown = JSON.parse(value); return Array.isArray(parsed) ? parsed.map(String) : []; } catch { return []; }
}

function quotaPercentage(used: unknown, total: unknown): number | undefined {
  if (used === null || used === undefined || total === null || total === undefined) return undefined;
  try {
    const usedValue = BigInt(String(used)); const totalValue = BigInt(String(total));
    if (usedValue < 0n || totalValue <= 0n) return undefined;
    return Number((usedValue > totalValue ? totalValue : usedValue) * 10000n / totalValue) / 100;
  } catch { return undefined; }
}

function EmptyState({ icon, title, description, action }: { icon: ReactNode; title: string; description: string; action?: ReactNode }) {
  return <StateEmpty icon={icon} title={title} description={description} action={action} />;
}

export function UserPages({ page, config, user, onNavigate }: { page: string; config: Row; user: Row; onNavigate?: (page: string) => void }) {
  const { t } = useTranslation(); const feedback = useFeedback();
  const [data, setData] = useState<Row>({}); const [busy, setBusy] = useState(true); const [error, setError] = useState('');
  const [editor, setEditor] = useState(false); const [secret, setSecret] = useState(''); const [detail, setDetail] = useState<Row>();
  const [checkout, setCheckout] = useState<Row>(); const [order, setOrder] = useState<Row>();
  const [provider, setProvider] = useState(''); const [amount, setAmount] = useState('1000'); const [code, setCode] = useState(''); const [actionBusy, setActionBusy] = useState(false);
  const [search, setSearch] = useState(''); const [stateFilter, setStateFilter] = useState(''); const [vendorFilter, setVendorFilter] = useState(''); const [taskFilter, setTaskFilter] = useState('');
  const load = useCallback(async () => {
    setBusy(true); setError(''); setData({});
    try { if (page === 'overview') { const [me, usage] = await Promise.all([request('/me'), request('/usage')]); setData({ ...me, items: items(usage).slice(0, 5) }); } else setData(await request('/' + page)); }
    catch (error) { setError(error instanceof Error ? error.message : 'failed'); } finally { setBusy(false); }
  }, [page]);
  useEffect(() => { void load(); }, [load]);
  useEffect(() => { setSearch(''); setStateFilter(''); setVendorFilter(''); setTaskFilter(''); }, [page]);
  const act = async <T,>(action: () => Promise<T>, onSuccess?: (value: T) => unknown | Promise<unknown>) => {
    setActionBusy(true); setError('');
    try { const result = await runMutation(action, onSuccess); if (!result.ok) setError(result.error instanceof Error ? result.error.message : 'failed'); else if (result.feedbackError) feedback.warning(t('actionCompleted')); }
    finally { setActionBusy(false); }
  };
  const providerList = Array.isArray(config.payment_providers) ? config.payment_providers as (Row | string)[] : [];
  const providers = providerList.filter(value => typeof value === 'string' || value.enabled === true).map(value => typeof value === 'string' ? { label: value, value } : { label: String(value.name ?? value.provider ?? value.id), value: String(value.id ?? value.provider) });
  const makeOrder = async () => {
    if (!provider) throw new Error('selectProvider');
    const result = await request('/orders', 'POST', { provider, kind: checkout?.kind ?? 'wallet', ...(checkout?.plan_id ? { plan_id: checkout.plan_id } : { amount: integer(amount, 1n) }), currency: String(user.currency ?? 'USD') });
    setOrder((result.order ?? result) as Row); setCheckout(undefined); await load();
  };
  const refreshOrder = () => act(async () => { const result = await request('/orders/' + encodeURIComponent(String(order?.id))); setOrder((result.order ?? result) as Row); await load(); });
  const copyEndpoint = async () => { try { await navigator.clipboard.writeText(window.location.origin); feedback.success(t('copied')); } catch { feedback.warning(t('copyFailed')); } };
  const account = data.account as Row | undefined; const currentUser = data.user as Row | undefined;
  const balance = account?.balance as Row | undefined; const plan = account?.plan as Row | undefined; const quota = plan?.quota as Row | undefined;
  const meta = config.meta as Row | undefined; const operator = meta?.operator as Row | undefined;
  const description = ['overview', 'keys', 'usage', 'plans', 'orders', 'catalog'].includes(page) ? page + 'Description' : 'adminDescription';
  const rows = page === 'catalog' ? Array.isArray(data.models) ? data.models as Row[] : [] : items(data);
  const query = search.trim().toLocaleLowerCase();
  const searchFields = page === 'keys' ? ['name', 'prefix', 'model_ids'] : page === 'usage' ? ['request_id', 'model_id', 'endpoint'] : page === 'orders' ? ['id', 'provider', 'kind'] : ['id', 'display_name', 'vendor', 'tasks'];
  const filteredRows = rows.filter(row => (!query || searchFields.some(key => text(row[key]).toLocaleLowerCase().includes(query))) && (!stateFilter || String(row.state) === stateFilter) && (!vendorFilter || String(row.vendor) === vendorFilter) && (!taskFilter || stringList(row.tasks).includes(taskFilter)));
  const vendors = [...new Set(rows.map(row => String(row.vendor ?? '')).filter(Boolean))].sort();
  const tasks = [...new Set(rows.flatMap(row => stringList(row.tasks)))].sort();
  const progress = quotaPercentage(quota?.used, quota?.total);
  const clearFilters = () => { setSearch(''); setStateFilter(''); setVendorFilter(''); setTaskFilter(''); };
  const filterSlots = <>
    {(page === 'usage' || page === 'orders') && <Select className="table-filter" value={stateFilter} onChange={setStateFilter} aria-label={t('state')} options={[{ label: t('allStates'), value: '' }, ...(page === 'usage' ? ['reserved', 'settled', 'released', 'reconciliation'] : ['pending', 'paid', 'cancelled', 'failed']).map(value => ({ label: t(value === 'reserved' ? 'reservedState' : value === 'failed' ? 'failedState' : value), value }))]} />}
    {page === 'catalog' && <><Select className="table-filter" value={vendorFilter} onChange={setVendorFilter} aria-label={t('vendor')} options={[{ label: t('allVendors'), value: '' }, ...vendors.map(value => ({ label: value, value }))]} /><Select className="table-filter" value={taskFilter} onChange={setTaskFilter} aria-label={t('tasks')} options={[{ label: t('allTasks'), value: '' }, ...tasks.map(value => ({ label: t(value), value }))]} /></>}
    </>;
  const activeFilters = [
    ...(stateFilter ? [{ id: 'state', label: `${t('state')}: ${t(stateFilter === 'reserved' ? 'reservedState' : stateFilter === 'failed' ? 'failedState' : stateFilter)}`, onRemove: () => setStateFilter('') }] : []),
    ...(vendorFilter ? [{ id: 'vendor', label: `${t('vendor')}: ${vendorFilter}`, onRemove: () => setVendorFilter('') }] : []),
    ...(taskFilter ? [{ id: 'task', label: `${t('tasks')}: ${t(taskFilter)}`, onRemove: () => setTaskFilter('') }] : []),
  ];
  const searchToolbar = <Filters title={t(page)} query={search} onQuery={setSearch} queryLabel={t('search')} placeholder={t(page === 'keys' ? 'searchKeys' : page === 'usage' ? 'searchUsage' : page === 'orders' ? 'searchOrders' : 'searchModels')} slots={page === 'keys' ? undefined : filterSlots} activeFilters={activeFilters} onClear={clearFilters} clearLabel={t('clearFilters')} removeLabel={t('removeFilter')} count={t('resultCount', { count: filteredRows.length })} />;
  const noMatches = <EmptyState icon={<IconSearch />} title={t('noMatches')} description={t('noMatchesHint')} action={<Button onClick={clearFilters}>{t('clearFilters')}</Button>} />;
  const desktopURL = `nomifun://add-provider?platform=nomifun-model-gateway&base_url=${encodeURIComponent(window.location.origin)}&name=${encodeURIComponent(String(operator?.name ?? 'Model Gateway'))}`;
  return <>
    <div className="page-heading"><div><h1>{t(page)}</h1><div className="muted">{t(description)}</div></div><Space><Button icon={<IconRefresh />} loading={busy} onClick={load}>{t('refresh')}</Button>{page === 'keys' && <Button type="primary" icon={<IconPlus />} onClick={() => setEditor(true)}>{t('createKey')}</Button>}{page === 'orders' && <Button type="primary" icon={<IconPlus />} onClick={() => setCheckout({ kind: 'wallet' })}>{t('topUp')}</Button>}</Space></div>
    <ErrorBox error={error} retry={load} />
    {error && !Object.keys(data).length ? null : busy && !Object.keys(data).length ? <div className="panel page-loading"><Spin /></div> : page === 'overview' ? <>
      <div className="welcome-panel"><div className="welcome-copy"><div className="welcome-kicker">{t('workspace')}</div><h2>{t('welcomeBack', { name: String(currentUser?.name ?? user.name ?? '') })}</h2><p className="muted">{t('overviewWelcomeHint')}</p></div><Button icon={<IconPlus />} type="primary" onClick={() => setEditor(true)}>{t('createKey')}</Button></div>
      <div className="grid-cards">
        <StatCard label={t('balance')} icon={<IconSafe />} value={balance?.amount === undefined && currentUser?.balance === undefined ? '—' : money(balance?.amount ?? currentUser?.balance, String(balance?.currency ?? currentUser?.currency ?? user.currency ?? 'USD'))} footer={<span>{t('reserved')}: {currentUser?.reserved_balance === undefined || currentUser.reserved_balance === null ? '—' : money(currentUser.reserved_balance, String(currentUser?.currency ?? user.currency ?? 'USD'))}</span>} action={<Button type="text" size="small" onClick={() => setCheckout({ kind: 'wallet' })}>{t('topUp')}<IconArrowRight /></Button>} />
        <StatCard className="cv-subscription-card" label={t('subscription')} icon={<IconCalendar />} value={text(plan?.name ?? t('noSubscription'))} footer={plan?.period_end ? t('validUntil', { date: new Date(String(plan.period_end)).toLocaleDateString() }) : t('subscriptionHint')} action={onNavigate && <Button type="text" size="small" onClick={() => onNavigate('plans')}>{t('viewPlans')}<IconArrowRight /></Button>} />
        <UsageMeter title={t('quota')} icon={<IconDashboard />} value={<>{integerText(quota?.used)}<span className="cv-value-denominator"> / {integerText(quota?.total)}</span></>} percent={progress} meterLabel={t('quota')} valueText={`${integerText(quota?.used)} / ${integerText(quota?.total)}`} hint={progress !== undefined ? t('quotaUsedPercent', { percent: progress }) : !quota ? t('quotaNoSubscription') : quota.total === null || quota.total === undefined ? t('quotaNotDisclosed') : t('quotaUsedCount', { used: integerText(quota.used) })} />
      </div>
      <div className="two-panels">
        <div className="panel onboarding-panel"><div className="section-heading"><div><h2>{t('quickStart')}</h2><p className="muted">{t('quickStartDescription')}</p></div><span className="metric-icon" aria-hidden="true"><IconDesktop /></span></div><div className="setup-steps">
          <div className="setup-step"><span className="setup-number">1</span><div className="setup-content"><h3>{t('createKey')}</h3><p className="muted">{t('setupKeyHint')}</p></div><Button size="small" onClick={() => setEditor(true)}>{t('create')}</Button></div>
          <div className="setup-step"><span className="setup-number">2</span><div className="setup-content"><h3>{t('setupGateway')}</h3><p className="muted">{t('setupGatewayHint')}</p></div><Button size="small" href={desktopURL}>{t('addDesktop')}</Button></div>
          <div className="setup-step"><span className="setup-number">3</span><div className="setup-content"><h3>{t('setupModel')}</h3><p className="muted">{t('setupModelHint')}</p></div>{onNavigate && <Button size="small" onClick={() => onNavigate('catalog')}>{t('catalog')}</Button>}</div>
        </div><div className="endpoint-value"><span><IconCode />{window.location.origin}</span><Button type="text" size="small" icon={<IconCopy />} onClick={copyEndpoint} aria-label={t('copy')}>{t('copy')}</Button></div></div>
        <div className="panel account-panel"><div className="section-heading"><div><h2>{t('account')}</h2><p className="muted">{t('accountSnapshotHint')}</p></div><span className="metric-icon" aria-hidden="true"><IconLock /></span></div><dl className="detail-grid"><dt>{t('name')}</dt><dd>{text(currentUser?.name ?? user.name)}</dd><dt>{t('email')}</dt><dd>{text(currentUser?.email ?? user.email)}</dd><dt>{t('operator')}</dt><dd>{text(operator?.name)}</dd></dl>{onNavigate && <div className="shortcut-grid"><button className="shortcut-card" onClick={() => onNavigate('catalog')}><span className="shortcut-icon"><IconApps /></span><span>{t('catalog')}</span><IconArrowRight /></button><button className="shortcut-card" onClick={() => onNavigate('usage')}><span className="shortcut-icon"><IconThunderbolt /></span><span>{t('usage')}</span><IconArrowRight /></button><button className="shortcut-card" onClick={() => onNavigate('orders')}><span className="shortcut-icon"><IconFile /></span><span>{t('orders')}</span><IconArrowRight /></button></div>}</div>
      </div>
      <div className="panel"><div className="section-heading"><div><h2>{t('latestUsage')}</h2><p className="muted">{t('latestUsageHint')}</p></div>{onNavigate && <Button type="text" onClick={() => onNavigate('usage')}>{t('viewAll')}<IconArrowRight /></Button>}</div>{items(data).length ? <RecordTable rows={items(data)} columns={['model_id', 'endpoint', 'actual_tokens', 'actual_amount', 'state', 'created_at']} actions={row => <Button size="small" onClick={() => setDetail(row)}>{t('details')}</Button>} /> : <EmptyState icon={<IconThunderbolt />} title={t('noUsageYet')} description={t('emptyUsageHint')} action={<Button onClick={() => setEditor(true)}>{t('createKey')}</Button>} />}</div>
    </> : page === 'keys' ? <div className="panel">{searchToolbar}{filteredRows.length ? <RecordTable rows={filteredRows} columns={['name', 'prefix', 'quota_used', 'quota_limit', 'expires_at', 'revoked']} actions={row => <><Button size="small" onClick={() => setDetail(row)}>{t('details')}</Button><Button size="small" status="danger" disabled={Boolean(row.revoked) || actionBusy} onClick={() => feedback.confirm({ title: t('confirmRevoke'), onOk: () => act(async () => { await request('/keys/' + encodeURIComponent(String(row.id)), 'DELETE'); await load(); }) })}>{t('revoke')}</Button></>} /> : rows.length ? noMatches : <EmptyState icon={<IconLock />} title={t('noKeysYet')} description={t('emptyKeysHint')} action={<Button type="primary" icon={<IconPlus />} onClick={() => setEditor(true)}>{t('createKey')}</Button>} />}</div>
      : page === 'usage' ? <div className="panel">{searchToolbar}{filteredRows.length ? <RecordTable rows={filteredRows} columns={['request_id', 'model_id', 'endpoint', 'actual_tokens', 'actual_amount', 'state', 'created_at']} actions={row => <Button size="small" onClick={() => setDetail(row)}>{t('details')}</Button>} /> : rows.length ? noMatches : <EmptyState icon={<IconThunderbolt />} title={t('noUsageYet')} description={t('emptyUsageHint')} action={onNavigate && <Button onClick={() => onNavigate('keys')}>{t('keys')}<IconArrowRight /></Button>} />}</div>
      : page === 'plans' ? <>{!providers.length && <Alert type="info" content={t('paymentUnavailable')} />}<div className="plan-grid">{items(data).map(row => { const modelIDs = stringList(row.model_ids ?? row.model_ids_json); return <div className="panel plan-card" key={String(row.id)}><div className="plan-card-top"><span className="metric-icon" aria-hidden="true"><IconCalendar /></span><Tag>{t('daysPeriod', { days: integerText(row.period_days) })}</Tag></div><h2>{text(row.name)}</h2><div className="plan-price">{money(row.price, String(row.currency))}</div><p className="plan-period muted">{t('planPriceHint')}</p><dl className="plan-facts"><dt>{t('token_quota')}</dt><dd>{integerText(row.token_quota)}</dd><dt>{t('period')}</dt><dd>{t('daysPeriod', { days: integerText(row.period_days) })}</dd></dl><div className="plan-models"><span className="muted">{t('model_ids')}</span><div className="model-tags">{modelIDs.length ? modelIDs.map(id => <Tag key={id}>{id}</Tag>) : <span>{t('allModels')}</span>}</div></div><div className="plan-card-footer"><Button type="primary" long disabled={!providers.length} onClick={() => setCheckout({ kind: 'subscription', plan_id: row.id, name: row.name, amount: row.price, currency: row.currency })}>{t('buyPlan')}<IconArrowRight /></Button><Button type="text" long onClick={() => setDetail(row)}>{t('details')}</Button></div></div>; })}</div>{!items(data).length && <div className="panel"><EmptyState icon={<IconCalendar />} title={t('noPlansYet')} description={t('emptyPlansHint')} action={onNavigate && <Button onClick={() => onNavigate('orders')}>{t('orders')}<IconArrowRight /></Button>} /></div>}</>
      : page === 'orders' ? <><div className="wallet-actions"><div className="panel wallet-action-card"><div className="wallet-action-heading"><span className="metric-icon" aria-hidden="true"><IconSafe /></span><div><h2>{t('topUp')}</h2><p className="muted">{t('topUpHint')}</p></div></div><Button onClick={() => setCheckout({ kind: 'wallet' })} disabled={!providers.length}>{t('selectTopUp')}<IconArrowRight /></Button>{!providers.length && <p className="muted">{t('paymentUnavailable')}</p>}</div><div className="panel wallet-action-card"><div className="wallet-action-heading"><span className="metric-icon" aria-hidden="true"><IconGift /></span><div><h2>{t('redeem')}</h2><p className="muted">{t('redeemHint')}</p></div></div><div className="redeem-form"><Input.Password value={code} onChange={setCode} placeholder={t('code')} aria-label={t('code')} autoComplete="off" /><Button type="primary" loading={actionBusy} disabled={!code.trim()} onClick={() => act(() => request('/redeem', 'POST', { code: code.trim() }), async () => { setCode(''); await load(); feedback.success(t('success')); })}>{t('redeem')}</Button></div></div></div><div className="panel"><div className="section-heading"><div><h2>{t('paymentHistory')}</h2><p className="muted">{t('paymentHistoryHint')}</p></div></div>{searchToolbar}{filteredRows.length ? <RecordTable rows={filteredRows} columns={['id', 'provider', 'kind', 'amount', 'state', 'created_at']} actions={row => <Button size="small" onClick={() => setOrder(row)}>{t('details')}</Button>} /> : rows.length ? noMatches : <EmptyState icon={<IconFile />} title={t('noOrdersYet')} description={t('emptyOrdersHint')} action={<Button onClick={() => setCheckout({ kind: 'wallet' })} disabled={!providers.length}>{t('topUp')}</Button>} />}</div></>
      : page === 'catalog' ? <><div className="panel catalog-toolbar">{searchToolbar}</div>{filteredRows.length ? <div className="catalog-grid">{filteredRows.map(row => <div className="panel model-card" key={String(row.id)}><div className="model-card-top"><div className="model-identity"><span className="model-monogram" aria-hidden="true">{String(row.vendor ?? row.display_name ?? row.id).slice(0, 1).toUpperCase()}</span><div><h2>{text(row.display_name ?? row.id)}</h2><span className="muted">{text(row.vendor)}</span></div></div><Tag color={row.status === 'available' ? 'green' : row.status === 'degraded' ? 'orange' : row.status === 'unavailable' ? 'red' : 'gray'}>{t(String(row.status))}</Tag></div><div className="model-id">{text(row.id)}</div><div className="model-tags">{stringList(row.tasks).map(task => <Tag key={task}>{t(task)}</Tag>)}</div><dl className="model-facts"><dt>{t('context_window')}</dt><dd>{integerText(row.context_window)}</dd><dt>{t('max_output_tokens')}</dt><dd>{integerText(row.max_output_tokens)}</dd></dl><div className="model-card-footer">{row.included_in_plan === true ? <span className="model-plan-note"><IconCalendar />{t('included_in_plan')}</span> : <span className="muted">{t('modelDetailsHint')}</span>}<Button size="small" onClick={() => setDetail(row)}>{t('details')}<IconArrowRight /></Button></div></div>)}</div> : <div className="panel">{rows.length ? noMatches : <EmptyState icon={<IconApps />} title={t('noModelsYet')} description={t('emptyModelsHint')} action={<Button icon={<IconRefresh />} onClick={load}>{t('refresh')}</Button>} />}</div>}</> : null}
    {editor && <Editor title={t('createKey')} fields={keyFields} onCancel={() => setEditor(false)} onSave={async value => { const result = await request('/keys', 'POST', value); setEditor(false); setSecret(String(result.api_key)); await load(); }} />}
    {secret && <Secret value={secret} kind="key" close={() => setSecret('')} />}{detail && <Details row={detail} close={() => setDetail(undefined)} />}
    {checkout && <Modal visible title={t(checkout.kind === 'wallet' ? 'topUp' : 'buyPlan')} onCancel={() => setCheckout(undefined)} onOk={() => act(makeOrder)} confirmLoading={actionBusy} okButtonProps={{ disabled: !providers.length || !provider }} okText={t('pay')} cancelText={t('cancel')}>
      <ErrorBox error={error} /><Alert type="info" content={t('walletHint')} />{!providers.length && <Alert type="warning" content={t('paymentUnavailable')} />}<Form layout="vertical"><Form.Item label={t('provider')}><Select value={provider || undefined} onChange={setProvider} placeholder={t('selectProvider')} options={providers} /></Form.Item>{checkout.kind === 'wallet' ? <Form.Item label={t('amount')}><Input value={amount} onChange={setAmount} inputMode="numeric" /></Form.Item> : <div className="checkout-summary"><span>{text(checkout.name)}</span><strong>{money(checkout.amount, String(checkout.currency))}</strong></div>}</Form>
    </Modal>}
    {order && <Modal visible title={t('orders')} onCancel={() => setOrder(undefined)} footer={<Space><Button icon={<IconRefresh />} loading={actionBusy} onClick={refreshOrder}>{t('refresh')}</Button>{safeHTTPS(order.checkout_url) && <Button type="primary" href={safeHTTPS(order.checkout_url)} target="_blank" rel="noopener noreferrer">{t('openCheckout')}</Button>}</Space>}>
      <dl className="detail-grid"><dt>{t('id')}</dt><dd>{text(order.id)}</dd><dt>{t('amount')}</dt><dd>{money(order.amount, String(order.currency))}</dd><dt>{t('state')}</dt><dd>{t(String(order.state))}</dd><dt>{t('provider')}</dt><dd>{text(order.provider)}</dd><dt>{t('expires_at')}</dt><dd>{text(order.expires_at)}</dd></dl><PaymentQR value={order.checkout_url} />{!safeHTTPS(order.checkout_url) && !String(order.checkout_url).startsWith('weixin://wxpay/') && <Alert type="info" content={t('checkoutUnavailable')} />}
    </Modal>}
  </>;
}
