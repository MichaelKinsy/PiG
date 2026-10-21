package telemetry_test

// Ports packages/telemetry/test/render.test.ts.
//
// SpanAttributes is an ordered bag, so RenderTrace writes attributes in insertion order as upstream does.

import (
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/telemetry"
)

func renderSpan(id string, mutate func(*telemetry.StoredSpan)) telemetry.StoredSpan {
	span := telemetry.StoredSpan{
		ID: id, TraceID: "trace", Name: id, StartedMs: 0, EndedMs: 10,
		Attributes: telemetry.SpanAttributes{}, Events: []telemetry.StoredSpanEvent{}, Links: []telemetry.StoredSpanLink{},
		Status: telemetry.SpanStatus{Status: telemetry.SpanStatusCodeOK},
	}
	if mutate != nil {
		mutate(&span)
	}
	return span
}

func TestRenderTrace(t *testing.T) {
	t.Run("renders an unordered trace with attributes, durations, errors, events, and links", func(t *testing.T) {
		root := renderSpan("root", func(span *telemetry.StoredSpan) {
			span.StartedMs, span.EndedMs = 1000, 1020
			span.Attributes = telemetry.SpanAttributes{
				telemetry.Attr("request", "a b"), telemetry.Attr("attempt", 2), telemetry.Attr("cached", false),
				telemetry.Attr("tags", []string{"a", "b"}), telemetry.Attr("sizes", []float64{1, 2}), telemetry.Attr("flags", []bool{true, false}),
			}
			span.Events = []telemetry.StoredSpanEvent{
				{Name: "checkpoint", TimeMs: 2, Attributes: telemetry.SpanAttributes{telemetry.Attr("phase", "tools")}},
				{Name: "tick", TimeMs: 3.5, Attributes: telemetry.SpanAttributes{}},
			}
			span.Links = []telemetry.StoredSpanLink{
				{TraceID: "remote-trace", SpanID: "remote", Attributes: telemetry.SpanAttributes{telemetry.Attr("reason", "steer")}},
				{TraceID: "other-trace", SpanID: "other"},
			}
		})
		first := renderSpan("first", func(span *telemetry.StoredSpan) {
			span.ParentID = new("root")
			span.StartedMs, span.EndedMs = 1005, 1009.5
			span.Status = telemetry.SpanStatus{Status: telemetry.SpanStatusCodeError, Error: &telemetry.SpanStatusError{Name: "Error", Message: "failed call"}}
		})
		nested := renderSpan("nested", func(span *telemetry.StoredSpan) {
			span.ParentID = new("first")
			span.StartedMs, span.EndedMs = 1006, 1007
		})
		second := renderSpan("second", func(span *telemetry.StoredSpan) {
			span.ParentID = new("root")
			span.StartedMs, span.EndedMs = 1010, 1012
			span.Status = telemetry.SpanStatus{Status: telemetry.SpanStatusCodeError}
		})
		spans := []telemetry.StoredSpan{second, nested, first, root}
		before := append([]telemetry.StoredSpan(nil), spans...)
		want := strings.Join([]string{
			`root request="a b" attempt=2 cached=false tags=["a","b"] sizes=[1,2] flags=[true,false] (20ms)`,
			`  · checkpoint phase="tools" (+2ms)`,
			"  · tick (+3.5ms)",
			`  → link trace remote-trace span remote reason="steer"`,
			"  → link trace other-trace span other",
			"  first (4.5ms) error Error: failed call",
			"    nested (1ms)",
			"  second (2ms) error",
		}, "\n")
		if got := telemetry.RenderTrace(spans); got != want {
			t.Fatalf("RenderTrace =\n%s\nwant\n%s", got, want)
		}
		if !reflect.DeepEqual(spans, before) {
			t.Fatal("RenderTrace changed its input")
		}
	})

	t.Run("treats missing parents as roots and orders roots and children by start time", func(t *testing.T) {
		got := telemetry.RenderTrace([]telemetry.StoredSpan{
			renderSpan("later", func(span *telemetry.StoredSpan) {
				span.ParentID, span.StartedMs, span.EndedMs = new("external"), 20, 30
			}),
			renderSpan("child", func(span *telemetry.StoredSpan) { span.ParentID, span.StartedMs, span.EndedMs = new("later"), 25, 30 }),
			renderSpan("earlier", func(span *telemetry.StoredSpan) {
				span.StartedMs, span.EndedMs = 10, 15
				span.Links = []telemetry.StoredSpanLink{{TraceID: "trace", SpanID: "later"}}
			}),
		})
		want := strings.Join([]string{"earlier (5ms)", "  → link trace trace span later", "later (10ms)", "  child (5ms)"}, "\n")
		if got != want {
			t.Fatalf("RenderTrace =\n%s\nwant\n%s", got, want)
		}
	})

	t.Run("prints cross-trace links without following them, including cycles", func(t *testing.T) {
		got := telemetry.RenderTrace([]telemetry.StoredSpan{
			renderSpan("server:1", func(span *telemetry.StoredSpan) {
				span.Name, span.TraceID = "server", "server-trace"
				span.Links = []telemetry.StoredSpanLink{{TraceID: "worker-trace", SpanID: "worker:1"}}
			}),
			renderSpan("worker:1", func(span *telemetry.StoredSpan) {
				span.Name, span.TraceID = "worker", "worker-trace"
				span.Links = []telemetry.StoredSpanLink{{TraceID: "server-trace", SpanID: "server:1"}}
			}),
		})
		want := strings.Join([]string{
			"server (10ms)",
			"  → link trace worker-trace span worker:1",
			"worker (10ms)",
			"  → link trace server-trace span server:1",
		}, "\n")
		if got != want {
			t.Fatalf("RenderTrace =\n%s\nwant\n%s", got, want)
		}
	})

	t.Run("deduplicates spans merged from stores by global ID", func(t *testing.T) {
		root := renderSpan("server:1", func(span *telemetry.StoredSpan) { span.Name = "root" })
		child := renderSpan("worker:1", func(span *telemetry.StoredSpan) {
			span.Name, span.ParentID = "child", new("server:1")
			span.Events = []telemetry.StoredSpanEvent{{Name: "done", TimeMs: 3, Attributes: telemetry.SpanAttributes{}}}
		})
		grandchild := renderSpan("worker:2", func(span *telemetry.StoredSpan) { span.Name, span.ParentID = "grandchild", new("worker:1") })
		got := telemetry.RenderTrace([]telemetry.StoredSpan{child, root, grandchild, root, child})
		if want := "root (10ms)\n  child (10ms)\n    · done (+3ms)\n    grandchild (10ms)"; got != want {
			t.Fatalf("RenderTrace =\n%s\nwant\n%s", got, want)
		}
	})

	t.Run("preserves input order for equal start times without mutating the input", func(t *testing.T) {
		spans := []telemetry.StoredSpan{
			renderSpan("root", nil),
			renderSpan("z", func(span *telemetry.StoredSpan) { span.ParentID = new("root") }),
			renderSpan("a", func(span *telemetry.StoredSpan) { span.ParentID = new("root") }),
			renderSpan("other", nil),
		}
		before := append([]telemetry.StoredSpan(nil), spans...)
		if got, want := telemetry.RenderTrace(spans), "root (10ms)\n  z (10ms)\n  a (10ms)\nother (10ms)"; got != want {
			t.Fatalf("RenderTrace =\n%s\nwant\n%s", got, want)
		}
		if !reflect.DeepEqual(spans, before) {
			t.Fatal("RenderTrace changed its input")
		}
	})

	t.Run("escapes string attribute values without hiding their contents", func(t *testing.T) {
		got := telemetry.RenderTrace([]telemetry.StoredSpan{renderSpan("root", func(span *telemetry.StoredSpan) {
			span.Attributes = telemetry.SpanAttributes{telemetry.Attr("text", "line one\n\"line two\"")}
		})})
		if want := `root text="line one\n\"line two\"" (10ms)`; got != want {
			t.Fatalf("RenderTrace = %s, want %s", got, want)
		}
	})

	t.Run("renders deep trees without recursive calls", func(t *testing.T) {
		spans := make([]telemetry.StoredSpan, 2000)
		for index := range spans {
			spans[index] = renderSpan(strconv.Itoa(index), func(span *telemetry.StoredSpan) {
				if index > 0 {
					span.ParentID = new(strconv.Itoa(index - 1))
				}
			})
		}
		rendered := telemetry.RenderTrace(spans)
		if lines := strings.Split(rendered, "\n"); len(lines) != len(spans) {
			t.Fatalf("lines = %d, want %d", len(lines), len(spans))
		}
		if !strings.HasSuffix(rendered, strings.Repeat("  ", 1999)+"1999 (10ms)") {
			t.Fatal("the deepest span is not rendered at depth 1999")
		}
	})

	t.Run("renders an empty input as an empty string", func(t *testing.T) {
		if got := telemetry.RenderTrace(nil); got != "" {
			t.Fatalf("RenderTrace(nil) = %q", got)
		}
	})
}

// RenderTrace writes values as JSON.stringify does: JavaScript number text, null for a non-finite number, and only quotes,
// backslashes and control characters escaped in strings.
func TestRenderTraceWritesJSONStringifyValues(t *testing.T) {
	got := telemetry.RenderTrace([]telemetry.StoredSpan{renderSpan("root", func(span *telemetry.StoredSpan) {
		span.Attributes = telemetry.SpanAttributes{
			telemetry.Attr("big", 1e21), telemetry.Attr("fraction", 0.1), telemetry.Attr("int", 7), telemetry.Attr("sizes", []float64{1.5, 2}),
			telemetry.Attr("text", "tab\there \u0001 \u2028 é<>&"),
		}
	})})
	if want := "root big=1e+21 fraction=0.1 int=7 sizes=[1.5,2] text=\"tab\\there \\u0001 \u2028 é<>&\" (10ms)"; got != want {
		t.Fatalf("RenderTrace = %q, want %q", got, want)
	}
}
