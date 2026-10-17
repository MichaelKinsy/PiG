package ai

import "testing"

// The chunk decode's parsesAsValue stands in for the provider event observer's validJSONParsesAsValue scan of every record it reads.
func TestOpenAIChunkDecodeParsesAsValueMatchesScan(t *testing.T) {
	long := "1"
	for len(long) <= 300 {
		long += "0"
	}
	records := []string{
		`{"id":"c","choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":null}]}`,
		`{"id":"c","created":1.5,"choices":[{"index":0,"delta":{"content":"1e5"},"finish_reason":null}]}`,
		`{"id":"c","created":1e5,"choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":null}]}`,
		`{"id":"c","created":-2E-3,"choices":[]}`,
		`{"id":"c","created":` + long + `,"choices":[]}`,
		`{"id":"c","created":` + long[:300] + `,"choices":[]}`,
	}
	decoder := &openAIChunkDecoder{}
	for _, record := range records {
		if _, ok := decoder.decode(record); !ok {
			t.Fatalf("decode declined %s", record)
		}
		if got, want := decoder.parsesAsValue, validJSONParsesAsValue(record); got != want {
			t.Errorf("%.60s: parsesAsValue %v, validJSONParsesAsValue %v", record, got, want)
		}
	}
}

func FuzzOpenAIChunkDecodeParsesAsValue(f *testing.F) {
	f.Add(`{"id":"c","created":1e5,"choices":[{"index":0,"delta":{"content":"x"},"finish_reason":null}]}`)
	f.Fuzz(func(t *testing.T, record string) {
		decoder := &openAIChunkDecoder{}
		if _, ok := decoder.decode(record); ok && decoder.parsesAsValue != validJSONParsesAsValue(record) {
			t.Fatalf("%q: parsesAsValue %v", record, decoder.parsesAsValue)
		}
	})
}
