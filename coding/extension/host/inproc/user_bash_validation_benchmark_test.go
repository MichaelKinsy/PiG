package inproc_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

func BenchmarkUserBashValidation(b *testing.B) {
	for _, size := range []int{0, 1024, 64 * 1024, 1024 * 1024} {
		b.Run(fmt.Sprintf("output-%d", size), func(b *testing.B) {
			raw, err := json.Marshal(map[string]any{"result": map[string]any{"output": strings.Repeat("x", size), "exitCode": 0, "cancelled": false, "truncated": false}})
			if err != nil {
				b.Fatal(err)
			}
			exts := make([]extension.Extension, 64)
			for i := range exts {
				exts[i] = upstreamHandlerExtension(fmt.Sprintf("handler-%d", i), "user_bash", func(...any) (any, error) { return nil, nil })
			}
			exts[len(exts)-1] = upstreamHandlerExtension("override", "user_bash", func(...any) (any, error) { return json.RawMessage(raw), nil })
			r := inproc.NewRunner(exts, ".")
			event := extension.UserBashEvent{Type: "user_bash", Command: "pwd", Cwd: "."}
			b.ReportAllocs()
			b.SetBytes(int64(size))
			for b.Loop() {
				if result, err := r.EmitUserBash(context.Background(), event); err != nil || result == nil {
					b.Fatalf("result=%+v error=%v", result, err)
				}
			}
		})
	}
}
