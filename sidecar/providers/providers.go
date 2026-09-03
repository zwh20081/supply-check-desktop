package providers

import (
	"context"
	"fmt"

	"supply-check-sdk/protocol"
)

func ListModels(ctx context.Context, request protocol.Request) ([]protocol.ModelInfo, error) {
	switch request.Credentials.Provider {
	// Responses 与 Chat Completions 共用 /v1/models
	case "openai", "openai-responses":
		return listOpenAIModels(ctx, request.Credentials)
	case "anthropic":
		return listAnthropicModels(ctx, request.Credentials)
	case "google":
		return listGoogleModels(ctx, request.Credentials)
	default:
		return nil, fmt.Errorf("不支持的 SDK provider: %s", request.Credentials.Provider)
	}
}

// Complete issues one upstream request. It is a variable rather than a plain
// function so the batch runner's request-count contract can be verified against
// a counting stub instead of real (billable) traffic.
var Complete = completeViaSDK

func completeViaSDK(ctx context.Context, request protocol.Request) (*protocol.Observation, error) {
	switch request.Credentials.Provider {
	case "openai":
		return completeOpenAI(ctx, request)
	case "openai-responses":
		return completeOpenAIResponses(ctx, request)
	case "anthropic":
		return completeAnthropic(ctx, request)
	case "google":
		return completeGoogle(ctx, request)
	default:
		return nil, fmt.Errorf("不支持的 SDK provider: %s", request.Credentials.Provider)
	}
}
