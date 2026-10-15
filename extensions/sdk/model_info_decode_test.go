package sdk

import (
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// The same replies as the Rust SDK's model_info_tests.rs: both typed views read them alike.
func TestModelInfoDecodeMatchesTheRustSDK(t *testing.T) {
	summary := func(raw string) (string, error) {
		var m ModelInfo
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			return "", err
		}
		if m.ID == "" {
			return "none", nil
		}
		return fmt.Sprintf("%s/%s/%s/%d/%d/%t/%g/%g/%g/%g", m.ID, m.Name, m.Provider, m.ContextWindow, m.MaxOutputTokens, m.Reasoning, m.InputCostPer1M, m.OutputCostPer1M, m.CacheReadCostPer1M, m.CacheWriteCostPer1M), nil
	}
	for raw, want := range map[string]string{
		`{"provider":"anthropic","id":"m1","name":"M One","reasoning":true,"contextWindow":200000,"maxTokens":64000,"cost":{"input":3,"output":15,"cacheRead":0.3,"cacheWrite":3.75}}`: "m1/M One/anthropic/200000/64000/true/3/15/0.3/3.75",
		`{"id":"a","maxOutputTokens":10,"maxTokens":20,"inputCostPer1M":1,"cost":{"input":9,"output":8}}`:                                                                              "a///0/10/false/1/8/0/0",
		`{"id":"a","maxOutputTokens":0,"maxTokens":20}`:                                     "a///0/20/false/0/0/0/0",
		`{"modelId":"b","displayName":"B","provider":{"id":"p"}}`:                           "b/B/p/0/0/false/0/0/0/0",
		`{"id":"c","name":"C","displayName":"ignored","modelId":"ignored","provider":null}`: "c/C//0/0/false/0/0/0/0",
		`{"id":"d","contextWindow":1000000,"maxTokens":2048}`:                               "d///1000000/2048/false/0/0/0/0",
		`{"id":"f","provider":7}`:                                                           "f///0/0/false/0/0/0/0",
		`{}`:                                                                                "none",
		`{"id":"","modelId":"","name":"x"}`:                                                 "none",
		`null`:                                                                              "none",
	} {
		if got, err := summary(raw); err != nil || got != want {
			t.Errorf("%s: %q, %v; want %q", raw, got, err, want)
		}
	}
	for _, bad := range []string{`{"id":"e","cost":{"input":"cheap"}}`, `{"id":"e","cost":"free"}`, `{"id":"e","contextWindow":"big"}`, `{"id":"e","contextWindow":1.5}`, `{"id":"e","maxTokens":"many"}`, `{"id":"e","maxOutputTokens":5,"maxTokens":"many"}`, `{"id":"e","reasoning":"yes"}`, `{"id":5}`, `{"id":"e","name":["x"]}`, `{"id":"e","inputCostPer1M":"x"}`, `{"id":"e","name":"N","displayName":5}`, `{"id":"e","inputLimits":5}`, `{"id":"e","contextWindow":1.0}`, `{"id":"e","contextWindow":1e3}`, `"x"`, `5`, `[]`} {
		if got, err := summary(bad); err == nil {
			t.Errorf("%s decoded as %q, want an error", bad, got)
		}
	}
}
