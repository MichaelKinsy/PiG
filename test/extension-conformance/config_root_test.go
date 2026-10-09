package extensionconformance

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/internal/configroot"
)

type configRootCase struct {
	Name string             `json:"name"`
	Env  map[string]*string `json:"env"`
	Want *string            `json:"want"`
}

func loadConfigRootMatrix(t *testing.T) []configRootCase {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "configroot-matrix.json"))
	if err != nil {
		t.Fatal(err)
	}
	var matrix struct {
		Cases []configRootCase `json:"cases"`
	}
	if err := json.Unmarshal(raw, &matrix); err != nil {
		t.Fatal(err)
	}
	if len(matrix.Cases) == 0 {
		t.Fatal("empty config root matrix")
	}
	return matrix.Cases
}

// processEnv is the environment of one probe: PATH only, plus the HOME of the run and the case's variables.
func (c configRootCase) processEnv(home string) []string {
	env := []string{"PATH=" + os.Getenv("PATH")}
	for _, name := range []string{"SYSTEMROOT", "CARGO_HOME", "RUSTUP_HOME"} {
		if v, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+v)
		}
	}
	homeValue := home
	if v, ok := c.Env["HOME"]; ok {
		homeValue = *v
	}
	env = append(env, "HOME="+homeValue, "USERPROFILE="+homeValue)
	for name, v := range c.Env {
		if name != "HOME" && v != nil {
			env = append(env, name+"="+*v)
		}
	}
	return env
}

// probeResult renders a getter's answer the same way in every language: "ok:<path>" or "err".
func probeResult(output string) string {
	for line := range strings.SplitSeq(output, "\n") {
		line = strings.TrimRight(line, "\r")
		if path, ok := strings.CutPrefix(line, "CONFIG_HOME_OK="); ok {
			return "ok:" + path
		}
		if strings.HasPrefix(line, "CONFIG_HOME_ERR") {
			return "err"
		}
	}
	return "no answer: " + output
}

func runProbe(t *testing.T, dir string, env []string, name string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), name, args...)
	cmd.Dir = dir
	cmd.Env = env
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out.String())
	}
	return out.String()
}

// rustProbeBinary builds the Rust SDK's unit-test binary once, so each case can run it under a replaced HOME that cargo itself could not run under.
func rustProbeBinary(t *testing.T, root string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "cargo", "test", "--quiet", "--lib", "--no-run", "--message-format=json")
	cmd.Dir = filepath.Join(root, "extensions", "sdk-rs")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("build the Rust SDK tests: %v\n%s", err, stderr.String())
	}
	for line := range strings.SplitSeq(string(out), "\n") {
		var message struct {
			Executable string `json:"executable"`
			Target     struct {
				Name string `json:"name"`
			} `json:"target"`
			Profile struct {
				Test bool `json:"test"`
			} `json:"profile"`
		}
		if json.Unmarshal([]byte(line), &message) == nil && message.Executable != "" && message.Profile.Test && message.Target.Name == "pig_sdk" {
			return message.Executable
		}
	}
	t.Fatal("cargo reported no pig_sdk test executable")
	return ""
}

// The host's internal/configroot and the Go, Python, Rust and Node SDK getters answer one env matrix identically: PIG_HOME set/empty/tilde/literal,
// XDG_CONFIG_HOME set/empty, and a failing home directory. Each case also matches the matrix's stated answer, so an agreement on a wrong value fails.
func TestConfigRootGettersAgreeAcrossLanguages(t *testing.T) {
	root := findModuleRoot(t)
	python, err := exec.LookPath(testPythonExecutable())
	if err != nil {
		t.Fatalf("python is required: %v", err)
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatalf("node is required: %v", err)
	}
	rustBinary := rustProbeBinary(t, root)
	pyPath := filepath.Join(root, "extensions", "sdk-py")
	nodeModule := filepath.Join(root, "coding", "extension", "host", "subprocess", "runtime-node", "shims", "pig-config.mjs")
	nodeScript := `import { pathToFileURL } from "node:url";
const { getConfigRoot } = await import(pathToFileURL(process.argv[1]).href);
try { console.log("CONFIG_HOME_OK=" + getConfigRoot()); } catch (error) { console.log("CONFIG_HOME_ERR=" + error.message); }`
	pythonScript := `import pig_sdk
try:
    print("CONFIG_HOME_OK=" + pig_sdk.Context(extension=None).config_home)
except RuntimeError as error:
    print("CONFIG_HOME_ERR=" + str(error))`

	for _, tc := range loadConfigRootMatrix(t) {
		t.Run(tc.Name, func(t *testing.T) {
			home := t.TempDir()
			env := tc.processEnv(home)
			want := "err"
			if tc.Want != nil {
				want = "ok:" + strings.ReplaceAll(*tc.Want, "<HOME>", filepath.ToSlash(home))
			}

			for _, name := range []string{"PIG_HOME", "XDG_CONFIG_HOME"} {
				t.Setenv(name, "")
				_ = os.Unsetenv(name)
			}
			homeValue := home
			if v, ok := tc.Env["HOME"]; ok {
				homeValue = *v
			}
			t.Setenv("HOME", homeValue)
			t.Setenv("USERPROFILE", homeValue)
			for name, v := range tc.Env {
				if name != "HOME" && v != nil {
					t.Setenv(name, *v)
				}
			}
			got := map[string]string{}
			if path, err := configroot.Resolve(); err == nil {
				got["host"] = "ok:" + filepath.ToSlash(path)
			} else {
				got["host"] = "err"
			}
			if path, err := (sdk.Context{}).ConfigHome(); err == nil {
				got["go-sdk"] = "ok:" + filepath.ToSlash(path)
			} else {
				got["go-sdk"] = "err"
			}
			pyEnv := append(slices.Clone(env), "PYTHONPATH="+pyPath, "PYTHONDONTWRITEBYTECODE=1")
			got["python-sdk"] = probeResult(runProbe(t, root, pyEnv, python, "-c", pythonScript))
			got["rust-sdk"] = probeResult(runProbe(t, root, env, rustBinary, "probe_config_home", "--ignored", "--nocapture"))
			got["node-sdk"] = probeResult(runProbe(t, root, env, node, "--input-type=module", "-e", nodeScript, nodeModule))

			for lang, result := range got {
				if strings.HasPrefix(result, "ok:") {
					got[lang] = filepath.ToSlash(result)
				}
				if got[lang] != want {
					t.Errorf("%s answered %q, want %q (all answers: %v)", lang, got[lang], want, got)
				}
			}
		})
	}
}
