package healthcheck

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"supply-check-sdk/internal/i18n"
	"supply-check-sdk/internal/model"
)

const (
	reportPDFWidth        = 595.28
	reportPDFHeight       = 841.89
	reportPDFMargin       = 50.0
	reportPDFContentWidth = reportPDFWidth - 2*reportPDFMargin
	reportPDFBottom       = 58.0
)

// HealthCheckReport contains the frozen data used to build one batch PDF.
// Runs is keyed by ProbeRun.Id so report generation has no database dependency.
type HealthCheckReport struct {
	Job   *model.HealthCheckJob
	Tasks []*model.HealthCheckTask
	Runs  map[int]*model.ProbeRun
}

// BuildProbeRunPDF creates a self-contained report for one channel/model run.
func BuildProbeRunPDF(run *model.ProbeRun, lang string) ([]byte, error) {
	if run == nil || run.Id <= 0 {
		return nil, errors.New("health check PDF report requires a probe run")
	}
	results := reportProbeResults(run.Results)
	doc := newPDFReport(lang)
	doc.addCover(
		trReport(lang, i18n.MsgHealthCheckReportTitle),
		trReport(lang, i18n.MsgHealthCheckReportSingleSubtitle),
	)
	doc.addMetadata([]reportField{
		{trReport(lang, i18n.MsgHealthCheckReportFieldReportID), strconv.Itoa(run.Id)},
		{trReport(lang, i18n.MsgHealthCheckReportFieldChannel), channelDisplay(run.ChannelId, run.ChannelName)},
		{trReport(lang, i18n.MsgHealthCheckReportFieldModel), run.Model},
		{trReport(lang, i18n.MsgHealthCheckReportFieldCreatedAt), reportTime(run.CreatedAt)},
	})
	doc.addSection(trReport(lang, i18n.MsgHealthCheckReportSectionSummary))
	doc.addVerdictCallout(localizedVerdict(lang, run.Verdict), run.TrustScore, run.UpstreamCost)
	doc.addCacheRateCallout(results)
	doc.addSection(trReport(lang, i18n.MsgHealthCheckReportSectionAnalysis))
	doc.addProbeCharts(results)
	doc.addSection(trReport(lang, i18n.MsgHealthCheckReportSectionDetails))
	doc.addProbeTable(results)
	doc.addEvidence(results)
	return doc.bytes(), nil
}

// BuildHealthCheckJobPDF creates a batch summary followed by evidence for each
// target that produced a persisted ProbeRun.
func BuildHealthCheckJobPDF(report HealthCheckReport, lang string) ([]byte, error) {
	if report.Job == nil || strings.TrimSpace(report.Job.ID) == "" {
		return nil, errors.New("health check PDF report requires a job")
	}
	report.Runs = reportRunsWithoutRemovedProbes(report.Runs)
	doc := newPDFReport(lang)
	doc.addCover(
		trReport(lang, i18n.MsgHealthCheckReportTitle),
		trReport(lang, i18n.MsgHealthCheckReportBatchSubtitle),
	)
	doc.addMetadata([]reportField{
		{trReport(lang, i18n.MsgHealthCheckReportFieldReportID), report.Job.ID},
		{trReport(lang, i18n.MsgHealthCheckReportFieldStatus), localizedJobStatus(lang, report.Job.Status)},
		{trReport(lang, i18n.MsgHealthCheckReportFieldCreatedAt), reportTime(report.Job.CreatedAt)},
		{trReport(lang, i18n.MsgHealthCheckReportFieldFinishedAt), reportTime(report.Job.FinishedAt)},
	})
	doc.addSection(trReport(lang, i18n.MsgHealthCheckReportSectionSummary))
	doc.addJobSummary(report.Job)
	doc.addBatchCacheRateCallout(report.Tasks, report.Runs)
	doc.addSection(trReport(lang, i18n.MsgHealthCheckReportSectionAnalysis))
	doc.addBatchCharts(report.Tasks, report.Runs)
	doc.addSection(trReport(lang, i18n.MsgHealthCheckReportSectionTargets))
	doc.addTaskTable(report.Tasks, report.Runs)

	for index, task := range report.Tasks {
		run := report.Runs[task.ProbeRunID]
		if run == nil {
			continue
		}
		doc.newPage()
		doc.addSection(fmt.Sprintf("%d. %s / %s", index+1, channelDisplay(task.ChannelID, task.ChannelName), task.Model))
		doc.addVerdictCallout(localizedVerdict(lang, run.Verdict), run.TrustScore, run.UpstreamCost)
		doc.addCacheRateCallout(run.Results)
		doc.addSection(trReport(lang, i18n.MsgHealthCheckReportSectionAnalysis))
		doc.addProbeCharts(run.Results)
		doc.addSection(trReport(lang, i18n.MsgHealthCheckReportSectionDetails))
		doc.addProbeTable(run.Results)
		doc.addEvidence(run.Results)
	}
	return doc.bytes(), nil
}

func reportProbeResults(results model.ProbeResultList) model.ProbeResultList {
	filtered := make(model.ProbeResultList, 0, len(results))
	for _, result := range results {
		if result.Kind == "cache_hit" || result.ProbeKey == "p7_cache_hit" {
			continue
		}
		filtered = append(filtered, result)
	}
	return filtered
}

func reportRunsWithoutRemovedProbes(runs map[int]*model.ProbeRun) map[int]*model.ProbeRun {
	filtered := make(map[int]*model.ProbeRun, len(runs))
	for id, run := range runs {
		if run == nil {
			continue
		}
		clone := *run
		clone.Results = reportProbeResults(run.Results)
		filtered[id] = &clone
	}
	return filtered
}

type reportField struct {
	label string
	value string
}

type pdfReport struct {
	lang  string
	pages []*pdfReportPage
	page  *pdfReportPage
	y     float64
}

type pdfReportPage struct {
	content strings.Builder
}

type pdfColor struct{ r, g, b float64 }

var (
	colorNavy      = pdfColor{0.043, 0.145, 0.271}
	colorBlue      = pdfColor{0.180, 0.455, 0.710}
	colorText      = pdfColor{0.125, 0.145, 0.180}
	colorMuted     = pdfColor{0.400, 0.440, 0.500}
	colorBorder    = pdfColor{0.820, 0.840, 0.870}
	colorLightFill = pdfColor{0.950, 0.965, 0.980}
	colorCallout   = pdfColor{0.957, 0.969, 0.984}
	colorPass      = pdfColor{0.180, 0.610, 0.420}
	colorWarn      = pdfColor{0.945, 0.640, 0.160}
	colorFail      = pdfColor{0.855, 0.245, 0.275}
	colorSkip      = pdfColor{0.570, 0.610, 0.670}
	colorError     = pdfColor{0.455, 0.360, 0.690}
)

func newPDFReport(lang string) *pdfReport {
	r := &pdfReport{lang: lang}
	r.newPage()
	return r
}

func (r *pdfReport) newPage() {
	page := &pdfReportPage{}
	r.pages = append(r.pages, page)
	r.page = page
	r.y = reportPDFHeight - reportPDFMargin
	if len(r.pages) > 1 {
		r.drawText(reportPDFMargin, r.y, 9, trReport(r.lang, i18n.MsgHealthCheckReportTitle), colorMuted, false)
		r.drawLine(reportPDFMargin, r.y-8, reportPDFWidth-reportPDFMargin, r.y-8, colorBorder, 0.6)
		r.y -= 30
	}
}

func (r *pdfReport) ensure(height float64) {
	if r.y-height < reportPDFBottom {
		r.newPage()
	}
}

func (r *pdfReport) addCover(title, subtitle string) {
	r.drawRect(reportPDFMargin, r.y-5, 44, 4, colorBlue, true)
	r.y -= 30
	for _, line := range wrapPDFText(title, reportPDFContentWidth, 27) {
		r.drawText(reportPDFMargin, r.y, 27, line, colorNavy, true)
		r.y -= 34
	}
	r.y -= 2
	for _, line := range wrapPDFText(subtitle, reportPDFContentWidth, 12.5) {
		r.drawText(reportPDFMargin, r.y, 12.5, line, colorMuted, false)
		r.y -= 18
	}
	r.y -= 18
}

func (r *pdfReport) addMetadata(fields []reportField) {
	const rowHeight = 31.0
	for index := 0; index < len(fields); index += 2 {
		r.ensure(rowHeight)
		left := fields[index]
		r.drawMetadataCell(reportPDFMargin, r.y-rowHeight, reportPDFContentWidth/2, rowHeight, left)
		if index+1 < len(fields) {
			r.drawMetadataCell(reportPDFMargin+reportPDFContentWidth/2, r.y-rowHeight, reportPDFContentWidth/2, rowHeight, fields[index+1])
		}
		r.y -= rowHeight
	}
	r.y -= 10
}

func (r *pdfReport) drawMetadataCell(x, y, width, height float64, field reportField) {
	r.drawRect(x, y, width, height, colorBorder, false)
	r.drawText(x+9, y+height-12, 8.2, field.label, colorMuted, true)
	lines := wrapPDFText(field.value, width-18, 9.5)
	if len(lines) > 1 {
		lines = lines[:1]
	}
	r.drawText(x+9, y+7, 9.5, firstOrEmpty(lines), colorText, false)
}

func (r *pdfReport) addSection(title string) {
	const (
		topGap           = 20.0
		lineHeight       = 21.0
		bottomGap        = 8.0
		followingReserve = 82.0
	)
	lines := wrapPDFText(title, reportPDFContentWidth, 16)
	height := topGap + float64(len(lines))*lineHeight + bottomGap
	// Keep both clear space above the heading and enough room below it for the
	// first meaningful row/callout. Besides avoiding collisions, this prevents
	// section headings from being orphaned at the bottom of a page.
	r.ensure(height + followingReserve)
	r.y -= topGap
	for _, line := range lines {
		r.drawText(reportPDFMargin, r.y, 16, line, colorBlue, true)
		r.y -= lineHeight
	}
	r.drawLine(reportPDFMargin, r.y+7, reportPDFWidth-reportPDFMargin, r.y+7, colorBorder, 0.7)
	r.y -= bottomGap
}

func (r *pdfReport) addVerdictCallout(verdict string, score, cost int) {
	text := trReport(r.lang, i18n.MsgHealthCheckReportSummaryLine, map[string]any{
		"Verdict": verdict,
		"Score":   score,
		"Cost":    reportCostDisplay(cost),
	})
	lines := wrapPDFText(text, reportPDFContentWidth-32, 12)
	height := 24 + float64(len(lines))*17
	r.ensure(height + 8)
	r.drawRect(reportPDFMargin, r.y-height, reportPDFContentWidth, height, colorCallout, true)
	r.drawRect(reportPDFMargin, r.y-height, 4, height, colorBlue, true)
	ty := r.y - 22
	for _, line := range lines {
		r.drawText(reportPDFMargin+18, ty, 12, line, colorNavy, true)
		ty -= 17
	}
	r.y -= height + 8
}

type reportCacheRate struct {
	rate         float64
	hitPrompts   int
	prompts      int
	warmLoops    int
	cachedTokens int
	promptTokens int
	items        []reportCacheRateItem
	telemetry    bool
	available    bool
}

type reportCacheRateItem struct {
	label        string
	rate         float64
	cachedTokens int
	totalTokens  int
}

type reportTargetCacheRate struct {
	label  string
	metric reportCacheRate
}

type reportBatchCacheRate struct {
	validTargets     int
	telemetryTargets int
	cachedTokens     int
	promptTokens     int
	targets          []reportTargetCacheRate
}

func cacheRateFromResults(results model.ProbeResultList) reportCacheRate {
	for _, result := range results {
		if result.Kind != model.ProbeKindCacheRate {
			continue
		}
		metric := reportCacheRate{
			rate:         evidenceNumber(result.Evidence["cache_rate_pct"]),
			hitPrompts:   int(evidenceNumber(result.Evidence["hit_warm_samples"])),
			prompts:      int(evidenceNumber(result.Evidence["expected_warm_samples"])),
			warmLoops:    int(evidenceNumber(result.Evidence["warm_loops"])),
			cachedTokens: int(evidenceNumber(result.Evidence["warm_cached_tokens"])),
			promptTokens: int(evidenceNumber(result.Evidence["warm_total_input_tokens"])),
		}
		if metric.hitPrompts == 0 {
			metric.hitPrompts = int(evidenceNumber(result.Evidence["hit_prompts"]))
		}
		if metric.prompts == 0 {
			metric.prompts = int(evidenceNumber(result.Evidence["prompt_variants"]))
		}
		if metric.warmLoops == 0 {
			metric.warmLoops = 1
		}
		if metric.promptTokens == 0 {
			metric.promptTokens = int(evidenceNumber(result.Evidence["warm_prompt_tokens"]))
		}
		itemIndexes := make(map[string]int)
		for _, sample := range evidenceMaps(result.Evidence["samples"]) {
			if fmt.Sprint(sample["role"]) != "warm" {
				continue
			}
			cachedTokens := int(evidenceNumber(sample["cached_tokens"]))
			totalTokens := int(evidenceNumber(sample["total_input_tokens"]))
			if totalTokens == 0 {
				totalTokens = int(evidenceNumber(sample["prompt_tokens"]))
			}
			label := fmt.Sprint(sample["prompt_id"])
			index, exists := itemIndexes[label]
			if !exists {
				index = len(metric.items)
				itemIndexes[label] = index
				metric.items = append(metric.items, reportCacheRateItem{label: label})
			}
			metric.items[index].cachedTokens += cachedTokens
			metric.items[index].totalTokens += totalTokens
		}
		for index := range metric.items {
			metric.items[index].rate = percentReport(metric.items[index].cachedTokens, metric.items[index].totalTokens)
		}
		metric.telemetry = int(evidenceNumber(result.Evidence["reported_warm_samples"])) > 0
		metric.available = metric.telemetry && metric.promptTokens > 0
		return metric
	}
	return reportCacheRate{}
}

func percentReport(numerator, denominator int) float64 {
	if denominator <= 0 {
		return 0
	}
	return float64(numerator) / float64(denominator) * 100
}

func evidenceNumber(value any) float64 {
	switch number := value.(type) {
	case float64:
		return number
	case float32:
		return float64(number)
	case int:
		return float64(number)
	case int64:
		return float64(number)
	case int32:
		return float64(number)
	case uint:
		return float64(number)
	case uint64:
		return float64(number)
	default:
		parsed, _ := strconv.ParseFloat(fmt.Sprint(value), 64)
		return parsed
	}
}

func (r *pdfReport) addCacheRateCallout(results model.ProbeResultList) {
	metric := cacheRateFromResults(results)
	if !metric.available {
		return
	}
	summary := trReport(r.lang, i18n.MsgHealthCheckReportCacheRateSingleSummary, map[string]any{
		"Hits": metric.hitPrompts, "Prompts": metric.prompts,
		"Loops":  metric.warmLoops,
		"Cached": metric.cachedTokens, "Tokens": metric.promptTokens,
	})
	r.drawCacheRateCallout(metric.rate, summary, metric.items)
}

func (r *pdfReport) addBatchCacheRateCallout(tasks []*model.HealthCheckTask, runs map[int]*model.ProbeRun) {
	metric := batchCacheRateFromRuns(tasks, runs)
	if metric.validTargets == 0 || metric.promptTokens <= 0 {
		return
	}
	rate := float64(metric.cachedTokens) / float64(metric.promptTokens) * 100
	summary := trReport(r.lang, i18n.MsgHealthCheckReportCacheRateBatchSummary, map[string]any{
		"Covered": metric.validTargets, "Telemetry": metric.telemetryTargets, "Targets": len(tasks),
		"Cached": metric.cachedTokens, "Tokens": metric.promptTokens,
	})
	r.drawCacheRateCallout(rate, summary, nil)
	r.addBatchCacheRateTable(metric.targets)
}

func batchCacheRateFromRuns(tasks []*model.HealthCheckTask, runs map[int]*model.ProbeRun) reportBatchCacheRate {
	metric := reportBatchCacheRate{targets: make([]reportTargetCacheRate, 0, len(tasks))}
	for _, task := range tasks {
		run := runs[task.ProbeRunID]
		if run == nil {
			continue
		}
		if hasCacheTelemetry(run.Results) {
			metric.telemetryTargets++
		}
		targetMetric := cacheRateFromResults(run.Results)
		if !targetMetric.available {
			continue
		}
		metric.validTargets++
		metric.cachedTokens += targetMetric.cachedTokens
		metric.promptTokens += targetMetric.promptTokens
		metric.targets = append(metric.targets, reportTargetCacheRate{
			label: task.Model, metric: targetMetric,
		})
	}
	return metric
}

func (r *pdfReport) drawCacheRateCallout(rate float64, summary string, items []reportCacheRateItem) {
	const (
		titleXOffset    = 18.0
		rightXOffset    = 175.0
		titleBaseline   = 20.0
		titleLineHeight = 13.0
		bodyLineHeight  = 13.0
		itemLineHeight  = 13.0
	)
	titleLines := wrapPDFText(
		trReport(r.lang, i18n.MsgHealthCheckReportCacheRateTitle),
		reportPDFContentWidth-2*titleXOffset,
		10,
	)
	rightWidth := reportPDFContentWidth - rightXOffset - 18
	summaryLines := wrapPDFText(summary, rightWidth, 9.5)
	itemCount := min(len(items), 5)
	itemLines := make([][]string, 0, itemCount)
	itemLineCount := 0
	for _, item := range items[:itemCount] {
		label := fmt.Sprintf("%s   %.2f%%   %d/%d token", item.label, item.rate, item.cachedTokens, item.totalTokens)
		lines := wrapPDFText(label, rightWidth, 10.5)
		itemLines = append(itemLines, lines)
		itemLineCount += len(lines)
	}
	bodyBaseline := titleBaseline + float64(len(titleLines))*titleLineHeight + 10
	itemsBaseline := bodyBaseline + float64(len(summaryLines))*bodyLineHeight + 7
	bodyBottom := max(bodyBaseline+38, itemsBaseline+float64(itemLineCount)*itemLineHeight)
	height := max(92.0, bodyBottom+14)
	r.ensure(height + 10)
	r.drawRect(reportPDFMargin, r.y-height, reportPDFContentWidth, height, pdfColor{0.925, 0.980, 0.950}, true)
	r.drawRect(reportPDFMargin, r.y-height, 5, height, colorPass, true)
	ty := r.y - titleBaseline
	for _, line := range titleLines {
		r.drawText(reportPDFMargin+titleXOffset, ty, 10, line, colorMuted, true)
		ty -= titleLineHeight
	}
	r.drawText(reportPDFMargin+titleXOffset, r.y-bodyBaseline-20, 31, fmt.Sprintf("%.2f%%", rate), colorPass, true)
	ty = r.y - bodyBaseline
	for _, line := range summaryLines {
		r.drawText(reportPDFMargin+rightXOffset, ty, 9.5, line, colorText, false)
		ty -= bodyLineHeight
	}
	itemY := r.y - itemsBaseline
	for _, lines := range itemLines {
		for _, line := range lines {
			r.drawText(reportPDFMargin+rightXOffset, itemY, 10.5, line, colorNavy, true)
			itemY -= itemLineHeight
		}
	}
	r.y -= height + 10
}

func (r *pdfReport) addBatchCacheRateTable(targets []reportTargetCacheRate) {
	if len(targets) == 0 {
		return
	}
	const topGap = 18.0
	// Reserve the title and at least the table header so the subheading cannot
	// touch the callout above it or become detached from its table.
	r.ensure(topGap + 52)
	r.y -= topGap
	r.drawText(reportPDFMargin, r.y, 12, trReport(r.lang, i18n.MsgHealthCheckReportCacheRatePerTargetTitle), colorNavy, true)
	r.y -= 18
	headers := []string{
		trReport(r.lang, i18n.MsgHealthCheckReportFieldModel),
		trReport(r.lang, i18n.MsgHealthCheckReportCacheRateFieldRate),
		trReport(r.lang, i18n.MsgHealthCheckReportFieldModel),
		trReport(r.lang, i18n.MsgHealthCheckReportCacheRateFieldRate),
	}
	half := (len(targets) + 1) / 2
	rows := make([][]string, 0, half)
	for index := 0; index < half; index++ {
		row := []string{targets[index].label, fmt.Sprintf("%.2f%%", targets[index].metric.rate), "", ""}
		if right := index + half; right < len(targets) {
			row[2] = targets[right].label
			row[3] = fmt.Sprintf("%.2f%%", targets[right].metric.rate)
		}
		rows = append(rows, row)
	}
	r.addTable(headers, rows, []float64{175, 72.64, 175, 72.64}, []bool{true, true, true, true})
	r.y -= 10
}

func (r *pdfReport) addJobSummary(job *model.HealthCheckJob) {
	rows := [][]string{
		{trReport(r.lang, i18n.MsgHealthCheckReportFieldProgress), fmt.Sprintf("%d/%d", job.CompletedTasks+job.FailedTasks, job.TotalTasks)},
		{trReport(r.lang, i18n.MsgHealthCheckReportFieldVerdict), localizedVerdict(r.lang, job.Verdict)},
		{trReport(r.lang, i18n.MsgHealthCheckReportFieldTrustScore), scoreDisplay(job.Verdict, job.TrustScore)},
		{trReport(r.lang, i18n.MsgHealthCheckReportFieldUpstreamCost), reportCostDisplay(job.UpstreamCost)},
	}
	r.addTable(nil, rows, []float64{150, reportPDFContentWidth - 150}, []bool{true, false})
}

func (r *pdfReport) addTaskTable(tasks []*model.HealthCheckTask, runs map[int]*model.ProbeRun) {
	headers := []string{
		trReport(r.lang, i18n.MsgHealthCheckReportFieldChannel),
		trReport(r.lang, i18n.MsgHealthCheckReportFieldModel),
		trReport(r.lang, i18n.MsgHealthCheckReportFieldStatus),
		trReport(r.lang, i18n.MsgHealthCheckReportFieldVerdict),
		trReport(r.lang, i18n.MsgHealthCheckReportFieldTrustScore),
		trReport(r.lang, i18n.MsgHealthCheckReportFieldUpstreamCost),
		trReport(r.lang, i18n.MsgHealthCheckReportFieldProbeCount),
	}
	rows := make([][]string, 0, len(tasks))
	for _, task := range tasks {
		probeCount := 0
		if run := runs[task.ProbeRunID]; run != nil {
			probeCount = len(run.Results)
		}
		rows = append(rows, []string{
			channelDisplay(task.ChannelID, task.ChannelName), task.Model,
			localizedJobStatus(r.lang, task.Status), localizedVerdict(r.lang, task.Verdict),
			scoreDisplay(task.Verdict, task.TrustScore),
			reportCostDisplay(task.UpstreamCost), strconv.Itoa(probeCount),
		})
	}
	r.addTable(headers, rows, []float64{82, 112, 66, 72, 58, 55, 50}, nil)
}

func (r *pdfReport) addProbeTable(results model.ProbeResultList) {
	headers := []string{
		trReport(r.lang, i18n.MsgHealthCheckReportFieldProbeKey),
		trReport(r.lang, i18n.MsgHealthCheckReportFieldProbe),
		trReport(r.lang, i18n.MsgHealthCheckReportFieldStatus),
		trReport(r.lang, i18n.MsgHealthCheckReportFieldLatency),
		trReport(r.lang, i18n.MsgHealthCheckReportFieldEvidenceCount),
	}
	rows := make([][]string, 0, len(results))
	for _, result := range results {
		latency := "-"
		if result.LatencyMs > 0 {
			latency = fmt.Sprintf("%d ms", result.LatencyMs)
		}
		rows = append(rows, []string{
			result.ProbeKey, localizedProbeKind(r.lang, result.Kind), localizedProbeStatus(r.lang, result.Status),
			latency, strconv.Itoa(len(result.Evidence)),
		})
	}
	if len(rows) == 0 {
		rows = append(rows, []string{"-", trReport(r.lang, i18n.MsgHealthCheckReportNoEvidence), "-", "-", "0"})
	}
	r.addTable(headers, rows, []float64{98, 197, 72, 70, 58}, nil)
}

type reportChartItem struct {
	label string
	value float64
	color pdfColor
}

func (r *pdfReport) addProbeCharts(results model.ProbeResultList) {
	statusCounts := map[string]int{}
	for _, result := range results {
		statusCounts[result.Status]++
	}
	r.addDistributionChart(trReport(r.lang, i18n.MsgHealthCheckReportChartStatusDistribution), []reportChartItem{
		{localizedProbeStatus(r.lang, model.ProbeStatusPass), float64(statusCounts[model.ProbeStatusPass]), colorPass},
		{localizedProbeStatus(r.lang, model.ProbeStatusWarn), float64(statusCounts[model.ProbeStatusWarn]), colorWarn},
		{localizedProbeStatus(r.lang, model.ProbeStatusFail), float64(statusCounts[model.ProbeStatusFail]), colorFail},
		{localizedProbeStatus(r.lang, model.ProbeStatusSkip), float64(statusCounts[model.ProbeStatusSkip]), colorSkip},
		{localizedProbeStatus(r.lang, model.ProbeStatusError), float64(statusCounts[model.ProbeStatusError]), colorError},
	})

	latencies := make([]reportChartItem, 0, len(results))
	var totalLatency int64
	var maxLatency int64
	for _, result := range results {
		if result.LatencyMs <= 0 {
			continue
		}
		totalLatency += result.LatencyMs
		maxLatency = max(maxLatency, result.LatencyMs)
		latencies = append(latencies, reportChartItem{
			label: localizedProbeKind(r.lang, result.Kind), value: float64(result.LatencyMs), color: colorBlue,
		})
	}
	if len(latencies) == 0 {
		r.addEmptyChart(trReport(r.lang, i18n.MsgHealthCheckReportChartLatency), trReport(r.lang, i18n.MsgHealthCheckReportChartNoLatency))
		return
	}
	r.addBarChart(
		trReport(r.lang, i18n.MsgHealthCheckReportChartLatency),
		trReport(r.lang, i18n.MsgHealthCheckReportChartLatencySummary, map[string]any{
			"Count": len(latencies), "Average": totalLatency / int64(len(latencies)), "Maximum": maxLatency,
		}),
		latencies, "ms", 174,
	)
}

func (r *pdfReport) addBatchCharts(tasks []*model.HealthCheckTask, runs map[int]*model.ProbeRun) {
	verdictCounts := map[string]int{}
	for _, task := range tasks {
		verdictCounts[task.Verdict]++
	}
	r.addDistributionChart(trReport(r.lang, i18n.MsgHealthCheckReportChartVerdictDistribution), []reportChartItem{
		{localizedVerdict(r.lang, model.ProbeVerdictOK), float64(verdictCounts[model.ProbeVerdictOK]), colorPass},
		{localizedVerdict(r.lang, model.ProbeVerdictSuspicious), float64(verdictCounts[model.ProbeVerdictSuspicious]), colorWarn},
		{localizedVerdict(r.lang, model.ProbeVerdictWatered), float64(verdictCounts[model.ProbeVerdictWatered]), colorFail},
		{localizedVerdict(r.lang, model.ProbeVerdictInconclusive), float64(verdictCounts[model.ProbeVerdictInconclusive] + verdictCounts[""]), colorError},
	})
	scores := make([]reportChartItem, 0, len(tasks))
	for _, task := range tasks {
		if task.Verdict == "" || task.Verdict == model.ProbeVerdictInconclusive {
			continue
		}
		scores = append(scores, reportChartItem{
			label: channelDisplay(task.ChannelID, task.ChannelName) + " / " + task.Model,
			value: float64(task.TrustScore), color: verdictColor(task.Verdict),
		})
	}
	if len(scores) == 0 {
		r.addEmptyChart(trReport(r.lang, i18n.MsgHealthCheckReportChartTrustScore), trReport(r.lang, i18n.MsgHealthCheckReportChartNoScores))
		return
	}
	r.addBarChart(trReport(r.lang, i18n.MsgHealthCheckReportChartTrustScore), "", scores, "/100", 190)

	totalProbes := 0
	for _, run := range runs {
		if run != nil {
			totalProbes += len(run.Results)
		}
	}
	r.drawText(reportPDFMargin, r.y, 8.5, trReport(r.lang, i18n.MsgHealthCheckReportChartCoverageSummary, map[string]any{
		"Targets": len(tasks), "Probes": totalProbes,
	}), colorMuted, false)
	r.y -= 18
}

func (r *pdfReport) addDistributionChart(title string, items []reportChartItem) {
	const height = 72.0
	r.ensure(height + 10)
	r.drawText(reportPDFMargin, r.y, 11, title, colorNavy, true)
	r.y -= 17
	total := 0.0
	for _, item := range items {
		total += item.value
	}
	barY := r.y - 15
	r.drawRect(reportPDFMargin, barY, reportPDFContentWidth, 15, colorLightFill, true)
	if total > 0 {
		x := reportPDFMargin
		for _, item := range items {
			if item.value <= 0 {
				continue
			}
			width := reportPDFContentWidth * item.value / total
			r.drawRect(x, barY, width, 15, item.color, true)
			x += width
		}
	}
	r.y = barY - 12
	x := reportPDFMargin
	for _, item := range items {
		legendWidth := reportPDFContentWidth / float64(len(items))
		r.drawRect(x, r.y-1, 7, 7, item.color, true)
		r.drawText(x+11, r.y-1, 8.2, fmt.Sprintf("%s %d", item.label, int(item.value)), colorText, false)
		x += legendWidth
	}
	r.y -= 22
}

func (r *pdfReport) addBarChart(title, subtitle string, items []reportChartItem, suffix string, labelWidth float64) {
	r.ensure(42)
	r.drawText(reportPDFMargin, r.y, 11, title, colorNavy, true)
	r.y -= 15
	if subtitle != "" {
		for _, line := range wrapPDFText(subtitle, reportPDFContentWidth, 8.3) {
			r.drawText(reportPDFMargin, r.y, 8.3, line, colorMuted, false)
			r.y -= 11
		}
		r.y -= 3
	}
	maximum := 0.0
	for _, item := range items {
		maximum = max(maximum, item.value)
	}
	if strings.Contains(suffix, "100") {
		maximum = 100
	}
	barWidth := reportPDFContentWidth - labelWidth - 58
	for _, item := range items {
		r.ensure(22)
		labelLines := wrapPDFText(item.label, labelWidth-8, 8.2)
		label := firstOrEmpty(labelLines)
		r.drawText(reportPDFMargin, r.y-1, 8.2, label, colorText, false)
		barX := reportPDFMargin + labelWidth
		r.drawRect(barX, r.y-4, barWidth, 9, colorLightFill, true)
		if maximum > 0 {
			r.drawRect(barX, r.y-4, barWidth*item.value/maximum, 9, item.color, true)
		}
		value := strconv.FormatFloat(item.value, 'f', -1, 64) + suffix
		r.drawText(barX+barWidth+8, r.y-1, 8.2, value, colorText, true)
		r.y -= 20
	}
	r.y -= 6
}

func (r *pdfReport) addEmptyChart(title, message string) {
	r.ensure(49)
	r.drawText(reportPDFMargin, r.y, 11, title, colorNavy, true)
	r.y -= 17
	r.drawRect(reportPDFMargin, r.y-23, reportPDFContentWidth, 23, colorLightFill, true)
	r.drawText(reportPDFMargin+9, r.y-15, 8.5, message, colorMuted, false)
	r.y -= 32
}

func verdictColor(verdict string) pdfColor {
	switch verdict {
	case model.ProbeVerdictOK:
		return colorPass
	case model.ProbeVerdictSuspicious:
		return colorWarn
	case model.ProbeVerdictWatered:
		return colorFail
	default:
		return colorError
	}
}

func (r *pdfReport) addTable(headers []string, rows [][]string, widths []float64, boldColumns []bool) {
	if len(headers) > 0 {
		r.drawTableRow(headers, widths, true, nil)
	}
	for _, row := range rows {
		r.drawTableRow(row, widths, false, boldColumns)
	}
	r.y -= 8
}

func (r *pdfReport) drawTableRow(values []string, widths []float64, header bool, boldColumns []bool) {
	fontSize := 8.7
	lineHeight := 12.0
	wrapped := make([][]string, len(widths))
	maxLines := 1
	for index, width := range widths {
		value := ""
		if index < len(values) {
			value = values[index]
		}
		wrapped[index] = wrapPDFText(value, width-12, fontSize)
		if len(wrapped[index]) > maxLines {
			maxLines = len(wrapped[index])
		}
	}
	height := 12 + float64(maxLines)*lineHeight
	r.ensure(height)
	x := reportPDFMargin
	for index, width := range widths {
		fill := colorLightFill
		if header {
			fill = pdfColor{0.910, 0.933, 0.960}
			r.drawRect(x, r.y-height, width, height, fill, true)
		}
		r.drawRect(x, r.y-height, width, height, colorBorder, false)
		ty := r.y - 10 - fontSize
		for _, line := range wrapped[index] {
			bold := header || (index < len(boldColumns) && boldColumns[index])
			r.drawText(x+6, ty, fontSize, line, colorText, bold)
			ty -= lineHeight
		}
		x += width
	}
	r.y -= height
}

func (r *pdfReport) addEvidence(results model.ProbeResultList) {
	if len(results) == 0 {
		return
	}
	r.addSection(trReport(r.lang, i18n.MsgHealthCheckReportSectionEvidence))
	for _, result := range results {
		name := localizedProbeKind(r.lang, result.Kind)
		r.addEvidenceHeading(name, localizedProbeStatus(r.lang, result.Status))
		renderedSamples := false
		if result.Kind == model.ProbeKindCacheRate || result.Kind == model.ProbeKindCacheAccounting {
			renderedSamples = r.addCacheRateSampleTable(result.Evidence["samples"])
		}
		keys := make([]string, 0, len(result.Evidence))
		for key := range result.Evidence {
			if (result.Kind == model.ProbeKindCacheRate || result.Kind == model.ProbeKindCacheAccounting) && key == "samples" {
				continue
			}
			keys = append(keys, key)
		}
		slices.Sort(keys)
		if len(keys) == 0 && !renderedSamples {
			keys = append(keys, trReport(r.lang, i18n.MsgHealthCheckReportNoEvidence))
		}
		for _, key := range keys {
			value := "-"
			if raw, ok := result.Evidence[key]; ok {
				value = evidenceValue(raw)
			}
			r.addEvidenceField(key, value)
		}
	}
}

func (r *pdfReport) addEvidenceHeading(name, status string) {
	const (
		topGap           = 18.0
		lineHeight       = 15.0
		bottomGap        = 6.0
		followingReserve = 28.0
		statusColumn     = 130.0
	)
	nameLines := wrapPDFText(name, reportPDFContentWidth-statusColumn, 11.5)
	height := topGap + float64(len(nameLines))*lineHeight + bottomGap
	r.ensure(height + followingReserve)
	r.y -= topGap
	ty := r.y
	for _, line := range nameLines {
		r.drawText(reportPDFMargin, ty, 11.5, line, colorNavy, true)
		ty -= lineHeight
	}
	statusX := reportPDFWidth - reportPDFMargin - pdfTextWidth(status, 9)
	r.drawText(statusX, r.y, 9, status, colorMuted, true)
	r.y -= float64(len(nameLines))*lineHeight + bottomGap
}

func (r *pdfReport) addCacheRateSampleTable(raw any) bool {
	samples := evidenceMaps(raw)
	if len(samples) == 0 {
		return false
	}
	headers := []string{
		trReport(r.lang, i18n.MsgHealthCheckReportCacheRateFieldPrompt),
		trReport(r.lang, i18n.MsgHealthCheckReportCacheRateFieldPhase),
		trReport(r.lang, i18n.MsgHealthCheckReportCacheRateFieldContext),
		trReport(r.lang, i18n.MsgHealthCheckReportCacheRateFieldPromptTok),
		trReport(r.lang, i18n.MsgHealthCheckReportCacheRateFieldCachedTok),
		trReport(r.lang, i18n.MsgHealthCheckReportCacheRateFieldRate),
		trReport(r.lang, i18n.MsgHealthCheckReportCacheRateFieldTTFT),
	}
	rows := make([][]string, 0, len(samples))
	for _, sample := range samples {
		phase := fmt.Sprint(sample["role"])
		promptID := fmt.Sprint(sample["prompt_id"])
		if phase == "<nil>" || phase == "" {
			legacyPhase := strings.ToUpper(fmt.Sprint(sample["phase"]))
			promptID = legacyPhase
			switch legacyPhase {
			case "A":
				phase = "cold"
			case "B":
				phase = "warm"
			case "C":
				phase = "changed"
			default:
				phase = legacyPhase
			}
		}
		switch phase {
		case "cold":
			phase = trReport(r.lang, i18n.MsgHealthCheckReportCacheRatePhaseCold)
		case "warm":
			phase = trReport(r.lang, i18n.MsgHealthCheckReportCacheRatePhaseWarm)
			if round := int(evidenceNumber(sample["round"])); round > 0 {
				phase = fmt.Sprintf("%s #%d", phase, round)
			}
		case "changed":
			phase = trReport(r.lang, i18n.MsgHealthCheckReportCacheRatePhaseChanged)
		}
		contextChars := evidenceNumber(sample["context_chars"])
		contextText := "-"
		if contextChars > 0 {
			contextText = fmt.Sprintf("%.0f", contextChars)
		}
		promptTokens := int(evidenceNumber(sample["prompt_tokens"]))
		cachedTokens := int(evidenceNumber(sample["cached_tokens"]))
		rate := evidenceNumber(sample["cache_rate_pct"])
		if _, exists := sample["cache_rate_pct"]; !exists {
			rate = percentReport(cachedTokens, promptTokens)
		}
		rows = append(rows, []string{
			promptID, phase, contextText,
			fmt.Sprintf("%d", promptTokens),
			fmt.Sprintf("%d", cachedTokens),
			fmt.Sprintf("%.2f%%", rate),
			fmt.Sprintf("%.0f ms", evidenceNumber(sample["first_response_ms"])),
		})
	}
	r.addTable(headers, rows, []float64{42, 45, 70, 82, 82, 82, 92}, nil)
	r.y -= 8
	return true
}

func evidenceMaps(raw any) []map[string]any {
	switch values := raw.(type) {
	case []map[string]any:
		return values
	case []any:
		result := make([]map[string]any, 0, len(values))
		for _, value := range values {
			if item, ok := value.(map[string]any); ok {
				result = append(result, item)
			}
		}
		return result
	default:
		return nil
	}
}

func (r *pdfReport) addEvidenceField(label, value string) {
	labelLines := wrapPDFText(label, 128, 8.5)
	valueLines := wrapPDFText(value, reportPDFContentWidth-148, 8.5)
	for len(valueLines) > 0 {
		availableLines := int((r.y - reportPDFBottom - 12) / 12)
		if availableLines < 1 {
			r.newPage()
			availableLines = int((r.y - reportPDFBottom - 12) / 12)
		}
		chunkSize := min(availableLines, len(valueLines))
		chunk := valueLines[:chunkSize]
		valueLines = valueLines[chunkSize:]
		rowLines := max(len(labelLines), len(chunk))
		height := 10 + float64(rowLines)*12
		r.ensure(height)
		r.drawRect(reportPDFMargin, r.y-height, 138, height, colorLightFill, true)
		r.drawRect(reportPDFMargin, r.y-height, reportPDFContentWidth, height, colorBorder, false)
		ty := r.y - 11
		for _, line := range labelLines {
			r.drawText(reportPDFMargin+6, ty, 8.5, line, colorText, true)
			ty -= 12
		}
		ty = r.y - 11
		for _, line := range chunk {
			r.drawText(reportPDFMargin+144, ty, 8.5, line, colorText, false)
			ty -= 12
		}
		r.y -= height
		labelLines = []string{"..."}
	}
}

func (r *pdfReport) drawText(x, y, size float64, text string, color pdfColor, bold bool) {
	if text == "" {
		return
	}
	cursor := x
	for _, segment := range splitPDFFontSegments(text) {
		font := "F1"
		encoded := "<" + pdfHexText(segment.text) + ">"
		if segment.latin {
			font = "F2"
			encoded = "(" + pdfLiteralText(segment.text) + ")"
		}
		command := fmt.Sprintf("BT /%s %.2f Tf %.3f %.3f %.3f rg 1 0 0 1 %.2f %.2f Tm %s Tj ET\n", font, size, color.r, color.g, color.b, cursor, y, encoded)
		r.page.content.WriteString(command)
		if bold {
			r.page.content.WriteString(fmt.Sprintf("BT /%s %.2f Tf %.3f %.3f %.3f rg 1 0 0 1 %.2f %.2f Tm %s Tj ET\n", font, size, color.r, color.g, color.b, cursor+0.22, y, encoded))
		}
		cursor += pdfTextWidth(segment.text, size)
	}
}

func (r *pdfReport) drawLine(x1, y1, x2, y2 float64, color pdfColor, width float64) {
	r.page.content.WriteString(fmt.Sprintf("%.3f %.3f %.3f RG %.2f w %.2f %.2f m %.2f %.2f l S\n", color.r, color.g, color.b, width, x1, y1, x2, y2))
}

func (r *pdfReport) drawRect(x, y, width, height float64, color pdfColor, fill bool) {
	op := "S"
	colorOp := "RG"
	if fill {
		op = "f"
		colorOp = "rg"
	}
	r.page.content.WriteString(fmt.Sprintf("%.3f %.3f %.3f %s %.2f %.2f %.2f %.2f re %s\n", color.r, color.g, color.b, colorOp, x, y, width, height, op))
}

func (r *pdfReport) bytes() []byte {
	for index, page := range r.pages {
		pageNumber := trReport(r.lang, i18n.MsgHealthCheckReportPageNumber, map[string]any{"Page": index + 1, "Total": len(r.pages)})
		r.page = page
		r.drawLine(reportPDFMargin, 42, reportPDFWidth-reportPDFMargin, 42, colorBorder, 0.5)
		r.drawText(reportPDFMargin, 27, 8, trReport(r.lang, i18n.MsgHealthCheckReportGeneratedAt, map[string]any{"Time": reportTime(time.Now().UnixMilli())}), colorMuted, false)
		r.drawText(reportPDFWidth-reportPDFMargin-75, 27, 8, pageNumber, colorMuted, false)
	}
	fontBase, fontEncoding, fontOrdering := pdfFontForLanguage(r.lang)
	return buildPDFPackage(r.pages, fontBase, fontEncoding, fontOrdering)
}

func buildPDFPackage(pages []*pdfReportPage, fontBase, fontEncoding, fontOrdering string) []byte {
	pageObjectStart := 7
	infoObject := pageObjectStart + len(pages)*2
	objects := make([][]byte, infoObject)
	objects[0] = []byte(`<< /Type /Catalog /Pages 2 0 R >>`)
	kids := make([]string, 0, len(pages))
	for index := range pages {
		kids = append(kids, fmt.Sprintf("%d 0 R", pageObjectStart+index*2))
	}
	objects[1] = []byte(fmt.Sprintf("<< /Type /Pages /Count %d /Kids [%s] >>", len(pages), strings.Join(kids, " ")))
	objects[2] = []byte(fmt.Sprintf("<< /Type /Font /Subtype /Type0 /BaseFont /%s /Encoding /%s /DescendantFonts [4 0 R] >>", fontBase, fontEncoding))
	objects[3] = []byte(fmt.Sprintf("<< /Type /Font /Subtype /CIDFontType0 /BaseFont /%s /CIDSystemInfo << /Registry (Adobe) /Ordering (%s) /Supplement 4 >> /FontDescriptor 5 0 R >>", fontBase, fontOrdering))
	objects[4] = []byte(fmt.Sprintf("<< /Type /FontDescriptor /FontName /%s /Flags 6 /FontBBox [-250 -300 1200 1000] /ItalicAngle 0 /Ascent 880 /Descent -250 /CapHeight 700 /StemV 80 >>", fontBase))
	objects[5] = []byte(`<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>`)
	for index, page := range pages {
		pageObject := pageObjectStart + index*2
		contentObject := pageObject + 1
		objects[pageObject-1] = []byte(fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %.2f %.2f] /Resources << /Font << /F1 3 0 R /F2 6 0 R >> >> /Contents %d 0 R >>", reportPDFWidth, reportPDFHeight, contentObject))
		stream := []byte(page.content.String())
		objects[contentObject-1] = []byte(fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream))
	}
	objects[infoObject-1] = []byte(`<< /Title <FEFF0053007500700070006C00790020004800650061006C0074006800200043006800650063006B0020005200650070006F00720074> /Author (Supply Check Desktop) /Creator (Supply Check Desktop) >>`)

	var out bytes.Buffer
	out.WriteString("%PDF-1.7\n%\xE2\xE3\xCF\xD3\n")
	offsets := make([]int, len(objects)+1)
	for index, object := range objects {
		offsets[index+1] = out.Len()
		fmt.Fprintf(&out, "%d 0 obj\n", index+1)
		out.Write(object)
		out.WriteString("\nendobj\n")
	}
	xref := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for index := 1; index <= len(objects); index++ {
		fmt.Fprintf(&out, "%010d 00000 n \n", offsets[index])
	}
	fmt.Fprintf(&out, "trailer\n<< /Size %d /Root 1 0 R /Info %d 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, infoObject, xref)
	return out.Bytes()
}

func pdfFontForLanguage(lang string) (string, string, string) {
	switch lang {
	case i18n.LangZhTW:
		return "MSung-Light", "UniCNS-UCS2-H", "CNS1"
	case i18n.LangJa:
		return "HeiseiKakuGo-W5", "UniJIS-UCS2-H", "Japan1"
	default:
		return "STSong-Light", "UniGB-UCS2-H", "GB1"
	}
}

func pdfHexText(text string) string {
	units := utf16.Encode([]rune(strings.ToValidUTF8(text, "?")))
	var b strings.Builder
	b.Grow(len(units) * 4)
	for _, unit := range units {
		fmt.Fprintf(&b, "%04X", unit)
	}
	return b.String()
}

type pdfFontSegment struct {
	text  string
	latin bool
}

func splitPDFFontSegments(text string) []pdfFontSegment {
	segments := make([]pdfFontSegment, 0, 4)
	var current strings.Builder
	latin := false
	for _, character := range text {
		nextLatin := character >= 0x20 && character <= 0x7e
		if current.Len() > 0 && nextLatin != latin {
			segments = append(segments, pdfFontSegment{text: current.String(), latin: latin})
			current.Reset()
		}
		latin = nextLatin
		current.WriteRune(character)
	}
	if current.Len() > 0 {
		segments = append(segments, pdfFontSegment{text: current.String(), latin: latin})
	}
	return segments
}

func pdfLiteralText(text string) string {
	value := strings.ReplaceAll(text, `\`, `\\`)
	value = strings.ReplaceAll(value, `(`, `\(`)
	return strings.ReplaceAll(value, `)`, `\)`)
}

func pdfTextWidth(text string, size float64) float64 {
	width := 0.0
	for _, character := range text {
		width += pdfRuneWidth(character) * size
	}
	return width
}

func wrapPDFText(text string, width, fontSize float64) []string {
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	paragraphs := strings.Split(text, "\n")
	lines := make([]string, 0, len(paragraphs))
	for _, paragraph := range paragraphs {
		if paragraph == "" {
			lines = append(lines, "")
			continue
		}
		line := make([]rune, 0, len(paragraph))
		for _, character := range paragraph {
			line = append(line, character)
			for len(line) > 1 && pdfTextWidth(string(line), fontSize) > width {
				cut := lastPDFWrapOpportunity(line[:len(line)-1])
				if cut < 0 {
					cut = len(line) - 2
				}
				includeBreak := line[cut] != ' ' && line[cut] != '\t'
				emitEnd := cut
				if includeBreak {
					emitEnd++
				}
				emitted := strings.TrimRight(string(line[:emitEnd]), " \t")
				if emitted != "" {
					lines = append(lines, emitted)
				}
				line = []rune(strings.TrimLeft(string(line[cut+1:]), " \t"))
			}
		}
		if len(line) > 0 {
			lines = append(lines, strings.TrimRight(string(line), " \t"))
		}
	}
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

func lastPDFWrapOpportunity(line []rune) int {
	for index := len(line) - 1; index >= 0; index-- {
		switch line[index] {
		case ' ', '\t', '_', '-', '/', ',', ';', ':':
			return index
		}
	}
	return -1
}

func pdfRuneWidth(r rune) float64 {
	switch {
	case r == ' ':
		return 0.32
	case r == '\t':
		return 1.2
	case r < utf8.RuneSelf:
		if strings.ContainsRune("ilI.,:;'|!", r) {
			return 0.28
		}
		if strings.ContainsRune("mwMW@#%&", r) {
			return 0.82
		}
		return 0.55
	default:
		return 1.0
	}
}

func trReport(lang, key string, args ...map[string]any) string {
	return i18n.Translate(lang, key, args...)
}

func localizedVerdict(lang, verdict string) string {
	switch verdict {
	case model.ProbeVerdictOK:
		return trReport(lang, i18n.MsgHealthCheckReportVerdictOK)
	case model.ProbeVerdictSuspicious:
		return trReport(lang, i18n.MsgHealthCheckReportVerdictSuspicious)
	case model.ProbeVerdictWatered:
		return trReport(lang, i18n.MsgHealthCheckReportVerdictWatered)
	case model.ProbeVerdictInconclusive, "":
		return trReport(lang, i18n.MsgHealthCheckReportVerdictInconclusive)
	default:
		return verdict
	}
}

func localizedJobStatus(lang, status string) string {
	key := map[string]string{
		model.HealthCheckJobStatusPending:   i18n.MsgHealthCheckReportJobPending,
		model.HealthCheckJobStatusRunning:   i18n.MsgHealthCheckReportJobRunning,
		model.HealthCheckJobStatusRetryWait: i18n.MsgHealthCheckReportJobRetryWait,
		model.HealthCheckJobStatusSucceeded: i18n.MsgHealthCheckReportJobSucceeded,
		model.HealthCheckJobStatusFailed:    i18n.MsgHealthCheckReportJobFailed,
		model.HealthCheckJobStatusCanceled:  i18n.MsgHealthCheckReportJobCanceled,
	}[status]
	if key == "" {
		return status
	}
	return trReport(lang, key)
}

func localizedProbeStatus(lang, status string) string {
	key := map[string]string{
		model.ProbeStatusPass:  i18n.MsgHealthCheckReportProbePass,
		model.ProbeStatusWarn:  i18n.MsgHealthCheckReportProbeWarn,
		model.ProbeStatusFail:  i18n.MsgHealthCheckReportProbeFail,
		model.ProbeStatusSkip:  i18n.MsgHealthCheckReportProbeSkip,
		model.ProbeStatusError: i18n.MsgHealthCheckReportProbeError,
	}[status]
	if key == "" {
		return status
	}
	return trReport(lang, key)
}

func localizedProbeKind(lang, kind string) string {
	key := map[string]string{
		model.ProbeKindTokenCount:           i18n.MsgHealthCheckReportProbeTokenCount,
		model.ProbeKindLength:               i18n.MsgHealthCheckReportProbeLength,
		model.ProbeKindIdentity:             i18n.MsgHealthCheckReportProbeIdentity,
		model.ProbeKindGolden:               i18n.MsgHealthCheckReportProbeGolden,
		model.ProbeKindLatency:              i18n.MsgHealthCheckReportProbeLatency,
		model.ProbeKindCostAnchor:           i18n.MsgHealthCheckReportProbeCostAnchor,
		model.ProbeKindCacheAccounting:      i18n.MsgHealthCheckReportProbeCacheAccounting,
		model.ProbeKindFreshnessIntegrity:   i18n.MsgHealthCheckReportProbeFreshness,
		model.ProbeKindProviderCacheControl: i18n.MsgHealthCheckReportProbeCacheControl,
		model.ProbeKindSelfReport:           i18n.MsgHealthCheckReportProbeSelfReport,
		model.ProbeKindProtocolContract:     i18n.MsgHealthCheckReportProbeProtocol,
		model.ProbeKindStreamIntegrity:      i18n.MsgHealthCheckReportProbeStream,
		model.ProbeKindUsageReconciliation:  i18n.MsgHealthCheckReportProbeUsage,
		model.ProbeKindCancellationContract: i18n.MsgHealthCheckReportProbeCancellation,
		model.ProbeKindToolSchemaFidelity:   i18n.MsgHealthCheckReportProbeToolSchema,
		model.ProbeKindRateLimitContract:    i18n.MsgHealthCheckReportProbeRateLimit,
		model.ProbeKindPromptLeakage:        i18n.MsgHealthCheckReportProbePromptLeakage,
		model.ProbeKindInstructionPolicy:    i18n.MsgHealthCheckReportProbeInstructionPolicy,
		model.ProbeKindToolSubstitution:     i18n.MsgHealthCheckReportProbeToolSubstitution,
		model.ProbeKindContextIntegrity:     i18n.MsgHealthCheckReportProbeContextIntegrity,
		model.ProbeKindChannelPurity:        i18n.MsgHealthCheckReportProbeChannelPurity,
		model.ProbeKindCacheRate:            i18n.MsgHealthCheckReportProbeCacheRate,
	}[kind]
	if key == "" {
		return kind
	}
	return trReport(lang, key)
}

func scoreDisplay(verdict string, score int) string {
	if verdict == "" || verdict == model.ProbeVerdictInconclusive {
		return "-"
	}
	return fmt.Sprintf("%d/100", score)
}

func channelDisplay(id int, name string) string {
	if strings.TrimSpace(name) == "" {
		return fmt.Sprintf("#%d", id)
	}
	return fmt.Sprintf("#%d %s", id, name)
}

func reportTime(value int64) string {
	if value <= 0 {
		return "-"
	}
	var timestamp time.Time
	if value > 10_000_000_000 {
		timestamp = time.UnixMilli(value)
	} else {
		timestamp = time.Unix(value, 0)
	}
	return timestamp.UTC().Format("2006-01-02 15:04:05 UTC")
}

func evidenceValue(value any) string {
	switch v := value.(type) {
	case nil:
		return "-"
	case string:
		return v
	case bool:
		return strconv.FormatBool(v)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(v), 'f', -1, 32)
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	default:
		if encoded, err := json.MarshalIndent(value, "", "  "); err == nil {
			return string(encoded)
		}
		return fmt.Sprint(value)
	}
}

func reportCostDisplay(cost int) string {
	if cost <= 0 {
		return "N/A"
	}
	return strconv.Itoa(cost)
}

func hasCacheTelemetry(results model.ProbeResultList) bool {
	for _, result := range results {
		if result.Kind != model.ProbeKindCacheRate && result.Kind != model.ProbeKindCacheAccounting {
			continue
		}
		reportedWarm, hasReportedWarm := result.Evidence["reported_warm_samples"]
		if hasReportedWarm && evidenceNumber(reportedWarm) > 0 {
			return true
		}
		if _, hasLegacyTotal := result.Evidence["warm_cached_tokens"]; hasLegacyTotal && !hasReportedWarm {
			return true
		}
		for _, sample := range evidenceMaps(result.Evidence["samples"]) {
			if rawReported, exists := sample["telemetry_reported"]; exists {
				if reported, ok := rawReported.(bool); ok && reported {
					return true
				}
				continue
			}
			if _, legacyCachedTokens := sample["cached_tokens"]; legacyCachedTokens {
				return true
			}
		}
	}
	return false
}

func firstOrEmpty(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}
