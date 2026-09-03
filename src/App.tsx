import { useEffect, useMemo, useRef, useState, type CSSProperties } from 'react';
import { invoke } from '@tauri-apps/api/core';
import { listen } from '@tauri-apps/api/event';
import {
  Badge, Body1, Button, Caption1, Card, CardHeader, Checkbox, Divider, Field,
  Input, MessageBar, MessageBarBody, MessageBarTitle, ProgressBar, SearchBox,
  Select, Skeleton, SkeletonItem, Slider, Spinner, Subtitle2, Tab, TabList,
  Title3, Tooltip, mergeClasses,
} from '@fluentui/react-components';
import {
  ArrowLeft, ArrowRight, Atom, CheckCircle, DiamondsFour, Eye, EyeSlash,
  Hexagon, Key, LinkSimple, Play, Pulse, StopCircle,
} from '@phosphor-icons/react';
import ReportView from './components/ReportView';
import { useAppStyles } from './useAppStyles';
import { LOCALES, LOCALE_LABELS, useI18n, type Locale } from './i18n';
import type { Messages } from './i18n/messages';
import type { BatchReport, Credentials, ModelInfo, ProgressEvent, Provider } from './types';

const PROVIDERS: Record<Provider, { label: string; protocol: string; defaultBaseUrl: string; Icon: typeof Atom }> = {
  openai: { label: 'OpenAI', protocol: 'Chat Completions', defaultBaseUrl: 'https://api.openai.com/v1', Icon: Atom },
  'openai-responses': { label: 'OpenAI', protocol: 'Responses API', defaultBaseUrl: 'https://api.openai.com/v1', Icon: Atom },
  anthropic: { label: 'Claude', protocol: 'Messages API', defaultBaseUrl: 'https://api.anthropic.com/v1', Icon: Hexagon },
  google: { label: 'Google', protocol: 'Gemini API', defaultBaseUrl: 'https://generativelanguage.googleapis.com/v1beta', Icon: DiamondsFour },
};

/** 只取 Messages 里值为 string 的键，避免把取参函数当文案渲染。 */
type TextKey = { [K in keyof Messages]: Messages[K] extends string ? K : never }[keyof Messages];

const BASES_KEY = 'supply-check-bases-v1';
/** 与侧车 batch.RequestsPerModel 对应：30 基础 + 10 档上下文 × 3 轮缓存率。改这里要同步改那边。 */
const REQUESTS_PER_MODEL = 60;

/** 侧车已不再夹取并发数，上限完全由这里决定。 */
const MAX_CONCURRENCY = 16;

/** 超过这个值就提示：峰值请求量可能压出上游限流，干扰限流契约探针。 */
const HIGH_CONCURRENCY = 4;

const STEPS = ['provider', 'connect', 'models', 'report'] as const;
type Step = (typeof STEPS)[number];

/** 步骤文案全部从词条表取，键名与 messages.ts 对应。 */
const STEP_KEYS: Record<Step, { label: TextKey; title: TextKey; hint: TextKey }> = {
  provider: { label: 'stepProvider', title: 'titleProvider', hint: 'hintProvider' },
  connect: { label: 'stepConnect', title: 'titleConnect', hint: 'hintConnect' },
  models: { label: 'stepModels', title: 'titleModels', hint: 'hintModels' },
  report: { label: 'stepReport', title: 'titleReport', hint: 'hintReport' },
};

type Connections = Record<Provider, { baseUrl: string; apiKey: string }>;

function loadConnections(): Connections {
  const defaults: Connections = {
    openai: { baseUrl: PROVIDERS.openai.defaultBaseUrl, apiKey: '' },
    'openai-responses': { baseUrl: PROVIDERS['openai-responses'].defaultBaseUrl, apiKey: '' },
    anthropic: { baseUrl: PROVIDERS.anthropic.defaultBaseUrl, apiKey: '' },
    google: { baseUrl: PROVIDERS.google.defaultBaseUrl, apiKey: '' },
  };
  try {
    const saved = JSON.parse(localStorage.getItem(BASES_KEY) || '{}') as Partial<Record<Provider, string>>;
    (Object.keys(defaults) as Provider[]).forEach((item) => { if (saved[item]) defaults[item].baseUrl = saved[item]!; });
  } catch { /* use defaults */ }
  return defaults;
}

function errorText(error: unknown, fallback: string) {
  if (typeof error === 'string') return error;
  if (error instanceof Error) return error.message;
  return fallback;
}

export default function App() {
  const styles = useAppStyles();
  const { t, locale, setLocale } = useI18n();
  const [step, setStep] = useState<Step>('provider');
  const [provider, setProvider] = useState<Provider>('openai');
  const [connections, setConnections] = useState<Connections>(loadConnections);
  const [models, setModels] = useState<Partial<Record<Provider, ModelInfo[]>>>({});
  const [selectedModels, setSelectedModels] = useState<string[]>([]);
  const [query, setQuery] = useState('');
  const [concurrency, setConcurrency] = useState(2);
  const [showKey, setShowKey] = useState(false);
  const [loadingModels, setLoadingModels] = useState(false);
  const [running, setRunning] = useState(false);
  const [progress, setProgress] = useState<ProgressEvent | null>(null);
  const [cancelling, setCancelling] = useState(false);
  const [cancelled, setCancelled] = useState(false);
  // 用 ref 而非 state：runAll 的 catch 里要读它，state 在闭包里是旧值
  const cancelledRef = useRef(false);
  const [error, setError] = useState('');
  const [report, setReport] = useState<BatchReport | null>(null);

  const connection = connections[provider];
  const providerMeta = PROVIDERS[provider];
  const providerModels = models[provider] || [];
  const filteredModels = useMemo(() => {
    const normalized = query.trim().toLowerCase();
    if (!normalized) return providerModels;
    return providerModels.filter((item) => item.id.toLowerCase().includes(normalized) || item.name.toLowerCase().includes(normalized));
  }, [providerModels, query]);
  const selectedSet = useMemo(() => new Set(selectedModels), [selectedModels]);
  const estimatedRequests = selectedModels.length * REQUESTS_PER_MODEL;
  const progressValue = progress?.total ? progress.index / progress.total : 0;
  const hasCredentials = Boolean(connection.baseUrl.trim() && connection.apiKey.trim());

  useEffect(() => {
    let active = true;
    let dispose: undefined | (() => void);
    listen<ProgressEvent>('healthcheck-progress', (event) => setProgress(event.payload))
      .then((value) => { if (active) dispose = value; else value(); })
      .catch(() => {});
    return () => { active = false; dispose?.(); };
  }, []);

  useEffect(() => {
    const bases = Object.fromEntries((Object.keys(connections) as Provider[]).map((item) => [item, connections[item].baseUrl]));
    localStorage.setItem(BASES_KEY, JSON.stringify(bases));
  }, [connections]);

  const credentials = (): Credentials => ({ provider, baseUrl: connection.baseUrl.trim(), apiKey: connection.apiKey.trim() });
  const updateConnection = (patch: Partial<Connections[Provider]>) =>
    setConnections((current) => ({ ...current, [provider]: { ...current[provider], ...patch } }));

  const selectProvider = (next: Provider) => {
    setProvider(next);
    setSelectedModels([]);
    setQuery('');
    setError('');
    setReport(null);
  };

  const fetchModels = async () => {
    setLoadingModels(true);
    setError('');
    try {
      const list = await invoke<ModelInfo[]>('list_models', { request: { credentials: credentials() } });
      setModels((current) => ({ ...current, [provider]: list }));
      setSelectedModels(list.map((item) => item.id));
      if (list.length === 0) setError(t.errNoContentModel);
      else setStep('models');
    } catch (nextError) {
      setError(errorText(nextError, t.errGeneric));
    } finally {
      setLoadingModels(false);
    }
  };

  const toggleModel = (id: string) =>
    setSelectedModels((current) => current.includes(id) ? current.filter((item) => item !== id) : [...current, id]);

  const runAll = async () => {
    if (selectedModels.length === 0) { setError(t.errNoModel); return; }
    setRunning(true);
    setReport(null);
    setError('');
    setCancelled(false);
    setStep('report');
    setProgress({
      index: 0, total: estimatedRequests, probe: 'starting',
      // phase 留空，让 progressLabel 退回 t.running，避免侧车首个事件到达前显示模型名
      phase: '', model: '', message: '',
    });
    try {
      const next = await invoke<BatchReport>('run_all_healthchecks', {
        // lang 决定 PDF 报告与文件名的语言
        request: { credentials: credentials(), models: selectedModels, concurrency, lang: locale },
      });
      setReport(next);
    } catch (nextError) {
      // 用户主动终止时侧车被杀，run_all 必然报错。那不是故障，
      // 所以只在非取消路径上展示错误。
      if (cancelledRef.current) setCancelled(true);
      else setError(errorText(nextError, t.errGeneric));
    } finally {
      setRunning(false);
      setCancelling(false);
      cancelledRef.current = false;
    }
  };

  const cancelRun = async () => {
    setCancelling(true);
    cancelledRef.current = true;
    try {
      await invoke('cancel_healthcheck');
    } catch {
      // 侧车可能刚好自己结束了，无需打扰用户
    }
  };

  // 步骤可达性：不允许跳到尚未满足前置条件的步骤
  const reachable = (target: Step) => {
    if (running) return target === 'report';
    switch (target) {
      case 'provider': return true;
      case 'connect': return true;
      case 'models': return providerModels.length > 0;
      case 'report': return Boolean(report);
    }
  };

  const stepKeys = STEP_KEYS[step];
  const title = t[stepKeys.title];
  const hint = t[stepKeys.hint];

  /**
   * 侧车只发机器可读的 phase 与 probe key，文案在这里本地化。
   * 认不出的 phase 退回侧车的英文 message，总比空白好。
   */
  const progressLabel = (event: ProgressEvent | null) => {
    if (!event) return t.running;
    const probe = t.probes[event.probe] || event.probe;
    switch (event.phase) {
      case 'starting': return t.phaseStarting(event.model);
      case 'probe': return t.phaseProbe(event.model, probe);
      case 'probeFailed': return t.phaseProbeFailed(event.model, probe);
      case 'done': return t.phaseDone(event.model);
      default: return event.message || t.running;
    }
  };

  return (
    <div className={styles.shell}>
      <div className={styles.workspace}>
        <header className={mergeClasses(styles.topbar, styles.glass, styles.topbarEnter)}>
          <div className={styles.brand}>
            <div className={styles.brandMark}><Pulse size={17} weight='bold' /></div>
            <div className={styles.brandText}>
              <Subtitle2>{t.appName}</Subtitle2>
              <Caption1 className={styles.navHint}>{t.appTagline}</Caption1>
            </div>
          </div>
          <TabList
            selectedValue={step}
            onTabSelect={(_, data) => setStep(data.value as Step)}
            size='small'
            appearance='subtle'
            className={styles.topbarTabs}
          >
            {STEPS.map((item, index) => (
              <Tab key={item} value={item} disabled={!reachable(item)}
                icon={reachable(item) && STEPS.indexOf(step) > index
                  ? <CheckCircle size={15} weight='fill' />
                  : undefined}
              >
                {t[STEP_KEYS[item].label]}
              </Tab>
            ))}
          </TabList>
          <Select
            size='small'
            value={locale}
            aria-label={t.language}
            className={styles.langPicker}
            onChange={(_, data) => setLocale(data.value as Locale)}
          >
            {LOCALES.map((item) => (
              <option key={item} value={item}>
                {LOCALE_LABELS[item]}
              </option>
            ))}
          </Select>
        </header>

        <main className={mergeClasses(styles.step, styles.glass, styles.stepEnter)} key={step}>
          <div className={styles.stepHead}>
            <Title3>{title}</Title3>
            <Body1 className={styles.navHint}>{hint}</Body1>
          </div>

          {/* 上游报错可能很长，必须多行否则会被截断 */}
          {error && (
            <MessageBar intent='error' layout='multiline'>
              <MessageBarBody className={styles.wrapAnywhere}>
                <MessageBarTitle>{t.errorTitle}</MessageBarTitle>
                {error}
              </MessageBarBody>
            </MessageBar>
          )}

          {step === 'provider' && (
            <>
              <div className={styles.providerGrid}>
                {(Object.keys(PROVIDERS) as Provider[]).map((item, index) => {
                  const info = PROVIDERS[item];
                  const Icon = info.Icon;
                  const on = provider === item;
                  return (
                    <Card
                      key={item}
                      appearance={on ? 'filled-alternative' : 'outline'}
                      style={{ '--i': index } as CSSProperties}
                      className={mergeClasses(
                        styles.providerCard, styles.stagger, styles.pressable,
                        on && styles.providerCardOn,
                      )}
                      onClick={() => selectProvider(item)}
                      onKeyDown={(event) => { if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); selectProvider(item); } }}
                      tabIndex={0}
                      role='radio'
                      aria-checked={on}
                    >
                      <CardHeader
                        image={<Icon size={26} weight={on ? 'fill' : 'light'} />}
                        header={<Subtitle2>{info.label}</Subtitle2>}
                        // 两个 OpenAI 卡靠 protocol 区分，所以协议名不能省
                        description={<Caption1 className={styles.navHint}>{info.protocol}</Caption1>}
                        action={on ? <CheckCircle size={19} weight='fill' /> : undefined}
                      />
                    </Card>
                  );
                })}
              </div>

              <div className={styles.stepNav}>
                <Button appearance='primary' icon={<ArrowRight size={17} />} iconPosition='after' onClick={() => setStep('connect')}>
                  {t.next}
                </Button>
              </div>
            </>
          )}

          {step === 'connect' && (
            <>
              <div className={styles.formGrid}>
                <Field label={t.baseUrl} hint={t.baseUrlHint}>
                  <Input
                    type='url'
                    value={connection.baseUrl}
                    onChange={(event) => updateConnection({ baseUrl: event.target.value })}
                    contentBefore={<LinkSimple size={17} weight='light' />}
                    spellCheck={false}
                  />
                </Field>
                <Field label={t.apiKey} hint={t.apiKeyHint}>
                  <Input
                    type={showKey ? 'text' : 'password'}
                    value={connection.apiKey}
                    onChange={(event) => updateConnection({ apiKey: event.target.value })}
                    autoComplete='off'
                    placeholder={t.apiKeyPlaceholder}
                    contentBefore={<Key size={17} weight='light' />}
                    contentAfter={
                      <Tooltip content={showKey ? t.hideKey : t.showKey} relationship='label'>
                        <Button
                          appearance='transparent' size='small'
                          icon={showKey ? <EyeSlash size={16} weight='light' /> : <Eye size={16} weight='light' />}
                          onClick={() => setShowKey((value) => !value)}
                        />
                      </Tooltip>
                    }
                  />
                </Field>
              </div>

              {loadingModels && (
                <Skeleton aria-label={t.fetchingModels}>
                  <div className={styles.colGap}>
                    <SkeletonItem size={16} />
                    <SkeletonItem size={16} />
                    <SkeletonItem size={16} />
                  </div>
                </Skeleton>
              )}

              <div className={styles.stepNav}>
                <Button appearance='subtle' icon={<ArrowLeft size={17} />} onClick={() => setStep('provider')}>
                  {t.prev}
                </Button>
                <Caption1 className={styles.navHint}>
                  {providerModels.length
                    ? t.loadedModels(providerModels.length)
                    : t.willConnect(`${providerMeta.label} ${providerMeta.protocol}`)}
                </Caption1>
                <Button
                  appearance='primary'
                  icon={loadingModels ? <Spinner size='tiny' /> : <ArrowRight size={17} />}
                  iconPosition='after'
                  onClick={fetchModels}
                  disabled={loadingModels || !hasCredentials}
                >
                  {loadingModels ? t.fetching : t.fetchModels}
                </Button>
              </div>
            </>
          )}

          {step === 'models' && (
            <>
              <div className={styles.row}>
                <SearchBox
                  placeholder={t.searchModels}
                  value={query}
                  onChange={(_, data) => setQuery(data.value)}
                  className={styles.grow}
                />
                <Button appearance='subtle' onClick={() => setSelectedModels(providerModels.map((item) => item.id))}>
                  {t.selectAll}
                </Button>
                <Button appearance='subtle' onClick={() => setSelectedModels([])}>{t.clearAll}</Button>
              </div>

              <div className={styles.row}>
                <Badge appearance='tint' color='brand'>
                  {t.selectedCount(selectedModels.length, providerModels.length)}
                </Badge>
                <Badge appearance='tint' color='informative'>
                  {t.estimatedRequests(estimatedRequests.toLocaleString())}
                </Badge>
              </div>

              <div className={styles.modelGrid} role='group' aria-label={t.titleModels}>
                {filteredModels.map((item) => {
                  const on = selectedSet.has(item.id);
                  return (
                    <div key={item.id} className={mergeClasses(styles.modelItem, on && styles.modelItemOn)}>
                      <Checkbox
                        checked={on}
                        onChange={() => toggleModel(item.id)}
                        label={
                          <span className={mergeClasses(styles.ellipsis, styles.mono)} title={item.id}>
                            {item.id}
                          </span>
                        }
                      />
                    </div>
                  );
                })}
                {filteredModels.length === 0 && (
                  <Caption1 className={styles.navHint}>{t.noMatch(query)}</Caption1>
                )}
              </div>

              <Divider />
              <Field label={t.concurrency} hint={t.concurrencyHint}>
                <div className={styles.sliderRow}>
                  <Slider
                    min={1}
                    max={MAX_CONCURRENCY}
                    step={1}
                    value={concurrency}
                    aria-label={t.concurrency}
                    aria-valuetext={String(concurrency)}
                    onChange={(_, data) => setConcurrency(data.value)}
                    className={styles.slider}
                  />
                  <Badge
                    appearance='tint'
                    color={concurrency > HIGH_CONCURRENCY ? 'warning' : 'brand'}
                    className={styles.sliderValue}
                  >
                    {concurrency}
                  </Badge>
                </div>
              </Field>
              {/* 两条警告叠在一起太吵，合并成一条：高并发时才追加限流失真的说明。
                  layout='multiline' 是必需的，默认单行布局会把长文案直接截断。 */}
              <MessageBar intent='warning' layout='multiline'>
                <MessageBarBody>
                  {t.costWarning}
                  {concurrency > HIGH_CONCURRENCY && ` ${t.concurrencyHighWarning}`}
                </MessageBarBody>
              </MessageBar>

              <div className={styles.stepNav}>
                <Button appearance='subtle' icon={<ArrowLeft size={17} />} onClick={() => setStep('connect')}>
                  {t.prev}
                </Button>
                <Button
                  appearance='primary'
                  icon={<Play size={17} weight='fill' />}
                  onClick={runAll}
                  disabled={selectedModels.length === 0}
                >
                  {t.startRun(selectedModels.length)}
                </Button>
              </div>
            </>
          )}

          {step === 'report' && (
            <>
              {running && (
                <div className={styles.runView} aria-live='polite'>
                  <Spinner size='huge' label={progressLabel(progress)} />
                  <div className={styles.runMeter}>
                    <ProgressBar value={progressValue} thickness='large' />
                    <Caption1 className={styles.navHint}>
                      {t.runMeter(progress?.index ?? 0, progress?.total ?? estimatedRequests, concurrency)}
                    </Caption1>
                  </div>
                  <Button
                    appearance='secondary'
                    icon={<StopCircle size={17} weight='fill' />}
                    onClick={cancelRun}
                    disabled={cancelling}
                  >
                    {cancelling ? t.cancelling : t.cancelRun}
                  </Button>
                </div>
              )}

              {!running && report && <ReportView report={report} onRetest={runAll} />}

              {!running && !report && (
                <>
                  {cancelled ? (
                    <MessageBar intent='warning' layout='multiline'>
                      <MessageBarBody>{t.cancelled}</MessageBarBody>
                    </MessageBar>
                  ) : (
                    <Body1 className={styles.navHint}>{t.noReport}</Body1>
                  )}
                  <div className={styles.stepNav}>
                    <Button appearance='subtle' icon={<ArrowLeft size={17} />} onClick={() => setStep('models')}>
                      {t.backToModels}
                    </Button>
                  </div>
                </>
              )}
            </>
          )}
        </main>
      </div>
    </div>
  );
}
