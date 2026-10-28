package pigletbuild

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	piglet "github.com/MichaelKinsy/PiG/coding/piglet"
	"github.com/MichaelKinsy/PiG/coding/piglet/signature"
)

// overlayHelloExtension is the Binary's fused extension; its flag shows in --help when the extension loads.
const overlayHelloExtension = `package hello

import sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

func Extension() *sdk.Extension {
	ext := sdk.New("hello")
	ext.Tool("hello_tool", "Tool of the Binary's own extension", sdk.Schema{"type": "object"}, func(sdk.Context, map[string]any) (any, error) { return "ok", nil })
	ext.Flag("base-flag", sdk.FlagOptions{Description: "Flag of the Binary's own extension", Type: sdk.FlagBoolean})
	return ext
}
`

// overlayFlagExtension is the extension an overlay adds. It runs as a subprocess extension resolved at launch.
const overlayFlagExtension = `export default function (pi) {
  pi.registerFlag("overlay-flag", { description: "Flag of the overlay's extension", type: "boolean" });
}
`

func writeOverlayFixture(t *testing.T, root string) {
	t.Helper()
	files := map[string]string{
		filepath.Join(root, "hello", "go.mod"):       "module example.com/overlay/hello\n\ngo 1.26\n\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\n",
		filepath.Join(root, "hello", "extension.go"): overlayHelloExtension,
		filepath.Join(root, "accepting.yaml"):        "name: accepting\nmodel:\n  provider: test-faux\n  name: faux-1\nstrip:\n  tools: [ls]\n  extensions: [mcp]\nbuild:\n  allowOverlays: true\nextensions:\n  - name: hello\n    origins: [local:./hello]\n",
		filepath.Join(root, "closed.yaml"):           "name: closed\nmodel:\n  provider: test-faux\n  name: faux-1\nstrip:\n  tools: [ls]\nextensions:\n  - name: hello\n    origins: [local:./hello]\n",
		filepath.Join(root, "overlays", "flag.mjs"):  overlayFlagExtension,
		filepath.Join(root, "overlays", "add.yaml"):  "name: add\nextensions:\n  - name: flag\n    origins: [local:./flag.mjs]\nstrip:\n  tools: [grep]\n",
	}
	for path, content := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// A Piglet Binary built from a Piglet with build.allowOverlays accepts --piglet and PIG_PIGLET_PATH as an overlay on its
// baked Piglet, and one built without it refuses them and names the setting. Every forbidden override is refused with the
// field and the Binary named, and a refused overlay never starts the Binary's own Piglet.
func TestPigletBinaryAcceptsOverlaysOnlyWhenItsPigletSaysSo(t *testing.T) {
	root := t.TempDir()
	writeOverlayFixture(t, root)
	keyPath := filepath.Join(t.TempDir(), "author.key")
	keyID, err := signature.GenerateKey(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	accepting := buildNativeBinary(t, root, filepath.Join(root, "accepting.yaml"), "pig-accepting", "--sign-key", keyPath)
	closed := buildNativeBinary(t, root, filepath.Join(root, "closed.yaml"), "pig-closed")
	add := filepath.Join(root, "overlays", "add.yaml")

	// The baked Piglet is the embedded piglet.yaml; the same words also appear in the Binary's messages and docs, so match its
	// YAML shape.
	const bakedSetting = "build:\n    allowOverlays: true\n"
	baked, err := os.ReadFile(accepting)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(baked), bakedSetting) {
		t.Fatal("the accepting Binary does not carry build.allowOverlays in its baked Piglet")
	}
	closedBytes, err := os.ReadFile(closed)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(closedBytes), bakedSetting) {
		t.Fatal("a Binary built without the setting carries it")
	}
	var stdout, stderr strings.Builder
	if code := piglet.RunCommand([]string{"piglet", "verify", accepting}, &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), "Signature: signed by "+keyID) {
		t.Fatalf("pig piglet verify: exit %d\n%s%s", code, stdout.String(), stderr.String())
	}

	assertHelp := func(t *testing.T, output string, wantOverlay bool) {
		t.Helper()
		if !strings.Contains(output, "--base-flag") {
			t.Fatalf("the Binary's own extension did not load:\n%s", output)
		}
		if strings.Contains(output, "--overlay-flag") != wantOverlay {
			t.Fatalf("overlay extension flag listed = %t, want %t:\n%s", !wantOverlay, wantOverlay, output)
		}
		if !strings.Contains(output, "  read       - ") || strings.Contains(output, "  ls         - ") {
			t.Fatalf("the Binary's own strip list does not apply:\n%s", output)
		}
		if grepListed := strings.Contains(output, "  grep       - "); grepListed == wantOverlay {
			t.Fatalf("overlay strip of grep applied = %t, want %t:\n%s", !grepListed, wantOverlay, output)
		}
	}

	t.Run("accepting Binary without a selector is unchanged", func(t *testing.T) {
		code, output := startPigletBinary(t, accepting, "--help")
		if code != 0 {
			t.Fatalf("exit %d\n%s", code, output)
		}
		assertHelp(t, output, false)
	})
	t.Run("--piglet lays the overlay over the baked Piglet", func(t *testing.T) {
		code, output := startPigletBinary(t, accepting, "--piglet", add, "--help")
		if code != 0 {
			t.Fatalf("exit %d\n%s", code, output)
		}
		assertHelp(t, output, true)
	})
	t.Run("PIG_PIGLET_PATH lays the overlay over the baked Piglet", func(t *testing.T) {
		t.Setenv("PIG_PIGLET_PATH", add)
		code, output := startPigletBinary(t, accepting, "--help")
		if code != 0 {
			t.Fatalf("exit %d\n%s", code, output)
		}
		assertHelp(t, output, true)
	})
	t.Run("the baked default model still answers with an overlay", func(t *testing.T) {
		t.Setenv("PIG_TEST_FAUX", "1")
		t.Setenv("PIG_OFFLINE", "1")
		code, output := startPigletBinary(t, accepting, "--piglet", add, "--no-session", "-p", "What is 20+22?")
		if code != 0 || !strings.Contains(output, "42") {
			t.Fatalf("exit %d\n%s", code, output)
		}
	})

	for _, selector := range []struct {
		name string
		env  string
		args []string
	}{
		{"--piglet", "", []string{"--piglet", add, "--help"}},
		{"PIG_PIGLET_PATH", "PIG_PIGLET_PATH", []string{"--help"}},
		{"PIG_PIGLET_NAME", "PIG_PIGLET_NAME", []string{"--help"}},
	} {
		t.Run("closed Binary refuses "+selector.name, func(t *testing.T) {
			if selector.env != "" {
				t.Setenv(selector.env, add)
			}
			code, output := startPigletBinary(t, closed, selector.args...)
			if code != 1 {
				t.Fatalf("exit %d, want 1\n%s", code, output)
			}
			for _, want := range []string{"this Piglet Binary (closed)", "does not accept --piglet overlays", "build.allowOverlays: true"} {
				if !strings.Contains(output, want) {
					t.Fatalf("refusal lacks %q:\n%s", want, output)
				}
			}
			if strings.Contains(output, "--overlay-flag") || strings.Contains(output, "Usage:") {
				t.Fatalf("a refused selector went on to start:\n%s", output)
			}
		})
	}

	mid := filepath.Join(root, "overlays", "mid.yaml")
	if err := os.WriteFile(mid, []byte("name: mid\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		body string
		want []string
	}{
		{"replacing the Binary's fused extension with another under its name", "name: x\nextends:\n  allowWiden: true\nextensions:\n  - name: hello\n    origins: [local:./flag.mjs]\n",
			[]string{`extensions[hello]`, `Piglet Binary "accepting"`, "cannot replace it with another one under the same name"}},
		{"setting the frontend slot", "name: x\nslots:\n  frontend:\n    member: ./tern\n",
			[]string{"slots.frontend", `Piglet Binary "accepting"`, "built without a frontend member", "compiled into the Binary"}},
		{"removing a frontend slot the Binary does not have", "name: x\nextends:\n  remove:\n    slots: [frontend]\n",
			[]string{"remove.slots", `Piglet Binary "accepting"`}},
		{"re-enabling a built-in a build tag compiled out", "name: x\nextends:\n  allowWiden: true\n  remove:\n    strip:\n      extensions: [mcp]\n",
			[]string{`strip.extensions: "mcp"`, `Piglet Binary "accepting"`, "pig_strip_mcp", "cannot re-enable it"}},
		{"widening without allowWiden", "name: x\ndiscovery:\n  extensions: [workspace]\n",
			[]string{"requires extends.allowWiden: true"}},
	} {
		t.Run("refuses "+tc.name, func(t *testing.T) {
			overlay := filepath.Join(root, "overlays", "refused.yaml")
			if err := os.WriteFile(overlay, []byte(tc.body), 0o644); err != nil {
				t.Fatal(err)
			}
			code, output := startPigletBinary(t, accepting, "--piglet", overlay, "--help")
			if code != 1 {
				t.Fatalf("exit %d, want 1\n%s", code, output)
			}
			for _, want := range append(tc.want, "the requested Piglet could not be loaded") {
				if !strings.Contains(output, want) {
					t.Fatalf("output lacks %q:\n%s", want, output)
				}
			}
			if strings.Contains(output, "Usage:") {
				t.Fatalf("a refused overlay fell back to the Binary's own Piglet:\n%s", output)
			}
		})
	}

	// The overlay merges as an extends child: it removes the fused extension (so the Binary does not start it), re-enables a
	// runtime strip entry and replaces the system prompt.
	t.Run("an overlay removes a fused member and re-enables a runtime strip entry", func(t *testing.T) {
		overlay := filepath.Join(root, "overlays", "remove.yaml")
		body := "name: lite\nsystemPrompt:\n  text: overlay prompt\nextends:\n  allowWiden: true\n  remove:\n    extensions: [hello]\n    strip:\n      tools: [ls]\n"
		if err := os.WriteFile(overlay, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		code, output := startPigletBinary(t, accepting, "--piglet", overlay, "--help")
		if code != 0 {
			t.Fatalf("exit %d\n%s", code, output)
		}
		if strings.Contains(output, "--base-flag") {
			t.Fatalf("the removed fused extension still started:\n%s", output)
		}
		if !strings.Contains(output, "  ls         - ") {
			t.Fatalf("the re-enabled runtime strip entry stays stripped:\n%s", output)
		}
	})
	t.Run("an overlay narrows a fused member's tools by naming it without origins", func(t *testing.T) {
		code, output := startPigletBinary(t, accepting, "--help")
		if code != 0 || !strings.Contains(output, "hello_tool") {
			t.Fatalf("the fused extension's tool is not listed: exit %d\n%s", code, output)
		}
		overlay := filepath.Join(root, "overlays", "narrow.yaml")
		if err := os.WriteFile(overlay, []byte("name: narrow\nextensions:\n  - name: hello\n    tools: []\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		code, output = startPigletBinary(t, accepting, "--piglet", overlay, "--help")
		if code != 0 || !strings.Contains(output, "--base-flag") || strings.Contains(output, "hello_tool") {
			t.Fatalf("narrowed fused member: exit %d\n%s", code, output)
		}
	})
	t.Run("--system-prompt is accepted by a Binary that accepts overlays and refused by one that does not", func(t *testing.T) {
		t.Setenv("PIG_TEST_FAUX", "1")
		t.Setenv("PIG_OFFLINE", "1")
		if code, output := startPigletBinary(t, accepting, "--system-prompt", "Answer with one word.", "--no-session", "-p", "What is 20+22?"); code != 0 || !strings.Contains(output, "42") {
			t.Fatalf("accepting Binary: exit %d\n%s", code, output)
		}
		code, output := startPigletBinary(t, closed, "--system-prompt", "Answer with one word.", "--no-session", "-p", "What is 20+22?")
		if code != 1 || !strings.Contains(output, "baseline system prompt cannot be replaced") {
			t.Fatalf("closed Binary: exit %d\n%s", code, output)
		}
	})
}
