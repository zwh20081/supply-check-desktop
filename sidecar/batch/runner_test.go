package batch

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"supply-check-sdk/internal/i18n"
	"supply-check-sdk/internal/model"

	"supply-check-sdk/protocol"
)

func TestPDFLangFallsBackToChinese(t *testing.T) {
	for _, item := range []struct{ in, want string }{
		{i18n.LangEn, i18n.LangEn},
		{i18n.LangZhCN, i18n.LangZhCN},
		{i18n.LangJa, i18n.LangJa},
		{"", i18n.LangZhCN},
		{"kl-GL", i18n.LangZhCN},
	} {
		if got := pdfLang(item.in); got != item.want {
			t.Errorf("pdfLang(%q) = %q, want %q", item.in, got, item.want)
		}
	}
}

func TestCompleteSuiteContract(t *testing.T) {
	if got := len(definitions()); got != 22 {
		t.Fatalf("complete suite should expose 22 results, got %d", got)
	}
	if RequestsPerModel != 63 {
		t.Fatalf("complete suite request estimate changed: %d", RequestsPerModel)
	}
}

func TestWritePDFUsesOriginalBatchRenderer(t *testing.T) {
	started := time.Unix(1_700_000_000, 0)
	finished := started.Add(time.Minute)
	reports := []protocol.ModelReport{{
		ID: 1, Model: "gpt-test", TrustScore: 98, Verdict: model.ProbeVerdictOK,
		RequestCount: RequestsPerModel, PromptTokens: 100, CompletionTokens: 20, TotalTokens: 120,
		Results: []protocol.ProbeResult{{
			ProbeKey: "p1_token_count", Kind: model.ProbeKindTokenCount, Status: model.ProbeStatusPass,
			Evidence: map[string]any{"ratio": 1},
		}},
	}}
	batchReport := buildBatchReport(protocol.Request{Credentials: protocol.Credentials{Provider: "openai"}}, reports, started, finished, RequestsPerModel)
	path := filepath.Join(t.TempDir(), "report.pdf")
	if err := writePDF(batchReport, reports, path, i18n.LangZhCN, started, finished); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(data, []byte("%PDF-1.7")) {
		t.Fatal("missing original PDF 1.7 header")
	}
	if bytes.Count(data, []byte("/Type /Page ")) < 2 {
		t.Fatal("batch report should contain summary and model detail pages")
	}
}
