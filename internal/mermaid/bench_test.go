package mermaid

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"
)

// reportPeakRSS adds the process's peak resident set (Linux VmHWM) as a
// benchmark metric. The peak spans the whole process, so run one case per
// process (-bench 'Render/flowchart_10x_caps') to attribute it to that case.
func reportPeakRSS(b *testing.B) {
	b.Helper()
	status, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return
	}
	for line := range strings.SplitSeq(string(status), "\n") {
		if rest, ok := strings.CutPrefix(line, "VmHWM:"); ok {
			kb, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(rest), "kB")), 64)
			if err == nil {
				b.ReportMetric(kb, "peak-RSS-KiB")
			}
			return
		}
	}
}

// commonCorpus is the number of leading corpus.txt sources shared with the
// grok-mermaid 0.2.2 port, so the same file measures both generations.
const commonCorpus = 90

func benchSources(b *testing.B, n int) []string {
	b.Helper()
	data, err := os.ReadFile("testdata/corpus.txt")
	if err != nil {
		b.Fatal(err)
	}
	var out []string
	for raw := range strings.SplitSeq(string(data), "\n") {
		if raw == "" || strings.HasPrefix(raw, "#") {
			continue
		}
		var src string
		if err := json.Unmarshal([]byte(raw), &src); err != nil {
			b.Fatal(err)
		}
		out = append(out, src)
	}
	if n > 0 && len(out) > n {
		out = out[:n]
	}
	return out
}

// largeSources are diagrams at and beyond the size caps (maxNodes, maxEdges):
// the "huge" ones must stay bounded by the caps, not by the input size.
func largeSources() map[string]string {
	flow := func(nodes, edges int) string {
		var s strings.Builder
		s.WriteString("graph TD\n")
		for i := range edges {
			s.WriteString("  n" + itoaR(i%nodes) + "[Node " + itoaR(i%nodes) + "] --> n" + itoaR((i*7+1)%nodes) + "\n")
		}
		return s.String()
	}
	seq := func(msgs int) string {
		var s strings.Builder
		s.WriteString("sequenceDiagram\n")
		for i := range 12 {
			s.WriteString("  participant P" + itoaR(i) + "\n")
		}
		for i := range msgs {
			s.WriteString("  P" + itoaR(i%12) + "->>P" + itoaR((i*5+1)%12) + ": message " + itoaR(i) + "\n")
		}
		return s.String()
	}
	state := func(n int) string {
		var s strings.Builder
		s.WriteString("stateDiagram-v2\n  [*] --> S0\n")
		for i := range n {
			s.WriteString("  S" + itoaR(i) + " --> S" + itoaR(i+1) + " : go " + itoaR(i) + "\n")
		}
		return s.String()
	}
	class := func(n int) string {
		var s strings.Builder
		s.WriteString("classDiagram\n")
		for i := range n {
			s.WriteString("  class C" + itoaR(i) + " {\n    +int a\n    +run() void\n  }\n")
			if i > 0 {
				s.WriteString("  C" + itoaR(i-1) + " <|-- C" + itoaR(i) + "\n")
			}
		}
		return s.String()
	}
	return map[string]string{
		"flowchart_at_caps":   flow(128, 512),
		"flowchart_10x_caps":  flow(1280, 5120),
		"sequence_at_caps":    seq(512),
		"sequence_10x_caps":   seq(5120),
		"state_at_caps":       state(100),
		"state_10x_caps":      state(1000),
		"class_at_caps":       class(60),
		"class_10x_caps":      class(600),
		"flowchart_deep_nest": strings.Repeat("graph TD\n", 1) + strings.Repeat("subgraph s\n", 6) + "A-->B\n" + strings.Repeat("end\n", 6),
	}
}

func BenchmarkRender(b *testing.B) {
	for name, n := range map[string]int{"corpus_common": commonCorpus, "corpus_all": 0} {
		srcs := benchSources(b, n)
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				for _, s := range srcs {
					_, _ = Render(s)
				}
			}
			reportPeakRSS(b)
		})
	}
	for name, src := range largeSources() {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_, _ = Render(src)
			}
			reportPeakRSS(b)
		})
	}
}

// parseAny runs the parser for the source's diagram family, through the entry
// points the 0.2.2 and 0.3.3 ports share.
func parseAny(src string) {
	switch {
	case strings.HasPrefix(src, "stateDiagram"):
		_ = parseState(src)
	case strings.HasPrefix(src, "sequenceDiagram"):
		_ = parseSequence(src)
	case strings.HasPrefix(src, "graph"), strings.HasPrefix(src, "flowchart"):
		_ = parseGraph(src)
	}
}

func BenchmarkParse(b *testing.B) {
	for name, n := range map[string]int{"corpus_common": commonCorpus, "corpus_all": 0} {
		srcs := benchSources(b, n)
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				for _, s := range srcs {
					parseAny(s)
				}
			}
			reportPeakRSS(b)
		})
	}
	for name, src := range largeSources() {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				parseAny(src)
			}
			reportPeakRSS(b)
		})
	}
}
