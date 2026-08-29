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

func validateHealthcheckTool(name string, raw []byte) (bool, bool) {
	if name == "" {
		return false, false
	}
	var args map[string]any
	if err := json.Unmarshal(raw, &args); err != nil {
		return true, false
	}
	value, ok := args["value"].(string)
	return true, ok && value == "probe-ok"
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
