// SPDX-License-Identifier: Apache-2.0
import { Alert, Button, Modal, Space, Table, Tag } from '@arco-design/web-react';
import { IconCheck, IconCopy } from '@arco-design/web-react/icon';
import { useEffect, useState } from 'react';
import QRCode from 'qrcode';
import { useTranslation } from 'react-i18next';
import { money, text } from './api';
import type { Row } from './api';
import { useFeedback } from './feedback';
import { StateEmpty } from './visuals/state-empty';
import { StateError } from './visuals/state-error';
export function ErrorBox({ error, retry }: { error: string; retry?: () => void }) {
  const { t } = useTranslation();
  return error ? <StateError className="inline-error" title={t('error')} message={t(error)} help={t('errorHelp')} retryLabel={t('retry')} onRetry={retry} /> : null;
}
function compareValues(a: unknown, b: unknown, key: string): number {
  if (a === b) return 0;
  if (a === null || a === undefined) return 1;
  if (b === null || b === undefined) return -1;
  if (typeof a === 'boolean' && typeof b === 'boolean') return Number(a) - Number(b);
  if (key.endsWith('_at')) {
    const aDate = Date.parse(String(a)); const bDate = Date.parse(String(b));
    if (!Number.isNaN(aDate) && !Number.isNaN(bDate)) return aDate - bDate;
  }
  // Compare integer strings as bigint as well: neither sorting nor display rounds int64 IDs or balances.
  if (/^-?\d+$/.test(String(a)) && /^-?\d+$/.test(String(b))) {
    const aInteger = BigInt(String(a)); const bInteger = BigInt(String(b));
    return aInteger < bInteger ? -1 : aInteger > bInteger ? 1 : 0;
  }
  return text(a).localeCompare(text(b), undefined, { numeric: true, sensitivity: 'base' });
}
export function RecordTable({ rows, columns, busy, actions, filtered, onReset }: { rows: Row[]; columns: string[]; busy?: boolean; actions?: (row: Row) => React.ReactNode; filtered?: boolean; onReset?: () => void; }) {
  const { t, i18n } = useTranslation();
  const [fixedActions, setFixedActions] = useState(() => typeof window !== 'undefined' && window.matchMedia('(min-width: 801px)').matches);
  useEffect(() => { const media = window.matchMedia('(min-width: 801px)'); const update = () => setFixedActions(media.matches); media.addEventListener('change', update); return () => media.removeEventListener('change', update); }, []);
  const currencyFields = new Set(['amount', 'balance', 'reserved_balance', 'actual_amount', 'reserved_amount', 'balance_after', 'price']);
  const stateNames: Record<string, string> = { reserved: 'reservedState', failed: 'failedState' };
  const widths = columns.map(key => key === 'base_url' ? 230 : key.endsWith('_at') ? 145 : currencyFields.has(key) ? 150 : key === 'request_id' ? 215 : key === 'model_id' || key === 'id' && rows.some(row => typeof row.id === 'string') ? 175 : key === 'id' || key === 'user_id' || key === 'api_key_id' ? 75 : ['enabled', 'disabled', 'revoked', 'admin', 'priority', 'weight'].includes(key) ? 95 : key === 'name' || key === 'email' ? 185 : 135);
  return <div className="table-wrap"><Table className="record-table" data={rows} loading={busy} border={false} tableLayoutFixed rowKey={row => String(row.id ?? row.request_id ?? text(row))} pagination={rows.length ? { pageSize: 10, sizeCanChange: true, showTotal: true } : false} noDataElement={<StateEmpty compact className="table-empty" title={t(filtered ? 'noSearchResults' : 'empty')} description={t(filtered ? 'adjustFilters' : 'emptyDescription')} actionLabel={filtered && onReset ? t('clearFilters') : undefined} onAction={filtered ? onReset : undefined} />} scroll={{ x: widths.reduce((sum, width) => sum + width, 0) + (actions ? 235 : 0) }} columns={[
    ...columns.map((key, index) => ({ title: t(key === 'action' ? 'actionField' : key), dataIndex: key, width: widths[index], sorter: (a: Row, b: Row) => compareValues(a[key], b[key], key), render: (value: unknown, row: Row) => {
      if (value === null || value === undefined || value === '') return <span className="muted">—</span>;
      if (typeof value === 'boolean') {
        const negative = key === 'disabled' || key === 'revoked';
        const label = key === 'enabled' ? value ? 'enabled' : 'disabled' : key === 'disabled' ? value ? 'disabled' : 'enabled' : key === 'revoked' ? value ? 'revoked' : 'active' : value ? 'booleanTrue' : 'booleanFalse';
        const positive = negative ? !value : value;
        return <Tag className={`status-tag ${positive ? 'status-success' : 'status-neutral'}`} color={positive ? 'green' : 'gray'}>{t(label)}</Tag>;
      }
      if (key === 'status' || key === 'state') {
        const state = String(value); const tone = ['active', 'available', 'paid', 'settled'].includes(state) ? 'success' : ['failed', 'unavailable'].includes(state) ? 'danger' : ['pending', 'reserved', 'reconciliation', 'preview', 'degraded'].includes(state) ? 'warning' : 'neutral';
        return <Tag className={`status-tag status-${tone}`} color={tone === 'success' ? 'green' : tone === 'danger' ? 'red' : tone === 'warning' ? 'orange' : 'gray'}>{t(stateNames[state] ?? state)}</Tag>;
      }
      if (currencyFields.has(key)) return <span className="table-cell-money">{money(value, String(row.currency ?? 'USD'))}</span>;
      if (key.endsWith('_at')) {
        const date = new Date(String(value));
        if (!Number.isNaN(date.getTime())) return <span className="table-cell-date" title={date.toLocaleString(i18n.resolvedLanguage)}>{date.toLocaleDateString(i18n.resolvedLanguage)}<small>{date.toLocaleTimeString(i18n.resolvedLanguage, { hour: '2-digit', minute: '2-digit' })}</small></span>;
      }
      if (Array.isArray(value)) return value.length ? <div className="table-tags" title={value.map(text).join(', ')}>{value.slice(0, 3).map((item, index) => <Tag key={index}>{text(item)}</Tag>)}{value.length > 3 && <Tag>+{value.length - 3}</Tag>}</div> : <span className="muted">—</span>;
      const output = text(value); return <span className={key === 'id' || key.endsWith('_id') || key === 'prefix' ? 'table-cell-id' : 'table-cell-text'} title={output}>{output.length > 90 ? output.slice(0, 88) + '…' : output}</span>;
    }})),
    ...(actions ? [{ title: t('action'), width: 235, fixed: fixedActions ? 'right' as const : undefined, render: (_: unknown, row: Row) => <Space className="row-actions" size={6}>{actions(row)}</Space> }] : [])
  ]} /></div>;
}
export function Secret({ value, kind, close }: { value: string; kind: 'key' | 'codes'; close: () => void }) {
  const { t } = useTranslation(); const feedback = useFeedback(); const [copied, setCopied] = useState(false);
  const copy = async () => { try { await navigator.clipboard.writeText(value); } catch { feedback.warning(t('copyFailed')); return; } setCopied(true); feedback.success(t('copied')); };
  return <Modal className="secret-modal" title={t('once')} visible onCancel={close} maskClosable={false} footer={<Space><Button onClick={close}>{t('done')}</Button><Button type="primary" icon={copied ? <IconCheck /> : <IconCopy />} onClick={copy}>{t(copied ? 'copied' : 'copy')}</Button></Space>} style={{ width: 'min(650px, calc(100vw - 32px))' }}><Alert type="warning" content={t(kind === 'key' ? 'keyOnce' : 'codesOnce')} /><div className="secret-content"><div className="secret-content-label">{t(kind === 'key' ? 'apiKeyValue' : 'redeemcodes')}</div><pre className="secret secret-value" tabIndex={0}>{value}</pre></div></Modal>;
}
export function Details({ row, close }: { row: Row; close: () => void }) {
  const { t } = useTranslation();
  return <Modal className="details-modal" title={t('details')} visible onCancel={close} footer={<Button onClick={close}>{t('done')}</Button>} style={{ width: 'min(760px, calc(100vw - 32px))' }}><dl className="detail-grid details-values">{Object.entries(row).filter(([key]) => !key.includes('secret') && !key.includes('hash') && !key.includes('encrypted')).map(([key, value]) => <div className="detail-item" key={key}><dt>{t(key === 'action' ? 'actionField' : key)}</dt><dd>{typeof value === 'boolean' ? <Tag color={value ? 'green' : 'gray'}>{t(value ? 'booleanTrue' : 'booleanFalse')}</Tag> : typeof value === 'object' && value !== null ? <pre className="detail-json">{text(value)}</pre> : <span className={key === 'id' || key.endsWith('_id') ? 'table-cell-id' : ''}>{text(value)}</span>}</dd></div>)}</dl></Modal>;
}
export function PaymentQR({ value }: { value: unknown }) {
  const { t } = useTranslation(); const [source, setSource] = useState('');
  const code = typeof value === 'string' && (value.startsWith('weixin://wxpay/') || value.startsWith('https://qr.alipay.com/')) ? value : '';
  useEffect(() => { let active = true; setSource(''); if (code) void QRCode.toDataURL(code, { width: 240, margin: 2 }).then(value => { if (active) setSource(value); }); return () => { active = false; }; }, [code]);
  return source ? <div style={{ textAlign: 'center' }}><img src={source} width={240} height={240} alt={t('qrCode')} /><p className="muted">{t('scanPayment')}</p></div> : null;
}
