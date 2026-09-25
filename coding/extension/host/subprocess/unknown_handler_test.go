package subprocess

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// A request for a handler the extension does not hold is host/extension state
// drift. Every SDK answers it with an error instead of silently acknowledging
// it, so the runner reports the failure.
func TestUnknownEventHandlerIsAnErrorInEverySDK(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and loads SDK fixtures")
	}
	conformance := filepath.Join("..", "..", "..", "..", "tests", "extension-conformance", "testdata")
	cases := []struct {
		name string
		cfg  func(t *testing.T) ExtConfig
	}{
		{"go", func(t *testing.T) ExtConfig {
			return ExtConfig{Name: "sdk-fixture", Path: buildSDKFixture(t), Enabled: true}
		}},
		{"node", func(t *testing.T) ExtConfig {
			requireTool(t, "node")
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "index.mjs"), []byte("export default function (pi) { pi.on(\"session_start\", () => {}); }\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			return ExtConfig{Name: "node-unknown-handler", Source: filepath.Join(dir, "index.mjs"), Enabled: true}
		}},
		{"python", func(t *testing.T) ExtConfig {
			requireTool(t, "python3")
			path, err := filepath.Abs(filepath.Join(conformance, "python-sdk-fixture", "main.py"))
			if err != nil {
				t.Fatal(err)
			}
			return ExtConfig{Name: "python-sdk-fixture", Path: path, Enabled: true, RuntimeLanguage: "python"}
		}},
		{"rust", func(t *testing.T) ExtConfig {
			requireTool(t, "cargo")
			dir, err := filepath.Abs(filepath.Join(conformance, "rust-sdk-fixture"))
			if err != nil {
				t.Fatal(err)
			}
			build := exec.CommandContext(testbudget.Context(t), "cargo", "build", "--release", "--quiet")
			build.Dir = dir
			if out, err := build.CombinedOutput(); err != nil {
				t.Fatalf("build rust fixture: %v\n%s", err, out)
			}
			target := os.Getenv("CARGO_TARGET_DIR")
			if target == "" {
				target = filepath.Join(dir, "target")
			} else if !filepath.IsAbs(target) {
				target = filepath.Join(dir, target)
			}
			return ExtConfig{Name: "rust-sdk-fixture", Path: filepath.Join(target, "release", "rust-sdk-fixture"), Enabled: true}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := tc.cfg(t)
			host := NewHost(t.TempDir())
			defer host.Shutdown("test done")
			if _, err := host.Load(testbudget.Context(t), cfg); err != nil {
				t.Fatal(err)
			}
			host.mu.Lock()
			me := host.exts[cfg.Name]
			host.mu.Unlock()
			_, err := me.makeEventHandler("session_start", 987654)(map[string]any{"type": "session_start"})
			if err == nil || !strings.Contains(err.Error(), "unknown event handler 987654") {
				t.Fatalf("unknown handler dispatch error = %v", err)
			}
		})
	}
}

func requireTool(t *testing.T, name string) {
	t.Helper()
	if _, err := exec.LookPath(name); err != nil {
		t.Fatalf("%s is required: %v", name, err)
	}
}
