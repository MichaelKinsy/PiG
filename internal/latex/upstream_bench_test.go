package latex

import (
	"bufio"
	"encoding/json"
	"os"
	"testing"
)

func BenchmarkUpstreamLatexCorpus(b *testing.B) {
	f, err := os.Open("testdata/upstream-corpus.txt")
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var inputs []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var input string
		if err := json.Unmarshal(scanner.Bytes(), &input); err != nil {
			b.Fatal(err)
		}
		inputs = append(inputs, input)
	}
	if err := scanner.Err(); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		for _, input := range inputs {
			RenderLatex(input, RenderLatexOptions{})
			RenderLatex(input, RenderLatexOptions{Display: true})
		}
	}
}
