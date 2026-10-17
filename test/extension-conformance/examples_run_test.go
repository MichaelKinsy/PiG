package extensionconformance

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/test/extension-conformance/sdkscenarios"
)

// TestExamplesRunThroughTheHost loads every example extension in
// examples/extensions into a real host with the SDK it is written for and calls
// its tools (hello, and count_words with a parameter and an error result), so an example that compiles but does not run, or an SDK change that
// breaks the published example, fails here and not in a reader's terminal.
func TestExamplesRunThroughTheHost(t *testing.T) {
	if testing.Short() {
		t.Skip("builds subprocess fixtures")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	examples := root + "/examples/extensions/"
	cases := []struct {
		sdk    sdkscenarios.SDK
		name   string
		config subprocess.ExtConfig
		want   string
	}{
		{sdkscenarios.Go, "go-factory", subprocess.ExtConfig{
			Source: examples + "go-factory", Factory: "Extension", SDKName: "github.com/MichaelKinsy/PiG/extensions/sdk",
			ModulePath: "example.com/exgo", Package: "example.com/exgo",
		}, "hello from go"},
		{sdkscenarios.Rust, "rust-factory", subprocess.ExtConfig{
			Source: examples + "rust-factory", Factory: "new_extension", SDKName: "pig-sdk", Package: "rust-factory",
		}, "hello from rust"},
		{sdkscenarios.Python, "python-factory", subprocess.ExtConfig{
			Source: examples + "python-factory", Factory: "new_extension", SDKName: "pig-sdk-py", Package: "python_factory",
		}, "hello from python"},
		{sdkscenarios.Node, "node-factory", subprocess.ExtConfig{Source: examples + "node-factory/index.mjs"}, "hello from node"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			config := tc.config
			config.Name, config.Enabled, config.Isolation = tc.name, true, "strict"
			if tc.sdk != sdkscenarios.Node {
				config.RuntimeKind, config.RuntimeLanguage, config.EntrypointKind = "subprocess", string(tc.sdk), "factory"
			}
			host := subprocess.NewHost(t.TempDir())
			host.SetUIBridge(subprocess.NewUIBridge(nil))
			t.Cleanup(func() { host.Shutdown("done") })
			loaded, errs := host.LoadAll(t.Context(), []subprocess.ExtConfig{config})
			if len(errs) != 0 {
				t.Fatalf("load %s: %v", tc.name, errs)
			}
			tool, ok := loaded[0].Tools["hello"]
			if !ok {
				t.Fatalf("%s registered no hello tool; it has %v", tc.name, loaded[0].ToolOrder)
			}
			result, err := tool.Definition.Execute(t.Context(), "call", json.RawMessage(`{}`), nil)
			if err != nil {
				t.Fatal(err)
			}
			if got := result.Text(); got != tc.want {
				t.Fatalf("hello answered %q, want %q", got, tc.want)
			}
			count, ok := loaded[0].Tools["count_words"]
			if !ok {
				t.Fatalf("%s registered no count_words tool; it has %v", tc.name, loaded[0].ToolOrder)
			}
			for text, want := range map[string]string{"  one two\tthree\n": "3 words", " \n": "0 words", "héllo wörld": "2 words"} {
				args, err := json.Marshal(map[string]any{"text": text})
				if err != nil {
					t.Fatal(err)
				}
				result, err := count.Definition.Execute(t.Context(), "call", args, nil)
				if err != nil {
					t.Fatalf("count_words %q: %v", text, err)
				}
				if got := result.Text(); got != want {
					t.Fatalf("count_words %q answered %q, want %q", text, got, want)
				}
			}
			for _, args := range []string{`{}`, `{"text":""}`, `{"text":3}`} {
				if _, err := count.Definition.Execute(t.Context(), "call", json.RawMessage(args), nil); err == nil || !strings.Contains(err.Error(), "text is required") {
					t.Fatalf("count_words %s: error = %v, want one naming the missing text", args, err)
				}
			}
		})
	}
}
