package coding

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type servicesProbe struct {
	CWD      *string `json:"cwd"`
	AgentDir *string `json:"agentDir"`
}

type servicesOut struct {
	CWD      *string `json:"cwd"`
	AgentDir *string `json:"agentDir"`
	Auth     bool    `json:"auth"`
	Error    *string `json:"error"`
}

func servicesProbes() []servicesProbe {
	str := func(s string) *string { return &s }
	cwds := []*string{str("{S}/work"), str("work"), str("./work/../work"), str("../{S}-neighbour"), str("~"), str("~/proj"), str("file://{S}/work"), str("file://{S}/work%20dir"), str("{S}/work/"), str("{S}//work//sub"), str("{S}/with space"), str("{S}/nbsp\u00a0dir"), str("."), str("")}
	agentDirs := []*string{nil, str(""), str("{S}/agent"), str("agent"), str("~/agent"), str("~"), str("file://{S}/agent"), str("{S}/agent/../agent2"), str("./rel/agent/"), str("{S}/日本/agent")}
	var probes []servicesProbe
	for _, cwd := range cwds {
		for _, agentDir := range agentDirs {
			probes = append(probes, servicesProbe{cwd, agentDir})
		}
	}
	return probes
}

// createAgentSessionServices (core/agent-session-services.ts:134-139) against pinned Pi over the paths a caller may pass: cwd and agentDir go through resolvePath, so a relative path anchors at the process directory, `~` and `~/x` at the home directory, a file:// URL becomes its path, and `..`, doubled and trailing separators collapse; an empty agentDir falls back to the default agent directory; the credential file is created in the resolved directory.
func TestAgentSessionServicesResolvePathsMatchPi(t *testing.T) {
	probes := servicesProbes()
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	piSandbox, pigSandbox := t.TempDir(), t.TempDir()
	cmd := exec.CommandContext(t.Context(), "node", "testdata/agent_session_services.mjs", pigversion.UpstreamPackageVersion, piSandbox)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, stderr.String())
	}
	var want []servicesOut
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatal(err)
	}
	if len(want) != len(probes) {
		t.Fatalf("Pi returned %d results for %d probes", len(want), len(probes))
	}

	t.Setenv("HOME", filepath.Join(pigSandbox, "home"))
	t.Setenv("PIG_CODING_AGENT_DIR", filepath.Join(pigSandbox, "default-agent"))
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(pigSandbox, "default-agent"))
	t.Setenv("PIG_HOME", "")
	t.Setenv("PI_HOME", "")
	if err := os.MkdirAll(filepath.Join(pigSandbox, "home"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(pigSandbox)
	fill := func(s *string) string {
		if s == nil {
			return ""
		}
		return strings.ReplaceAll(*s, "{S}", pigSandbox)
	}
	strip := func(s string) *string { v := strings.ReplaceAll(s, pigSandbox, "{S}"); return &v }
	for i, probe := range probes {
		var got servicesOut
		services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: fill(probe.CWD), AgentDir: fill(probe.AgentDir)})
		if err != nil {
			got.Error = strip(err.Error())
		} else {
			got.CWD, got.AgentDir = strip(services.CWD()), strip(services.AgentDir())
			_, statErr := os.Stat(filepath.Join(services.AgentDir(), "auth.json"))
			got.Auth = statErr == nil
			services.Close()
		}
		if !reflect.DeepEqual(got, want[i]) {
			in, _ := json.Marshal(probe)
			gotJSON, _ := json.Marshal(got)
			wantJSON, _ := json.Marshal(want[i])
			t.Errorf("probe %d %s\nPig: %s\nPi:  %s", i, in, gotJSON, wantJSON)
		}
	}
}
