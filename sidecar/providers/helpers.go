package providers

import (
	"encoding/json"
	"sort"
	"strings"
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
