package service

import (
	"strings"
	"sync"

	"github.com/tiktoken-go/tokenizer"
	"github.com/tiktoken-go/tokenizer/codec"
)

// Local token recount. The P1 token-count probe compares the upstream's
// reported prompt_tokens against this local number, so any change here shifts
// every token-count verdict.

// openAITextModels: only these families get an exact tiktoken count, everything
// else uses the cheaper estimator.
var openAITextModels = []string{"gpt-", "o1", "o3", "o4", "chatgpt"}

var (
	defaultTokenEncoder tokenizer.Codec
	tokenEncoderMap     = make(map[string]tokenizer.Codec)
	tokenEncoderMutex   sync.RWMutex
	defaultEncoderOnce  sync.Once
)

// CountTextToken counts tokens in text as billed for model.
func CountTextToken(text string, model string) int {
	if text == "" {
		return 0
	}
	if isOpenAITextModel(model) {
		return getTokenNum(getTokenEncoder(model), text)
	}
	// Non-OpenAI models: tiktoken is meaningless, estimate instead.
	return EstimateTokenByModel(model, text)
}

func isOpenAITextModel(modelName string) bool {
	modelName = strings.ToLower(modelName)
	for _, m := range openAITextModels {
		if strings.Contains(modelName, m) {
			return true
		}
	}
	return false
}

func getTokenEncoder(model string) tokenizer.Codec {
	defaultEncoderOnce.Do(func() { defaultTokenEncoder = codec.NewCl100kBase() })

	tokenEncoderMutex.RLock()
	if encoder, exists := tokenEncoderMap[model]; exists {
		tokenEncoderMutex.RUnlock()
		return encoder
	}
	tokenEncoderMutex.RUnlock()

	tokenEncoderMutex.Lock()
	defer tokenEncoderMutex.Unlock()
	if encoder, exists := tokenEncoderMap[model]; exists {
		return encoder
	}
	modelCodec, err := tokenizer.ForModel(tokenizer.Model(model))
	if err != nil {
		// Cache the fallback so an unknown model does not retry every call.
		tokenEncoderMap[model] = defaultTokenEncoder
		return defaultTokenEncoder
	}
	tokenEncoderMap[model] = modelCodec
	return modelCodec
}

func getTokenNum(tokenEncoder tokenizer.Codec, text string) int {
	if text == "" {
		return 0
	}
	count, _ := tokenEncoder.Count(text)
	return count
}
