package subprocess

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The Node runtime's getConfigRoot answers the shared env matrix (test/extension-conformance/testdata/configroot-matrix.json) as the host's
// internal/configroot and the Go, Python and Rust SDKs do: PIG_HOME, else XDG_CONFIG_HOME/pig, else ~/.pig; an empty variable falls through; ~ and
// ~/ expand; any other value stays literal; an unavailable home directory is an error, never a relative path. Pi-sharing mode does not move it (D2).
func TestNodeConfigRootMatchesTheSharedMatrix(t *testing.T) {
	root := findModuleRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, "test", "extension-conformance", "testdata", "configroot-matrix.json"))
	if err != nil {
		t.Fatal(err)
	}
	var matrix struct {
		Cases []struct {
			Name string             `json:"name"`
			Env  map[string]*string `json:"env"`
			Want *string            `json:"want"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &matrix); err != nil {
		t.Fatal(err)
	}
	if len(matrix.Cases) == 0 {
		t.Fatal("empty matrix")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatalf("node is required: %v", err)
	}
	module := filepath.Join(root, "coding", "extension", "host", "subprocess", "runtime-node", "shims", "pig-config.mjs")
	script := `import { pathToFileURL } from "node:url";
const { getConfigRoot } = await import(pathToFileURL(process.argv[1]).href);
try { console.log("OK=" + getConfigRoot()); } catch (error) { console.log("ERR=" + error.message); }`
	for _, tc := range matrix.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			home := t.TempDir()
			homeValue := home
			if v, ok := tc.Env["HOME"]; ok {
				homeValue = *v
			}
			env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + homeValue, "USERPROFILE=" + homeValue}
			for name, v := range tc.Env {
				if name != "HOME" && v != nil {
					env = append(env, name+"="+*v)
				}
			}
			// PIG_USE_PI_DIRS moves only the agent and project directories, never the config root (D2).
			env = append(env, "PIG_USE_PI_DIRS=1")
			cmd := exec.CommandContext(t.Context(), node, "--input-type=module", "-e", script, module)
			cmd.Env = env
			var out bytes.Buffer
			cmd.Stdout, cmd.Stderr = &out, &out
			if err := cmd.Run(); err != nil {
				t.Fatalf("node: %v\n%s", err, out.String())
			}
			got := strings.TrimSpace(out.String())
			if tc.Want == nil {
				if !strings.HasPrefix(got, "ERR=") {
					t.Fatalf("getConfigRoot() = %q, want an error", got)
				}
				return
			}
			want := "OK=" + strings.ReplaceAll(*tc.Want, "<HOME>", filepath.ToSlash(home))
			if filepath.ToSlash(got) != want {
				t.Fatalf("getConfigRoot() = %q, want %q", got, want)
			}
		})
	}
}
