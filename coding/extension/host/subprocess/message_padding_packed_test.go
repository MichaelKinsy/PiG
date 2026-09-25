package subprocess

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

func TestPackedMessageOutputPadAcrossSDKs(t *testing.T) {
	root := findModuleRoot(t)
	t.Setenv("PIG_SDK_GO_ROOT", filepath.Join(root, "extensions", "sdk"))
	t.Setenv("PIG_SDK_PY_ROOT", filepath.Join(root, "extensions", "sdk-py"))
	t.Setenv("PIG_SDK_RS_ROOT", filepath.Join(root, "extensions", "sdk-rs"))
	for _, language := range []string{"go", "python", "rust"} {
		t.Run(language, func(t *testing.T) {
			configs := []ExtConfig{paddingFactory(t, language, "padding_a"), paddingFactory(t, language, "padding_b")}
			host := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
			defer host.Shutdown("test done")
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
			defer cancel()
			loaded, errs := host.LoadAll(ctx, configs)
			if len(errs) != 0 || len(loaded) != len(configs) {
				t.Fatalf("load: %v (%d)", errs, len(loaded))
			}
			first, second := host.exts[configs[0].Name], host.exts[configs[1].Name]
			if first.packedCellKey == "" || first.packedCellKey != second.packedCellKey {
				t.Fatal("fixtures did not share a packed cell")
			}
			for _, ext := range loaded {
				renderer := ext.MessageRenderers["padding"]
				if renderer == nil {
					t.Fatal("missing renderer")
				}
				component := renderer(extension.CustomMessageRef{CustomType: "padding", Content: "custom"}, extension.MessageRenderOptions{OutputPad: 1}, nil).(*renderProxyComponent)
				for _, padding := range []int{1, 0, 1} {
					component.SetOutputPad(padding)
					component.SetExpanded(padding == 0)
					want := map[string]any{"expanded": padding == 0, "outputPad": float64(padding)}
					waitForRenderProxy(t, func() bool {
						lines := component.Render(40)
						var got map[string]any
						return len(lines) == 1 && json.Unmarshal([]byte(lines[0]), &got) == nil && reflect.DeepEqual(got, want)
					})
				}
			}
		})
	}
}

func paddingFactory(t *testing.T, language, name string) ExtConfig {
	t.Helper()
	dir := t.TempDir()
	write := func(path, text string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, path), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	switch language {
	case "go":
		module := "example.com/" + name
		sdkMod, err := os.ReadFile(filepath.Join(findModuleRoot(t), "extensions", "sdk", "go.mod"))
		if err != nil {
			t.Fatal(err)
		}
		_, directive, ok := strings.Cut(string(sdkMod), "\ngo ")
		if !ok {
			t.Fatal("SDK go.mod has no language floor")
		}
		floor, _, _ := strings.Cut(directive, "\n")
		write("go.mod", "module "+module+"\n\ngo "+floor+"\n\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\n")
		write("ext.go", fmt.Sprintf(`package ext
import (
    "encoding/json"
    "github.com/MichaelKinsy/PiG/extensions/sdk"
)
func Extension() *sdk.Extension {
    ext := sdk.New(%q)
    ext.MessageRenderer("padding", func(_ sdk.Context, _ map[string]any, options sdk.MessageRenderOptions, _ int) ([]string, error) {
        wire, err := json.Marshal(options)
        return []string{string(wire)}, err
    })
    return ext
}
`, name))
		return packedFactoryConfig(name, dir, module, name)
	case "python":
		write(name+".py", fmt.Sprintf(`import json
import pig_sdk

def new_extension():
    ext = pig_sdk.Extension(%q)
    ext.message_renderer("padding", lambda _ctx, _message, options, _width: [json.dumps(options)])
    return ext
`, name))
		return packedPythonFactoryConfig(name, dir, name, name)
	case "rust":
		write("Cargo.toml", fmt.Sprintf("[package]\nname = %q\nversion = \"0.0.0\"\nedition = \"2024\"\n[dependencies]\npig-sdk = { path = %q }\nserde_json = \"1\"\n", name, filepath.Join(findModuleRoot(t), "extensions", "sdk-rs")))
		if err := os.Mkdir(filepath.Join(dir, "src"), 0o700); err != nil {
			t.Fatal(err)
		}
		write("src/lib.rs", fmt.Sprintf(`use pig_sdk::Extension;
pub fn new_extension() -> Extension {
    let mut ext = Extension::new(%q);
    ext.message_renderer("padding", |_ctx, _message, options, _width| {
        Ok(vec![serde_json::to_string(&options).map_err(|e| e.to_string())?])
    });
    ext
}
`, name))
		return packedRustFactoryConfig(name, dir, name, name)
	default:
		t.Fatalf("unknown language: %s", language)
		return ExtConfig{}
	}
}
