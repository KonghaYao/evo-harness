package ingest

import (
	"encoding/json"
	"math"
	"strings"
)

// Usage is extracted from Harbor agent_result (or top-level aliases).
// Missing/null/empty fields persist as 0. Reported is true only if at least
// one field was a JSON number — never treat missing-0 as a measured zero.
type Usage struct {
	NInputTokens  int64
	NCacheTokens  int64
	NOutputTokens int64
	CostUSD       float64
	NAgentSteps   int64
	Reported      bool
}

// ExtractUsage applies design §3.6 zero-fill rules to a JSON object.
func ExtractUsage(obj map[string]any) Usage {
	return UsageFromMaps(obj, nil)
}

// UsageFromMaps merges agent_result then top-level per field.
func UsageFromMaps(agentResult, top map[string]any) Usage {
	var u Usage
	get := func(key string) (float64, bool) {
		if agentResult != nil {
			if v, ok := numericField(agentResult, key); ok {
				return v, true
			}
		}
		if top != nil {
			if v, ok := numericField(top, key); ok {
				return v, true
			}
		}
		return 0, false
	}
	if v, ok := get("n_input_tokens"); ok {
		u.NInputTokens = toInt64(v)
		u.Reported = true
	}
	if v, ok := get("n_cache_tokens"); ok {
		u.NCacheTokens = toInt64(v)
		u.Reported = true
	}
	if v, ok := get("n_output_tokens"); ok {
		u.NOutputTokens = toInt64(v)
		u.Reported = true
	}
	if v, ok := get("cost_usd"); ok {
		u.CostUSD = v
		u.Reported = true
	}
	if v, ok := get("n_agent_steps"); ok {
		u.NAgentSteps = toInt64(v)
		u.Reported = true
	} else if v, ok := get("n_steps"); ok {
		u.NAgentSteps = toInt64(v)
		u.Reported = true
	}
	return u
}

func UsageFromTrialObject(top map[string]any) Usage {
	var ar map[string]any
	if m, ok := asMap(top["agent_result"]); ok {
		ar = m
	}
	return UsageFromMaps(ar, top)
}

func numericField(obj map[string]any, key string) (float64, bool) {
	v, ok := obj[key]
	if !ok || v == nil {
		return 0, false
	}
	switch t := v.(type) {
	case string:
		if strings.TrimSpace(t) == "" {
			return 0, false
		}
		var n float64
		if err := json.Unmarshal([]byte(t), &n); err != nil {
			return 0, false
		}
		if math.IsNaN(n) || math.IsInf(n, 0) {
			return 0, false
		}
		return n, true
	case json.Number:
		n, err := t.Float64()
		if err != nil {
			return 0, false
		}
		return n, true
	case float64:
		if math.IsNaN(t) || math.IsInf(t, 0) {
			return 0, false
		}
		return t, true
	case float32:
		return float64(t), true
	case int:
		return float64(t), true
	case int64:
		return float64(t), true
	case json.RawMessage:
		if len(t) == 0 || string(t) == "null" {
			return 0, false
		}
		var n float64
		if err := json.Unmarshal(t, &n); err != nil {
			return 0, false
		}
		return n, true
	default:
		return 0, false
	}
}

func toInt64(v float64) int64 {
	return int64(math.Round(v))
}

func asMap(v any) (map[string]any, bool) {
	switch t := v.(type) {
	case map[string]any:
		return t, true
	case json.RawMessage:
		var m map[string]any
		if json.Unmarshal(t, &m) == nil {
			return m, true
		}
	}
	return nil, false
}
