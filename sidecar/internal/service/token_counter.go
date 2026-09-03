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

// Chat-envelope overhead. A chat request bills more than its raw text: each
// message carries role/delimiter tokens and the reply is primed with a few more.
// Counting only the text makes upstream look inflated by a fixed amount, which
// on a short prompt is a large PERCENTAGE — the P1/P2 probes then flag honest
// channels. Values follow OpenAI's published counting recipe (3 per message,
// 3 priming the reply); other families are close enough that using the same
// figures is far better than assuming zero.
const (
	tokensPerMessage = 3
	tokensReplyPrime = 3
)

// ChatEnvelopeOverhead is the non-text token cost of wrapping the given prompts
// into a chat request. Pass an empty systemPrompt when the probe sends none.
func ChatEnvelopeOverhead(systemPrompt string) int {
	messages := 1 // the user turn
	if strings.TrimSpace(systemPrompt) != "" {
		messages++
	}
	return messages*tokensPerMessage + tokensReplyPrime
}

// CountChatPromptToken is the local baseline the P1 probe compares an upstream's
// reported prompt_tokens against: message text PLUS the chat envelope the
// provider legitimately bills for.
func CountChatPromptToken(systemPrompt, userPrompt, model string) int {
	total := ChatEnvelopeOverhead(systemPrompt)
	if text := strings.TrimSpace(systemPrompt); text != "" {
		total += CountTextToken(systemPrompt, model)
	}
	return total + CountTextToken(userPrompt, model)
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
