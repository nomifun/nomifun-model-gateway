// SPDX-License-Identifier: Apache-2.0
import { useId, useState } from 'react';
import { Alert, Button, Input, Modal, Space } from '@arco-design/web-react';
import { useTranslation } from 'react-i18next';
import { exactJSON, integer, text } from './api';
import type { Row } from './api';
export type Field = { key: string; label?: string; kind?: 'text' | 'password' | 'secret' | 'int' | 'csv' | 'json' | 'boolean' | 'select' | 'date' | 'https'; required?: boolean; hint?: string; options?: { label: string; value: string }[]; initial?: unknown; wide?: boolean; min?: bigint; advanced?: boolean; };
export function fieldValue(field: Field, value: unknown): unknown {
  if (field.kind === 'boolean') return Boolean(value);
  const input = String(value ?? '').trim();
  if (!input) {
    if (field.required) throw new Error('required');
    if (field.kind === 'csv') return [];
    if (field.kind === 'int' || field.kind === 'date' || field.kind === 'https') return null;
    if (field.kind === 'password' || field.kind === 'secret') return undefined;
    return field.kind === 'json' ? null : '';
  }
  if (field.kind === 'int') return integer(input, field.min ?? 0n);
  if (field.kind === 'csv') return [...new Set(input.split(',').map(x => x.trim()).filter(Boolean))];
  if (field.kind === 'json') { try { return exactJSON.parse(input); } catch { throw new Error('invalidJSON'); } }
  if (field.kind === 'date') { if (Number.isNaN(Date.parse(input))) throw new Error('expiryHint'); return new Date(input).toISOString(); }
  if (field.kind === 'https') { try { const url = new URL(input); if (url.protocol !== 'https:' || url.username || url.password) throw new Error('httpsHint'); } catch { throw new Error('httpsHint'); } }
  return input;
}
function initialValue(field: Field, value: unknown) {
  const current = value ?? field.initial;
  if (field.kind === 'boolean') return Boolean(current);
  if (field.kind === 'csv') {
    if (typeof current === 'string' && current.startsWith('[')) { try { return (exactJSON.parse(current) as unknown[]).join(', '); } catch { return current; } }
    return Array.isArray(current) ? current.join(', ') : String(current ?? '');
  }
  if (field.kind === 'json') return current ? typeof current === 'string' ? current : exactJSON.stringify(current, null, 2) : '';
  if (field.kind === 'password' || field.kind === 'secret') return '';
  return current === null || current === undefined ? '' : text(current);
}
export function Editor({ title, fields, value = {}, hint, onCancel, onSave }: { title: string; fields: Field[]; value?: Row; hint?: string; onCancel: () => void; onSave: (value: Row) => Promise<void>; }) {
  const { t } = useTranslation();
  const editorId = useId();
  const [values, setValues] = useState<Row>(() => Object.fromEntries(fields.map(field => [field.key, initialValue(field, value[field.key])])));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const [advancedOpen, setAdvancedOpen] = useState(false);
  // Required fields always remain visible, even if future definitions mark them advanced.
  const basicFields = fields.filter(field => !field.advanced || field.required);
  const advancedFields = fields.filter(field => field.advanced && !field.required);
  const message = (error: unknown) => t(error instanceof Error ? error.message : 'failed');
  const validate = (field: Field, input: unknown) => {
    try { fieldValue(field, input); setFieldErrors(old => { const next = { ...old }; delete next[field.key]; return next; }); }
    catch (error) { setFieldErrors(old => ({ ...old, [field.key]: message(error) })); }
  };
  const update = (field: Field, input: unknown) => {
    setValues(old => ({ ...old, [field.key]: input }));
    if (fieldErrors[field.key]) validate(field, input);
  };
  const save = async () => {
    if (busy) return;
    setError('');
    const result: Row = {};
    const errors: Record<string, string> = {};
    for (const field of fields) {
      try { const parsed = fieldValue(field, values[field.key]); if (parsed !== undefined) result[field.key] = parsed; }
      catch (error) { errors[field.key] = message(error); }
    }
    setFieldErrors(errors);
    if (Object.keys(errors).length) {
      if (advancedFields.some(field => errors[field.key])) setAdvancedOpen(true);
      requestAnimationFrame(() => {
        const first = fields.find(field => errors[field.key]);
        const input = first && document.getElementById(`${editorId}-${first.key}`);
        input?.focus(); input?.scrollIntoView({ block: 'nearest', behavior: window.matchMedia('(prefers-reduced-motion: reduce)').matches ? 'auto' : 'smooth' });
      });
      return;
    }
    setBusy(true);
    try {
      await onSave(result);
    } catch (error) { setError(message(error)); } finally { setBusy(false); }
  };
  const renderFields = (section: Field[]) => <div className="form-grid">{section.map(field => {
    const id = `${editorId}-${field.key}`;
    const helpId = `${id}-hint`; const errorId = `${id}-error`;
    const error = fieldErrors[field.key];
    const accessibility = { id, disabled: busy, 'aria-required': field.required || undefined, 'aria-invalid': !!error, 'aria-describedby': [field.hint && helpId, error && errorId].filter(Boolean).join(' ') || undefined };
    return <div key={field.key} className={`editor-field${field.wide ? ' wide' : ''}${error ? ' has-error' : ''}`}>
      {field.kind === 'boolean' ? <label className="editor-boolean" htmlFor={id}><input {...accessibility} type="checkbox" checked={Boolean(values[field.key])} onChange={event => update(field, event.target.checked)} /><span>{t(field.label ?? field.key)}</span></label> : <>
        <label className="editor-label" htmlFor={id}>{t(field.label ?? field.key)}{field.required && <span className="required-mark" aria-hidden="true">*</span>}</label>
        {field.kind === 'select' ? <select {...accessibility} className="editor-select" value={String(values[field.key] ?? '')} onBlur={() => validate(field, values[field.key])} onChange={event => update(field, event.target.value)}><option value="" disabled>{t('select')}</option>{field.options?.map(item => <option key={item.value} value={item.value}>{t(item.label)}</option>)}</select>
          : field.kind === 'password' ? <Input.Password {...accessibility} status={error ? 'error' : undefined} autoComplete="new-password" value={String(values[field.key] ?? '')} onBlur={() => validate(field, values[field.key])} onChange={input => update(field, input)} />
          : field.kind === 'json' || field.kind === 'secret' ? <Input.TextArea {...accessibility} status={error ? 'error' : undefined} autoComplete="off" spellCheck={false} className="json-editor" autoSize={{ minRows: field.kind === 'json' ? 5 : 3, maxRows: 12 }} value={String(values[field.key] ?? '')} onBlur={() => validate(field, values[field.key])} onChange={input => update(field, input)} />
          : <Input {...accessibility} status={error ? 'error' : undefined} autoComplete="off" inputMode={field.kind === 'int' ? 'numeric' : undefined} value={String(values[field.key] ?? '')} onBlur={() => validate(field, values[field.key])} onChange={input => update(field, input)} />}
      </>}
      {field.hint && <div id={helpId} className="field-hint">{t(field.hint)}</div>}
      {error && <div id={errorId} role="alert" className="field-error">{error}</div>}
    </div>;
  })}</div>;
  return <Modal className="editor-modal" title={title} visible onCancel={onCancel} maskClosable={!busy} escToExit={!busy} closable={!busy} footer={<Space><Button disabled={busy} onClick={onCancel}>{t('cancel')}</Button><Button type="primary" htmlType="submit" form={editorId} loading={busy}>{t('save')}</Button></Space>} style={{ width: 'min(760px, calc(100vw - 32px))' }} unmountOnExit>
    {hint && <Alert className="editor-intro" type="info" content={t(hint)} />}{error && <Alert className="editor-intro" type="error" content={error} />}
    <form id={editorId} className="editor-form" noValidate onSubmit={event => { event.preventDefault(); void save(); }}>
      <section className="editor-section">{advancedFields.length > 0 && <h3 className="editor-section-heading">{t('basicSettings')}</h3>}{renderFields(basicFields)}</section>
      {advancedFields.length > 0 && <details className="editor-advanced" open={advancedOpen} onToggle={event => setAdvancedOpen(event.currentTarget.open)}><summary>{t('advancedSettings')}<span className="muted">{t('optionalFields')}</span></summary>{renderFields(advancedFields)}</details>}
    </form>
  </Modal>;
}
export const keyFields: Field[] = [
  { key: 'name', required: true }, { key: 'expires_at', kind: 'date', hint: 'expiryHint' },
  { key: 'quota_limit', kind: 'int', hint: 'unlimitedHint', advanced: true }, { key: 'model_ids', kind: 'csv', hint: 'csvHint', advanced: true },
  { key: 'allowed_ips', kind: 'csv', hint: 'csvHint', advanced: true }, { key: 'requests_per_minute', kind: 'int', hint: 'unlimitedHint', advanced: true },
  { key: 'tokens_per_minute', kind: 'int', hint: 'unlimitedHint', advanced: true }, { key: 'concurrent_requests', kind: 'int', hint: 'unlimitedHint', advanced: true }
];
export const channelFields: Field[] = [
  { key: 'name', required: true }, { key: 'kind', kind: 'select', required: true, options: ['openai', 'anthropic', 'gemini', 'compatible', 'azure'].map(value => ({ value, label: value })), initial: 'openai' },
  { key: 'base_url', required: true }, { key: 'api_key', kind: 'password', hint: 'secretHint' },
  { key: 'model_ids', label: 'modelMap', kind: 'json', required: true, wide: true },
  { key: 'endpoints', kind: 'csv', hint: 'endpointsHint', required: true, wide: true },
  { key: 'priority', kind: 'int', initial: 0, advanced: true }, { key: 'weight', kind: 'int', min: 1n, initial: 1, advanced: true }, { key: 'api_version', advanced: true }, { key: 'enabled', kind: 'boolean', initial: true }
];
export const modelFields: Field[] = [
  { key: 'id', required: true }, { key: 'display_name', required: true }, { key: 'vendor', required: true },
  { key: 'status', kind: 'select', initial: 'available', required: true, options: ['available', 'degraded', 'unavailable'].map(value => ({ value, label: value })) },
  { key: 'tasks', kind: 'csv', required: true }, { key: 'input_modalities', kind: 'csv', initial: ['text'], advanced: true }, { key: 'context_window', kind: 'int', advanced: true }, { key: 'max_output_tokens', kind: 'int' },
  { key: 'task_endpoints', kind: 'json', wide: true, required: true }, { key: 'pricing', kind: 'json', wide: true, required: true }, { key: 'traits', kind: 'csv', advanced: true }, { key: 'enabled', kind: 'boolean', initial: true }, { key: 'subscription_only', kind: 'boolean' }
];
export const planFields: Field[] = [
  { key: 'name', required: true }, { key: 'price', kind: 'int', required: true }, { key: 'currency', initial: 'USD', required: true }, { key: 'period_days', kind: 'int', initial: 30, min: 1n, required: true },
  { key: 'token_quota', kind: 'int', hint: 'unlimitedHint', advanced: true }, { key: 'model_ids', kind: 'csv', hint: 'csvHint', advanced: true }, { key: 'enabled', kind: 'boolean', initial: true }
];
export const settingFields: Field[] = [
  { key: 'name', label: 'operatorName', required: true },
  { key: 'currency', required: true, hint: 'currencyHint', initial: 'USD' },
  ...['homepage_url', 'console_url', 'purchase_url', 'terms_url', 'privacy_url'].map(key => ({ key, kind: 'https' as const, hint: 'httpsHint' })),
  { key: 'registration_enabled', kind: 'boolean' }, { key: 'capabilities', kind: 'csv', hint: 'endpointsHint', advanced: true }, { key: 'optional_endpoints', kind: 'csv', advanced: true }
];
