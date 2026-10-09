package extensionconformance

import (
	"context"
	"encoding/json"
	"runtime"
	"slices"
	"strconv"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// execOutcome is how the exec-reject-probe fixtures report one pi.exec call:
// "rejected:" and the error message, or "resolved:" and the exit code.
func execOutcome(result extension.ExecResult, err error) string {
	if err != nil {
		return "rejected:" + err.Error()
	}
	return "resolved:" + strconv.Itoa(result.Code)
}

// execReference is the Go reference for extension.API.Exec: the host's production ExecCommand behind the API method, as the host's
// exec action runs it for an SDK (Pi's pi.exec, packages/coding-agent/src/core/extensions/types.ts:1724).
type execReference struct {
	extension.API
	cwd string
}

func (r execReference) Exec(command string, args []string, options *extension.ExecOptions) (extension.ExecResult, error) {
	return extension.ExecCommand(context.Background(), r.cwd, command, args, options)
}

// Pi's execCommand (core/exec.ts) spawns inside its Promise executor, so what
// spawn throws rejects pi.exec with that error's message: Node's
// ERR_INVALID_ARG_VALUE for an empty command or a NUL, and on Windows "spawn
// EINVAL" for a .cmd or .bat program. A program that is not found resolves
// with code 1. Every SDK must reject with the host's exact message: a code
// prefix or a fallback such as "None:" or "call_failed:" is drift. The host's
// exec is the production ExecCommand; the in-process call is the reference (extension.API.Exec, Pi pi.exec,
// packages/coding-agent/src/core/extensions/types.ts:1724).
func TestExecRejectionsAcrossSDKs(t *testing.T) {
	t.Parallel()
	commands := []string{"", "tool.cmd", "TOOL.BAT ", "a\x00b", "pig-missing-program"}
	probe, err := json.Marshal(commands)
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	var api extension.API = execReference{cwd: t.TempDir()}
	for _, command := range commands {
		want = append(want, execOutcome(api.Exec(command, nil, nil))+":info")
	}
	// The rows must exercise both rejections, not only resolutions.
	if want[0] != "rejected:The argument 'file' cannot be empty. Received '':info" || want[3] != `rejected:The argument 'file' must be a string without null bytes. Received 'a\x00b':info` || want[4] != "resolved:1:info" {
		t.Fatalf("reference outcomes = %q", want)
	}
	if runtime.GOOS == "windows" && (want[1] != "rejected:spawn EINVAL:info" || want[2] != "rejected:spawn EINVAL:info") {
		t.Fatalf("reference outcomes = %q, want spawn EINVAL for batch files", want)
	}
	cases := sdkHarnessCases()
	for _, language := range []string{"go", "rust", "python"} {
		cases = append(cases, harnessCase{name: "packed-" + language, make: func(t *testing.T) *harness { return makePackedUIHarness(t, language) }})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			if h.bridge == nil {
				t.Fatal("subprocess harness has no bridge")
			}
			cwd := t.TempDir()
			h.bridge.SetHostAction("exec", func(ctx context.Context, command string, args []string, opts *extension.ExecOptions) (extension.ExecResult, error) {
				return extension.ExecCommand(ctx, cwd, command, args, opts)
			})
			command, ok := findCommand(h.runner, "exec-reject-probe")
			if !ok {
				t.Fatal("exec-reject-probe command missing")
			}
			*h.notify = nil
			if err := command.Handler(h.runner.DispatchContext(t.Context()), string(probe)); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(*h.notify, want) {
				t.Errorf("exec outcomes\n got %q\nwant %q", *h.notify, want)
			}
		})
	}
}
