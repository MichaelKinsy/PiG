//go:build !race

package inproc_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// Collecting the Markdown transformers allocates the list and nothing else, whatever their number: a registration holds the wrapper that limits its
// transformer to its surfaces (Pi runner.ts getMarkdownTransformers makes that wrapper again on every call). One allocation for 64 transformers
// among 128 extensions, none for a runner without one.
func TestRunnerMarkdownTransformerCollectionAllocatesTheListOnce(t *testing.T) {
	const registered = 64
	exts := make([]extension.Extension, 2*registered)
	for i := 0; i < len(exts); i += 2 {
		exts[i].MarkdownTransformer = extension.NewMarkdownTransformerRegistration(func(text string, _ extension.MarkdownTransformContext) string { return text + "!" }, nil)
	}
	runner := inproc.NewRunner(exts, t.TempDir())
	var transformers []extension.MarkdownTransformer
	if got := testing.AllocsPerRun(20, func() { transformers = runner.GetMarkdownTransformers() }); got != 1 {
		t.Errorf("collecting %d transformers allocated %v times, want 1: the list", registered, got)
	}
	if len(transformers) != registered || cap(transformers) != registered {
		t.Fatalf("collected %d transformers in a list of capacity %d, want %d and no spare capacity", len(transformers), cap(transformers), registered)
	}
	text := ""
	for _, transformer := range transformers {
		text = transformer(text, extension.MarkdownTransformContext{MessageType: extension.MarkdownMessageAssistant})
	}
	if len(text) != registered {
		t.Fatalf("the collected transformers appended %d marks, want one each (%d)", len(text), registered)
	}
	none := inproc.NewRunner(make([]extension.Extension, registered), t.TempDir())
	if got := testing.AllocsPerRun(20, func() { transformers = none.GetMarkdownTransformers() }); got != 0 || transformers != nil {
		t.Errorf("a runner without a transformer allocated %v times and returned %v, want no allocation and a nil list", got, transformers)
	}
}
