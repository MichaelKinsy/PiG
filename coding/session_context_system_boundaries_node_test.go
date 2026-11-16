//go:build !pig_strip_node_extensions

package coding

import (
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The same identity rules hold through every SDK transport (runner.ts
// sameMessages, restoreSystemMessages): a list of the same messages, or messages
// edited in place, keeps every system message where it was, while a reordered
// list or a list of fresh messages that equal the old ones in value collapses them
// into one leading system message. Each SDK writes a row in its own idiom; the Rust
// SDK holds values, not Pi's objects, so it reports a list it returns as a
// replacement and the conversation it left in place as unchanged only when it says so.
func TestContextPhaseIdentityThroughSDKTransports(t *testing.T) {
	collapsedReordered := []string{"system:first\n\nsecond", "user:two", "user:one"}
	collapsed := []string{"system:first\n\nsecond", "user:one", "user:two"}
	kept := []string{"system:first", "user:edited", "system:second", "user:two"}
	untouched := []string{"system:first", "user:one", "system:second", "user:two"}
	want := map[string][]string{"reorder": collapsedReordered, "equal": collapsed, "edit": kept, "same": untouched}
	goHandlers := map[string]func(sdk.Context, map[string]any) (any, error){
		"reorder": func(_ sdk.Context, data map[string]any) (any, error) {
			messages := data["messages"].([]any)
			messages[0], messages[1] = messages[1], messages[0]
			return nil, nil
		},
		"equal": func(_ sdk.Context, data map[string]any) (any, error) {
			messages := data["messages"].([]any)
			replacement := maps.Clone(messages[0].(map[string]any))
			return map[string]any{"messages": []any{replacement, messages[1]}}, nil
		},
		"edit": func(_ sdk.Context, data map[string]any) (any, error) {
			data["messages"].([]any)[0].(map[string]any)["content"] = "edited"
			return nil, nil
		},
		"same": func(_ sdk.Context, data map[string]any) (any, error) {
			return map[string]any{"messages": slices.Clone(data["messages"].([]any))}, nil
		},
	}
	nodeHandlers := map[string]string{
		"reorder": `event.messages.reverse();`,
		"equal":   `return { messages: [{ ...event.messages[0] }, event.messages[1]] };`,
		"edit":    `event.messages[0].content = "edited";`,
		"same":    `return { messages: [...event.messages] };`,
	}
	pythonHandlers := map[string]string{
		"reorder": `data["messages"].reverse()`,
		"equal":   `return {"messages": [dict(data["messages"][0]), data["messages"][1]]}`,
		"edit":    `data["messages"][0]["content"] = "edited"`,
		"same":    `return {"messages": list(data["messages"])}`,
	}
	rustHandlers := map[string]string{
		"reorder": `data["messages"].as_array_mut().unwrap().reverse(); None`,
		"equal":   `let first = data["messages"][0].clone(); let second = data["messages"][1].clone(); Some(json!({"messages": [first, second]}))`,
		"edit":    `data["messages"][0]["content"] = json!("edited"); pig_sdk::context_unchanged()`,
		"same":    `None`,
	}
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	load := func(t *testing.T, config subprocess.ExtConfig) extension.Extension {
		t.Helper()
		host := subprocess.NewHost(t.TempDir())
		t.Cleanup(func() { host.Shutdown("test complete") })
		loaded, err := host.Load(t.Context(), config)
		if err != nil {
			t.Fatal(err)
		}
		return *loaded
	}
	for name, handler := range goHandlers {
		t.Run("fused-go/"+name, func(t *testing.T) {
			ext := sdk.New("context-" + name)
			ext.OnEvent("context", handler)
			host := subprocess.NewHost(t.TempDir())
			defer host.Shutdown("test complete")
			loaded, err := host.LoadInProcess(t.Context(), subprocess.ExtConfig{Name: "context-" + name, Enabled: true}, ext.RunWithConn)
			if err != nil {
				t.Fatal(err)
			}
			contextBoundaryThroughRunner(t, *loaded, want[name])
		})
	}
	for name, body := range nodeHandlers {
		t.Run("node/"+name, func(t *testing.T) {
			if _, err := exec.LookPath("node"); err != nil {
				t.Fatalf("node is required: %v", err)
			}
			source := filepath.Join(t.TempDir(), "index.mjs")
			if err := os.WriteFile(source, []byte(`export default function (pi) { pi.on("context", (event) => { `+body+` }); }`+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			contextBoundaryThroughRunner(t, load(t, subprocess.ExtConfig{Name: "context-" + name, Source: source, Enabled: true}), want[name])
		})
	}
	for name, body := range pythonHandlers {
		t.Run("python/"+name, func(t *testing.T) {
			source := filepath.Join(t.TempDir(), "main.py")
			program := fmt.Sprintf("import sys\nsys.path.insert(0, %q)\nimport pig_sdk\n\n\ndef handle(ctx, data):\n    %s\n\n\next = pig_sdk.Extension(%q)\next.on_event(\"context\", handle)\n\nif __name__ == \"__main__\":\n    ext.run()\n", filepath.ToSlash(filepath.Join(root, "extensions", "sdk-py")), body, "context-"+name)
			if err := os.WriteFile(source, []byte(program), 0o644); err != nil {
				t.Fatal(err)
			}
			contextBoundaryThroughRunner(t, load(t, subprocess.ExtConfig{Name: "context-" + name, Path: source, Enabled: true, RuntimeLanguage: "python"}), want[name])
		})
	}
	for name, body := range rustHandlers {
		t.Run("rust/"+name, func(t *testing.T) {
			dir := t.TempDir()
			cargo := fmt.Sprintf("[package]\nname = %q\nversion = \"0.0.0\"\nedition = \"2024\"\n\n[dependencies]\npig-sdk = { path = %q }\nserde_json = \"1\"\n", "context-"+name, filepath.ToSlash(filepath.Join(root, "extensions", "sdk-rs")))
			program := fmt.Sprintf("use pig_sdk::Extension;\nuse serde_json::json;\n\nfn main() {\n    let mut ext = Extension::new(%q);\n    ext.on_event(\"context\", |_ctx, data| { %s });\n    ext.run().unwrap();\n}\n", "context-"+name, body)
			if err := os.WriteFile(filepath.Join(dir, "Cargo.toml"), []byte(cargo), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(filepath.Join(dir, "src"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "src", "main.rs"), []byte(program), 0o644); err != nil {
				t.Fatal(err)
			}
			contextBoundaryThroughRunner(t, load(t, subprocess.ExtConfig{Name: "context-" + name, Source: dir, Enabled: true}), want[name])
		})
	}
}
