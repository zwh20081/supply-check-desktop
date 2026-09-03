package providers

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/param"

	"supply-check-sdk/protocol"
)

func newAnthropicClient(credentials protocol.Credentials, recorder *metadataRecorder) anthropic.Client {
	options := []option.RequestOption{
		option.WithAPIKey(strings.TrimSpace(credentials.APIKey)),
		option.WithBaseURL(stripVersionSuffix(credentials.BaseURL, "v1")),
		option.WithRequestTimeout(300 * time.Second),
		option.WithMaxRetries(0),
	}
	if recorder != nil {
		options = append(options, option.WithMiddleware(func(request *http.Request, next option.MiddlewareNext) (*http.Response, error) {
			response, err := next(request)
			recorder.observe(response)
			return response, err
		}))
	}
	return anthropic.NewClient(options...)
}

func listAnthropicModels(ctx context.Context, credentials protocol.Credentials) ([]protocol.ModelInfo, error) {
	client := newAnthropicClient(credentials, nil)
	page, err := client.Models.List(ctx, anthropic.ModelListParams{Limit: param.NewOpt[int64](1000)})
	if err != nil {
		return nil, fmt.Errorf("Anthropic SDK 拉取模型失败: %w", err)
	}
	models := make([]protocol.ModelInfo, 0, len(page.Data))
	for _, model := range page.Data {
		if strings.TrimSpace(model.ID) == "" {
			continue
		}
		owner := "Anthropic"
		name := model.DisplayName
		if name == "" {
			name = model.ID
		}
		models = append(models, protocol.ModelInfo{ID: model.ID, Name: name, OwnedBy: &owner})
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	return dedupeModels(models), nil
}

func anthropicParams(request protocol.Request) anthropic.MessageNewParams {
	block := anthropic.NewTextBlock(request.Prompt)
	if request.CacheMode == "on" && block.OfText != nil {
		block.OfText.CacheControl = anthropic.NewCacheControlEphemeralParam()
	}
	params := anthropic.MessageNewParams{
		Model: anthropic.Model(request.Model), MaxTokens: int64(request.MaxTokens),
		Messages: []anthropic.MessageParam{anthropic.NewUserMessage(block)},
	}
	if request.SystemPrompt != "" {
		params.System = []anthropic.TextBlockParam{{Text: request.SystemPrompt}}
	}
	if request.ToolContract {
		schema := anthropic.ToolInputSchemaParam{
			Properties:  map[string]any{"value": map[string]any{"type": "string", "const": "probe-ok"}},
			Required:    []string{"value"},
			ExtraFields: map[string]any{"additionalProperties": false},
		}
		params.Tools = []anthropic.ToolUnionParam{anthropic.ToolUnionParamOfTool(schema, "healthcheck_echo")}
		params.ToolChoice = anthropic.ToolChoiceParamOfTool("healthcheck_echo")
	}
	return params
}

func completeAnthropic(ctx context.Context, request protocol.Request) (*protocol.Observation, error) {
	if request.Stream {
		return streamAnthropic(ctx, request)
	}
	recorder := &metadataRecorder{}
	client := newAnthropicClient(request.Credentials, recorder)
	started := time.Now()
	response, err := client.Messages.New(ctx, anthropicParams(request))
	requestMs := uint64(time.Since(started).Milliseconds())
	if err != nil {
		return nil, fmt.Errorf("Anthropic SDK 请求失败: %w", err)
	}
	observation := anthropicObservation(response, requestMs)
	recorder.apply(observation)
	applyAnthropicToolObservation(observation, response.Content)
	if request.CacheMode == "on" {
		observation.ProviderCacheControlApplied = []string{"explicit_on"}
	} else if request.CacheMode == "off" {
		observation.ProviderCacheControlApplied = []string{"explicit_off"}
	}
	return observation, nil
}

func streamAnthropic(ctx context.Context, request protocol.Request) (*protocol.Observation, error) {
	recorder := &metadataRecorder{}
	client := newAnthropicClient(request.Credentials, recorder)
	started := time.Now()
	stream := client.Messages.NewStreaming(ctx, anthropicParams(request))
	message := anthropic.Message{}
	contentAt := make([]time.Time, 0, 32)
	frames := 0
	for stream.Next() {
		event := stream.Current()
		frames++
		if event.Type == "content_block_delta" {
			contentAt = append(contentAt, time.Now())
		}
		if err := message.Accumulate(event); err != nil {
			return nil, fmt.Errorf("Anthropic SDK 流聚合失败: %w", err)
		}
	}
	if err := stream.Err(); err != nil {
		return nil, fmt.Errorf("Anthropic SDK 流式请求失败: %w", err)
	}
	first, p50 := streamTiming(started, contentAt)
	observation := anthropicObservation(&message, uint64(time.Since(started).Milliseconds()))
	recorder.apply(observation)
	observation.FirstChunkMs = first
	observation.InterTokenMsP50 = p50
	observation.Chunks = len(contentAt)
	observation.StreamDataFrames = frames
	observation.StreamTerminalObserved = true
	applyAnthropicToolObservation(observation, message.Content)
	return observation, nil
}

func anthropicObservation(response *anthropic.Message, requestMs uint64) *protocol.Observation {
	var content strings.Builder
	for _, block := range response.Content {
		if block.Type == "text" {
			content.WriteString(block.Text)
		}
	}
	usage := response.Usage
	return &protocol.Observation{
		Content: content.String(), UpstreamModel: string(response.Model), PromptTokens: nonNegative(usage.InputTokens),
		CompletionTokens: nonNegative(usage.OutputTokens),
		TotalTokens:      nonNegative(usage.InputTokens + usage.OutputTokens + usage.CacheReadInputTokens + usage.CacheCreationInputTokens),
		CachedTokens:     nonNegative(usage.CacheReadInputTokens), CacheCreationTokens: nonNegative(usage.CacheCreationInputTokens),
		// output_tokens stays the authoritative billed total; thinking_tokens is a
		// read-only decomposition of the part that was never returned as text.
		ReasoningTokens:   nonNegative(usage.OutputTokensDetails.ThinkingTokens),
		ReasoningReported: usage.JSON.OutputTokensDetails.Valid(),
		FinishReason:      string(response.StopReason), StopReason: string(response.StopReason), RequestMs: requestMs,
		UsageReported: response.JSON.Usage.Valid(), CacheTelemetryReported: usage.JSON.CacheReadInputTokens.Valid() || usage.JSON.CacheCreationInputTokens.Valid(),
		MessageID: response.ID, ContentType: "application/json", Endpoint: "/v1/messages",
		ResponseFormat: "anthropic_messages", ProtocolValid: true, TransportContextBound: true,
	}
}

func applyAnthropicToolObservation(observation *protocol.Observation, blocks []anthropic.ContentBlockUnion) {
	for _, block := range blocks {
		if block.Type != "tool_use" {
			continue
		}
		applyHealthcheckToolObservation(observation, block.Name, block.Input)
		return
	}
}
