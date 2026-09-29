package subprocess

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func BenchmarkToolParameterSchemaValidation(b *testing.B) {
	for _, size := range []int{0, 1024, 64 << 10} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			raw, err := json.Marshal(map[string]any{"type": "object", "description": strings.Repeat("x", size), "properties": map[string]any{}})
			if err != nil {
				b.Fatal(err)
			}
			tools := make([]ToolDecl, 64)
			for i := range tools {
				tools[i] = ToolDecl{Name: fmt.Sprintf("tool-%d", i), Parameters: raw}
			}
			b.ReportAllocs()
			for b.Loop() {
				reg := &RegisterPayload{Name: "schema", Tools: tools}
				if err := validateRegisterPayload("schema", reg); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
