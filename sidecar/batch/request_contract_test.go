package batch

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"supply-check-sdk/internal/model"
	"supply-check-sdk/protocol"
	"supply-check-sdk/providers"
)

// Request-count contract, verified by COUNTING.
//
// The previous contract test asserted `RequestsPerModel != 60`, but that
// constant is a compile-time expression — the assertion reduced to 30+30==60
// and could never fail. It let the frontend drift to 63 silently, and a real
// cost estimate is what users budget against.
//
// These tests drive the actual suite against a counting stub, so the constant
// must agree with the number of upstream calls the runner really makes.

// stubUpstream replaces providers.Complete for the duration of a test.
type stubUpstream struct {
	calls   atomic.Int64
	byProbe sync.Map // probe prompt shape → count
	respond func(protocol.Request) (*protocol.Observation, error)
}

func (s *stubUpstream) install(t *testing.T) {
	t.Helper()
	original := providers.Complete
	providers.Complete = func(_ context.Context, request protocol.Request) (*protocol.Observation, error) {
		s.calls.Add(1)
		if s.respond != nil {
			return s.respond(request)
		}
		return okObservation(request), nil
	}
	t.Cleanup(func() { providers.Complete = original })
}

// okObservation is a plausible healthy reply: it satisfies the marker-echo
// probes so the suite follows its normal path rather than an error path.
func okObservation(request protocol.Request) *protocol.Observation {
	content := "ok"
	// Echo whatever marker the prompt asked for, so freshness/context/policy
	// probes behave like a well-run channel.
	if index := strings.LastIndex(request.Prompt, "Return exactly "); index >= 0 {
		content = strings.TrimSpace(request.Prompt[index+len("Return exactly "):])
	} else if strings.Contains(request.Prompt, "output only the marker") {
		if start := strings.Index(request.Prompt, "marker "); start >= 0 {
			content = strings.Fields(request.Prompt[start+len("marker "):])[0]
			content = strings.TrimSuffix(content, ".")
		}
	}
	if request.SystemPrompt != "" && strings.Contains(request.SystemPrompt, "Reply with exactly ") {
		marker := strings.TrimPrefix(request.SystemPrompt, "Reply with exactly ")
		content = strings.Fields(marker)[0]
	}
	return &protocol.Observation{
		Content:       content,
		UpstreamModel: request.Model,
		PromptTokens:  40, CompletionTokens: 10, TotalTokens: 50,
		RequestMs: 120, FirstChunkMs: 60, Chunks: 4,
		UsageReported: true, ProtocolValid: true, HTTPStatus: 200,
		ResponseFormat: "openai_chat", MessageID: "chatcmpl-stub",
		StreamTerminalObserved: true, StreamDataFrames: 4,
		TransportContextBound: true,
	}
}

func TestRequestsPerModelMatchesActualUpstreamCalls(t *testing.T) {
	stub := &stubUpstream{}
	stub.install(t)

	report, err := RunAll(context.Background(), protocol.Request{
		Credentials: protocol.Credentials{Provider: "openai", BaseURL: "https://x/v1", APIKey: "k"},
		Models:      []string{"gpt-4o"},
		Concurrency: 4,
	}, nil)
	if err != nil {
		t.Fatalf("RunAll: %v", err)
	}

	actual := stub.calls.Load()
	if actual != int64(RequestsPerModel) {
		t.Errorf("suite issued %d upstream requests but RequestsPerModel says %d\n"+
			"  the constant drives the UI cost estimate — one of them is wrong",
			actual, RequestsPerModel)
	}
	if got := report.EstimatedRequests; got != RequestsPerModel {
		t.Errorf("report estimate = %d, want %d", got, RequestsPerModel)
	}
	if got := report.CompletedRequests; int64(got) != actual {
		t.Errorf("report counted %d completed requests, stub saw %d", got, actual)
	}
}

func TestRequestCountScalesPerModel(t *testing.T) {
	stub := &stubUpstream{}
	stub.install(t)

	models := []string{"gpt-4o", "gpt-4o-mini", "gpt-5"}
	if _, err := RunAll(context.Background(), protocol.Request{
		Credentials: protocol.Credentials{Provider: "openai", BaseURL: "https://x/v1", APIKey: "k"},
		Models:      models,
		Concurrency: 8,
	}, nil); err != nil {
		t.Fatalf("RunAll: %v", err)
	}

	want := int64(len(models) * RequestsPerModel)
	if got := stub.calls.Load(); got != want {
		t.Errorf("%d models issued %d requests, want %d", len(models), got, want)
	}
}

// TestSuiteProducesEveryDefinedProbe: the suite must emit one result per
// definition. A probe that silently stops being appended would otherwise just
// vanish from the report.
func TestSuiteProducesEveryDefinedProbe(t *testing.T) {
	stub := &stubUpstream{}
	stub.install(t)

	report, err := RunAll(context.Background(), protocol.Request{
		Credentials: protocol.Credentials{Provider: "openai", BaseURL: "https://x/v1", APIKey: "k"},
		Models:      []string{"gpt-4o"},
	}, nil)
	if err != nil {
		t.Fatalf("RunAll: %v", err)
	}

	results := report.Models[0].Results
	if len(results) != len(definitions()) {
		t.Errorf("suite produced %d results but %d probes are defined", len(results), len(definitions()))
	}

	seen := make(map[string]bool, len(results))
	for _, result := range results {
		if seen[result.ProbeKey] {
			t.Errorf("duplicate probe key %q", result.ProbeKey)
		}
		seen[result.ProbeKey] = true
	}
	for _, definition := range definitions() {
		if !seen[definition.Key] {
			t.Errorf("defined probe %q produced no result", definition.Key)
		}
	}
}

// TestStonewalledUpstreamReportsInconclusive is the end-to-end counterpart of
// the scorer's adversarial suite: a relay that fails every request must not
// come out clean at the batch level either.
func TestStonewalledUpstreamReportsInconclusive(t *testing.T) {
	stub := &stubUpstream{respond: func(protocol.Request) (*protocol.Observation, error) {
		return nil, context.DeadlineExceeded
	}}
	stub.install(t)

	report, err := RunAll(context.Background(), protocol.Request{
		Credentials: protocol.Credentials{Provider: "openai", BaseURL: "https://x/v1", APIKey: "k"},
		Models:      []string{"gpt-4o"},
	}, nil)
	if err != nil {
		t.Fatalf("RunAll: %v", err)
	}

	if got := report.Verdict; got != model.ProbeVerdictInconclusive {
		t.Errorf("an upstream that answered nothing produced verdict %q, want INCONCLUSIVE", got)
	}
	modelReport := report.Models[0]
	if modelReport.CriticalErrorRate != 1 {
		t.Errorf("critical error rate = %v, want 1", modelReport.CriticalErrorRate)
	}
	if modelReport.InsufficientReason == "" {
		t.Error("an unmeasured channel must record why the evidence was insufficient")
	}
}

// TestRetriesAreCountedAsBilledRequests documents real cost: a failing probe
// retries up to 3 times and every attempt is billable.
func TestRetriesAreCountedAsBilledRequests(t *testing.T) {
	var attempts atomic.Int64
	stub := &stubUpstream{respond: func(request protocol.Request) (*protocol.Observation, error) {
		// Fail only the tool probe, which the runner attempts 3 times.
		if request.ToolContract {
			attempts.Add(1)
			return nil, context.DeadlineExceeded
		}
		return okObservation(request), nil
	}}
	stub.install(t)

	if _, err := RunAll(context.Background(), protocol.Request{
		Credentials: protocol.Credentials{Provider: "openai", BaseURL: "https://x/v1", APIKey: "k"},
		Models:      []string{"gpt-4o"},
	}, nil); err != nil {
		t.Fatalf("RunAll: %v", err)
	}

	if got := attempts.Load(); got != 3 {
		t.Errorf("a failing probe made %d upstream attempts, want 3 (retry budget)", got)
	}
	// RequestsPerModel counts logical probes, not attempts, so the real billed
	// total exceeds it whenever anything retries. Keep that visible.
	if total := stub.calls.Load(); total <= int64(RequestsPerModel) {
		t.Errorf("billed calls %d should exceed the %d logical requests when retries occur",
			total, RequestsPerModel)
	}
}
