package inproc_test

import (
	"errors"
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
	a := extension.Extension{Path: "z-first.ts", MarkdownTransformer: extension.NewMarkdownTransformerRegistration(transform("A"), nil)}
	b := extension.Extension{Path: "a-second.ts", MarkdownTransformer: extension.NewMarkdownTransformerRegistration(transform("B"), nil)}
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
				exts[i].MarkdownTransformer = extension.NewMarkdownTransformerRegistration(func(text string, _ extension.MarkdownTransformContext) string { return text }, nil)
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

// Pi runner.ts getMarkdownTransformers reads every extension's registration on each call and hands out functions that hold what it read: a list
// collected earlier keeps its transformers and their surfaces, and the next collection sees a registration made, replaced or removed since, also
// when a transformer of the running chain is what made it.
func TestRunnerMarkdownTransformerCollectionFollowsRegistrations(t *testing.T) {
	named := func(name string) extension.MarkdownTransformer {
		return func(markdown string, _ extension.MarkdownTransformContext) string { return name + "(" + markdown + ")" }
	}
	expect := func(step string, transformers []extension.MarkdownTransformer, messageType extension.MarkdownMessageType, want string) {
		t.Helper()
		got := "x"
		for _, transformer := range transformers {
			got = transformer(got, extension.MarkdownTransformContext{MessageType: messageType})
		}
		if got != want {
			t.Errorf("%s, on %s: %q, want %q", step, messageType, got, want)
		}
	}
	exts := []extension.Extension{{Path: "first.ts"}, {Path: "second.ts"}}
	exts[0].MarkdownTransformer = extension.NewMarkdownTransformerRegistration(named("A"), nil)
	runner := inproc.NewRunner(exts, t.TempDir())
	first := runner.GetMarkdownTransformers()
	expect("one registration", first, extension.MarkdownMessageAssistant, "A(x)")

	exts[1].MarkdownTransformer = extension.NewMarkdownTransformerRegistration(named("B"), nil)
	second := runner.GetMarkdownTransformers()
	expect("a registration made after the first collection", second, extension.MarkdownMessageAssistant, "B(A(x))")
	expect("the first collection after that registration", first, extension.MarkdownMessageAssistant, "A(x)")

	exts[0].MarkdownTransformer = extension.NewMarkdownTransformerRegistration(named("A2"), []extension.MarkdownMessageType{extension.MarkdownMessageChangelog})
	third := runner.GetMarkdownTransformers()
	expect("a registration replaced", third, extension.MarkdownMessageAssistant, "B(x)")
	expect("a registration replaced", third, extension.MarkdownMessageChangelog, "A2(x)")
	expect("the second collection after the replacement", second, extension.MarkdownMessageAssistant, "B(A(x))")
	expect("the second collection after the replacement", second, extension.MarkdownMessageChangelog, "x")

	exts[1].MarkdownTransformer = nil
	if fourth := runner.GetMarkdownTransformers(); len(fourth) != 1 {
		t.Fatalf("the runner collects %d transformers after a removal, want 1", len(fourth))
	}
	expect("the third collection after the removal", third, extension.MarkdownMessageAssistant, "B(x)")

	// A transformer that registers another and collects while its chain runs: the chain is the list it was collected into.
	var inner []extension.MarkdownTransformer
	exts[0].MarkdownTransformer = extension.NewMarkdownTransformerRegistration(func(markdown string, _ extension.MarkdownTransformContext) string {
		exts[1].MarkdownTransformer = extension.NewMarkdownTransformerRegistration(named("C"), nil)
		inner = runner.GetMarkdownTransformers()
		return "R(" + markdown + ")"
	}, nil)
	outer := runner.GetMarkdownTransformers()
	expect("a chain whose transformer registers another", outer, extension.MarkdownMessageUser, "R(x)")
	if len(outer) != 1 || len(inner) != 2 {
		t.Fatalf("the chain holds %d transformers and the collection made inside it %d, want 1 and 2", len(outer), len(inner))
	}
	expect("the transformer registered inside the chain", inner[1:], extension.MarkdownMessageUser, "C(x)")

	// A transformer's panic (Pi's thrown exception) reaches the caller through the wrapper, and only on a surface its registration lists.
	failure := errors.New("transformer failed")
	exts[1].MarkdownTransformer = extension.NewMarkdownTransformerRegistration(func(string, extension.MarkdownTransformContext) string { panic(failure) }, []extension.MarkdownMessageType{extension.MarkdownMessageSummary})
	failing := runner.GetMarkdownTransformers()[1:]
	expect("a failing transformer off its surfaces", failing, extension.MarkdownMessageUser, "x")
	func() {
		defer func() {
			if got := recover(); got != failure {
				t.Errorf("the transformer's panic reached the caller as %v, want %v", got, failure)
			}
		}()
		failing[0]("x", extension.MarkdownTransformContext{MessageType: extension.MarkdownMessageSummary})
	}()

	// A registration made without the constructor holds no wrapper: the runner makes one from its members.
	exts[1].MarkdownTransformer = &extension.MarkdownTransformerRegistration{Transformer: named("L"), MessageTypes: map[extension.MarkdownMessageType]struct{}{extension.MarkdownMessageCustom: {}}}
	literal := runner.GetMarkdownTransformers()[1:]
	expect("a registration made as a literal", literal, extension.MarkdownMessageCustom, "L(x)")
	expect("a registration made as a literal", literal, extension.MarkdownMessageUser, "x")
}

// Pi eebd5d90 extensions-runner.test.ts "runs Markdown transformers only on their message types": getMarkdownTransformers wraps each transformer so Markdown of
// a surface its registration does not list passes through unchanged; a registration without a list runs on the transcript types.
func TestRunnerMarkdownTransformersRunOnlyOnTheirMessageTypes(t *testing.T) {
	appendTo := func(suffix string) extension.MarkdownTransformer {
		return func(markdown string, _ extension.MarkdownTransformContext) string { return markdown + suffix }
	}
	runner := inproc.NewRunner([]extension.Extension{
		{Path: "markdown-default.ts", MarkdownTransformer: extension.NewMarkdownTransformerRegistration(appendTo(" default"), nil)},
		{Path: "markdown-opt-in.ts", MarkdownTransformer: extension.NewMarkdownTransformerRegistration(appendTo(" opt-in"), []extension.MarkdownMessageType{extension.MarkdownMessageAssistant, extension.MarkdownMessageChangelog})},
		{Path: "markdown-none.ts", MarkdownTransformer: extension.NewMarkdownTransformerRegistration(appendTo(" none"), []extension.MarkdownMessageType{})},
	}, t.TempDir())
	apply := func(messageType extension.MarkdownMessageType) string {
		got := "text"
		for _, transformer := range runner.GetMarkdownTransformers() {
			got = transformer(got, extension.MarkdownTransformContext{MessageType: messageType, AvailableWidth: 80})
		}
		return got
	}
	for messageType, want := range map[extension.MarkdownMessageType]string{
		extension.MarkdownMessageUser:              "text default",
		extension.MarkdownMessageAssistantThinking: "text default",
		extension.MarkdownMessageAssistant:         "text default opt-in",
		extension.MarkdownMessageChangelog:         "text opt-in",
		extension.MarkdownMessageCustom:            "text",
		extension.MarkdownMessageSummary:           "text",
	} {
		if got := apply(messageType); got != want {
			t.Errorf("%s: %q, want %q", messageType, got, want)
		}
	}
}
