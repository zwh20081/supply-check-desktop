package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"google.golang.org/genai"

	"supply-check-sdk/protocol"
)

func newGoogleClient(ctx context.Context, credentials protocol.Credentials) (*genai.Client, error) {
	baseURL, version := splitGoogleBaseURL(credentials.BaseURL)
	timeout := 300 * time.Second
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey: strings.TrimSpace(credentials.APIKey), Backend: genai.BackendGeminiAPI,
		HTTPOptions: genai.HTTPOptions{BaseURL: baseURL, APIVersion: version, Timeout: &timeout},
	})
	if err != nil {
		return nil, fmt.Errorf("创建 Google Gen AI SDK 客户端失败: %w", err)
	}
	return client, nil
}

func listGoogleModels(ctx context.Context, credentials protocol.Credentials) ([]protocol.ModelInfo, error) {
	client, err := newGoogleClient(ctx, credentials)
	if err != nil {
		return nil, err
	}
	page, err := client.Models.List(ctx, &genai.ListModelsConfig{PageSize: 1000})
	if err != nil {
		return nil, fmt.Errorf("Google Gen AI SDK 拉取模型失败: %w", err)
	}
	models := make([]protocol.ModelInfo, 0, len(page.Items))
	for {
		for _, model := range page.Items {
			if model == nil || strings.TrimSpace(model.Name) == "" || !supportsGenerateContent(model) {
				continue
			}
			id := strings.TrimPrefix(model.Name, "models/")
			name := model.DisplayName
			if name == "" {
				name = id
			}
			owner := "Google"
			models = append(models, protocol.ModelInfo{ID: id, Name: name, OwnedBy: &owner})
		}
		if page.NextPageToken == "" {
			break
		}
		page, err = page.Next(ctx)
		if err != nil {
			return nil, fmt.Errorf("Google Gen AI SDK 翻页失败: %w", err)
		}
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	return dedupeModels(models), nil
}

func googleConfig(request protocol.Request) *genai.GenerateContentConfig {
	config := &genai.GenerateContentConfig{MaxOutputTokens: int32(request.MaxTokens)}
	if request.SystemPrompt != "" {
		config.SystemInstruction = &genai.Content{Role: "system", Parts: []*genai.Part{{Text: request.SystemPrompt}}}
	}
	if request.ToolContract {
		config.Tools = []*genai.Tool{{FunctionDeclarations: []*genai.FunctionDeclaration{{
			Name: "healthcheck_echo", Description: "Echo the exact supplied health check value.",
			Parameters: &genai.Schema{Type: genai.TypeObject, Properties: map[string]*genai.Schema{
				"value": {Type: genai.TypeString, Enum: []string{"probe-ok"}},
			}, Required: []string{"value"}},
		}}}}
		config.ToolConfig = &genai.ToolConfig{FunctionCallingConfig: &genai.FunctionCallingConfig{
			Mode: genai.FunctionCallingConfigModeAny, AllowedFunctionNames: []string{"healthcheck_echo"},
		}}
	}
	return config
}

func completeGoogle(ctx context.Context, request protocol.Request) (*protocol.Observation, error) {
	if request.Stream {
		return streamGoogle(ctx, request)
	}
	client, err := newGoogleClient(ctx, request.Credentials)
	if err != nil {
		return nil, err
	}
	started := time.Now()
	response, err := client.Models.GenerateContent(ctx, strings.TrimPrefix(request.Model, "models/"), genai.Text(request.Prompt), googleConfig(request))
	requestMs := uint64(time.Since(started).Milliseconds())
	if err != nil {
		return nil, fmt.Errorf("Google Gen AI SDK 请求失败: %w", err)
	}
	observation := googleObservation(response, requestMs)
	applyGoogleToolObservation(observation, response.FunctionCalls())
	return observation, nil
}

func streamGoogle(ctx context.Context, request protocol.Request) (*protocol.Observation, error) {
	client, err := newGoogleClient(ctx, request.Credentials)
	if err != nil {
		return nil, err
	}
	started := time.Now()
	contentAt := make([]time.Time, 0, 32)
	var content strings.Builder
	var last *genai.GenerateContentResponse
	frames := 0
	var calls []*genai.FunctionCall
	for response, streamErr := range client.Models.GenerateContentStream(ctx, strings.TrimPrefix(request.Model, "models/"), genai.Text(request.Prompt), googleConfig(request)) {
		if streamErr != nil {
			return nil, fmt.Errorf("Google Gen AI SDK 流式请求失败: %w", streamErr)
		}
		frames++
		last = response
		text := response.Text()
		if text != "" {
			contentAt = append(contentAt, time.Now())
			content.WriteString(text)
		}
		calls = append(calls, response.FunctionCalls()...)
	}
	if last == nil {
		return nil, fmt.Errorf("Google Gen AI SDK 流式响应为空")
	}
	first, p50 := streamTiming(started, contentAt)
	observation := googleObservation(last, uint64(time.Since(started).Milliseconds()))
	observation.Content = content.String()
	observation.FirstChunkMs = first
	observation.InterTokenMsP50 = p50
	observation.Chunks = len(contentAt)
	observation.StreamDataFrames = frames
	observation.StreamTerminalObserved = true
	applyGoogleToolObservation(observation, calls)
	return observation, nil
}

func googleObservation(response *genai.GenerateContentResponse, requestMs uint64) *protocol.Observation {
	observation := &protocol.Observation{
		Content: response.Text(), UpstreamModel: strings.TrimPrefix(response.ModelVersion, "models/"), RequestMs: requestMs,
		MessageID: response.ResponseID, ContentType: "application/json", Endpoint: "/v1beta/models:generateContent",
		HTTPStatus: 200, ResponseFormat: "google_generate_content", ProtocolValid: true, TransportContextBound: true,
	}
	if response.UsageMetadata != nil {
		usage := response.UsageMetadata
		observation.PromptTokens = uint64(max(usage.PromptTokenCount, 0))
		observation.CompletionTokens = uint64(max(usage.CandidatesTokenCount, 0))
		observation.TotalTokens = uint64(max(usage.TotalTokenCount, 0))
		observation.CachedTokens = uint64(max(usage.CachedContentTokenCount, 0))
		observation.UsageReported = true
		observation.CacheTelemetryReported = usage.CachedContentTokenCount > 0 || len(usage.CacheTokensDetails) > 0
	}
	if len(response.Candidates) > 0 && response.Candidates[0] != nil {
		observation.FinishReason = string(response.Candidates[0].FinishReason)
		observation.StopReason = observation.FinishReason
	}
	return observation
}

func applyGoogleToolObservation(observation *protocol.Observation, calls []*genai.FunctionCall) {
	if len(calls) == 0 || calls[0] == nil {
		return
	}
	raw, err := json.Marshal(calls[0].Args)
	observed, matched := validateHealthcheckTool(calls[0].Name, raw)
	observation.ToolCallObserved = observed
	observation.ToolCallName = calls[0].Name
	observation.ToolArgumentsValid = err == nil
	observation.ToolSchemaMatched = matched
}

func supportsGenerateContent(model *genai.Model) bool {
	if len(model.SupportedActions) == 0 {
		return true
	}
	for _, action := range model.SupportedActions {
		if action == "generateContent" {
			return true
		}
	}
	return false
}

func splitGoogleBaseURL(raw string) (string, string) {
	base := strings.TrimRight(strings.TrimSpace(raw), "/")
	for _, version := range []string{"v1beta", "v1"} {
		if strings.HasSuffix(base, "/"+version) {
			return strings.TrimSuffix(base, "/"+version), version
		}
	}
	return base, "v1beta"
}
