// SPDX-License-Identifier: Apache-2.0
import { useEffect, useId, useState } from 'react';
import type { ChangeEvent } from 'react';
import { useTranslation } from 'react-i18next';
import { exactJSON } from './api';
import type { Row } from './api';
import { CHANNEL_KINDS, ENDPOINTS, mappingRows, PRICE_METERS, record, stringList, TASK_ENDPOINTS } from './catalog-form';
import type { CatalogIssue, MappingRow } from './catalog-form';
import './catalog-editors.css';

const labels = {
  id: ['Public model ID', '公开模型 ID'], display_name: ['Display name', '显示名称'], vendor: ['Vendor', '供应商品牌'], status: ['Catalog status', '目录状态'],
  name: ['Channel name', '渠道名称'], kind: ['Protocol / authentication type', '协议 / 认证类型'], base_url: ['Upstream base URL', '上游基础地址'], api_key: ['Upstream API key', '上游 API Key'], api_version: ['API version', 'API 版本'],
  context_window: ['Context window (tokens)', '上下文窗口（token）'], max_output_tokens: ['Maximum output tokens', '最大输出 token'], input_modalities: ['Input modalities', '输入模态'], traits: ['Catalog traits', '目录特征'],
  enabled: ['Enabled', '启用'], subscription_only: ['Subscription access only', '仅允许套餐访问'], available: ['Available', '可用'], degraded: ['Degraded', '降级'], unavailable: ['Unavailable', '不可用'],
  tasks: ['Tasks and native endpoints', '任务与原生端点'], chat: ['Chat', '对话'], image_generation: ['Image generation', '图像生成'], image_edit: ['Image editing', '图像编辑'], embedding: ['Embedding', '向量'], rerank: ['Reranking', '重排'],
  addTask: ['Add task', '添加任务'], remove: ['Remove', '移除'], preferred: ['Preferred endpoint', '首选端点'], select: ['Select…', '请选择…'],
  routeHint: ['Select only endpoints confirmed for this upstream model. A catalog declaration does not verify native behavior.', '仅选择已确认适用于该上游模型的端点。目录声明不能替代原生调用验收。'],
  limitHint: ['Leave unknown limits blank. Anthropic as the preferred endpoint requires a confirmed output limit.', '未知限额保持留空。以 Anthropic 为首选端点时，必须填写已确认的输出限额。'],
  descriptorHint: ['Comma-separated catalog descriptions. These fields do not enable function calling, reasoning or streaming.', '用逗号分隔目录说明。这些字段不会开启工具调用、推理或流式能力。'],
  prices: ['Task pricing', '按任务配置售价'], pricingHint: ['Amount is an integer in minor currency units per unit size. A blank or missing price is unconfigured; enter 0 only to explicitly offer free usage.', '金额是每个计量基数对应的整数最小货币单位。空白或缺少价格表示待配置；仅在明确免费时填写 0。'],
  pricingEmpty: ['No prices configured. This model must stay disabled until pricing and publication checks pass.', '尚未配置售价。完成售价与发布检查前，此模型应保持禁用。'],
  task: ['Task', '任务'], meter: ['Meter', '计量单位'], unit_size: ['Unit size', '计量基数'], amount: ['Amount (minor units)', '金额（最小货币单位）'], currency: ['Currency', '币种'], addPrice: ['Add price', '添加售价'],
  mapping: ['Public model → upstream model', '公开模型 → 上游模型'], publicID: ['Public model ID', '公开模型 ID'], upstreamID: ['Upstream model ID / deployment', '上游模型 ID / 部署名'], addMapping: ['Add mapping', '添加映射'], mappingEmpty: ['Add at least one public-to-upstream model mapping.', '至少添加一条公开模型到上游模型的映射。'],
  endpoints: ['Declared native endpoints', '声明的原生端点'], channelEndpointHint: ['Endpoints must match the protocol type and the actual upstream API. Confirm model-specific support before publishing.', '端点需与协议类型及真实上游 API 一致。发布前确认每个模型的实际支持情况。'],
  priority: ['Priority', '优先级'], weight: ['Weight', '权重'], identityHint: ['Protocol type, upstream URL, API version and credential are account identity. Create a new channel to change them.', '协议类型、上游地址、API 版本和凭据是账号身份。变更时请创建新渠道。'],
  advanced: ['Advanced JSON', '高级 JSON'], applyJSON: ['Apply JSON', '应用 JSON'], discardJSON: ['Discard JSON edits', '放弃 JSON 编辑'], advancedHint: ['Existing fields and exact integer values are preserved. Apply or discard JSON edits before changing visual fields or saving.', '保留现有字段及精确整数值。修改可视字段或保存前，请先应用或放弃 JSON 编辑。'], advancedPending: ['Apply or discard the pending JSON edits before saving', '请先应用或放弃待确认的 JSON 编辑，再保存'],
  invalidJSON: ['JSON must be an object with no duplicate or unsafe property names.', 'JSON 必须是对象，且不能包含重复或不安全的字段名。'], required: ['Required', '必填'],
  arrayRequired: ['Must be an array', '必须为数组'], emptyOrDuplicate: ['Empty or duplicate value', '存在空值或重复项'], integer_required: ['Enter an integer without fractions or exponent notation', '请输入整数，不能包含小数或指数'], integer_range: ['Integer is outside the permitted range', '整数超出允许范围'],
  idTooLong: ['ID cannot exceed 200 characters', 'ID 不能超过 200 个字符'], unknownTask: ['Unrecognized task', '未知任务'], endpointForTask: ['Endpoint does not match this task', '端点不适用于此任务'], preferredIncluded: ['Select a preferred endpoint from the selected endpoints', '首选端点必须包含在已选择端点中'],
  anthropicLimit: ['An Anthropic preferred endpoint requires maximum output tokens', 'Anthropic 首选端点要求填写最大输出 token'], taskNotSelected: ['Endpoint configuration exists for an unselected task', '存在未选择任务的端点配置'], statusRequired: ['Select a catalog status', '请选择目录状态'],
  priceTask: ['Price must belong to a selected task', '售价必须属于已选择任务'], priceUnsupportedMeter: ['This deployment cannot bill this meter', '当前实例无法对此单位计费'], currencyRequired: ['Use a three-letter uppercase currency code', '请使用三个大写字母的币种代码'], duplicatePrice: ['Duplicate task / meter / currency price', '任务 / 计量单位 / 币种售价重复'], singleCurrency: ['All model prices must use one currency', '一个模型的售价必须使用同一币种'], taskUnpriced: ['An enabled task has no price; missing is not free', '已启用任务没有售价；缺少价格不代表免费'], cacheAliasOverlap: ['Cache-read aliases cannot both be priced', '两个缓存读取别名不能同时定价'],
  baseURL: ['Use an absolute HTTP(S) URL without credentials, query or fragment', '请使用不含凭据、查询或片段的完整 HTTP(S) 地址'], baseURLTemplate: ['Replace the YOUR-* placeholder with your actual upstream resource address', '请将 YOUR-* 占位符替换为真实上游资源地址'], immutableIdentity: ['Account identity is immutable; create a new channel', '账号身份不可变，请新建渠道'], duplicatePublicID: ['Public model ID is duplicated', '公开模型 ID 重复'], unknownEndpoint: ['Unrecognized native endpoint', '未知原生端点'],
  input_tokens: ['Input tokens', '输入 token'], output_tokens: ['Output tokens', '输出 token'], requests: ['Requests', '请求次数'], images: ['Images', '图像数量'], cached_input_tokens: ['Cached input tokens (alias)', '缓存输入 token（别名）'], cache_read_input_tokens: ['Cache-read input tokens', '缓存读取 token'], cache_creation_input_tokens: ['Cache-creation input tokens', '缓存创建 token'], reasoning_tokens: ['Reasoning tokens', '推理 token']
} as const;
export function useCatalogLabels() {
  const { i18n } = useTranslation(); const chinese = (i18n.resolvedLanguage ?? i18n.language ?? '').startsWith('zh');
  return (key: string): string => labels[key as keyof typeof labels]?.[chinese ? 1 : 0] ?? key;
}
type EditorProps = { value: Row; onChange: (value: Row) => void; disabled?: boolean; issues?: CatalogIssue[]; editing?: boolean };

function Issue({ issues = [], path, id }: { issues?: CatalogIssue[]; path: string; id?: string }) {
  const label = useCatalogLabels(); const matches = issues.filter(issue => issue.path === path || issue.path.startsWith(path + '.'));
  return matches.length ? <div id={id} className="field-error" role="alert">{[...new Set(matches.map(issue => label(issue.code)))].join(' · ')}</div> : null;
}
export function CatalogIssueList({ issues }: { issues: CatalogIssue[] }) {
  const label = useCatalogLabels();
  return issues.length ? <ul className="catalog-issues" role="alert">{issues.map((issue, index) => <li key={`${issue.path}-${index}`}><code>{issue.path}</code>: {label(issue.code)}</li>)}</ul> : null;
}

function BasicField({ label, path, value, onChange, disabled, issues, type = 'text', hint, options }: { label: string; path: string; value: unknown; onChange: (value: string) => void; disabled?: boolean; issues?: CatalogIssue[]; type?: string; hint?: string; options?: { value: string; label: string }[] }) {
  const id = useId(); const issue = issues?.some(issue => issue.path === path || issue.path.startsWith(path + '.'));
  const props = { id, disabled, 'aria-invalid': !!issue, 'aria-describedby': `${id}-details`, value: String(value ?? ''), onChange: (event: ChangeEvent<HTMLInputElement | HTMLSelectElement>) => onChange(event.currentTarget.value) };
  return <div className={`editor-field${issue ? ' has-error' : ''}`}><label className="editor-label" htmlFor={id}>{label}</label>
    {options ? <select {...props} className="editor-select"><option value="">—</option>{options.map(option => <option key={option.value} value={option.value}>{option.label}</option>)}</select> : <input {...props} className="catalog-input" type={type === 'integer' ? 'text' : type} inputMode={type === 'integer' ? 'numeric' : undefined} autoComplete={type === 'password' ? 'new-password' : 'off'} />}
    <div id={`${id}-details`}>{hint && <div className="field-hint">{hint}</div>}<Issue path={path} issues={issues} /></div>
  </div>;
}
function BooleanField({ label, value, onChange, disabled }: { label: string; value: unknown; onChange: (value: boolean) => void; disabled?: boolean }) {
  return <label className="editor-boolean"><input type="checkbox" disabled={disabled} checked={Boolean(value)} onChange={event => onChange(event.target.checked)} /><span>{label}</span></label>;
}
function AdvancedJSON({ value, apply, pending, discard, disabled }: { value: Row; apply: (value: Row) => void; pending: () => void; discard: () => void; disabled?: boolean }) {
  const label = useCatalogLabels(); const [raw, setRaw] = useState(''); const [error, setError] = useState(''); const [dirty, setDirty] = useState(false); const id = useId();
  const serialized = exactJSON.stringify(value, null, 2);
  useEffect(() => { if (!dirty) setRaw(serialized); }, [serialized, dirty]);
  return <details className="editor-advanced catalog-advanced" onToggle={event => { if (event.currentTarget.open && !dirty) setRaw(exactJSON.stringify(value, null, 2)); }}><summary>{label('advanced')}</summary>
    <label className="field-hint" htmlFor={id}>{label('advancedHint')}</label><textarea id={id} className="catalog-input json-editor" rows={9} disabled={disabled} spellCheck={false} value={raw} aria-invalid={!!error} onChange={event => { setRaw(event.target.value); setDirty(true); setError(''); pending(); }} />
    {error && <div className="field-error" role="alert">{error}</div>}<div className="catalog-choices"><button className="catalog-button" type="button" disabled={disabled} onClick={() => { try { const parsed = exactJSON.parse(raw); if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) throw new Error(); apply(parsed as Row); setDirty(false); setError(''); } catch { setError(label('invalidJSON')); } }}>{label('applyJSON')}</button>{dirty && <button className="catalog-button" type="button" disabled={disabled} onClick={() => { discard(); setDirty(false); setError(''); }}>{label('discardJSON')}</button>}</div>
  </details>;
}

export function ModelFieldsEditor({ value, onChange, disabled = false, issues = [], editing = false }: EditorProps) {
  const actionDisabled = disabled; disabled ||= Boolean(value.__advanced_pending);
  const label = useCatalogLabels(); const editorId = useId(); const update = (key: string, input: unknown) => onChange({ ...value, [key]: input });
  const tasks = stringList(value.tasks); const routes = record(value.task_endpoints); const prices = Array.isArray(value.pricing) ? value.pricing.map(record) : [];
  const [newTask, setNewTask] = useState('chat');
  const updateRoute = (task: string, route: Row) => update('task_endpoints', { ...routes, [task]: route });
  const updatePrice = (index: number, key: string, input: unknown) => update('pricing', prices.map((price, current) => current === index ? { ...price, [key]: input } : price));
  const field = (key: string, input?: unknown, type = 'text', hint?: string) => <BasicField key={key} label={label(key)} path={key} value={input ?? value[key]} disabled={disabled || (editing && key === 'id')} issues={issues} type={type} hint={hint} onChange={input => update(key, input)} />;
  return <div className="catalog-fields">
    <div className="form-grid">{field('id')}{field('display_name')}{field('vendor')}<BasicField label={label('status')} path="status" value={value.status} disabled={disabled} issues={issues} onChange={input => update('status', input)} options={['available', 'degraded', 'unavailable'].map(status => ({ value: status, label: label(status) }))} /></div>
    <section className="catalog-section"><h3>{label('tasks')}</h3><p className="field-hint">{label('routeHint')}</p><Issue issues={issues} path="tasks" />
      {tasks.map((task, index) => {
        const route = record(routes[task]); const endpoints = stringList(route.endpoints); const choices = [...new Set([...(Object.hasOwn(TASK_ENDPOINTS, task) ? TASK_ENDPOINTS[task] : []), ...endpoints])];
        return <div className="catalog-route" key={`${task}-${index}`}><div className="catalog-row-heading"><strong>{label(task)}</strong><button className="catalog-button catalog-remove" type="button" disabled={disabled} onClick={() => { const nextRoutes = { ...routes }; delete nextRoutes[task]; onChange({ ...value, tasks: tasks.filter((_, current) => current !== index), task_endpoints: nextRoutes }); }}>{label('remove')}</button></div>
          <fieldset className="catalog-choices" aria-invalid={issues.some(issue => issue.path.startsWith(`task_endpoints.${task}.endpoints`))} aria-describedby={`${editorId}-${index}-endpoints-error`}><legend className="catalog-sr-only">{label(task)} · {label('endpoints')}</legend>{choices.map(endpoint => <label key={endpoint} className="editor-boolean"><input type="checkbox" disabled={disabled} checked={endpoints.includes(endpoint)} onChange={event => { const next = event.target.checked ? [...endpoints, endpoint] : endpoints.filter(value => value !== endpoint); updateRoute(task, { ...route, endpoints: next }); }} /><span>{endpoint}</span></label>)}</fieldset>
          <BasicField label={label('preferred')} path={`task_endpoints.${task}.preferred_endpoint`} value={route.preferred_endpoint} disabled={disabled} issues={issues} options={[...new Set([...endpoints, ...(route.preferred_endpoint ? [String(route.preferred_endpoint)] : [])])].map(endpoint => ({ value: endpoint, label: endpoint }))} onChange={input => updateRoute(task, { ...route, preferred_endpoint: input })} />
          <Issue id={`${editorId}-${index}-endpoints-error`} path={`task_endpoints.${task}.endpoints`} issues={issues} />
        </div>;
      })}
      <div className="catalog-add-row"><select className="editor-select" aria-label={label('tasks')} disabled={disabled} value={newTask} onChange={event => setNewTask(event.target.value)}>{Object.keys(TASK_ENDPOINTS).map(task => <option key={task} value={task}>{label(task)}</option>)}</select><button className="catalog-button" type="button" disabled={disabled || tasks.includes(newTask)} onClick={() => onChange({ ...value, tasks: [...tasks, newTask], task_endpoints: { ...routes, [newTask]: routes[newTask] ?? { endpoints: [], preferred_endpoint: '' } } })}>{label('addTask')}</button></div>
      <Issue path="task_endpoints" issues={issues.filter(issue => issue.code === 'taskNotSelected')} />
    </section>
    <div className="form-grid">{field('context_window', undefined, 'integer', label('limitHint'))}{field('max_output_tokens', undefined, 'integer')}
      <BasicField label={label('input_modalities')} path="input_modalities" value={stringList(value.input_modalities).join(', ')} disabled={disabled} issues={issues} hint={label('descriptorHint')} onChange={input => update('input_modalities', stringList(input))} />
      <BasicField label={label('traits')} path="traits" value={stringList(value.traits).join(', ')} disabled={disabled} issues={issues} hint={label('descriptorHint')} onChange={input => update('traits', stringList(input))} />
    </div>
    <section className="catalog-section"><h3>{label('prices')}</h3><p className="field-hint">{label('pricingHint')}</p>{!prices.length && <p className="catalog-empty">{label('pricingEmpty')}</p>}
      <div className="catalog-table-wrap"><table className="catalog-table"><caption className="sr-only">{label('prices')}</caption><thead><tr>{['task', 'meter', 'unit_size', 'amount', 'currency', 'remove'].map(key => <th key={key}>{label(key)}</th>)}</tr></thead><tbody>{prices.map((price, index) => <tr key={index}>
        <td><select className="editor-select" disabled={disabled} aria-label={`${label('task')} ${index + 1}`} aria-invalid={issues.some(issue => issue.path === `pricing.${index}.task`)} aria-describedby={`${editorId}-price-${index}-task-error`} value={String(price.task ?? '')} onChange={event => updatePrice(index, 'task', event.target.value)}><option value="">{label('select')}</option>{[...new Set([...tasks, ...(price.task ? [String(price.task)] : [])])].map(task => <option key={task} value={task}>{label(task)}</option>)}</select><Issue id={`${editorId}-price-${index}-task-error`} issues={issues} path={`pricing.${index}.task`} /></td>
        <td><select className="editor-select" disabled={disabled} aria-label={`${label('meter')} ${index + 1}`} aria-invalid={issues.some(issue => issue.path === `pricing.${index}.meter`)} aria-describedby={`${editorId}-price-${index}-meter-error`} value={String(price.meter ?? '')} onChange={event => updatePrice(index, 'meter', event.target.value)}><option value="">{label('select')}</option>{[...new Set([...PRICE_METERS, ...(price.meter ? [String(price.meter)] : [])])].map(meter => <option key={meter} value={meter}>{label(meter)}</option>)}</select><Issue id={`${editorId}-price-${index}-meter-error`} issues={issues} path={`pricing.${index}.meter`} /></td>
        {['unit_size', 'amount', 'currency'].map(key => <td key={key}><input className="catalog-input" disabled={disabled} aria-label={`${label(key)} ${index + 1}`} aria-invalid={issues.some(issue => issue.path === `pricing.${index}.${key}`)} aria-describedby={`${editorId}-price-${index}-${key}-error`} inputMode={key === 'currency' ? undefined : 'numeric'} autoComplete="off" value={String(price[key] ?? '')} onChange={event => updatePrice(index, key, event.target.value)} /><Issue id={`${editorId}-price-${index}-${key}-error`} issues={issues} path={`pricing.${index}.${key}`} /></td>)}
        <td><button className="catalog-button catalog-remove" type="button" disabled={disabled} aria-label={`${label('remove')} ${index + 1}`} onClick={() => update('pricing', prices.filter((_, current) => current !== index))}>{label('remove')}</button></td>
      </tr>)}</tbody></table></div><Issue issues={issues.filter(issue => !/^pricing\.\d+\./.test(issue.path))} path="pricing" />
      <button className="catalog-button" type="button" disabled={disabled} onClick={() => update('pricing', [...prices, { task: tasks[0] ?? '', meter: '', unit_size: '1', amount: '', currency: prices[0]?.currency ?? '' }])}>{label('addPrice')}</button>
    </section>
    <div className="catalog-choices"><BooleanField label={label('enabled')} value={value.enabled} onChange={input => update('enabled', input)} disabled={disabled} /><BooleanField label={label('subscription_only')} value={value.subscription_only} onChange={input => update('subscription_only', input)} disabled={disabled} /></div>
    <AdvancedJSON value={value} disabled={actionDisabled} pending={() => update('__advanced_pending', true)} discard={() => { const next = { ...value }; delete next.__advanced_pending; onChange(next); }} apply={input => { delete input.__advanced_pending; if (editing) input.id = value.id; onChange(input); }} />
  </div>;
}

export function ChannelFieldsEditor({ value, onChange, disabled = false, issues = [], editing = false }: EditorProps) {
  const actionDisabled = disabled; disabled ||= Boolean(value.__advanced_pending);
  const label = useCatalogLabels(); const editorId = useId(); const rows = mappingRows(value.__mapping_rows ?? value.model_ids); const endpoints = stringList(value.endpoints);
  const update = (key: string, input: unknown) => onChange({ ...value, [key]: input });
  const updateMappings = (next: MappingRow[]) => onChange({ ...value, __mapping_rows: next, model_ids: Object.fromEntries(next.filter(row => row.public_id.trim() && row.upstream_id.trim()).map(row => [row.public_id.trim(), row.upstream_id.trim()])) });
  const field = (key: string, type = 'text') => <BasicField key={key} label={label(key)} path={key} value={value[key]} disabled={disabled || (editing && ['kind', 'base_url', 'api_version', 'api_key'].includes(key))} type={type} issues={issues} onChange={input => update(key, input)} />;
  return <div className="catalog-fields"><div className="form-grid">{field('name')}<BasicField label={label('kind')} path="kind" value={value.kind} disabled={disabled || editing} issues={issues} options={CHANNEL_KINDS.map(kind => ({ value: kind, label: kind }))} onChange={input => update('kind', input)} />{field('base_url')}{field('api_version')}{!editing && field('api_key', 'password')}</div>
    {editing && <p className="field-hint">{label('identityHint')}</p>}
    <section className="catalog-section"><h3>{label('mapping')}</h3>{!rows.length && <p className="catalog-empty">{label('mappingEmpty')}</p>}<div className="catalog-table-wrap"><table className="catalog-table"><caption className="sr-only">{label('mapping')}</caption><thead><tr><th>{label('publicID')}</th><th>{label('upstreamID')}</th><th>{label('remove')}</th></tr></thead><tbody>{rows.map((row, index) => <tr key={index}>
      {(['public_id', 'upstream_id'] as const).map(key => <td key={key}><input className="catalog-input" disabled={disabled} aria-label={`${label(key === 'public_id' ? 'publicID' : 'upstreamID')} ${index + 1}`} autoComplete="off" value={row[key]} aria-invalid={issues.some(issue => issue.path === `model_ids.${index}.${key}`)} aria-describedby={`${editorId}-mapping-${index}-${key}-error`} onChange={event => updateMappings(rows.map((entry, current) => current === index ? { ...entry, [key]: event.target.value } : entry))} /><Issue id={`${editorId}-mapping-${index}-${key}-error`} path={`model_ids.${index}.${key}`} issues={issues} /></td>)}
      <td><button className="catalog-button catalog-remove" type="button" disabled={disabled} aria-label={`${label('remove')} ${index + 1}`} onClick={() => updateMappings(rows.filter((_, current) => current !== index))}>{label('remove')}</button></td>
    </tr>)}</tbody></table></div><Issue issues={issues.filter(issue => issue.path === 'model_ids')} path="model_ids" /><button className="catalog-button" type="button" disabled={disabled} onClick={() => updateMappings([...rows, { public_id: '', upstream_id: '' }])}>{label('addMapping')}</button></section>
    <section className="catalog-section"><h3>{label('endpoints')}</h3><p className="field-hint">{label('channelEndpointHint')}</p><fieldset className="catalog-choices" aria-invalid={issues.some(issue => issue.path === 'endpoints' || issue.path.startsWith('endpoints.'))} aria-describedby={`${editorId}-channel-endpoints-error`}><legend className="catalog-sr-only">{label('endpoints')}</legend>{[...new Set([...ENDPOINTS, ...endpoints])].map(endpoint => <label key={endpoint} className="editor-boolean"><input type="checkbox" disabled={disabled} checked={endpoints.includes(endpoint)} onChange={event => update('endpoints', event.target.checked ? [...endpoints, endpoint] : endpoints.filter(value => value !== endpoint))} /><span>{endpoint}</span></label>)}</fieldset><Issue id={`${editorId}-channel-endpoints-error`} issues={issues} path="endpoints" /></section>
    <div className="form-grid">{field('priority', 'integer')}{field('weight', 'integer')}</div><BooleanField label={label('enabled')} value={value.enabled} disabled={disabled} onChange={input => update('enabled', input)} />
    <AdvancedJSON value={{ name: value.name, model_ids: value.model_ids, endpoints: value.endpoints, priority: value.priority, weight: value.weight, enabled: value.enabled }} disabled={actionDisabled} pending={() => update('__advanced_pending', true)} discard={() => { const next = { ...value }; delete next.__advanced_pending; onChange(next); }} apply={input => { const next = { ...value, ...input }; if (exactJSON.stringify(next.model_ids) !== exactJSON.stringify(value.model_ids)) delete next.__mapping_rows; delete next.__advanced_pending; if (editing) { for (const key of ['kind', 'base_url', 'api_version']) next[key] = value[key]; delete next.api_key; } onChange(next); }} />
  </div>;
}
