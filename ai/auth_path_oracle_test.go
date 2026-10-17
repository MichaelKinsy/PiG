package ai

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type authPathOut struct {
	Outcome string   `json:"outcome"`
	Files   []string `json:"files"`
}

// pi: packages/coding-agent/src/core/auth-storage.ts AuthStorage.create (normalizePath of the auth path): a path spelled absolute, relative to the process directory, with `~` or `~/x`, as a file:// URL, with dot segments, doubled separators or a no-break space reaches the same file in Pi and in Pig.
func TestAuthStorageAuthPathSpellingsMatchPi(t *testing.T) {
	probes := []string{
		"{S}/a/auth.json", "rel/auth.json", "./rel/../r2/auth.json", "~/auth.json", "~/sub/../auth.json", "~/deep/er/auth.json", "~user/auth.json",
		"file://{S}/f/auth.json", "file://{S}/sp%20ace/auth.json", "file://{S}/日本/auth.json", "file://localhost{S}/lh/auth.json", "{S}//dbl//auth.json", "{S}/nbsp\u00a0dir/auth.json",
		"{S}/trail/./auth.json", "auth.json", "file://", "file:///",
	}
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	piSandbox, pigSandbox := t.TempDir(), t.TempDir()
	cmd := exec.CommandContext(t.Context(), "node", "testdata/auth_path.mjs", pigversion.UpstreamPackageVersion, piSandbox)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, stderr.String())
	}
	var want []authPathOut
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatal(err)
	}
	if len(want) != len(probes) {
		t.Fatalf("Pi returned %d results for %d probes", len(want), len(probes))
	}
	for i, probe := range probes {
		base := filepath.Join(pigSandbox, fmt.Sprintf("p%d", i))
		if err := os.MkdirAll(filepath.Join(base, "home"), 0o755); err != nil {
			t.Fatal(err)
		}
		// os.UserHomeDir reads USERPROFILE on Windows and HOME elsewhere, as Node's os.homedir() does.
		t.Setenv("HOME", filepath.Join(base, "home"))
		t.Setenv("USERPROFILE", filepath.Join(base, "home"))
		t.Chdir(base)
		got := authPathOut{Files: []string{}}
		storage, err := NewAuthStorage(strings.ReplaceAll(probe, "{S}", base))
		if err == nil {
			err = storage.Set("openai", Credential{Type: "api_key", Key: "sk-1"})
		}
		if err == nil {
			var cred Credential
			var ok bool
			if cred, ok, err = storage.Get("openai"); err == nil {
				raw, _ := json.Marshal(struct {
					Type string `json:"type"`
					Key  string `json:"key"`
				}{string(cred.Type), cred.Key})
				got.Outcome = string(raw)
				if !ok {
					got.Outcome = "null"
				}
			}
		}
		if err != nil {
			got.Outcome = "error " + strings.ReplaceAll(err.Error(), base, "{S}")
		}
		_ = filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() && !strings.Contains(path, ".lock") {
				rel, _ := filepath.Rel(base, path)
				got.Files = append(got.Files, rel)
			}
			return nil
		})
		slices.Sort(got.Files)
		if !reflect.DeepEqual(got, want[i]) || (want[i].Files == nil) != (got.Files == nil) {
			gotJSON, _ := json.Marshal(got)
			wantJSON, _ := json.Marshal(want[i])
			t.Errorf("probe %q\nPig: %s\nPi:  %s", probe, gotJSON, wantJSON)
		}
	}
}
