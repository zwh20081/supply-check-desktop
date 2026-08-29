import { useMemo, useState } from 'react';
import { invoke } from '@tauri-apps/api/core';
import {
  Accordion, AccordionHeader, AccordionItem, AccordionPanel, Badge, Body1, Button,
  Caption1, Card, CardHeader, Divider, MessageBar, MessageBarBody, Subtitle2, Title3,
  Tooltip, mergeClasses,
} from '@fluentui/react-components';
import {
  ArrowCounterClockwise, CheckCircle, FilePdf, MinusCircle, WarningCircle, XCircle,
} from '@phosphor-icons/react';
import { useAppStyles } from '../useAppStyles';
import { useI18n } from '../i18n';
import type { Messages } from '../i18n/messages';
import type { BatchProbeResult, BatchReport } from '../types';

type BadgeColor = 'success' | 'warning' | 'danger' | 'subtle';

/** 只取值为 string 的词条键。 */
type TextKey = { [K in keyof Messages]: Messages[K] extends string ? K : never }[keyof Messages];

/** 判定与状态的颜色固定，文案走词条表。 */
const VERDICT_COLORS: Record<string, { color: BadgeColor; key: TextKey }> = {
  OK: { color: 'success', key: 'verdictOK' },
  SUSPICIOUS: { color: 'warning', key: 'verdictSuspicious' },
  WATERED: { color: 'danger', key: 'verdictWatered' },
  INCONCLUSIVE: { color: 'subtle', key: 'verdictInconclusive' },
};

const STATUS_META = {
  pass: { color: 'success', key: 'statusPass', Icon: CheckCircle },
  warn: { color: 'warning', key: 'statusWarn', Icon: WarningCircle },
  fail: { color: 'danger', key: 'statusFail', Icon: XCircle },
  skip: { color: 'subtle', key: 'statusSkip', Icon: MinusCircle },
  error: { color: 'subtle', key: 'statusError', Icon: WarningCircle },
} as const satisfies Record<string, { color: BadgeColor; key: TextKey; Icon: typeof CheckCircle }>;

interface Props { report: BatchReport; onRetest: () => void; }

export default function ReportView({ report, onRetest }: Props) {
  const styles = useAppStyles();
  const { t } = useI18n();

  const formatDuration = (milliseconds: number) =>
    milliseconds < 1000 ? `${milliseconds} ms` : t.seconds((milliseconds / 1000).toFixed(1));

  const verdictOf = (value: string) => {
    const entry = VERDICT_COLORS[value] ?? VERDICT_COLORS.INCONCLUSIVE;
    return { color: entry.color, label: t[entry.key] };
  };
  const [selectedID, setSelectedID] = useState(report.models[0]?.id ?? 0);
  const selected = useMemo(
    () => report.models.find((item) => item.id === selectedID) ?? report.models[0],
    [report.models, selectedID],
  );
  const verdict = verdictOf(report.verdict);

  const metrics = [
    { label: t.avgScore, value: String(report.trustScore) },
    { label: t.duration, value: formatDuration(report.durationMs) },
    { label: t.upstreamRequests, value: String(report.completedRequests) },
    { label: t.okModels, value: String(report.completedModels) },
    { label: t.failedModels, value: String(report.failedModels) },
  ];

  return (
    <div className={styles.colGap}>
      <div className={styles.row}>
        <Title3>{t.reportTitle(report.providerLabel)}</Title3>
        <Badge appearance='filled' color={verdict.color} size='large'>{verdict.label}</Badge>
        <div className={styles.grow} />
        <Button appearance='secondary' icon={<ArrowCounterClockwise size={16} />} onClick={onRetest}>
          {t.retest}
        </Button>
        <Tooltip content={report.pdfPath || t.noPdf} relationship='label'>
          <Button
            appearance='primary'
            icon={<FilePdf size={16} weight='fill' />}
            onClick={() => invoke('open_pdf', { path: report.pdfPath })}
            disabled={!report.pdfPath}
          >
            {t.openPdf}
          </Button>
        </Tooltip>
      </div>

      <Caption1 className={styles.navHint}>{t.reportSubtitle(report.totalModels)}</Caption1>

      <div className={styles.metricGrid}>
        {metrics.map((item) => (
          <div key={item.label} className={styles.metric}>
            <Caption1 className={styles.navHint}>{item.label}</Caption1>
            <Subtitle2>{item.value}</Subtitle2>
          </div>
        ))}
      </div>

      <Divider />

      <div className={styles.reportSplit}>
        <Card appearance='subtle' size='small'>
          <CardHeader header={<Subtitle2>{t.modelResults}</Subtitle2>}
            description={<Caption1 className={styles.navHint}>{t.countUnit(report.models.length)}</Caption1>} />
          <nav className={styles.modelNav}>
            {report.models.map((item) => {
              const tone = verdictOf(item.verdict);
              return (
                <button
                  key={item.id}
                  type='button'
                  onClick={() => setSelectedID(item.id)}
                  className={mergeClasses(styles.modelNavItem, item.id === selected?.id && styles.modelNavOn)}
                >
                  <span className={mergeClasses(styles.ellipsis, styles.mono)} title={item.model}>
                    {item.model}
                  </span>
                  <Badge appearance='tint' color={tone.color} size='small'>
                    {item.error ? t.errorLabel : item.trustScore}
                  </Badge>
                </button>
              );
            })}
          </nav>
        </Card>

        {selected && (
          <div className={styles.colGap}>
            <div className={styles.row}>
              <Subtitle2 className={styles.mono}>{selected.model}</Subtitle2>
              <Badge appearance='tint' color={verdictOf(selected.verdict).color}>
                {verdictOf(selected.verdict).label}
              </Badge>
              <div className={styles.grow} />
              <Caption1 className={styles.navHint}>
                {t.probeSummary(selected.results.length, formatDuration(selected.durationMs))}
              </Caption1>
            </div>

            {/* 侧车回传的错误常带堆栈或 URL，必须多行 */}
            {selected.error && (
              <MessageBar intent='error' layout='multiline'>
                <MessageBarBody className={styles.wrapAnywhere}>{selected.error}</MessageBarBody>
              </MessageBar>
            )}

            <Accordion collapsible multiple>
              {selected.results.map((result) => {
                const status = STATUS_META[result.status] ?? STATUS_META.error;
                const Icon = status.Icon;
                return (
                  <AccordionItem value={result.probeKey} key={result.probeKey}>
                    <AccordionHeader expandIconPosition='end' icon={<Icon size={17} weight='fill' />}>
                      <div className={styles.row}>
                        <Body1>{t.probes[result.kind] || result.kind}</Body1>
                        <Badge appearance='tint' color={status.color} size='small'>
                          {t[status.key]}
                        </Badge>
                        {result.latencyMs > 0 && (
                          <Caption1 className={styles.navHint}>{formatDuration(result.latencyMs)}</Caption1>
                        )}
                      </div>
                    </AccordionHeader>
                    <AccordionPanel>
                      <Evidence result={result} className={styles.evidence} />
                    </AccordionPanel>
                  </AccordionItem>
                );
              })}
            </Accordion>
          </div>
        )}
      </div>
    </div>
  );
}

function Evidence({ result, className }: { result: BatchProbeResult; className: string }) {
  return <pre className={className}>{JSON.stringify(result.evidence, null, 2)}</pre>;
}
