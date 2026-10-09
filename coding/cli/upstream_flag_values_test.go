package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

func TestHeadlessFlagBindingSharesReferenceAndWireValues(t *testing.T) {
	values := map[string]any{"enabled": true, "disabled": false, "empty": "", "configured": "cli-value"}
	runner := inproc.NewRunner(nil, t.TempDir())
	bridge := subprocess.NewUIBridge(func() {})
	bindSessionExtensionActions(runner, bridge, func() *coding.Session { return nil }, extension.ContextActions{
		GetFlagValue: func(name string) any { return values[name] },
	})
	ctx := extension.FromContext(runner.DispatchContext(t.Context()))
	for _, tc := range []struct{ name, wire string }{
		{"enabled", `{"value":true}`}, {"disabled", `{"value":false}`}, {"empty", `{"value":""}`}, {"configured", `{"value":"cli-value"}`}, {"missing", `{"value":null}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ctx.GetFlagValue(tc.name); got != values[tc.name] {
				t.Fatalf("reference value = %#v", got)
			}
			args, err := json.Marshal(map[string]string{"name": tc.name})
			if err != nil {
				t.Fatal(err)
			}
			result, err := bridge.HandleCall("flags", &subprocess.CallPayload{Method: "getFlag", Args: args})
			if err != nil {
				t.Fatal(err)
			}
			if string(result.Result) != tc.wire {
				t.Fatalf("wire value = %s; want %s", result.Result, tc.wire)
			}
		})
	}
}

// Pi's main passes extension CLI values to the runtime before binding any mode;
// getFlag reads those values in session_start and command handlers.
// .upstream/v0.87.1/packages/coding-agent/src/main.ts:741
// .upstream/v0.87.1/packages/coding-agent/src/core/extensions/loader.ts:344
func TestExtensionCLIFlagsReachEveryHeadlessMode(t *testing.T) {
	t.Parallel()
	binary := buildPigBinaryForSignalTest(t)
	for _, mode := range []string{"print", "json", "rpc"} {
		t.Run(mode, func(t *testing.T) {
			home := t.TempDir()
			cwd := filepath.Join(home, "cwd")
			if err := os.MkdirAll(cwd, 0o755); err != nil {
				t.Fatal(err)
			}
			artifact := filepath.Join(home, "flags.json")
			entry := filepath.Join(home, "flags.ts")
			writeStartupFixtureFile(t, entry, `import { writeFileSync } from "node:fs";
export default function(pi) {
 pi.registerFlag("configured",{type:"string",default:"factory-default"});
 pi.registerFlag("enabled",{type:"boolean",default:false});
 const record = () => writeFileSync(`+strconv.Quote(artifact)+`,JSON.stringify([pi.getFlag("configured"),pi.getFlag("enabled")]));
 pi.on("session_start", record);
 pi.registerCommand("flags",{handler:async()=>{record();}});
}`)
			args := []string{"--no-session", "-e", entry, "--configured", "cli-value", "--enabled"}
			input := ""
			switch mode {
			case "print":
				args = append(args, "-p", "/flags")
			case "json":
				args = append(args, "--mode", "json", "/flags")
			case "rpc":
				args = append(args, "--mode", "rpc")
				input = "{\"id\":\"flags\",\"type\":\"prompt\",\"message\":\"/flags\"}\n"
			}
			run := runPigStartup(t, binary, home, filepath.Join(home, "agent"), cwd, input, args...)
			if run.err != nil {
				t.Fatalf("%v\nstdout: %s\nstderr: %s", run.err, run.stdout, run.stderr)
			}
			if mode == "rpc" && !strings.Contains(run.stdout, `"success":true`) {
				t.Fatalf("command did not complete: %s", run.stdout)
			}
			data, err := os.ReadFile(artifact)
			if err != nil {
				t.Fatal(err)
			}
			var values []any
			if err := json.Unmarshal(data, &values); err != nil {
				t.Fatal(err)
			}
			if len(values) != 2 || values[0] != "cli-value" || values[1] != true {
				t.Fatalf("flags=%s; want [\"cli-value\",true]", data)
			}
		})
	}
}
