package subprocess

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/packagecontent"
)

// .upstream/v0.87.1/packages/coding-agent/test/extensions-runner.test.ts:708 creates two distinct source factories with identity transformers and expects both. Keep that fixture separate from the order-sensitive guard.
func TestUpstreamRunnerMarkdownSourceFactories(t *testing.T) {
	t.Parallel()
	nodeCellRequireNode(t)
	for _, isolation := range []string{"", "isolated"} {
		t.Run("isolation="+isolation, func(t *testing.T) {
			root := t.TempDir()
			const code = `export default function(pi) {
	pi.registerMarkdownTransformer((markdown) => markdown);
}`
			for _, name := range []string{"markdown-renderer-a.ts", "markdown-renderer-b.ts"} {
				write(t, filepath.Join(root, "extensions", name), code)
			}
			var configs []ExtConfig
			for _, path := range packagecontent.DiscoverAutomatic(filepath.Join(root, "extensions"), packagecontent.Extensions) {
				configs = append(configs, ExtConfig{Name: strings.TrimSuffix(filepath.Base(path), ".ts"), Source: path, Enabled: true, Isolation: isolation})
			}
			host := NewHost(root)
			t.Cleanup(func() { host.Shutdown("test done") })
			loaded, failures := host.LoadAll(t.Context(), configs)
			if len(failures) != 0 {
				t.Fatalf("load errors: %v", failures)
			}
			runner := inproc.NewRunner(loaded, root)
			transformers := runner.GetMarkdownTransformers()
			if len(transformers) != 2 {
				t.Fatalf("transformers=%d; want two source registrations", len(transformers))
			}
			for i, transform := range transformers {
				if got := transform("**identity**", extension.MarkdownTransformContext{Context: t.Context(), MessageType: "user", AvailableWidth: 80}); got != "**identity**" {
					t.Fatalf("transformer[%d]=%q; want identity", i, got)
				}
			}
		})
	}
}
