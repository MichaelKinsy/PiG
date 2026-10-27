package durable

import "testing"

// The typed-value path of the stored documents and results: a struct copied to a JSON value and back.

type benchRecord struct {
	Status   string            `json:"status"`
	Output   []string          `json:"output"`
	Details  map[string]string `json:"details"`
	Duration int64             `json:"durationMs"`
	IsError  bool              `json:"isError"`
}

func benchValue() benchRecord {
	return benchRecord{Status: "completed", Output: []string{"line one of the output", "line two <with> & characters", "line three"},
		Details: map[string]string{"fullOutputPath": "/tmp/out.txt", "exit": "0", "name": "read"}, Duration: 1234}
}

func BenchmarkToJsonValueTyped(b *testing.B) {
	value := benchValue()
	b.ReportAllocs()
	for b.Loop() {
		if _, err := ToJsonValue(value); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkFromJsonValueTyped(b *testing.B) {
	encoded, err := ToJsonValue(benchValue())
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := FromJsonValue[benchRecord](encoded); err != nil {
			b.Fatal(err)
		}
	}
}
