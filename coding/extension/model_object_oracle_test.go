package extension

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// The model objects an extension receives are Pi's: for every built-in model, the bytes PiG sends equal JSON.stringify of the
// model object Pi 1.2.0's ModelRuntime holds (testdata/model-object-oracle.mjs), member order included.
func TestModelObjectsMatchPisModelObjects(t *testing.T) {
	root, err := filepath.EvalSymlinks("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Version string `json:"version"`
	}
	raw, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &manifest); err != nil || manifest.Version != pigversion.UpstreamPackageVersion {
		t.Fatalf("installed Pi %q (%v), want %s", manifest.Version, err, pigversion.UpstreamPackageVersion)
	}
	home := t.TempDir()
	cmd := exec.CommandContext(t.Context(), "node", "testdata/model-object-oracle.mjs", root)
	cmd.Env = append(os.Environ(), "HOME="+home, "PI_OFFLINE=1", "PI_CODING_AGENT_DIR="+filepath.Join(home, "agent"))
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("run oracle: %v", err)
	}
	want := map[string]string{}
	scanner := bufio.NewScanner(bytes.NewReader(out))
	scanner.Buffer(make([]byte, 1<<20), 1<<22)
	for scanner.Scan() {
		var key struct{ Provider, ID string }
		if err := json.Unmarshal(scanner.Bytes(), &key); err != nil {
			t.Fatal(err)
		}
		want[key.Provider+"/"+key.ID] = scanner.Text()
	}
	// pig divergence (D240): Pi 4b9777737 marks Claude Sonnet 5 as not supporting temperature. The host catalog, which the extension-visible model object
	// follows, carries compat.supportsTemperature false on seven records the pinned Pi 1.5.0 object lacks it on. Only that member, after forceAdaptiveThinking, is added to those records.
	for _, key := range d240SonnetFiveRecords {
		if object, ok := want[key]; ok && !strings.Contains(object, `"supportsTemperature"`) {
			want[key] = strings.Replace(object, `"forceAdaptiveThinking":true`, `"forceAdaptiveThinking":true,"supportsTemperature":false`, 1)
		}
	}
	models := make([]*ai.Model, len(ai.GeneratedModels))
	for i := range ai.GeneratedModels {
		models[i] = ai.GeneratedModels[i].ToModel()
	}
	if len(want) != len(models) {
		t.Fatalf("Pi holds %d models, PiG's catalog %d", len(want), len(models))
	}
	encoded, err := MarshalModelObjects(models)
	if err != nil {
		t.Fatal(err)
	}
	var got []json.RawMessage
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	failures := 0
	for i, model := range models {
		key := model.ProviderMeta.ProviderID + "/" + model.ID
		if string(got[i]) != want[key] {
			if failures < 3 {
				t.Errorf("%s:\n got %s\nwant %s", key, got[i], want[key])
			}
			failures++
		}
	}
	if failures > 0 {
		t.Errorf("%d of %d model objects differ from Pi's", failures, len(models))
	}
}

// d240SonnetFiveRecords are the records the D240 allowance covers; the same list as d240SonnetFiveRecords in ai/d240_sonnet5_allowance_test.go.
var d240SonnetFiveRecords = []string{
	"anthropic/claude-sonnet-5",
	"cloudflare-ai-gateway/claude-sonnet-5",
	"github-copilot/claude-sonnet-5",
	"opencode/claude-sonnet-5",
	"openrouter/anthropic/claude-sonnet-5",
	"vercel-ai-gateway/anthropic/claude-sonnet-5",
	"vercel-ai-gateway/anthropic/claude-sonnet-5-batch",
}
