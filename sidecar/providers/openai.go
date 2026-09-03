package providers

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/shared"

	"supply-check-sdk/protocol"
)

func newOpenAIClient(credentials protocol.Credentials, recorder *metadataRecorder) openai.Client {
	options := []option.RequestOption{
		option.WithAPIKey(strings.TrimSpace(credentials.APIKey)),
		option.WithBaseURL(strings.TrimRight(strings.TrimSpace(credentials.BaseURL), "/")),
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
	return openai.NewClient(options...)
}

func listOpenAIModels(ctx context.Context, credentials protocol.Credentials) ([]protocol.ModelInfo, error) {
	client := newOpenAIClient(credentials, nil)
	page, err := client.Models.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("OpenAI SDK 拉取模型失败: %w", err)
	}
	models := make([]protocol.ModelInfo, 0, len(page.Data))
	for _, model := range page.Data {
		if strings.TrimSpace(model.ID) == "" {
			continue
		}
		owner := model.OwnedBy
		models = append(models, protocol.ModelInfo{ID: model.ID, Name: model.ID, OwnedBy: &owner})
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	return dedupeModels(models), nil
}

func openAIParams(request protocol.Request) openai.ChatCompletionNewParams {
	messages := make([]openai.ChatCompletionMessageParamUnion, 0, 2)
	if request.SystemPrompt != "" {
		messages = append(messages, openai.SystemMessage(request.SystemPrompt))
	}
	messages = append(messages, openai.UserMessage(request.Prompt))
	params := openai.ChatCompletionNewParams{Model: shared.ChatModel(request.Model), Messages: messages}
	if isOpenAIReasoningModel(request.Model) {
		params.MaxCompletionTokens = param.NewOpt(int64(request.MaxTokens))
	} else {
		params.MaxTokens = param.NewOpt(int64(request.MaxTokens))
	}
	if request.CacheKey != "" {
		params.PromptCacheKey = param.NewOpt(request.CacheKey)
	}
	if request.ToolContract {
		params.Tools = []openai.ChatCompletionToolUnionParam{{OfFunction: &openai.ChatCompletionFunctionToolParam{
			Function: shared.FunctionDefinitionParam{
				Name:        "healthcheck_echo",
				Description: param.NewOpt("Echo the exact supplied health check value."),
				Parameters: openai.FunctionParameters{
					"type":                 "object",
					"properties":           map[string]any{"value": map[string]any{"type": "string", "const": "probe-ok"}},
					"required":             []string{"value"},
					"additionalProperties": false,
				},
				Strict: param.NewOpt(true),
			},
		}}}
		params.ToolChoice = openai.ToolChoiceOptionFunctionToolChoice(openai.ChatCompletionNamedToolChoiceFunctionParam{Name: "healthcheck_echo"})
	}
	return params
}

func completeOpenAI(ctx context.Context, request protocol.Request) (*protocol.Observation, error) {
	if request.Stream {
		return streamOpenAI(ctx, request)
	}
	recorder := &metadataRecorder{}
	client := newOpenAIClient(request.Credentials, recorder)
	started := time.Now()
	response, err := client.Chat.Completions.New(ctx, openAIParams(request))
	requestMs := uint64(time.Since(started).Milliseconds())
	if err != nil {
		return nil, fmt.Errorf("OpenAI SDK 请求失败: %w", err)
	}
	if len(response.Choices) == 0 {
		return nil, fmt.Errorf("OpenAI SDK 响应缺少 choices")
	}
	choice := response.Choices[0]
	observation := openAIObservation(response.ID, response.Model, response.SystemFingerprint, response.Usage, choice.Message.Content, choice.FinishReason, requestMs)
	recorder.apply(observation)
	if request.CacheKey != "" {
		observation.ProviderCacheControlApplied = []string{"key_partition"}
	}
	applyOpenAIToolObservation(observation, choice.Message.ToolCalls)
	return observation, nil
}

func streamOpenAI(ctx context.Context, request protocol.Request) (*protocol.Observation, error) {
	recorder := &metadataRecorder{}
	client := newOpenAIClient(request.Credentials, recorder)
	params := openAIParams(request)
	params.StreamOptions = openai.ChatCompletionStreamOptionsParam{IncludeUsage: param.NewOpt(true)}
	started := time.Now()
	stream := client.Chat.Completions.NewStreaming(ctx, params)
	acc := openai.ChatCompletionAccumulator{}
	contentAt := make([]time.Time, 0, 32)
	frames := 0
	for stream.Next() {
		chunk := stream.Current()
		frames++
		for _, choice := range chunk.Choices {
			if choice.Delta.Content != "" {
				contentAt = append(contentAt, time.Now())
			}
		}
		acc.AddChunk(chunk)
	}
	if err := stream.Err(); err != nil {
		return nil, fmt.Errorf("OpenAI SDK 流式请求失败: %w", err)
	}
	if len(acc.Choices) == 0 {
		return nil, fmt.Errorf("OpenAI SDK 流式响应缺少 choices")
	}
	first, p50 := streamTiming(started, contentAt)
	choice := acc.Choices[0]
	observation := openAIObservation(acc.ID, acc.Model, acc.SystemFingerprint, acc.Usage, choice.Message.Content, choice.FinishReason, uint64(time.Since(started).Milliseconds()))
	recorder.apply(observation)
	observation.FirstChunkMs = first
	observation.InterTokenMsP50 = p50
	observation.Chunks = len(contentAt)
	observation.StreamDataFrames = frames
	observation.StreamTerminalObserved = true
	if request.CacheKey != "" {
		observation.ProviderCacheControlApplied = []string{"key_partition"}
	}
	applyOpenAIToolObservation(observation, choice.Message.ToolCalls)
	return observation, nil
}

func openAIObservation(id, model, fingerprint string, usage openai.CompletionUsage, content, finish string, requestMs uint64) *protocol.Observation {
	return &protocol.Observation{
		Content: content, UpstreamModel: model, SystemFingerprint: fingerprint,
		PromptTokens: nonNegative(usage.PromptTokens), CompletionTokens: nonNegative(usage.CompletionTokens),
		TotalTokens: nonNegative(usage.TotalTokens), CachedTokens: nonNegative(usage.PromptTokensDetails.CachedTokens),
		CacheCreationTokens: nonNegative(usage.PromptTokensDetails.CacheWriteTokens), FinishReason: finish,
		// reasoning_tokens is billed inside completion_tokens but never returned
		// as text, so P2 must exclude it before recounting the visible answer.
		ReasoningTokens:   nonNegative(usage.CompletionTokensDetails.ReasoningTokens),
		ReasoningReported: usage.JSON.CompletionTokensDetails.Valid(),
		StopReason:        finish, RequestMs: requestMs,
		UsageReported:          usage.JSON.PromptTokens.Valid() || usage.JSON.TotalTokens.Valid(),
		CacheTelemetryReported: usage.JSON.PromptTokensDetails.Valid(), MessageID: id,
		ContentType: "application/json", Endpoint: "/v1/chat/completions",
		ResponseFormat: "openai_chat", ProtocolValid: true, TransportContextBound: true,
	}
}

func applyOpenAIToolObservation(observation *protocol.Observation, calls []openai.ChatCompletionMessageToolCallUnion) {
	if len(calls) == 0 {
		return
	}
	call := calls[0].AsFunction()
	applyHealthcheckToolObservation(observation, call.Function.Name, []byte(call.Function.Arguments))
}

func isOpenAIReasoningModel(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	return strings.HasPrefix(model, "o1") || strings.HasPrefix(model, "o3") || strings.HasPrefix(model, "o4") || strings.HasPrefix(model, "gpt-5")
}
