package providers

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"

	"supply-check-sdk/protocol"
)

// Responses API 与 Chat Completions 是两套端点。很多第三方中转站只兑了
// /v1/chat/completions，所以这里作为独立 provider 存在，便于对比同一渠道
// 在两个端点下的表现，而不是悄悄回退。

func responsesParams(request protocol.Request) responses.ResponseNewParams {
	params := responses.ResponseNewParams{
		Model: shared.ResponsesModel(request.Model),
		Input: responses.ResponseNewParamsInputUnion{
			OfString: param.NewOpt(request.Prompt),
		},
		// 不落库到 OpenAI 侧，体检不需要会话状态
		Store: param.NewOpt(false),
	}
	if request.SystemPrompt != "" {
		params.Instructions = param.NewOpt(request.SystemPrompt)
	}
	if request.MaxTokens > 0 {
		params.MaxOutputTokens = param.NewOpt(int64(request.MaxTokens))
	}
	if request.CacheKey != "" {
		params.PromptCacheKey = param.NewOpt(request.CacheKey)
	}
	if request.ToolContract {
		params.Tools = []responses.ToolUnionParam{responses.ToolParamOfFunction(
			"healthcheck_echo",
			map[string]any{
				"type":                 "object",
				"properties":           map[string]any{"value": map[string]any{"type": "string", "const": "probe-ok"}},
				"required":             []string{"value"},
				"additionalProperties": false,
			},
			true,
		)}
		params.ToolChoice = responses.ResponseNewParamsToolChoiceUnion{
			OfFunctionTool: &responses.ToolChoiceFunctionParam{Name: "healthcheck_echo"},
		}
	}
	return params
}

func completeOpenAIResponses(ctx context.Context, request protocol.Request) (*protocol.Observation, error) {
	if request.Stream {
		return streamOpenAIResponses(ctx, request)
	}
	recorder := &metadataRecorder{}
	client := newOpenAIClient(request.Credentials, recorder)
	started := time.Now()
	response, err := client.Responses.New(ctx, responsesParams(request))
	requestMs := uint64(time.Since(started).Milliseconds())
	if err != nil {
		return nil, fmt.Errorf("OpenAI Responses SDK 请求失败: %w", err)
	}
	observation := responsesObservation(response, response.OutputText(), requestMs)
	recorder.apply(observation)
	if request.CacheKey != "" {
		observation.ProviderCacheControlApplied = []string{"key_partition"}
	}
	applyResponsesToolObservation(observation, response)
	return observation, nil
}

func streamOpenAIResponses(ctx context.Context, request protocol.Request) (*protocol.Observation, error) {
	recorder := &metadataRecorder{}
	client := newOpenAIClient(request.Credentials, recorder)
	started := time.Now()
	stream := client.Responses.NewStreaming(ctx, responsesParams(request))

	var (
		builder    strings.Builder
		final      *responses.Response
		contentAt  = make([]time.Time, 0, 32)
		frames     int
		terminated bool
	)

	for stream.Next() {
		event := stream.Current()
		frames++
		switch event.Type {
		case "response.output_text.delta":
			if event.Delta != "" {
				builder.WriteString(event.Delta)
				contentAt = append(contentAt, time.Now())
			}
		case "response.completed":
			completed := event.Response
			final = &completed
			terminated = true
		case "response.failed", "response.incomplete":
			completed := event.Response
			final = &completed
			terminated = true
		}
	}
	if err := stream.Err(); err != nil {
		return nil, fmt.Errorf("OpenAI Responses SDK 流式请求失败: %w", err)
	}
	if final == nil {
		return nil, fmt.Errorf("OpenAI Responses SDK 流式响应缺少 response.completed 事件")
	}

	content := builder.String()
	if content == "" {
		content = final.OutputText()
	}
	first, p50 := streamTiming(started, contentAt)
	observation := responsesObservation(final, content, uint64(time.Since(started).Milliseconds()))
	recorder.apply(observation)
	observation.FirstChunkMs = first
	observation.InterTokenMsP50 = p50
	observation.Chunks = len(contentAt)
	observation.StreamDataFrames = frames
	observation.StreamTerminalObserved = terminated
	if request.CacheKey != "" {
		observation.ProviderCacheControlApplied = []string{"key_partition"}
	}
	applyResponsesToolObservation(observation, final)
	return observation, nil
}

func responsesObservation(response *responses.Response, content string, requestMs uint64) *protocol.Observation {
	usage := response.Usage
	// Responses 用 input/output 命名，探针评分器读的是 prompt/completion，这里做映射
	return &protocol.Observation{
		Content:             content,
		UpstreamModel:       response.Model,
		PromptTokens:        nonNegative(usage.InputTokens),
		CompletionTokens:    nonNegative(usage.OutputTokens),
		TotalTokens:         nonNegative(usage.TotalTokens),
		CachedTokens:        nonNegative(usage.InputTokensDetails.CachedTokens),
		CacheCreationTokens: nonNegative(usage.InputTokensDetails.CacheWriteTokens),
		// Responses bills reasoning inside output_tokens without returning it.
		ReasoningTokens:   nonNegative(usage.OutputTokensDetails.ReasoningTokens),
		ReasoningReported: usage.JSON.OutputTokensDetails.Valid(),
		FinishReason:      string(response.Status),
		StopReason:        string(response.Status),
		RequestMs:         requestMs,
		UsageReported:     usage.JSON.InputTokens.Valid() || usage.JSON.TotalTokens.Valid(),
		// Responses 把缓存计数放在 input_tokens_details 下
		CacheTelemetryReported: usage.JSON.InputTokensDetails.Valid(),
		MessageID:              response.ID,
		ContentType:            "application/json",
		Endpoint:               "/v1/responses",
		ResponseFormat:         "openai_responses",
		ProtocolValid:          true,
		TransportContextBound:  true,
	}
}

func applyResponsesToolObservation(observation *protocol.Observation, response *responses.Response) {
	for _, item := range response.Output {
		if item.Type != "function_call" {
			continue
		}
		call := item.AsFunctionCall()
		applyHealthcheckToolObservation(observation, call.Name, []byte(call.Arguments))
		return
	}
}
