package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// handlerEffectKinds are the host calls testdata/rpc-handler-effects.mjs makes, in the order it makes them, as RPC mode writes them (rpc-mode.ts createExtensionUIContext).
var handlerEffectKinds = []string{"notify", "setStatus", "setWidget", "setTitle", "set_editor_text"}

// handlerEffectModels is the models.json of a reasoning model that nothing is ever sent to, so that set_thinking_level changes the level.
const handlerEffectModels = `{"providers":{"probe":{"baseUrl":"http://127.0.0.1:1","apiKey":"unused","api":"openai-completions","models":[{"id":"r1","name":"R1","reasoning":true}]}}}`

type handlerEffectImplementation struct {
	name  string
	start func(t *testing.T, busyMS int) *rpcProcess
}

func handlerEffectImplementations(t *testing.T) []handlerEffectImplementation {
	t.Helper()
	fixture, err := filepath.Abs(filepath.Join("testdata", "rpc-handler-effects.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	sibling, err := filepath.Abs(filepath.Join("testdata", "rpc-handler-effects-sibling.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	agentDir := func(t *testing.T, home, name string) string {
		dir := filepath.Join(home, "agent")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(handlerEffectModels), 0o600); err != nil {
			t.Fatal(err)
		}
		return name + "=" + dir
	}
	pig := func(extensions ...string) func(t *testing.T, busyMS int) *rpcProcess {
		return func(t *testing.T, busyMS int) *rpcProcess {
			t.Helper()
			home := t.TempDir()
			env := []string{"HOME=" + home, "PIG_HOME=" + filepath.Join(home, ".pig"), agentDir(t, home, "PIG_CODING_AGENT_DIR"), fmt.Sprintf("RPC_EFFECT_BUSY_MS=%d", busyMS)}
			args := []string{"--no-extensions", "--no-skills", "--no-prompt-templates", "--no-session", "--model", "probe/r1"}
			for _, extension := range extensions {
				args = append(args, "-e", extension)
			}
			return startRPCProcessAt(t, t.TempDir(), env, args...)
		}
	}
	return []handlerEffectImplementation{
		{"pi", func(t *testing.T, busyMS int) *rpcProcess {
			t.Helper()
			root, err := filepath.Abs("../..")
			if err != nil {
				t.Fatal(err)
			}
			piRoot := filepath.Join(root, "extensions", "sdk-ts", "node_modules", "@earendil-works", "pi-coding-agent")
			metadata, err := os.ReadFile(filepath.Join(piRoot, "package.json"))
			if err != nil {
				t.Fatal(err)
			}
			var pkg struct{ Version string }
			if err := json.Unmarshal(metadata, &pkg); err != nil || pkg.Version != pigversion.UpstreamPackageVersion {
				t.Fatalf("Pi version = %q, %v", pkg.Version, err)
			}
			node, err := exec.LookPath("node")
			if err != nil {
				t.Fatal(err)
			}
			home := t.TempDir()
			env := []string{"HOME=" + home, agentDir(t, home, "PI_CODING_AGENT_DIR"), fmt.Sprintf("RPC_EFFECT_BUSY_MS=%d", busyMS)}
			return startJSONLProcessAt(t, t.TempDir(), env, node, filepath.Join(piRoot, "dist", "cli.js"), "--mode", "rpc", "--no-extensions", "--no-skills", "--no-prompt-templates", "--no-session", "--model", "probe/r1", "-e", fixture)
		}},
		{"pig-node", pig(fixture)},
		{"pig-node-packed", pig(fixture, sibling)},
	}
}

// handlerEffectOrder sends command, then returns the records that matter in the order RPC mode wrote them: the host effects the extension's handler made, and "reply" for the command's response.
func handlerEffectOrder(p *rpcProcess, command map[string]any) []string {
	p.t.Helper()
	awaitReady(p)
	p.sendJSON(command)
	var order []string
	p.awaitProgress(func() string { return fmt.Sprintf("the reply and the five effects of the handler (seen %v)", order) }, func(r rpcRecord) bool {
		switch {
		case r["type"] == "extension_ui_request" && slices.Contains(handlerEffectKinds, fmt.Sprint(r["method"])):
			order = append(order, fmt.Sprint(r["method"]))
		case r["type"] == "response" && r["id"] == command["id"]:
			order = append(order, "reply")
		}
		return len(order) == len(handlerEffectKinds)+1
	})
	return order
}

// A handler of session_info_changed or thinking_level_select that calls the host in its synchronous part has every effect written before the reply of set_session_name or set_thinking_level: Pi runs the handler to its first await inside emit (runner.ts:1110), and the command emits the event
// before it returns (agent-session.ts:4122, :2801). A handler that computes for 0 or 300 ms before its calls changes no order. The order is compared with Pi's.
func TestRPCHandlerEffectsPrecedeTheReplyOfTheCommandThatEmittedTheEvent(t *testing.T) {
	want := append(slices.Clone(handlerEffectKinds), "reply")
	commands := []struct {
		name    string
		command map[string]any
	}{
		{"set_session_name", map[string]any{"id": "c", "type": "set_session_name", "name": "renamed"}},
		{"set_thinking_level", map[string]any{"id": "c", "type": "set_thinking_level", "level": "low"}},
	}
	for _, impl := range handlerEffectImplementations(t) {
		for _, c := range commands {
			for _, busyMS := range []int{0, 300} {
				t.Run(fmt.Sprintf("%s/%s/compute-%dms", impl.name, c.name, busyMS), func(t *testing.T) {
					p := impl.start(t, busyMS)
					got := handlerEffectOrder(p, c.command)
					if !slices.Equal(got, want) {
						t.Errorf("order of the handler's effects and the reply = %s, want %s (Pi's)", strings.Join(got, " "), strings.Join(want, " "))
					}
					p.closeAndWait("after the reply")
				})
			}
		}
	}
}
