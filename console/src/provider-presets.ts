// SPDX-License-Identifier: Apache-2.0
// Independently authored from official API documentation, reviewed 2026-10-08.
// These are channel entry templates, not upstream acceptance or model capability claims.
export type ChannelKind = 'openai' | 'anthropic' | 'gemini' | 'compatible' | 'azure';
export type NativeEndpoint = 'openai' | 'openai-response' | 'anthropic' | 'gemini' | 'image-generation' | 'embeddings' | 'jina-rerank';
export type ProviderPreset = {
  id: string;
  name: string;
  kind: ChannelKind;
  /** Gateway appends native request paths; keep product prefixes, omit the /v1 suffix. */
  base_url: string;
  endpoints: NativeEndpoint[];
  api_version?: string;
  /** Models means a documented list operation, not proof that this account can invoke a model. */
  discovery: 'models' | 'manual';
  notes: string[];
  official_docs: { title: string; url: string }[];
};

const bailianDocs = [
  { title: 'Model Studio base URLs and regions', url: 'https://help.aliyun.com/zh/model-studio/base-url' },
  { title: 'OpenAI-compatible Chat API', url: 'https://help.aliyun.com/zh/model-studio/qwen-api-via-openai-chat-completions' },
  { title: 'Native model catalog API', url: 'https://help.aliyun.com/zh/model-studio/list-models' },
];
const bailianNotes = [
  'Replace YOUR-WORKSPACE-ID with the API Host from the selected regional workspace. Keys and catalogs are region specific.',
  'Use a standard pay-as-you-go API key. Coding Plan, Token Plan and trial products are separate services.',
  'The native catalog uses /api/v1/models and page_no pagination, not /compatible-mode/v1/models. Use manual model IDs in this preset.',
];

export const providerPresets: ProviderPreset[] = [
  {
    id: 'custom', name: 'Custom', kind: 'compatible', base_url: '', endpoints: ['openai'], discovery: 'manual',
    notes: ['Choose the existing protocol/authentication kind and enter the operator-provided API address. Model-list availability depends on that service.'],
    official_docs: [],
  },
  {
    id: 'openai', name: 'OpenAI', kind: 'openai', base_url: 'https://api.openai.com', endpoints: ['openai', 'openai-response'], discovery: 'models',
    notes: ['Uses an OpenAI Platform API key with Bearer authentication. Each model must be configured only for the endpoints it supports.'],
    official_docs: [
      { title: 'API authentication', url: 'https://developers.openai.com/api/reference/overview' },
      { title: 'List models', url: 'https://developers.openai.com/api/reference/resources/models/methods/list' },
      { title: 'Responses API', url: 'https://developers.openai.com/api/reference/resources/responses/methods/create' },
    ],
  },
  {
    id: 'anthropic', name: 'Anthropic', kind: 'anthropic', base_url: 'https://api.anthropic.com', endpoints: ['anthropic'], api_version: '2023-06-01', discovery: 'models',
    notes: ['Uses x-api-key and anthropic-version headers. The catalog is paginated with after_id; Bedrock and Vertex AI use different authentication and are separate products.'],
    official_docs: [
      { title: 'Messages API', url: 'https://platform.claude.com/docs/en/api/messages/create' },
      { title: 'List models', url: 'https://platform.claude.com/docs/en/api/models/list' },
    ],
  },
  {
    id: 'gemini', name: 'Gemini Developer API', kind: 'gemini', base_url: 'https://generativelanguage.googleapis.com', endpoints: ['gemini'], discovery: 'models',
    notes: ['Uses the native v1beta generateContent protocol with x-goog-api-key. List results may include other operations; select only models supporting generateContent. Vertex AI is a separate product.'],
    official_docs: [
      { title: 'API keys', url: 'https://ai.google.dev/gemini-api/docs/api-key' },
      { title: 'Models and pagination', url: 'https://ai.google.dev/api/models' },
      { title: 'Generate content', url: 'https://ai.google.dev/api/generate-content' },
    ],
  },
  {
    id: 'azure', name: 'Azure OpenAI', kind: 'azure', base_url: 'https://YOUR-RESOURCE-NAME.openai.azure.com', endpoints: ['openai', 'openai-response'], discovery: 'manual',
    notes: ['Replace YOUR-RESOURCE-NAME with the resource endpoint. Use its API key; Microsoft Entra token refresh is outside this template.',
      'Leave api_version empty for the current /openai/v1 API. Enter deployment names as upstream IDs; a global model catalog does not identify your resource deployments.',
      'A dated api_version selects the existing legacy deployment API and must be checked against the chosen endpoint and region.'],
    official_docs: [
      { title: 'Azure OpenAI v1 API', url: 'https://learn.microsoft.com/en-us/azure/foundry/openai/api-version-lifecycle' },
      { title: 'Working with deployments', url: 'https://learn.microsoft.com/en-us/azure/ai-foundry/openai/how-to/working-with-models' },
    ],
  },
  {
    id: 'deepseek', name: 'DeepSeek', kind: 'compatible', base_url: 'https://api.deepseek.com', endpoints: ['openai'], discovery: 'models',
    notes: ['Uses Bearer authentication and the OpenAI-compatible Chat API. Do not substitute retired model aliases for discovered IDs. Responses and Anthropic alternatives require separate endpoint validation.'],
    official_docs: [
      { title: 'API and base URL', url: 'https://api-docs.deepseek.com/' },
      { title: 'Chat Completions', url: 'https://api-docs.deepseek.com/api/create-chat-completion/' },
      { title: 'Documented v1 Chat path', url: 'https://api-docs.deepseek.com/quick_start/agent_integrations/workbuddy/' },
      { title: 'List models', url: 'https://api-docs.deepseek.com/api/list-models/' },
    ],
  },
  {
    id: 'moonshot-cn', name: 'Moonshot / Kimi (China)', kind: 'compatible', base_url: 'https://api.moonshot.cn', endpoints: ['openai'], discovery: 'models',
    notes: ['Uses a China-platform API key with Bearer authentication. China and international keys, balances and catalogs are separate. This template selects Chat Completions.'],
    official_docs: [
      { title: 'API overview', url: 'https://platform.kimi.com/docs/api/overview' },
      { title: 'List models', url: 'https://platform.kimi.com/docs/api/list-models' },
    ],
  },
  {
    id: 'moonshot-global', name: 'Moonshot / Kimi (International)', kind: 'compatible', base_url: 'https://api.moonshot.ai', endpoints: ['openai'], discovery: 'models',
    notes: ['Uses an international-platform API key with Bearer authentication. Keys from the China platform cannot be reused here. This template selects Chat Completions.'],
    official_docs: [
      { title: 'API overview', url: 'https://platform.kimi.ai/docs/api/overview' },
      { title: 'List models', url: 'https://platform.kimi.ai/docs/api/list-models' },
    ],
  },
  {
    id: 'bailian-cn', name: 'Alibaba Bailian (Beijing)', kind: 'compatible', base_url: 'https://YOUR-WORKSPACE-ID.cn-beijing.maas.aliyuncs.com/compatible-mode', endpoints: ['openai'], discovery: 'manual',
    notes: [...bailianNotes], official_docs: [...bailianDocs],
  },
  {
    id: 'bailian-sg', name: 'Alibaba Model Studio (Singapore)', kind: 'compatible', base_url: 'https://YOUR-WORKSPACE-ID.ap-southeast-1.maas.aliyuncs.com/compatible-mode', endpoints: ['openai'], discovery: 'manual',
    notes: [...bailianNotes], official_docs: [...bailianDocs],
  },
  {
    id: 'bailian-hk', name: 'Alibaba Model Studio (Hong Kong)', kind: 'compatible', base_url: 'https://YOUR-WORKSPACE-ID.cn-hongkong.maas.aliyuncs.com/compatible-mode', endpoints: ['openai'], discovery: 'manual',
    notes: [...bailianNotes], official_docs: [...bailianDocs],
  },
  {
    id: 'bailian-us', name: 'Alibaba Model Studio (US Virginia)', kind: 'compatible', base_url: 'https://YOUR-WORKSPACE-ID.us-east-1.maas.aliyuncs.com/compatible-mode', endpoints: ['openai'], discovery: 'manual',
    notes: [...bailianNotes], official_docs: [...bailianDocs],
  },
  {
    id: 'siliconflow-cn', name: 'SiliconFlow (China)', kind: 'compatible', base_url: 'https://api.siliconflow.cn', endpoints: ['openai'], discovery: 'models',
    notes: ['Uses a China-platform API key with Bearer authentication. The catalog also contains non-chat models; configure only tasks and native endpoints supported by the chosen model.'],
    official_docs: [
      { title: 'Chat Completions', url: 'https://docs.siliconflow.cn/docs/api/chat-completions-post' },
      { title: 'List models', url: 'https://docs.siliconflow.cn/docs/api/models-get' },
    ],
  },
  {
    id: 'siliconflow-global', name: 'SiliconFlow (International)', kind: 'compatible', base_url: 'https://api.siliconflow.com', endpoints: ['openai'], discovery: 'models',
    notes: ['Uses an international-platform API key with Bearer authentication. The address and catalog are separate from the China platform; do not assume matching model IDs or account access.'],
    official_docs: [
      { title: 'Chat Completions', url: 'https://docs.siliconflow.com/en/api-reference/chat-completions/chat-completions' },
      { title: 'List models', url: 'https://docs.siliconflow.com/en/api-reference/models/get-model-list' },
    ],
  },
  {
    id: 'stepfun', name: 'StepFun', kind: 'compatible', base_url: 'https://api.stepfun.com', endpoints: ['openai'], discovery: 'models',
    notes: ['Uses the standard pay-as-you-go API key with Bearer authentication. Step Plan uses a separate address and credentials; this template does not route subscription traffic.'],
    official_docs: [
      { title: 'API platform and Chat Completions', url: 'https://platform.stepfun.com/' },
      { title: 'List models', url: 'https://platform.stepfun.com/docs/zh/api-reference/models/list' },
    ],
  },
  {
    id: 'openrouter', name: 'OpenRouter', kind: 'compatible', base_url: 'https://openrouter.ai/api', endpoints: ['openai'], discovery: 'models',
    notes: ['Uses Bearer authentication for Chat Completions. Catalog entries and supplier prices do not establish account access or your gateway selling price.',
      'This template selects the OpenAI-compatible Chat API. A Claude or Gemini model name does not enable that provider native protocol.',
      'OpenRouter Responses is stateless and rejects store:true and non-null previous_response_id; it is not enabled by this preset.'],
    official_docs: [
      { title: 'Quickstart', url: 'https://openrouter.ai/docs/quickstart' },
      { title: 'List models', url: 'https://openrouter.ai/docs/api/api-reference/models/list-all-models-and-their-properties' },
    ],
  },
];

/** Return an editable copy so switching or editing drafts cannot mutate future presets. */
export function findProviderPreset(id: string): ProviderPreset | undefined {
  const preset = providerPresets.find(item => item.id === id);
  return preset && { ...preset, endpoints: [...preset.endpoints], notes: [...preset.notes], official_docs: preset.official_docs.map(item => ({ ...item })) };
}
