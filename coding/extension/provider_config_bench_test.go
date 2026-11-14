package extension

import (
	"encoding/json"
	"testing"
)

// BenchmarkProviderConfigRoundTrip decodes the configuration of one registerProvider call and encodes it again, as the model registry does.
func BenchmarkProviderConfigRoundTrip(b *testing.B) {
	raw := []byte(`{"baseUrl":"https://probe.invalid/v1","api":"openai-completions","apiKey":"probe-key","models":[` +
		`{"id":"a","name":"Model A","reasoning":false,"input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":128000,"maxTokens":4096,"compat":{"openRouterRouting":{"sort":"price"}}},` +
		`{"id":"b","name":"Model B","reasoning":true,"input":["text","image"],"cost":{"input":1,"output":2,"cacheRead":0,"cacheWrite":0},"contextWindow":200000,"maxTokens":8192}]}`)
	b.ReportAllocs()
	for b.Loop() {
		var config ProviderConfig
		if err := json.Unmarshal(raw, &config); err != nil {
			b.Fatal(err)
		}
		if _, err := json.Marshal(config); err != nil {
			b.Fatal(err)
		}
	}
}
