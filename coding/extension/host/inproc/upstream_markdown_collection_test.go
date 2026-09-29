package inproc_test

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// Pi runner.ts:725-733 collects without invoking or sorting the callbacks. The original source-factory case is exercised by TestUpstreamRunnerMarkdownSourceFactories.
func TestRunnerMarkdownTransformerCollectionOrder(t *testing.T) {
	var calls []string
	transform := func(name string) extension.MarkdownTransformer {
		return func(markdown string, ctx extension.MarkdownTransformContext) string {
			calls = append(calls, name)
			if ctx.MessageType != "assistant" || !ctx.IsStreaming || ctx.AvailableWidth != 73 {
				t.Fatalf("context changed: %+v", ctx)
			}
			return name + "(" + markdown + ")"
		}
	}
	a := extension.Extension{Path: "z-first.ts", MarkdownTransformer: transform("A")}
	b := extension.Extension{Path: "a-second.ts", MarkdownTransformer: transform("B")}
	for _, tc := range []struct {
		name  string
		exts  []extension.Extension
		want  string
		calls []string
	}{
		{"empty", nil, "x", nil},
		{"absent", []extension.Extension{{Path: "absent.ts"}}, "x", nil},
		{"singleton", []extension.Extension{a}, "A(x)", []string{"A"}},
		{"load order with absent between", []extension.Extension{a, {Path: "absent.ts"}, b}, "B(A(x))", []string{"A", "B"}},
		{"reversed load order", []extension.Extension{b, a}, "A(B(x))", []string{"B", "A"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls = nil
			runner := inproc.NewRunner(tc.exts, t.TempDir())
			transformers := runner.GetMarkdownTransformers()
			if len(transformers) != len(tc.calls) || len(calls) != 0 {
				t.Fatalf("collection count=%d calls=%v; want count=%d and no invocation", len(transformers), calls, len(tc.calls))
			}
			got := "x"
			for _, transformer := range transformers {
				got = transformer(got, extension.MarkdownTransformContext{MessageType: "assistant", IsStreaming: true, AvailableWidth: 73})
			}
			if got != tc.want || !reflect.DeepEqual(calls, tc.calls) {
				t.Fatalf("result=%q calls=%v; want %q calls=%v", got, calls, tc.want, tc.calls)
			}
			if len(transformers) > 0 {
				transformers[0] = nil
				if runner.GetMarkdownTransformers()[0] == nil {
					t.Fatal("mutating the returned collection changed the registration")
				}
			}
		})
	}
}

func BenchmarkRunnerMarkdownTransformerCollection(b *testing.B) {
	for _, count := range []int{0, 1, 64, 1024} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			exts := make([]extension.Extension, count)
			for i := range exts {
				exts[i].MarkdownTransformer = func(text string, _ extension.MarkdownTransformContext) string { return text }
			}
			runner := inproc.NewRunner(exts, b.TempDir())
			b.ReportAllocs()
			for b.Loop() {
				if len(runner.GetMarkdownTransformers()) != count {
					b.Fatal("collection lost a registration")
				}
			}
		})
	}
}
