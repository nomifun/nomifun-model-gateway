// SPDX-License-Identifier: Apache-2.0
import { useRef, useState } from 'react';
import { Alert, Button, Modal, Space, Tag } from '@arco-design/web-react';
import { useTranslation } from 'react-i18next';
import { APIError, request } from './api';
import type { Row } from './api';
import { CatalogIssueList, ChannelFieldsEditor, ModelFieldsEditor } from './CatalogEditors';
import { normalizeChannelDraft, normalizeModelDraft } from './catalog-form';
import type { CatalogIssue } from './catalog-form';
import { providerPresets, findProviderPreset } from './provider-presets';
import { addDiscoveredMappings, channelSubmission, mappingPreview, onboardingWords, synchronizeModelDrafts, writeOutcomeUnknown } from './onboarding';
import type { DiscoveryResult, OnboardingWord, PublishCheck } from './onboarding';
import './onboarding.css';
function useWords() { const { i18n } = useTranslation(); return (key: OnboardingWord) => onboardingWords[key][i18n.language.startsWith('zh') ? 1 : 0]; }
function Issues({ check }: { check?: PublishCheck }) { const label = useWords(); return check ? <div aria-live="polite">{check.ready ? <Alert type="success" content={label('ready')} /> : <><Alert type="warning" content={label('pending')} /><ul className="publication-issues">{check.issues.map((issue, index) => <li key={index}><strong>{issue.model_id ? `${issue.model_id} · ` : ''}{issue.field}</strong>: {issue.message}</li>)}</ul></>}</div> : null; }
export function CatalogEditorModal({ kind, value, editing, onCancel, onSaved }: { kind: 'channel' | 'model'; value: Row; editing: boolean; onCancel: () => void; onSaved: () => Promise<void> }) {
  const label = useWords(); const { t } = useTranslation();
  const [draft, setDraft] = useState(value); const [busy, setBusy] = useState(false); const lock = useRef(false); const [error, setError] = useState(''); const [check, setCheck] = useState<PublishCheck>(); const [uncertain, setUncertain] = useState(false);
  const [fieldIssues, setFieldIssues] = useState<CatalogIssue[]>([]);
  const update = (next: Row) => { setDraft(next); setCheck(undefined); setFieldIssues([]); setError(''); };
  const perform = async (save: boolean) => {
    if (lock.current || uncertain) return; lock.current = true; setBusy(true); setError('');
    let dispatch = false; let wrote = false;
    try {
      const normalized = kind === 'channel' ? normalizeChannelDraft(draft, editing) : normalizeModelDraft(draft);
      setFieldIssues(normalized.issues); if (normalized.issues.length) { requestAnimationFrame(() => document.querySelector<HTMLElement>('.catalog-modal [aria-invalid="true"]')?.focus()); return; }
      if (!save && kind === 'model') { setCheck(await request<PublishCheck>('/admin/models/check', 'POST', normalized.value)); return; }
      const path = kind === 'channel' ? `/admin/channels/${encodeURIComponent(String(value.id))}` : `/admin/models${editing ? '/' + encodeURIComponent(String(value.id)) : ''}`;
      dispatch = true; await request(path, editing ? 'PATCH' : 'POST', kind === 'channel' ? channelSubmission(normalized.value) : normalized.value); wrote = true; onCancel(); await onSaved();
    } catch (error) { if (error instanceof APIError && Array.isArray(error.details?.issues)) setCheck(error.details as unknown as PublishCheck); if (save && dispatch && !wrote && writeOutcomeUnknown(error)) { setUncertain(true); setError(label('ambiguous')); } else setError(error instanceof Error ? error.message : 'failed'); }
    finally { lock.current = false; setBusy(false); }
  };
  return <Modal visible title={`${t(editing ? 'edit' : 'create')} · ${t(kind === 'channel' ? 'channels' : 'models')}`} className="catalog-modal" style={{ width: 'min(1000px, calc(100vw - 24px))' }} onCancel={onCancel} maskClosable={!busy} escToExit={!busy} closable={!busy} footer={<Space><Button disabled={busy} onClick={onCancel}>{label('cancel')}</Button>{kind === 'model' && <Button loading={busy} disabled={uncertain} onClick={() => void perform(false)}>{label('check')}</Button>}<Button type="primary" loading={busy} disabled={uncertain} onClick={() => void perform(true)}>{t('save')}</Button></Space>}>
    {kind === 'channel' && editing && <Alert type="info" content={label('immutable')} />}{error && <Alert type="error" content={error} />}
    {kind === 'channel' ? <ChannelFieldsEditor value={draft} onChange={update} disabled={busy || uncertain} editing={editing} issues={fieldIssues} /> : <ModelFieldsEditor value={draft} onChange={update} disabled={busy || uncertain} editing={editing} issues={fieldIssues} />}<CatalogIssueList issues={fieldIssues} /><Issues check={check} />
  </Modal>;
}
export function ProviderOnboarding({ catalog, onCancel, onSaved }: { catalog: Row[]; onCancel: () => void; onSaved: () => Promise<void> }) {
  const label = useWords(); const [step, setStep] = useState(0); const [presetID, setPresetID] = useState('custom');
  const [channel, setChannel] = useState<Row>({ name: '', kind: 'compatible', base_url: '', api_key: '', model_ids: {}, endpoints: ['openai'], priority: 0, weight: 1, enabled: true, api_version: '' });
  const [models, setModels] = useState<Row[]>([]); const [discovery, setDiscovery] = useState<DiscoveryResult>(); const [selected, setSelected] = useState<string[]>([]); const [check, setCheck] = useState<PublishCheck>();
  const [error, setError] = useState(''); const [busy, setBusy] = useState(false); const lock = useRef(false); const [uncertain, setUncertain] = useState(false);
  const [channelIssues, setChannelIssues] = useState<CatalogIssue[]>([]); const [modelIssues, setModelIssues] = useState<CatalogIssue[][]>([]);
  const preset = findProviderPreset(presetID); const resetCheck = () => { setCheck(undefined); setError(''); };
  const updateChannel = (value: Row) => { setChannel(value); setChannelIssues([]); resetCheck(); };
  const selectPreset = (id: string) => { const next = findProviderPreset(id); setPresetID(id); setDiscovery(undefined); setSelected([]); if (next) updateChannel({ ...channel, name: next.name, kind: next.kind, base_url: next.base_url, api_version: next.api_version ?? '', endpoints: next.endpoints }); };
  const read = async () => {
    if (lock.current) return; lock.current = true; setBusy(true); setError(''); setDiscovery(undefined); setSelected([]);
    try { setDiscovery(await request<DiscoveryResult>('/admin/channels/discover-preview', 'POST', { kind: channel.kind, base_url: channel.base_url, api_key: channel.api_key, api_version: channel.api_version })); }
    catch (error) { setError(error instanceof Error ? error.message : 'failed'); } finally { lock.current = false; setBusy(false); }
  };
  const prepare = () => {
    const normalized = normalizeChannelDraft(channel);
    setChannelIssues(normalized.issues);
    if (normalized.issues.length) { requestAnimationFrame(() => document.querySelector<HTMLElement>('.catalog-modal [aria-invalid="true"]')?.focus()); return false; }
    if (Object.keys(normalized.value.model_ids as object).some(id => catalog.some(row => row.id === id))) { setError(label('idConflict')); return false; }
    setChannel(normalized.value); setModels(synchronizeModelDrafts(normalized.value, models, preset?.name ?? String(channel.name), discovery?.models ?? [])); return true;
  };
  const next = () => { resetCheck(); if (step === 1 && !prepare()) return; if (step === 2) { const draftModels = models.map(normalizeModelDraft); setModelIssues(draftModels.map(item => item.issues)); if (draftModels.some(item => item.issues.length)) { requestAnimationFrame(() => { document.querySelectorAll<HTMLDetailsElement>('.onboarding-model').forEach(item => item.open = true); document.querySelector<HTMLElement>('.catalog-modal [aria-invalid="true"]')?.focus(); }); return; } } setStep(step + 1); };
  const payload = () => { const normalized = normalizeChannelDraft(channel); const draftModels = models.map(normalizeModelDraft); setChannelIssues(normalized.issues); setModelIssues(draftModels.map(item => item.issues)); const issues = [...normalized.issues, ...draftModels.flatMap(item => item.issues)]; if (issues.length) throw new Error(issues.map(issue => `${issue.path}: ${issue.code}`).join('\n')); return { channel: channelSubmission(normalized.value), models: draftModels.map(item => item.value) }; };
  const perform = async (save: boolean) => {
    if (lock.current || uncertain) return; lock.current = true; setBusy(true); setError(''); let dispatch = false; let wrote = false;
    try { const body = payload(); if (!save) { setCheck(await request<PublishCheck>('/admin/onboarding/check', 'POST', body)); return; } dispatch = true; await request('/admin/onboarding', 'POST', body); wrote = true; onCancel(); await onSaved(); }
    catch (error) { if (error instanceof APIError && Array.isArray(error.details?.issues)) setCheck(error.details as unknown as PublishCheck); if (save && dispatch && !wrote && writeOutcomeUnknown(error)) { setUncertain(true); setError(label('ambiguous')); } else setError(error instanceof Error ? error.message : 'failed'); }
    finally { lock.current = false; setBusy(false); }
  };
  const steps = ['channel', 'discover', 'models', 'review'] as const;
  return <Modal visible title={label('title')} className="catalog-modal" style={{ width: 'min(1080px, calc(100vw - 24px))' }} onCancel={onCancel} maskClosable={!busy} escToExit={!busy} closable={!busy} footer={<Space><Button disabled={busy} onClick={onCancel}>{label('cancel')}</Button>{step > 0 && <Button disabled={busy || uncertain} onClick={() => { resetCheck(); setStep(step - 1); }}>{label('back')}</Button>}{step < 3 ? <Button type="primary" disabled={busy} onClick={next}>{label('next')}</Button> : <><Button loading={busy} disabled={uncertain} onClick={() => void perform(false)}>{label('check')}</Button><Button type="primary" loading={busy} disabled={uncertain || !check} onClick={() => void perform(true)}>{label('save')}</Button></>}</Space>}>
    <ol className="onboarding-steps">{steps.map((name, index) => <li key={name} aria-current={index === step ? 'step' : undefined}>{index + 1}. {label(name)}</li>)}</ol>
    {error && <Alert type="error" content={error} />}
    {step === 0 && <><Alert type="info" content={label('templateOnly')} /><label className="preset-choice">{label('preset')}<select disabled={busy} value={presetID} onChange={event => selectPreset(event.target.value)}>{providerPresets.map(item => <option key={item.id} value={item.id}>{item.name}</option>)}</select></label>{preset && <div className="preset-notes">{preset.notes.map(note => <p key={note}>{note}</p>)}<div>{preset.official_docs.map(doc => <a key={doc.url} href={doc.url} target="_blank" rel="noopener noreferrer">{doc.title}</a>)}</div></div>}<ChannelFieldsEditor value={channel} onChange={updateChannel} disabled={busy} issues={channelIssues} /></>}
    {step === 1 && <><Alert type="info" content={label('manual')} /><Button loading={busy} disabled={presetID !== 'custom' && preset?.discovery === 'manual'} onClick={() => void read()}>{label('read')}</Button>{discovery && <><p>{discovery.pages} pages · {discovery.models.length} models</p>{!discovery.complete && <Alert type="warning" content={label('partial')} />}{discovery.warnings.map((warning, index) => <Alert key={index} type="warning" content={warning} />)}{discovery.models.length ? <div className="discovery-results">{mappingPreview(channel.model_ids as Record<string, string>, discovery.models, catalog).map(model => <label key={model.id}><input type="checkbox" disabled={busy || model.state !== 'new'} checked={selected.includes(model.id)} onChange={event => setSelected(old => event.target.checked ? [...old, model.id] : old.filter(id => id !== model.id))} /><span>{model.id}{model.display_name && ` · ${model.display_name}`}</span><Tag>{label(model.state as OnboardingWord)}</Tag></label>)}</div> : <p>{label('empty')}</p>}<Button disabled={busy || !selected.length} onClick={() => { updateChannel(addDiscoveredMappings(channel, discovery.models.filter(model => selected.includes(model.id)))); setSelected([]); }}>{label('apply')}</Button></>}<ChannelFieldsEditor value={channel} onChange={updateChannel} disabled={busy} issues={channelIssues} /><Alert type="info" content={label('unknown')} /></>}
    {step === 2 && <><Alert type="warning" content={label('pending')} />{models.map((model, index) => <details key={String(model.id)} className="onboarding-model" open={models.length === 1}><summary>{String(model.id)} · {model.enabled ? 'enabled' : 'disabled'}</summary><ModelFieldsEditor value={model} editing issues={modelIssues[index] ?? []} onChange={value => { setModels(old => old.map((item, i) => i === index ? value : item)); resetCheck(); }} disabled={busy} /></details>)}</>}
    {step === 3 && <><Alert type="info" content={label('checkBeforeSave')} /><dl className="onboarding-summary"><dt>{label('channelSummary')}</dt><dd>{String(channel.name)} · {String(channel.kind)} · {String(channel.base_url)}</dd>{models.map(model => <div key={String(model.id)}><dt>{String(model.id)}</dt><dd>{model.enabled ? 'enabled' : 'disabled'} · {String((channel.model_ids as Row)[String(model.id)])}</dd></div>)}</dl><Issues check={check} /></>}
  </Modal>;
}
export function SavedDiscoveryModal({ channel, catalog, onCancel, onSaved }: { channel: Row; catalog: Row[]; onCancel: () => void; onSaved: () => Promise<void> }) {
  const label = useWords(); const [result, setResult] = useState<DiscoveryResult>(); const [draft, setDraft] = useState(channel); const [busy, setBusy] = useState(false); const lock = useRef(false); const [error, setError] = useState(''); const [selected, setSelected] = useState<string[]>([]);
  const read = async () => { if (lock.current) return; lock.current = true; setBusy(true); setError(''); setResult(undefined); setSelected([]); try { setResult(await request<DiscoveryResult>(`/admin/channels/${encodeURIComponent(String(channel.id))}/discover`, 'POST')); } catch (error) { setError(error instanceof Error ? error.message : 'failed'); } finally { lock.current = false; setBusy(false); } };
  const [uncertain, setUncertain] = useState(false);
  const save = async () => { if (lock.current || uncertain) return; lock.current = true; setBusy(true); setError(''); let dispatch = false; let wrote = false; try { const normalized = normalizeChannelDraft(draft, true); if (normalized.issues.length) throw new Error(normalized.issues.map(issue => `${issue.path}: ${issue.code}`).join('\n')); dispatch = true; await request(`/admin/channels/${encodeURIComponent(String(channel.id))}`, 'PATCH', channelSubmission(normalized.value)); wrote = true; onCancel(); await onSaved(); } catch (error) { if (dispatch && !wrote && writeOutcomeUnknown(error)) { setUncertain(true); setError(label('ambiguous')); } else setError(error instanceof Error ? error.message : 'failed'); } finally { lock.current = false; setBusy(false); } };
  return <Modal visible title={label('savedDiscovery')} className="catalog-modal" style={{ width: 'min(1000px, calc(100vw - 24px))' }} onCancel={onCancel} maskClosable={!busy} closable={!busy} escToExit={!busy} footer={<Space><Button disabled={busy} onClick={onCancel}>{label('cancel')}</Button><Button type="primary" disabled={uncertain} loading={busy} onClick={() => void save()}>{label('save')}</Button></Space>}><Alert type="info" content={label('manual')} />{error && <Alert type="error" content={error} />}<Button loading={busy} onClick={() => void read()}>{label('read')}</Button>{result && <>{!result.complete && <Alert type="warning" content={label('partial')} />}{result.warnings.map((warning, i) => <Alert key={i} type="warning" content={warning} />)}<div className="discovery-results">{mappingPreview(draft.model_ids as Record<string, string>, result.models, catalog).map(model => <label key={model.id}><input type="checkbox" disabled={busy || uncertain || ['changed', 'unchanged'].includes(model.state)} checked={selected.includes(model.id)} onChange={event => setSelected(old => event.target.checked ? [...old, model.id] : old.filter(id => id !== model.id))} /><span>{model.id}</span><Tag>{label(model.state as OnboardingWord)}</Tag></label>)}</div><Button disabled={!selected.length || busy || uncertain} onClick={() => { setDraft(addDiscoveredMappings(draft, result.models.filter(model => selected.includes(model.id)))); setSelected([]); }}>{label('apply')}</Button></>}<ChannelFieldsEditor value={draft} onChange={setDraft} editing disabled={busy || uncertain} /><Alert type="warning" content={label('unknown')} /></Modal>;
}
