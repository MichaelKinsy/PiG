package ai

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func BenchmarkToolArgumentValidation(b *testing.B) {
	readSchema := json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"offset":{"type":"number"},"limit":{"type":"number"}},"required":["path"]}`)
	editsSchema := json.RawMessage(`{"type":"object","properties":{"edits":{"type":"array","items":{"type":"object","properties":{"oldText":{"type":"string"},"newText":{"type":"string"}},"required":["oldText","newText"]}}},"required":["edits"]}`)
	for _, count := range []int{1, 1000} {
		b.Run(fmt.Sprintf("edits-%d", count), func(b *testing.B) {
			args := json.RawMessage(`{"edits":[` + strings.TrimSuffix(strings.Repeat(`{"oldText":42,"newText":true},`, count), ",") + `]}`)
			b.ReportAllocs()
			for b.Loop() {
				if _, err := validateToolArgsSchema("edit", editsSchema, args); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
	b.Run("read", func(b *testing.B) {
		args := json.RawMessage(`{"path":"file.txt","offset":"2","limit":null}`)
		b.ReportAllocs()
		for b.Loop() {
			if _, err := validateToolArgsSchema("read", readSchema, args); err != nil {
				b.Fatal(err)
			}
		}
	})
}
