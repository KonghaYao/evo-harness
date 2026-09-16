package ingest

import (
	"encoding/json"
	"testing"
)

func TestExtractUsageTable(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		in       string
		want     Usage
		reported bool
	}{
		{
			name:     "all missing keys",
			in:       `{}`,
			want:     Usage{},
			reported: false,
		},
		{
			name:     "all json null",
			in:       `{"n_input_tokens":null,"n_cache_tokens":null,"n_output_tokens":null,"cost_usd":null,"n_agent_steps":null}`,
			want:     Usage{},
			reported: false,
		},
		{
			name:     "empty strings are missing not measured zero",
			in:       `{"n_input_tokens":"","n_cache_tokens":"","n_output_tokens":"","cost_usd":"","n_agent_steps":""}`,
			want:     Usage{},
			reported: false,
		},
		{
			name:     "numeric zero is reported",
			in:       `{"n_input_tokens":0,"n_cache_tokens":null,"n_output_tokens":null,"cost_usd":null,"n_agent_steps":null}`,
			want:     Usage{NInputTokens: 0, Reported: true},
			reported: true,
		},
		{
			name:     "partial numbers fill rest with zero",
			in:       `{"n_input_tokens":10,"n_output_tokens":3,"cost_usd":1.25}`,
			want:     Usage{NInputTokens: 10, NOutputTokens: 3, CostUSD: 1.25, Reported: true},
			reported: true,
		},
		{
			name:     "n_steps maps to n_agent_steps",
			in:       `{"n_steps":7}`,
			want:     Usage{NAgentSteps: 7, Reported: true},
			reported: true,
		},
		{
			name:     "stream_type_hist is not steps",
			in:       `{"metadata":{"stream_type_hist":{"tool_use":99}},"n_agent_steps":null}`,
			want:     Usage{},
			reported: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var obj map[string]any
			if err := json.Unmarshal([]byte(tc.in), &obj); err != nil {
				t.Fatal(err)
			}
			got := ExtractUsage(obj)
			if got.Reported != tc.reported {
				t.Fatalf("usage_reported=%v want %v (missing-0 is not a measured zero)", got.Reported, tc.reported)
			}
			if got.NInputTokens != tc.want.NInputTokens || got.NCacheTokens != tc.want.NCacheTokens ||
				got.NOutputTokens != tc.want.NOutputTokens || got.CostUSD != tc.want.CostUSD ||
				got.NAgentSteps != tc.want.NAgentSteps {
				t.Fatalf("got %#v want %#v", got, tc.want)
			}
		})
	}
}

func TestUsageFromTrialPrefersAgentResult(t *testing.T) {
	raw := []byte(`{
		"n_input_tokens": 1,
		"agent_result": {"n_input_tokens": 9, "n_output_tokens": null}
	}`)
	var top map[string]any
	if err := json.Unmarshal(raw, &top); err != nil {
		t.Fatal(err)
	}
	u := UsageFromTrialObject(top)
	if !u.Reported || u.NInputTokens != 9 {
		t.Fatalf("got %#v", u)
	}
}
