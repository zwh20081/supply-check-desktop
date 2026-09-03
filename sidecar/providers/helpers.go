package providers

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"supply-check-sdk/protocol"
)

func stripVersionSuffix(raw, version string) string {
	base := strings.TrimRight(strings.TrimSpace(raw), "/")
	return strings.TrimSuffix(base, "/"+version)
}

func streamTiming(started time.Time, contentAt []time.Time) (uint64, float64) {
	if len(contentAt) == 0 {
		return 0, 0
	}
	first := uint64(contentAt[0].Sub(started).Milliseconds())
	if len(contentAt) < 2 {
		return first, 0
	}
	intervals := make([]float64, 0, len(contentAt)-1)
	for i := 1; i < len(contentAt); i++ {
		intervals = append(intervals, float64(contentAt[i].Sub(contentAt[i-1]).Microseconds())/1000)
	}
	sort.Float64s(intervals)
	middle := len(intervals) / 2
	if len(intervals)%2 == 0 {
		return first, (intervals[middle-1] + intervals[middle]) / 2
	}
	return first, intervals[middle]
}

func validateHealthcheckTool(raw []byte) (bool, bool) {
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return false, false
	}
	args, ok := decoded.(map[string]any)
	if !ok {
		return true, false
	}
	value, ok := args["value"].(string)
	return true, ok && value == "probe-ok" && len(args) == 1
}

func applyHealthcheckToolObservation(observation *protocol.Observation, name string, raw []byte) {
	valid, matched := validateHealthcheckTool(raw)
	observation.ToolCallObserved = true
	observation.ToolCallName = name
	observation.ToolArgumentsCaptured = true
	// Copy the SDK-owned buffer so streamed/final response reuse cannot mutate the
	// diagnostic input. The protocol type prevents this raw value being serialized.
	observation.ToolArgumentsRaw = append([]byte(nil), raw...)
	observation.ToolArgumentsValid = valid
	observation.ToolSchemaMatched = matched
}

func nonNegative(value int64) uint64 {
	if value < 0 {
		return 0
	}
	return uint64(value)
}

// rateLimitHeaderAllowlist is the only response metadata copied out of an
// upstream reply. Headers can carry credentials and routing details, so the
// P15 contract probe receives exactly the rate-limit family it judges and
// nothing else — never the whole header map.
func isCapturedRateLimitHeader(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	return name == "retry-after" ||
		strings.HasPrefix(name, "x-ratelimit-") ||
		strings.HasPrefix(name, "ratelimit-") ||
		strings.HasPrefix(name, "anthropic-ratelimit-")
}

// captureRateLimitHeaders extracts the allowlisted rate-limit headers from a
// real HTTP response. Returns nil when the upstream exposed none, which the
// probe reads as "unsupported" rather than "violated".
func captureRateLimitHeaders(header http.Header) map[string]string {
	if len(header) == 0 {
		return nil
	}
	out := make(map[string]string, 8)
	for name, values := range header {
		if !isCapturedRateLimitHeader(name) || len(values) == 0 {
			continue
		}
		out[strings.ToLower(name)] = values[0]
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// httpMetadata is the real transport metadata observed for one upstream call.
type httpMetadata struct {
	status  int
	headers map[string]string
}

// metadataRecorder builds an SDK middleware that records the genuine status
// code and rate-limit headers. Both OpenAI and Anthropic SDKs expose the same
// middleware signature, so one recorder serves both.
type metadataRecorder struct {
	mu   sync.Mutex
	meta httpMetadata
}

func (r *metadataRecorder) observe(response *http.Response) {
	if response == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.meta.status = response.StatusCode
	r.meta.headers = captureRateLimitHeaders(response.Header)
}

func (r *metadataRecorder) snapshot() httpMetadata {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.meta
}

// apply copies the recorded transport metadata onto an observation. A zero
// status means the transport never reported one; ProbeProtocolContract treats
// 0 as "not observable" instead of asserting a fake 200.
func (r *metadataRecorder) apply(observation *protocol.Observation) {
	if observation == nil {
		return
	}
	meta := r.snapshot()
	observation.HTTPStatus = meta.status
	observation.UpstreamHeaders = meta.headers
}

// roundTripRecorder is the http.RoundTripper equivalent for SDKs (genai) that
// accept a custom *http.Client rather than a middleware.
type roundTripRecorder struct {
	base     http.RoundTripper
	recorder *metadataRecorder
}

func (t *roundTripRecorder) RoundTrip(request *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	response, err := base.RoundTrip(request)
	if err != nil {
		return nil, err
	}
	t.recorder.observe(response)
	return response, nil
}

func dedupeModels(models []protocol.ModelInfo) []protocol.ModelInfo {
	if len(models) < 2 {
		return models
	}
	out := models[:0]
	for _, model := range models {
		if len(out) > 0 && out[len(out)-1].ID == model.ID {
			continue
		}
		out = append(out, model)
	}
	return out
}
