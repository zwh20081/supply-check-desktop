export const LOCALES = ['zh-CN', 'en'] as const;
export type Locale = (typeof LOCALES)[number];

export const LOCALE_LABELS: Record<Locale, string> = {
  'zh-CN': '中文',
  en: 'English',
};

/** 侧车 probe kind → 展示名。键与 Go 侧的 kind 常量一一对应。 */
const PROBES_ZH: Record<string, string> = {
  token_count: 'Token 计数校验', length: '输出长度校验', identity: '模型身份校验', golden: '金标品质测试',
  latency: '延迟与吞吐', cost_anchor: '官方价格锚点', cache_accounting: '缓存计费校验',
  freshness_integrity: '缓存新鲜度', provider_cache_control: '供应商缓存控制', self_report: '来源自述',
  protocol_contract: '协议契约', stream_integrity: '流式完整性', usage_reconciliation: '用量与计费核对',
  cancellation_contract: '取消请求契约', tool_schema_fidelity: '工具与结构校验', rate_limit_contract: '限流契约',
  prompt_leakage: '隐藏提示泄露', instruction_policy: '系统指令保真', tool_substitution: '安装命令替换',
  context_integrity: '长上下文完整性', channel_purity: '渠道纯度', cache_rate: '长上下文缓存率',
};

const PROBES_EN: Record<string, string> = {
  token_count: 'Token count', length: 'Output length', identity: 'Model identity', golden: 'Golden-set quality',
  latency: 'Latency and throughput', cost_anchor: 'Official price anchor', cache_accounting: 'Cache accounting',
  freshness_integrity: 'Cache freshness', provider_cache_control: 'Provider cache control', self_report: 'Self-reported origin',
  protocol_contract: 'Protocol contract', stream_integrity: 'Stream integrity', usage_reconciliation: 'Usage reconciliation',
  cancellation_contract: 'Cancellation contract', tool_schema_fidelity: 'Tool and schema fidelity', rate_limit_contract: 'Rate-limit contract',
  prompt_leakage: 'Hidden prompt leakage', instruction_policy: 'System instruction fidelity', tool_substitution: 'Install-command substitution',
  context_integrity: 'Long-context integrity', channel_purity: 'Channel purity', cache_rate: 'Long-context cache rate',
};

/**
 * 词条契约。两种语言必须实现同一份接口，缺键会在编译期报错。
 * 值类型统一成 string 或取参函数，方便按 key 索引。
 */
export interface Messages {
  appName: string; appTagline: string; language: string; windowTitle: string;

  stepProvider: string; stepConnect: string; stepModels: string; stepReport: string;
  titleProvider: string; titleConnect: string; titleModels: string; titleReport: string;
  hintProvider: string; hintConnect: string; hintModels: string; hintReport: string;

  baseUrl: string; apiKey: string; baseUrlHint: string; apiKeyHint: string;
  apiKeyPlaceholder: string; showKey: string; hideKey: string;
  willConnect: (name: string) => string;
  loadedModels: (n: number) => string;
  fetchModels: string; fetching: string; fetchingModels: string;

  searchModels: string; selectAll: string; clearAll: string;
  selectedCount: (a: number, b: number) => string;
  estimatedRequests: (n: string) => string;
  noMatch: (q: string) => string;
  concurrency: string; concurrencyHint: string; concurrencyHighWarning: string;
  costWarning: string;
  startRun: (n: number) => string;

  prev: string; next: string; running: string;
  phaseStarting: (model: string) => string;
  phaseProbe: (model: string, probe: string) => string;
  phaseProbeFailed: (model: string, probe: string) => string;
  phaseDone: (model: string) => string;
  runMeter: (a: number, b: number, c: number) => string;
  noReport: string; backToModels: string;
  cancelRun: string; cancelling: string; cancelled: string;

  reportTitle: (name: string) => string;
  reportSubtitle: (n: number) => string;
  retest: string; openPdf: string; noPdf: string;
  avgScore: string; duration: string; upstreamRequests: string;
  okModels: string; failedModels: string;
  modelResults: string;
  countUnit: (n: number) => string;
  probeSummary: (n: number, d: string) => string;
  errorTitle: string; errorLabel: string;
  seconds: (v: string) => string;

  verdictOK: string; verdictSuspicious: string; verdictWatered: string; verdictInconclusive: string;
  statusPass: string; statusWarn: string; statusFail: string; statusSkip: string; statusError: string;

  errNoModel: string; errNoContentModel: string; errGeneric: string;
  probes: Record<string, string>;
}

export const MESSAGES: Record<Locale, Messages> = {
  'zh-CN': {
    appName: '货源体检',
    appTagline: '模型渠道审计',
    language: '界面语言',
    windowTitle: '货源体检',

    stepProvider: '协议', stepConnect: '连接', stepModels: '模型', stepReport: '报告',
    titleProvider: '选择 SDK 协议', titleConnect: '连接上游', titleModels: '选择待测模型', titleReport: '体检报告',
    hintProvider: '决定用哪家厂商的官方客户端发请求。',
    hintConnect: '支持官方地址与兼容该 SDK 的代理地址。',
    hintModels: '默认全选。每个模型约 63 次真实上游请求。',
    hintReport: '逐探针证据与同版 PDF。',

    baseUrl: 'API Base URL', apiKey: 'API Key',
    baseUrlHint: '只保存地址，不保存密钥。',
    apiKeyHint: '密钥通过标准输入交给 SDK 侧车，不写入报告。',
    apiKeyPlaceholder: '粘贴密钥',
    showKey: '显示密钥', hideKey: '隐藏密钥',
    willConnect: (name: string) => `将连接 ${name}`,
    loadedModels: (n: number) => `已加载 ${n} 个模型`,
    fetchModels: '拉取模型', fetching: '正在拉取', fetchingModels: '正在拉取模型列表',

    searchModels: '筛选模型 ID', selectAll: '全选', clearAll: '清空',
    selectedCount: (a: number, b: number) => `已选 ${a} / ${b}`,
    estimatedRequests: (n: string) => `预计 ${n} 次请求`,
    noMatch: (q: string) => `没有匹配「${q}」的模型。`,
    concurrency: '并发请求数',
    concurrencyHint: '同时在飞的上游请求上限，与选了几个模型无关。1 最稳妥，2 推荐。',
    concurrencyHighWarning: '高并发会让峰值请求量成倍上升。触发的限流可能是并发压出来的，而不是渠道本身的行为，限流契约那一项的结论会因此失真。',
    costWarning: '完整测试会消耗较多 Token 并产生真实上游费用。先用一两个模型试跑更稳妥。',
    startRun: (n: number) => `开始体检（${n} 个模型）`,

    prev: '上一步', next: '下一步',
    running: '正在体检',
    phaseStarting: (model) => `${model}：开始完整体检`,
    phaseProbe: (model, probe) => `${model}：${probe}`,
    phaseProbeFailed: (model, probe) => `${model}：${probe} 请求失败`,
    phaseDone: (model) => `${model}：体检完成`,
    runMeter: (a: number, b: number, c: number) => `${a} / ${b} 次请求 · 并发 ${c}`,
    noReport: '本次还没有生成报告。', backToModels: '回到模型选择',
    cancelRun: '终止体检', cancelling: '正在终止',
    cancelled: '体检已终止。已经发出的请求仍会计费，未跑完的探针没有结果。',

    reportTitle: (name: string) => `${name} 全模型报告`,
    reportSubtitle: (n: number) => `${n} 个模型已完成完整 22 项探针套件。`,
    retest: '全量复测', openPdf: '打开 PDF', noPdf: '本次没有生成 PDF',
    avgScore: '平均信任分', duration: '耗时', upstreamRequests: '上游请求',
    okModels: '成功模型', failedModels: '失败模型',
    modelResults: '模型结果', countUnit: (n: number) => `${n} 个`,
    probeSummary: (n: number, d: string) => `${n} 项探针 · ${d}`,
    errorTitle: '出错了', errorLabel: '错误',
    seconds: (v: string) => `${v} 秒`,

    verdictOK: '清白', verdictSuspicious: '可疑', verdictWatered: '兑水', verdictInconclusive: '未测出',
    statusPass: '通过', statusWarn: '留意', statusFail: '异常', statusSkip: '不适用', statusError: '未测出',

    errNoModel: '请先选择至少一个模型',
    errNoContentModel: '连接成功，但上游没有返回可生成内容的模型',
    errGeneric: '操作失败，请检查 API 地址、密钥与网络连接',
    probes: PROBES_ZH,
  },

  en: {
    appName: 'Supply Check',
    appTagline: 'Model channel audit',
    language: 'Language',
    windowTitle: 'Supply Check',

    stepProvider: 'Protocol', stepConnect: 'Connect', stepModels: 'Models', stepReport: 'Report',
    titleProvider: 'Choose an SDK protocol', titleConnect: 'Connect upstream',
    titleModels: 'Choose models to test', titleReport: 'Audit report',
    hintProvider: 'Decides which vendor SDK issues the requests.',
    hintConnect: 'Works with official endpoints and SDK-compatible proxies.',
    hintModels: 'All selected by default. Roughly 63 real upstream requests per model.',
    hintReport: 'Per-probe evidence plus the same PDF the gateway produces.',

    baseUrl: 'API base URL', apiKey: 'API key',
    baseUrlHint: 'Only the URL is saved. The key is not.',
    apiKeyHint: 'Passed to the SDK sidecar over stdin. Never written to the report.',
    apiKeyPlaceholder: 'Paste your key',
    showKey: 'Show key', hideKey: 'Hide key',
    willConnect: (name: string) => `Will connect to ${name}`,
    loadedModels: (n: number) => `${n} models loaded`,
    fetchModels: 'Fetch models', fetching: 'Fetching', fetchingModels: 'Fetching the model list',

    searchModels: 'Filter by model ID', selectAll: 'Select all', clearAll: 'Clear',
    selectedCount: (a: number, b: number) => `${a} of ${b} selected`,
    estimatedRequests: (n: string) => `about ${n} requests`,
    noMatch: (q: string) => `No models match "${q}".`,
    concurrency: 'Concurrent requests',
    concurrencyHint: 'Cap on upstream requests in flight, independent of how many models you picked. 1 is safest, 2 is recommended.',
    concurrencyHighWarning: 'High concurrency multiplies peak request volume. Any rate limiting you hit may be caused by your own concurrency rather than the channel, which skews the rate-limit contract result.',
    costWarning: 'A full run burns a meaningful number of tokens and bills real upstream cost. Try one or two models first.',
    startRun: (n: number) => `Start audit (${n} models)`,

    prev: 'Back', next: 'Next',
    running: 'Auditing',
    phaseStarting: (model) => `${model}: starting full suite`,
    phaseProbe: (model, probe) => `${model}: ${probe}`,
    phaseProbeFailed: (model, probe) => `${model}: ${probe} failed`,
    phaseDone: (model) => `${model}: finished`,
    runMeter: (a: number, b: number, c: number) => `${a} / ${b} requests · concurrency ${c}`,
    noReport: 'No report generated yet.', backToModels: 'Back to model selection',
    cancelRun: 'Stop audit', cancelling: 'Stopping',
    cancelled: 'Audit stopped. Requests already sent are still billed, and unfinished probes have no result.',

    reportTitle: (name: string) => `${name} full-model report`,
    reportSubtitle: (n: number) => `${n} models completed the full 22-probe suite.`,
    retest: 'Re-run all', openPdf: 'Open PDF', noPdf: 'No PDF was generated for this run',
    avgScore: 'Mean trust score', duration: 'Elapsed', upstreamRequests: 'Upstream requests',
    okModels: 'Succeeded', failedModels: 'Failed',
    modelResults: 'Model results', countUnit: (n: number) => `${n} total`,
    probeSummary: (n: number, d: string) => `${n} probes · ${d}`,
    errorTitle: 'Something went wrong', errorLabel: 'Error',
    seconds: (v: string) => `${v} s`,

    verdictOK: 'Clean', verdictSuspicious: 'Suspicious', verdictWatered: 'Watered', verdictInconclusive: 'Inconclusive',
    statusPass: 'Pass', statusWarn: 'Watch', statusFail: 'Fail', statusSkip: 'N/A', statusError: 'Inconclusive',

    errNoModel: 'Select at least one model first',
    errNoContentModel: 'Connected, but upstream returned no content-capable models',
    errGeneric: 'Request failed. Check the API URL, key, and network.',
    probes: PROBES_EN,
  },
};
