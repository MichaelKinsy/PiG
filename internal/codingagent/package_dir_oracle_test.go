package codingagent

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// getPackageDir and getAgentDir (config.ts) against pinned Pi for a set PI_PACKAGE_DIR and agent-directory variable (Pig reads PIG_CODING_AGENT_DIR, D2): absolute, relative, ~, ~/x, ~x, file:// URLs with escapes, surrounding spaces and an @ prefix. An invalid file URL makes Pi throw; Pig's string-only API returns the raw value, so those are not compared.
func TestGetPackageDirMatchesPi(t *testing.T) {
	script, err := filepath.Abs("testdata/package_dir.mjs")
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PIG_PACKAGE_DIR", "")
	values := []string{"/opt/pi", "/opt/pi/", "/opt//pi/../pi", "relative/dir", "./rel", "~", "~/pkg", "~/", "~x", "~/a b", "file:///opt/pi", "file:///opt/p%20q", "file:///opt/p%2Fq", "file:///", "file://localhost/opt/pi", " /opt/pi ", "@/opt/pi", "/opt/ü/日本", "C:\\x", "~\\x", "/opt/pi\n", "/", "//", "pkg\\dir"}
	input, err := json.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", script, pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	cmd.Env = append(os.Environ(), "HOME="+home, "USERPROFILE="+home)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var want []struct {
		Dir   string `json:"dir"`
		Agent string `json:"agent"`
		Threw bool   `json:"threw"`
	}
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatal(err)
	}
	for i, value := range values {
		t.Setenv("PI_PACKAGE_DIR", value)
		t.Setenv("PIG_CODING_AGENT_DIR", value)
		t.Setenv("PI_CODING_AGENT_DIR", value)
		got := GetPackageDir()
		if want[i].Threw {
			continue
		}
		if got != want[i].Dir {
			t.Errorf("PI_PACKAGE_DIR=%q: Go %q, Pi %q", value, got, want[i].Dir)
		}
		if agent := AgentDir(); agent != want[i].Agent {
			t.Errorf("PI_CODING_AGENT_DIR=%q: AgentDir %q, Pi getAgentDir %q", value, agent, want[i].Agent)
		}
	}
}
