export type Provider = 'openai' | 'openai-responses' | 'anthropic' | 'google';

export interface Credentials {
  provider: Provider;
  baseUrl: string;
  apiKey: string;
}

export interface ModelInfo {
  id: string;
  name: string;
  ownedBy?: string;
}

export interface ProgressEvent {
  index: number;
  total: number;
  probe: string;
  /** 机器可读阶段，前端据此本地化文案。侧车不知道界面语言。 */
  phase: string;
  model: string;
  /** 侧车的英文兜底文案，仅在 phase 无法识别时展示。 */
  message: string;
}

export interface ProbeResult {
  key: string;
  kind: string;
  label: string;
  description: string;
  status: 'pass' | 'warn' | 'fail' | 'skip' | 'error';
  weight: number;
  durationMs: number;
  evidence: Record<string, unknown>;
}

export interface BatchProbeResult {
  probeKey: string;
  kind: string;
  status: 'pass' | 'warn' | 'fail' | 'skip' | 'error';
  evidence: Record<string, unknown>;
  latencyMs: number;
}

export interface ModelReport {
  id: number;
  model: string;
  trustScore: number;
  verdict: 'OK' | 'SUSPICIOUS' | 'WATERED' | 'INCONCLUSIVE';
  requestCount: number;
  promptTokens: number;
  completionTokens: number;
  totalTokens: number;
  durationMs: number;
  error?: string;
  results: BatchProbeResult[];
}

export interface BatchReport {
  id: string;
  provider: Provider;
  providerLabel: string;
  baseUrl: string;
  startedAt: string;
  finishedAt: string;
  durationMs: number;
  totalModels: number;
  completedModels: number;
  failedModels: number;
  estimatedRequests: number;
  completedRequests: number;
  trustScore: number;
  verdict: 'OK' | 'SUSPICIOUS' | 'WATERED' | 'INCONCLUSIVE';
  pdfPath: string;
  models: ModelReport[];
}

export interface HealthReport {
  id: string;
  provider: Provider;
  providerLabel: string;
  baseUrl: string;
  model: string;
  startedAt: string;
  finishedAt: string;
  durationMs: number;
  trustScore: number;
  verdict: 'OK' | 'SUSPICIOUS' | 'WATERED' | 'INCONCLUSIVE';
  requestCount: number;
  promptTokens: number;
  completionTokens: number;
  results: ProbeResult[];
}
