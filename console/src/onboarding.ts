// SPDX-License-Identifier: Apache-2.0
import { APIError } from './api.ts';
import type { Row } from './api.ts';
import { mappingRows, MAX_TOKEN_LIMIT, record, stringList, TASK_ENDPOINTS } from './catalog-form.ts';
export type DiscoveredModel = Row & { id: string; display_name?: string; context_limit?: number | bigint; output_limit?: number | bigint; token_limit_sources?: { context_limit?: string; output_limit?: string; context_limit_kind?: string }; tasks?: string[]; tasks_source?: string };
export type DiscoveryResult = { models: DiscoveredModel[]; complete: boolean; pages: number; warnings: string[]; catalog_source?: 'remote'; advisory?: boolean };
export type DiscoveryMetadataPreview = { tasks: string[]; unsupportedTasks: string[]; contextLimit?: bigint; outputLimit?: bigint; invalidLimits: string[] };
function discoveredLimit(value: unknown): bigint | undefined {
  if (typeof value !== 'number' && typeof value !== 'bigint') return undefined;
  if (typeof value === 'number' && !Number.isSafeInteger(value)) return undefined;
  const limit = BigInt(value);
  return limit > 0n && limit <= MAX_TOKEN_LIMIT ? limit : undefined;
}
export function discoveryMetadataPreview(model: DiscoveredModel): DiscoveryMetadataPreview {
  // Only structured native declarations carry task evidence. IDs/names and old vendor hints are opaque.
  const tasks = model.tasks_source ? [...new Set(stringList(model.tasks))] : [];
  const contextLimit = discoveredLimit(model.context_limit); const outputLimit = discoveredLimit(model.output_limit);
  return {
    tasks: tasks.filter(task => Object.hasOwn(TASK_ENDPOINTS, task)), unsupportedTasks: tasks.filter(task => !Object.hasOwn(TASK_ENDPOINTS, task)),
    contextLimit, outputLimit,
    invalidLimits: [['context_limit', model.context_limit, contextLimit], ['output_limit', model.output_limit, outputLimit]].filter(([, raw, limit]) => raw !== undefined && raw !== null && limit === undefined).map(([key]) => String(key))
  };
}
function missing(value: unknown): boolean { return value === undefined || value === null || value === ''; }
// Apply is an explicit operator action. Existing choices and edited values always win.
// Native endpoint preference and selling prices still need a separate deliberate choice.
export function applyDiscoveredMetadata(draft: Row, model: DiscoveredModel): Row {
  const preview = discoveryMetadataPreview(model); const next = { ...draft };
  if (!stringList(draft.tasks).length && preview.tasks.length) {
    next.tasks = preview.tasks;
    const routes = record(draft.task_endpoints);
    next.task_endpoints = { ...routes, ...Object.fromEntries(preview.tasks.filter(task => !Object.hasOwn(routes, task)).map(task => [task, { endpoints: [], preferred_endpoint: '' }])) };
  }
  if (missing(draft.context_window) && preview.contextLimit !== undefined) next.context_window = preview.contextLimit;
  if (missing(draft.max_output_tokens) && preview.outputLimit !== undefined) next.max_output_tokens = preview.outputLimit;
  return next;
}
export type PublishIssue = { model_id?: string; field: string; message: string };
export type PublishCheck = { ready: boolean; issues: PublishIssue[] };
export function writeOutcomeUnknown(error: unknown): boolean {
  return !(error instanceof APIError) || error.status < 400 || error.status >= 500;
}
export function channelSubmission(value: Row): Row {
  return Object.fromEntries(['name', 'kind', 'base_url', 'api_key', 'model_ids', 'endpoints', 'priority', 'weight', 'enabled', 'api_version'].filter(key => value[key] !== undefined).map(key => [key, value[key]]));
}
export function mappingPreview(current: Record<string, string>, models: DiscoveredModel[], catalog: Row[]) {
  const used = new Set(catalog.map(row => String(row.id)));
  return models.map(model => ({ ...model, state: current[model.id] ? current[model.id] === model.id ? 'unchanged' : 'changed' : used.has(model.id) ? 'conflict' : 'new' }));
}
// Selected catalog entries only add missing mappings. Existing operational data
// and mappings remain explicit decisions in the mapping table.
export function addDiscoveredMappings(channel: Row, selected: DiscoveredModel[]): Row {
  const mappings = { ...(channel.model_ids as Record<string, string> ?? {}) };
  const rows = mappingRows(channel.__mapping_rows ?? mappings);
  for (const model of selected) if (!rows.some(row => row.public_id.trim() === model.id)) { rows.push({ public_id: model.id, upstream_id: model.id }); if (!Object.hasOwn(mappings, model.id)) mappings[model.id] = model.id; }
  return { ...channel, model_ids: mappings, __mapping_rows: rows };
}
export function synchronizeModelDrafts(channel: Row, drafts: Row[], vendor: string, discovered: DiscoveredModel[]): Row[] {
  const mappings = channel.model_ids as Record<string, string> ?? {};
  return Object.keys(mappings).map(id => drafts.find(model => model.id === id) ?? {
    id, display_name: discovered.find(model => model.id === mappings[id])?.display_name ?? id,
    vendor, tasks: [], task_endpoints: {}, context_window: null, max_output_tokens: null,
    input_modalities: [], traits: [], pricing: [], status: 'available', enabled: false, subscription_only: false,
  });
}
export const onboardingWords = {
  title: ['Provider setup', '供应商快捷录入'], channel: ['Provider & channel', '供应商与渠道'], discover: ['Discover & map models', '发现与映射模型'], models: ['Capabilities & prices', '能力与售价'], review: ['Check & save', '检查并保存'],
  preset: ['Provider preset', '供应商预设'], templateOnly: ['Presets fill in connection fields. Confirm the upstream product and each model’s native capabilities before publishing.', '预设帮助填写连接信息。发布前请确认上游产品和每个模型的原生能力。'],
  next: ['Next', '下一步'], back: ['Back', '上一步'], cancel: ['Cancel', '取消'], save: ['Save configuration', '保存配置'], read: ['Read model catalog', '读取模型目录'],
  manual: ['Enter mappings manually if model listing is unavailable. Reading the catalog does not perform inference or prove native call support.', '无法读取模型目录时可手填映射。目录读取不会发送生成请求，也不证明实际调用支持。'],
  unknown: ['Remote catalog metadata is advisory. Review and explicitly apply native declarations; confirm routes, limits and selling prices before publishing.', '远程目录元数据仅供参考。请审阅并明确应用原生声明，发布前确认路线、限额与售价。'],
  reviewMetadata: ['Review remote metadata', '审阅远程元数据'], applyMetadata: ['Apply missing metadata to draft', '将缺失元数据应用到草稿'], metadataAdvisory: ['Remote catalog declarations are advisory, not successful native-call acceptance. Applying fills only empty tasks and limits; existing edits, endpoint preferences and prices are preserved.', '远程目录声明仅供参考，不代表原生调用验收成功。应用仅填入空缺任务与限额；保留已有编辑、首选端点与售价。'],
  metadataTasks: ['Declared native tasks', '原生声明任务'], metadataContext: ['Declared context limit', '声明上下文限额'], metadataOutput: ['Declared output limit', '声明输出限额'], metadataUnknown: ['Not declared', '未声明'], metadataGap: ['Desktop tasks outside gateway v1; shown for review only', 'Desktop 任务超出网关 v1 支持范围，仅供审阅'], metadataInvalid: ['Invalid token limits cannot be applied', '无效 token 限额不能应用'], metadataEdit: ['Apply to model editor', '应用到模型编辑器'], metadataNoModel: ['Save the mapping first, then configure its public model.', '请先保存映射，再配置对应公开模型。'],
  apply: ['Add selected mappings', '添加所选映射'], empty: ['No model IDs returned. Use the mapping table below.', '未返回模型 ID，请使用下方映射表手填。'],
  new: ['New', '新增'], unchanged: ['Already mapped', '已映射'], changed: ['Mapping differs; preserved', '映射不同，已保留原值'], conflict: ['Public ID exists; rename in mapping table', '公开 ID 已存在，请在映射表改名'],
  check: ['Run publication checks', '执行发布检查'], ready: ['Configuration passed publication checks.', '配置已通过发布检查。'], pending: ['Incomplete models must stay disabled. Missing prices do not mean free.', '待配置模型须保持停用。缺少价格不等于免费。'],
  immutable: ['The upstream account identity is fixed. Create a new channel to change its protocol, address, API version or credentials.', '上游账号身份不可变。变更协议、地址、API 版本或凭据须新建渠道。'],
  savedDiscovery: ['Discover models', '发现模型'], partial: ['Partial catalog; review warnings before importing.', '目录不完整，请检查警告后再导入。'],
  ambiguous: ['The write outcome is unknown. Close this dialog and refresh channels/models before retrying; do not resubmit blindly.', '保存结果尚未确认。请关闭窗口并刷新渠道与模型，确认后再操作，避免重复提交。'],
  requiredMapping: ['Add at least one unique public → upstream model mapping.', '请添加至少一条唯一的公开模型 → 上游模型映射。'],
  idConflict: ['A public model ID already exists. Rename it or edit that existing model separately.', '公开模型 ID 已存在。请改名，或单独编辑现有模型。'],
  channelSummary: ['Channel', '渠道'], checkBeforeSave: ['Check the final configuration before saving. Enabled models must pass every check.', '保存前检查最终配置。启用的模型必须通过所有检查。'],
  finishSaved: ['Configuration saved.', '配置已保存。'],
} as const;
export type OnboardingWord = keyof typeof onboardingWords;
