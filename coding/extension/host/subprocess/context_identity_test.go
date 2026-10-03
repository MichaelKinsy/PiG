package subprocess

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

const nodeContextIdentityExtension = `export default function (pi) {
  pi.on("context", (event) => {
    if (event.messages[0].content === "replace") return { messages: [{ ...event.messages[0], content: "edited" }, event.messages[1]] };
    event.messages[0].content = "edited";
    if (event.messages[0].timestamp === 1) return { messages: event.messages };
  });
}
`

// Source: runner.ts emitContext/sameMessages. An SDK handler's in-place edit
// (returning nothing or the same list) crosses the wire as an unchanged
// conversation, so the Session keeps every system message in place; a new
// list is a replacement. REFNL-003.
func TestContextIdentityCrossesSDKTransports(t *testing.T) {
	loaders := map[string]func(t *testing.T) extension.Extension{
		"fused-go": func(t *testing.T) extension.Extension {
			ext := sdk.New("context-identity")
			ext.OnEvent("context", func(_ sdk.Context, data map[string]any) (any, error) {
				messages := data["messages"].([]any)
				first := messages[0].(map[string]any)
				if first["content"] == "replace" {
					copied := map[string]any{"role": first["role"], "content": "edited", "timestamp": first["timestamp"]}
					return map[string]any{"messages": []any{copied, messages[1]}}, nil
				}
				first["content"] = "edited"
				if first["timestamp"] == float64(1) {
					return map[string]any{"messages": messages}, nil
				}
				return nil, nil
			})
			host := NewHost(t.TempDir())
			t.Cleanup(func() { host.Shutdown("test complete") })
			loaded, err := host.LoadInProcess(t.Context(), ExtConfig{Name: "context-identity", Enabled: true}, ext.RunWithConn)
			if err != nil {
				t.Fatal(err)
			}
			return *loaded
		},
		"node": func(t *testing.T) extension.Extension {
			if _, err := exec.LookPath("node"); err != nil {
				t.Fatalf("node is required: %v", err)
			}
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "index.mjs"), []byte(nodeContextIdentityExtension), 0o644); err != nil {
				t.Fatal(err)
			}
			host := NewHost(t.TempDir())
			t.Cleanup(func() { host.Shutdown("test complete") })
			loaded, err := host.Load(testbudget.Context(t), ExtConfig{Name: "context-identity", Source: filepath.Join(dir, "index.mjs"), Enabled: true})
			if err != nil {
				t.Fatal(err)
			}
			return *loaded
		},
	}
	for name, load := range loaders {
		t.Run(name, func(t *testing.T) {
			runner := inproc.NewRunner([]extension.Extension{load(t)}, t.TempDir())
			defer runner.Invalidate("test complete")
			for _, tc := range []struct {
				name         string
				content      string
				timestamp    int
				wantReplaced bool
			}{
				{name: "in place", content: "original", timestamp: 0},
				{name: "same list", content: "original", timestamp: 1},
				{name: "replacement", content: "replace", timestamp: 0, wantReplaced: true},
			} {
				messages := []extension.AgentMessage{
					map[string]any{"role": "user", "content": tc.content, "timestamp": tc.timestamp},
					map[string]any{"role": "user", "content": "two", "timestamp": 0},
				}
				got, replaced, err := runner.EmitContextTracked(t.Context(), messages)
				if err != nil {
					t.Fatalf("%s: %v", tc.name, err)
				}
				if replaced != tc.wantReplaced {
					t.Fatalf("%s: replaced = %t, want %t", tc.name, replaced, tc.wantReplaced)
				}
				if len(got) != 2 || got[0].(map[string]any)["content"] != "edited" || got[1].(map[string]any)["content"] != "two" {
					t.Fatalf("%s: context = %#v", tc.name, got)
				}
				if messages[0].(map[string]any)["content"] != tc.content {
					t.Fatalf("%s: context transform mutated its input", tc.name)
				}
			}
		})
	}
}

const nodeContextResultShapeExtension = `export default function (pi) {
  pi.on("context", (event) => {
    const [first, second] = event.messages;
    if (first.content === "typed") return { messages: [second] };
    if (first.content === "same") return { messages: [first, second] };
    if (first.content === "malformed") return { messages: "not-a-list" };
    if (first.content === "inplace" || first.content === "null") {
      first.content = "edited";
      if (second.content === "two-null") return { messages: null };
    }
  });
}
`

const pythonContextResultShapeExtension = `import sys
sys.path.insert(0, %q)
import pig_sdk


def handle(ctx, data):
    first, second = data["messages"][0], data["messages"][1]
    content = first["content"]
    if content == "typed":
        return {"messages": [second]}
    if content == "same":
        return {"messages": [first, second]}
    if content == "malformed":
        return {"messages": "not-a-list"}
    if content in ("inplace", "null"):
        first["content"] = "edited"
        if second["content"] == "two-null":
            return {"messages": None}
    return None


ext = pig_sdk.Extension("context-result-shape")
ext.on_event("context", handle)

if __name__ == "__main__":
    ext.run()
`

const rustContextResultShapeExtension = `use pig_sdk::Extension;
use serde_json::{json, Value};

fn main() {
    let mut ext = Extension::new("context-result-shape");
    ext.on_event("context", false, |_ctx, data| {
        let first = data["messages"][0].clone();
        let second = data["messages"][1].clone();
        match first["content"].as_str().unwrap_or_default() {
            "typed" => Some(json!({"messages": [second]})),
            "same" => Some(json!({"messages": [first, second]})),
            "malformed" => Some(json!({"messages": "not-a-list"})),
            "inplace" | "null" => {
                data["messages"][0]["content"] = json!("edited");
                if second["content"] == "two-null" { Some(json!({"messages": Value::Null})) } else { None }
            }
            _ => None,
        }
    });
    ext.run().unwrap();
}
`

// Source: runner.ts emitContext takes `handlerResult?.messages` as the
// replacement whenever it is not nullish, and sameMessages keeps the current
// list only for the same message objects in order. A Go handler's
// []map[string]any list is such an array, so it replaces the context (or keeps
// it when it holds the same objects); a messages value that is not a list is
// that handler's error, never a silently unchanged context. A handler that
// edits event.messages in place and returns nothing or a null messages value
// keeps its edit. The Rust SDK reports no list identity, so the host compares
// its lists by value and an in-place edit counts as a replacement there.
func TestContextResultShapesCrossSDKTransports(t *testing.T) {
	t.Parallel()
	type loader struct {
		load          func(t *testing.T) extension.Extension
		valueIdentity bool
	}
	loadSubprocess := func(t *testing.T, config ExtConfig) extension.Extension {
		t.Helper()
		host := NewHost(t.TempDir())
		t.Cleanup(func() { host.Shutdown("test complete") })
		loaded, err := host.Load(testbudget.Context(t), config)
		if err != nil {
			t.Fatal(err)
		}
		return *loaded
	}
	loaders := map[string]loader{
		"fused-go": {load: func(t *testing.T) extension.Extension {
			ext := sdk.New("context-result-shape")
			ext.OnEvent("context", func(_ sdk.Context, data map[string]any) (any, error) {
				messages := data["messages"].([]any)
				first, second := messages[0].(map[string]any), messages[1].(map[string]any)
				switch first["content"] {
				case "typed":
					return map[string]any{"messages": []map[string]any{second}}, nil
				case "same":
					return map[string]any{"messages": []map[string]any{first, second}}, nil
				case "malformed":
					return map[string]any{"messages": "not-a-list"}, nil
				case "inplace", "null":
					first["content"] = "edited"
					if second["content"] == "two-null" {
						return map[string]any{"messages": []any(nil)}, nil
					}
				}
				return nil, nil
			})
			host := NewHost(t.TempDir())
			t.Cleanup(func() { host.Shutdown("test complete") })
			loaded, err := host.LoadInProcess(t.Context(), ExtConfig{Name: "context-result-shape", Enabled: true}, ext.RunWithConn)
			if err != nil {
				t.Fatal(err)
			}
			return *loaded
		}},
		"node": {load: func(t *testing.T) extension.Extension {
			if _, err := exec.LookPath("node"); err != nil {
				t.Fatalf("node is required: %v", err)
			}
			source := filepath.Join(t.TempDir(), "index.mjs")
			if err := os.WriteFile(source, []byte(nodeContextResultShapeExtension), 0o644); err != nil {
				t.Fatal(err)
			}
			return loadSubprocess(t, ExtConfig{Name: "context-result-shape", Source: source, Enabled: true})
		}},
		"python": {load: func(t *testing.T) extension.Extension {
			source := filepath.Join(t.TempDir(), "main.py")
			sdkRoot := filepath.ToSlash(filepath.Join(findModuleRoot(t), "extensions", "sdk-py"))
			if err := os.WriteFile(source, fmt.Appendf(nil, pythonContextResultShapeExtension, sdkRoot), 0o644); err != nil {
				t.Fatal(err)
			}
			return loadSubprocess(t, ExtConfig{Name: "context-result-shape", Path: source, Enabled: true, RuntimeLanguage: "python"})
		}},
		"rust": {valueIdentity: true, load: func(t *testing.T) extension.Extension {
			dir := t.TempDir()
			sdkRoot := filepath.ToSlash(filepath.Join(findModuleRoot(t), "extensions", "sdk-rs"))
			cargo := fmt.Sprintf("[package]\nname = \"context-result-shape\"\nversion = \"0.0.0\"\nedition = \"2024\"\n\n[dependencies]\npig-sdk = { path = %q }\nserde_json = \"1\"\n", sdkRoot)
			if err := os.WriteFile(filepath.Join(dir, "Cargo.toml"), []byte(cargo), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(filepath.Join(dir, "src"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "src", "main.rs"), []byte(rustContextResultShapeExtension), 0o644); err != nil {
				t.Fatal(err)
			}
			return loadSubprocess(t, ExtConfig{Name: "context-result-shape", Source: dir, Enabled: true})
		}},
	}
	for name, sdkLoader := range loaders {
		t.Run(name, func(t *testing.T) {
			runner := inproc.NewRunner([]extension.Extension{sdkLoader.load(t)}, t.TempDir())
			defer runner.Invalidate("test complete")
			var mu sync.Mutex
			var failures []string
			runner.AddErrorListener(func(e *extension.ExtensionError) {
				if e.Event == "context" {
					mu.Lock()
					failures = append(failures, e.Error)
					mu.Unlock()
				}
			})
			for _, tc := range []struct {
				content, second string
				want            []string
				wantReplaced    bool
				wantError       bool
			}{
				{content: "typed", second: "two", want: []string{"two"}, wantReplaced: true},
				{content: "same", second: "two", want: []string{"same", "two"}},
				{content: "malformed", second: "two", want: []string{"malformed", "two"}, wantError: true},
				{content: "inplace", second: "two", want: []string{"edited", "two"}, wantReplaced: sdkLoader.valueIdentity},
				{content: "null", second: "two-null", want: []string{"edited", "two-null"}, wantReplaced: sdkLoader.valueIdentity},
			} {
				mu.Lock()
				failures = nil
				mu.Unlock()
				messages := []extension.AgentMessage{
					map[string]any{"role": "user", "content": tc.content, "timestamp": 0},
					map[string]any{"role": "user", "content": tc.second, "timestamp": 0},
				}
				got, replaced, err := runner.EmitContextTracked(t.Context(), messages)
				if err != nil {
					t.Fatalf("%s: %v", tc.content, err)
				}
				contents := make([]string, len(got))
				for i, message := range got {
					contents[i], _ = message.(map[string]any)["content"].(string)
				}
				if !slices.Equal(contents, tc.want) {
					t.Fatalf("%s: context = %q, want %q", tc.content, contents, tc.want)
				}
				if replaced != tc.wantReplaced {
					t.Fatalf("%s: replaced = %t, want %t", tc.content, replaced, tc.wantReplaced)
				}
				mu.Lock()
				gotFailures := slices.Clone(failures)
				mu.Unlock()
				if (len(gotFailures) == 1) != tc.wantError || len(gotFailures) > 1 {
					t.Fatalf("%s: context handler errors = %q, want error %t", tc.content, gotFailures, tc.wantError)
				}
			}
		})
	}
}
