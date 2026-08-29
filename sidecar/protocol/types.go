package protocol

type Credentials struct {
	Provider string `json:"provider"`
	BaseURL  string `json:"baseUrl"`
	APIKey   string `json:"apiKey"`
}

type Request struct {
	Action       string      `json:"action"`
	Credentials  Credentials `json:"credentials"`
	Model        string      `json:"model,omitempty"`
	Prompt       string      `json:"prompt,omitempty"`
	SystemPrompt string      `json:"systemPrompt,omitempty"`
	MaxTokens    uint32      `json:"maxTokens,omitempty"`
	Stream       bool        `json:"stream,omitempty"`
	ToolContract bool        `json:"toolContract,omitempty"`
	CacheMode    string      `json:"cacheMode,omitempty"`
	CacheKey     string      `json:"cacheKey,omitempty"`
	Models       []string    `json:"models,omitempty"`
	Concurrency  int         `json:"concurrency,omitempty"`
	OutputPath   string      `json:"outputPath,omitempty"`
	// PDF 报告语言。空值时由 runner 退回 zh-CN，保持旧行为。
	Lang string `json:"lang,omitempty"`
}

type ModelInfo struct {
	ID      string  `json:"id"`
	Name    string  `json:"name"`
	OwnedBy *string `json:"ownedBy,omitempty"`
}

type Observation struct {
	Content                     string            `json:"content"`
	UpstreamModel               string            `json:"upstreamModel"`
	SystemFingerprint           string            `json:"systemFingerprint"`
	PromptTokens                uint64            `json:"promptTokens"`
	CompletionTokens            uint64            `json:"completionTokens"`
	TotalTokens                 uint64            `json:"totalTokens"`
	CachedTokens                uint64            `json:"cachedTokens"`
	CacheCreationTokens         uint64            `json:"cacheCreationTokens"`
	FinishReason                string            `json:"finishReason"`
	RequestMs                   uint64            `json:"requestMs"`
	FirstChunkMs                uint64            `json:"firstChunkMs"`
	InterTokenMsP50             float64           `json:"interTokenMsP50"`
	Chunks                      int               `json:"chunks"`
	UsageReported               bool              `json:"usageReported"`
	CacheTelemetryReported      bool              `json:"cacheTelemetryReported"`
	MessageID                   string            `json:"messageId"`
	StopReason                  string            `json:"stopReason"`
	ContentType                 string            `json:"contentType"`
	Endpoint                    string            `json:"endpoint"`
	HTTPStatus                  int               `json:"httpStatus"`
	ResponseFormat              string            `json:"responseFormat"`
	ProtocolValid               bool              `json:"protocolValid"`
	StreamTerminalObserved      bool              `json:"streamTerminalObserved"`
	StreamDataFrames            int               `json:"streamDataFrames"`
	StreamInvalidFrames         int               `json:"streamInvalidFrames"`
	UpstreamHeaders             map[string]string `json:"upstreamHeaders,omitempty"`
	TransportContextBound       bool              `json:"transportContextBound"`
	ToolCallObserved            bool              `json:"toolCallObserved"`
	ToolCallName                string            `json:"toolCallName"`
	ToolArgumentsValid          bool              `json:"toolArgumentsValid"`
	ToolSchemaMatched           bool              `json:"toolSchemaMatched"`
	ProviderCacheControlApplied []string          `json:"providerCacheControlApplied,omitempty"`
}

type ProbeResult struct {
	ProbeKey  string         `json:"probeKey"`
	Kind      string         `json:"kind"`
	Status    string         `json:"status"`
	Evidence  map[string]any `json:"evidence,omitempty"`
	LatencyMs int64          `json:"latencyMs,omitempty"`
}

type ModelReport struct {
	ID               int           `json:"id"`
	Model            string        `json:"model"`
	TrustScore       int           `json:"trustScore"`
	Verdict          string        `json:"verdict"`
	RequestCount     int           `json:"requestCount"`
	PromptTokens     int           `json:"promptTokens"`
	CompletionTokens int           `json:"completionTokens"`
	TotalTokens      int           `json:"totalTokens"`
	DurationMs       int64         `json:"durationMs"`
	Error            string        `json:"error,omitempty"`
	Results          []ProbeResult `json:"results"`
}

type BatchReport struct {
	ID                string        `json:"id"`
	Provider          string        `json:"provider"`
	ProviderLabel     string        `json:"providerLabel"`
	BaseURL           string        `json:"baseUrl"`
	StartedAt         string        `json:"startedAt"`
	FinishedAt        string        `json:"finishedAt"`
	DurationMs        int64         `json:"durationMs"`
	TotalModels       int           `json:"totalModels"`
	CompletedModels   int           `json:"completedModels"`
	FailedModels      int           `json:"failedModels"`
	EstimatedRequests int           `json:"estimatedRequests"`
	CompletedRequests int           `json:"completedRequests"`
	TrustScore        int           `json:"trustScore"`
	Verdict           string        `json:"verdict"`
	PDFPath           string        `json:"pdfPath"`
	Models            []ModelReport `json:"models"`
}

type Response struct {
	Models      []ModelInfo  `json:"models,omitempty"`
	Observation *Observation `json:"observation,omitempty"`
	Report      *BatchReport `json:"report,omitempty"`
	Error       string       `json:"error,omitempty"`
}
