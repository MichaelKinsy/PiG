package extensionconformance

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// Pi's loader gives the factory a pi.exec that spawns the child directly (loader.ts:411-414, core/exec.ts), so a factory can run a program before the extension registers and before any session binds. The fixture's factory awaits several children while it loads and its command reports each outcome. The host's exec is the production ExecCommand and the in-process call is the reference, so every outcome must equal it, including the loader's working directory when the call names none (`options?.cwd ?? cwd`), the rejection message of an empty command and a timeout's kill. A host that serves exec only after registration rejects each call with "extension connection closed or replaced".
func TestNodeFactoryExecDuringLoad(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatalf("node is required for SDK conformance: %v", err)
	}
	// The fixture runs process.execPath, the node that runs the extension.
	printed, err := exec.Command(node, "-p", "process.execPath").Output()
	if err != nil {
		t.Fatalf("node -p process.execPath: %v", err)
	}
	execPath := strings.TrimSpace(string(printed))
	outcome := func(cwd, command string, args []string, opts *extension.ExecOptions) string {
		result, err := extension.ExecCommand(context.Background(), cwd, command, args, opts)
		if err != nil {
			return "rejected:" + err.Error()
		}
		return "resolved:" + strings.Join([]string{strconv.Itoa(result.Code), result.Stdout, result.Stderr, strconv.FormatBool(result.Killed)}, ":")
	}
	cwd := t.TempDir()
	want := []string{
		"ok=" + outcome(cwd, execPath, []string{"-e", "process.stdout.write('load-exec-ok');process.stderr.write('err')"}, nil),
		"exit=" + outcome(cwd, execPath, []string{"-e", "process.exit(3)"}, nil),
		"defaultCwd=" + outcome(cwd, execPath, []string{"-e", "process.stdout.write(process.cwd())"}, nil),
		"cwd=" + outcome(cwd, execPath, []string{"-e", "process.stdout.write(process.cwd())"}, &extension.ExecOptions{CWD: filepath.Dir(execPath)}),
		"timeout=" + outcome(cwd, execPath, []string{"-e", "setTimeout(() => {}, 60000)"}, &extension.ExecOptions{Timeout: 200}),
		"empty=" + outcome(cwd, "", []string{}, nil),
		"background=resolved:background",
	}
	for _, packed := range []bool{false, true} {
		name := "subprocess-node"
		if packed {
			name += "-packed"
		}
		t.Run(name, func(t *testing.T) {
			notify := &[]string{}
			status := &[]string{}
			ui := newRecordingUI(notify, status)
			bridge := subprocess.NewUIBridge(func() {})
			bridge.SetUIContext(ui)
			bridge.SetNotifyFunc(ui.RecordNotify)
			h := subprocess.NewHost(cwd)
			h.SetMode(conformanceMode)
			h.SetUIBridge(bridge)
			t.Cleanup(func() { h.Shutdown("test done") })

			source := filepath.Join(findModuleRoot(t), "test", "extension-conformance", "testdata", "node-load-exec-fixture", "main.mjs")
			configs := []subprocess.ExtConfig{{Name: "node-load-exec-fixture", Source: source, Enabled: true}}
			if packed {
				peer := filepath.Join(t.TempDir(), "node-cell-peer.mjs")
				if err := os.WriteFile(peer, []byte("export default function () {}\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				configs = append(configs, subprocess.ExtConfig{Name: "node-cell-peer", Source: peer, Enabled: true})
			}
			// A host that never answers the factory's exec leaves the factory waiting, as an unanswered call would in Pi; the bound turns that into a failure.
			loadCtx, cancelLoad := context.WithTimeout(t.Context(), time.Minute)
			t.Cleanup(cancelLoad)
			exts, errs := h.LoadAll(loadCtx, configs)
			if len(errs) != 0 || len(exts) != len(configs) {
				t.Fatalf("LoadAll: %d loaded, %v", len(exts), errs)
			}
			runner := inproc.NewRunner(exts, t.TempDir())
			bridge.SetUIPromptScope(runner)

			command, ok := findCommand(runner, "load-exec-probe")
			if !ok {
				t.Fatal("load-exec-probe command missing")
			}
			if err := command.Handler(runner.DispatchContext(t.Context()), ""); err != nil {
				t.Fatal(err)
			}
			if len(*notify) != 1 {
				t.Fatalf("notifications = %q", *notify)
			}
			var got []string
			if err := json.Unmarshal([]byte(strings.TrimSuffix((*notify)[0], ":info")), &got); err != nil {
				t.Fatalf("outcomes %q: %v", (*notify)[0], err)
			}
			if !slices.Equal(got, want) {
				t.Errorf("load-time exec outcomes\n got %q\nwant %q", got, want)
			}
		})
	}
}
