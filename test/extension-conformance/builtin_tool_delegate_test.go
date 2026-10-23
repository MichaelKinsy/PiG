package extensionconformance

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// TestConformance_BuiltinToolDelegationAcrossSDKs: Pi's create<Tool>ToolDefinition(cwd, options) and create<Tool>Tool factories (core/tools/*.ts, exported from the
// package index) give an extension the built-in tool, which it wraps to override a built-in and delegates to. The Node SDK runs Pi's own code, so its answer is
// the reference. Every case runs in each SDK through the delegation API and must report what Pi's code reports, for the semantics an override depends on:
// which working directory wins (the extension context's, not the factory's, as `ctx?.cwd || cwd` reads), the signal, the streamed updates, the errors the tool
// throws, the options (including the `operations` and `spawnHook` functions, which stay in the extension), and createLocalBashOperations.
//
// The tools run on the host in the native SDKs, so the values below (file contents on the host's disk, the output of a shell command whose result the shell
// computes, the session identity the host reports) cannot be produced by an SDK that never made the call.
func TestConformance_BuiltinToolDelegationAcrossSDKs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	if runtime.GOOS == "windows" {
		t.Skip("the cases run POSIX shell commands (arithmetic expansion, exec, printf) through Pi's bash tool, which on Windows needs a Git bash or WSL the Windows runners do not guarantee, and compare file paths as text; the test binary is compiled for windows in CI of this lane (GOOS=windows go test -c) and the PowerShell path (createLocalPowerShellOperations, the powershell tool) has no Windows runner to validate it on")
	}
	t.Parallel()

	type tcase = map[string]any
	// cases are built per harness, because they name the host's working directory.
	cases := func(hostCwd, factoryCwd string) []tcase {
		return []tcase{
			// cwd: the context's wins over the factory's; a tool without a context uses the factory's.
			{"kind": "delegate", "tool": "read", "factoryCwd": factoryCwd, "params": tcase{"path": "marker.txt"}},
			{"kind": "delegate", "tool": "read", "factoryCwd": factoryCwd, "params": tcase{"path": "marker.txt"}, "noContext": true},
			{"kind": "delegate", "tool": "read", "factoryCwd": factoryCwd, "params": tcase{"path": "multi.txt", "offset": 3, "limit": 2}},
			{"kind": "delegate", "tool": "read", "factoryCwd": factoryCwd, "params": tcase{"path": "multi.txt"}, "noContext": true},
			// errors the tool throws
			{"kind": "delegate", "tool": "read", "factoryCwd": factoryCwd, "params": tcase{"path": "nope.txt"}},
			{"kind": "delegate", "tool": "read", "factoryCwd": factoryCwd, "params": tcase{"path": "multi.txt", "offset": 99}},
			{"kind": "delegate", "tool": "read", "factoryCwd": factoryCwd, "params": tcase{"path": "marker.txt"}, "preAborted": true},
			// operations stay in the extension
			{"kind": "delegate", "tool": "read", "factoryCwd": factoryCwd, "params": tcase{"path": "anything.txt"}, "ops": "custom"},
			{"kind": "delegate", "tool": "write", "factoryCwd": factoryCwd, "params": tcase{"path": "out/new.txt", "content": "hi"}, "ops": "custom"},
			{"kind": "delegate", "tool": "edit", "factoryCwd": factoryCwd, "params": tcase{"path": "e.txt", "edits": []tcase{{"oldText": "world", "newText": "there"}}}, "ops": "custom"},
			{"kind": "delegate", "tool": "ls", "factoryCwd": factoryCwd, "params": tcase{"path": "."}, "ops": "custom"},
			{"kind": "delegate", "tool": "grep", "factoryCwd": factoryCwd, "params": tcase{"pattern": "x", "path": "gone"}, "ops": "custom"},
			{"kind": "delegate", "tool": "find", "factoryCwd": factoryCwd, "params": tcase{"pattern": "*.go", "path": "gone"}, "ops": "custom"},
			// real writes and edits on the host's disk
			{"kind": "delegate", "tool": "write", "factoryCwd": factoryCwd, "params": tcase{"path": "written/w.txt", "content": "w-body"}},
			{"kind": "delegate", "tool": "edit", "factoryCwd": factoryCwd, "params": tcase{"path": "marker.txt", "edits": []tcase{{"oldText": "from-H", "newText": "edited-H"}}}},
			{"kind": "delegate", "tool": "read", "factoryCwd": factoryCwd, "params": tcase{"path": "marker.txt"}},
			// bash
			{"kind": "delegate", "tool": "bash", "factoryCwd": factoryCwd, "params": tcase{"command": "echo sentinel-$((6*7)); pwd"}},
			{"kind": "delegate", "tool": "bash", "factoryCwd": factoryCwd, "params": tcase{"command": "echo sentinel-$((6*7)); pwd"}, "noContext": true},
			{"kind": "delegate", "tool": "bash", "factoryCwd": factoryCwd, "params": tcase{"command": "echo oops; exit 3"}},
			{"kind": "delegate", "tool": "bash", "factoryCwd": factoryCwd, "params": tcase{"command": "echo one; sleep 0.4; echo two"}, "updates": true},
			{"kind": "delegate", "tool": "bash", "factoryCwd": factoryCwd, "params": tcase{"command": "echo partial; exec sleep 30"}, "abortOnOutput": true},
			{"kind": "delegate", "tool": "bash", "factoryCwd": factoryCwd, "params": tcase{"command": "exec sleep 30", "timeout": 1}},
			{"kind": "delegate", "tool": "bash", "factoryCwd": factoryCwd, "params": tcase{"command": "echo [$PI_SESSION_ID][$PI_MODEL][$PI_PROVIDER]"}},
			{"kind": "delegate", "tool": "bash", "factoryCwd": factoryCwd, "params": tcase{"command": "echo [$PI_SESSION_ID][$PI_MODEL]"}, "exposeSessionEnvironment": false},
			{"kind": "delegate", "tool": "bash", "factoryCwd": factoryCwd, "params": tcase{"command": "echo [$PI_SESSION_ID]"}, "noContext": true},
			{"kind": "delegate", "tool": "bash", "factoryCwd": factoryCwd, "params": tcase{"command": "echo $PFX"}, "commandPrefix": "export PFX=pfx-1"},
			{"kind": "delegate", "tool": "bash", "factoryCwd": factoryCwd, "params": tcase{"command": "echo original"}, "hook": true},
			{"kind": "delegate", "tool": "bash", "factoryCwd": factoryCwd, "params": tcase{"command": "echo hi"}, "ops": "custom"},
			// the definitions
			{"kind": "definition", "tool": "read", "factoryCwd": factoryCwd},
			{"kind": "definition", "tool": "write", "factoryCwd": factoryCwd},
			{"kind": "definition", "tool": "edit", "factoryCwd": factoryCwd},
			{"kind": "definition", "tool": "bash", "factoryCwd": factoryCwd},
			{"kind": "definition", "tool": "bash", "factoryCwd": factoryCwd, "exposeSessionEnvironment": false},
			{"kind": "definition", "tool": "grep", "factoryCwd": factoryCwd},
			{"kind": "definition", "tool": "find", "factoryCwd": factoryCwd},
			{"kind": "definition", "tool": "ls", "factoryCwd": factoryCwd},
			{"kind": "definition", "tool": "powershell", "factoryCwd": factoryCwd},
			// createLocalBashOperations
			{"kind": "local", "shell": "bash", "factoryCwd": hostCwd, "command": "echo local-$((1+1)); printf err >&2"},
			{"kind": "local", "shell": "bash", "factoryCwd": hostCwd, "command": "echo bye; exit 3"},
			{"kind": "local", "shell": "bash", "factoryCwd": hostCwd, "command": "echo $FOO", "env": tcase{"PATH": os.Getenv("PATH"), "FOO": "bar"}},
			{"kind": "local", "shell": "bash", "factoryCwd": hostCwd, "command": "echo begun; exec sleep 30", "abortOnOutput": true},
			{"kind": "local", "shell": "bash", "factoryCwd": hostCwd, "command": "exec sleep 30", "timeout": 1},
			{"kind": "local", "shell": "bash", "factoryCwd": filepath.Join(hostCwd, "no-such-dir"), "command": "echo x"},
			// the functions of the options and the streamed updates, for a tool without an extension context (the host reaches back into the extension for them)
			{"kind": "delegate", "tool": "read", "factoryCwd": factoryCwd, "params": tcase{"path": "anything.txt"}, "ops": "custom", "noContext": true},
			{"kind": "delegate", "tool": "bash", "factoryCwd": factoryCwd, "params": tcase{"command": "echo hi"}, "ops": "custom", "noContext": true},
			{"kind": "delegate", "tool": "bash", "factoryCwd": factoryCwd, "params": tcase{"command": "echo one; sleep 0.4; echo two"}, "updates": true, "noContext": true},
			{"kind": "delegate", "tool": "bash", "factoryCwd": factoryCwd, "params": tcase{"command": "echo original"}, "hook": true, "noContext": true},
			// A truncated read's details, whose members Pi writes in an order a sorted map would lose
			{"kind": "delegate", "tool": "read", "factoryCwd": factoryCwd, "params": tcase{"path": "big.txt"}},
			// createCodingTools and createReadOnlyTools
			{"kind": "sets", "factoryCwd": factoryCwd},
		}
	}
	// A host file system with the files the cases read, created through the context's working directory once the harness reports it.
	setup := func(t *testing.T, hostCwd, factoryCwd string) {
		t.Helper()
		var multi strings.Builder
		for i := 1; i <= 30; i++ {
			fmt.Fprintf(&multi, "l%d\n", i)
		}
		for path, content := range map[string]string{
			filepath.Join(hostCwd, "marker.txt"):    "from-H\nline2\n",
			filepath.Join(hostCwd, "multi.txt"):     multi.String(),
			filepath.Join(hostCwd, "big.txt"):       strings.Repeat("a line of the big file\n", 2500),
			filepath.Join(factoryCwd, "marker.txt"): "from-X\n",
			filepath.Join(factoryCwd, "multi.txt"):  "x1\nx2\n",
		} {
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}

	run := func(t *testing.T, h *harness, c tcase) map[string]any {
		t.Helper()
		args, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		before := len(h.ui.Recorded())
		if !h.runner.ExecuteCommand(context.Background(), "builtin_probe", string(args)) {
			t.Fatal("fixture has no builtin_probe command")
		}
		var reported string
		pollUntilConformance(t, "builtin_probe: no report for "+string(args), func() bool {
			for _, n := range h.ui.Recorded()[before:] {
				if rest, ok := strings.CutPrefix(n, "builtin:"); ok {
					reported = strings.TrimSuffix(strings.TrimSuffix(rest, ":info"), ":end")
					return true
				}
			}
			return false
		})
		var got map[string]any
		if err := json.Unmarshal([]byte(reported), &got); err != nil {
			t.Fatalf("%s is not JSON: %v", reported, err)
		}
		return got
	}

	// observe runs every case in one harness and returns the reports, normalized: the working directories are replaced by names, and the streamed updates by
	// what is stable about them (their timing is not).
	observe := func(t *testing.T, h *harness) []map[string]any {
		t.Helper()
		hostCwd, _ := run(t, h, tcase{"kind": "cwd"})["cwd"].(string)
		if hostCwd == "" {
			t.Fatal("the probe reported no working directory")
		}
		factoryCwd := t.TempDir()
		setup(t, hostCwd, factoryCwd)
		var out []map[string]any
		for _, c := range cases(hostCwd, factoryCwd) {
			report := run(t, h, c)
			encoded, err := json.Marshal(report)
			if err != nil {
				t.Fatal(err)
			}
			// A shell reports the real path of a working directory that is reached through a symlink (macOS /var), which the tools do not resolve.
			text := string(encoded)
			for _, dir := range [][2]string{{hostCwd, "<host>"}, {factoryCwd, "<factory>"}} {
				if real, err := filepath.EvalSymlinks(dir[0]); err == nil && real != dir[0] {
					text = strings.ReplaceAll(text, real, dir[1])
				}
				text = strings.ReplaceAll(text, dir[0], dir[1])
			}
			var normalized map[string]any
			if err := json.Unmarshal([]byte(text), &normalized); err != nil {
				t.Fatal(err)
			}
			if updates, ok := normalized["updates"].([]any); ok {
				// Pi sends an empty first update, then snapshots of the output so far, the last of which is the whole output.
				summary := map[string]any{"count>=2": len(updates) >= 2}
				if len(updates) > 0 {
					summary["first"] = updates[0]
					summary["last"] = updates[len(updates)-1]
				}
				normalized["updates"] = summary
			}
			// The written file is the host's and shared by the cases that follow: reset it.
			_ = os.RemoveAll(filepath.Join(hostCwd, "written"))
			_ = os.WriteFile(filepath.Join(hostCwd, "marker.txt"), []byte("from-H\nline2\n"), 0o644)
			out = append(out, normalized)
		}
		return out
	}

	var reference []map[string]any
	var referenceCases []tcase
	for _, tc := range sdkHarnessCases() {
		if tc.name != "subprocess-node" {
			continue
		}
		h := tc.make(t)
		reference = observe(t, h)
		referenceCases = cases("<host>", "<factory>")
		if h.cleanup != nil {
			h.cleanup()
		}
		if h.host != nil {
			h.host.Shutdown("test done")
		}
	}
	if len(reference) == 0 || len(reference) != len(referenceCases) {
		t.Fatalf("the Node reference ran %d of %d cases", len(reference), len(referenceCases))
	}

	// Pi's own values, so a mistake shared by every SDK (and by the reference's harness) cannot pass.
	text := func(report map[string]any) string {
		result, _ := report["result"].(map[string]any)
		content, _ := result["content"].([]any)
		var parts []string
		for _, block := range content {
			parts = append(parts, block.(map[string]any)["text"].(string))
		}
		return strings.Join(parts, "\n")
	}
	errorOf := func(report map[string]any) string { s, _ := report["error"].(string); return s }
	definitionField := func(field string) func(map[string]any) string {
		return func(report map[string]any) string {
			definition, _ := report["definition"].(map[string]any)
			encoded, _ := json.Marshal(definition[field])
			return string(encoded)
		}
	}
	expectations := []struct {
		index int
		check func(map[string]any) string
		want  string
	}{
		{0, text, "from-H\nline2\n"}, // the context's working directory wins over the factory's
		{1, text, "from-X\n"},        // a tool without a context uses the factory's
		{2, text, "l3\nl4\n\n[27 more lines in file. Use offset=5 to continue.]"},
		{4, errorOf, "ENOENT: no such file or directory, access '<host>/nope.txt'"},
		{5, errorOf, "Offset 99 is beyond end of file (31 lines total)"},
		{6, errorOf, "Operation aborted"},
		{7, text, "OPS:<host>/anything.txt"},
		{16, text, "sentinel-42\n<host>\n"},
		{17, text, "sentinel-42\n<factory>\n"},
		{20, errorOf, "Command aborted"},
		{21, errorOf, "Command timed out after 1 seconds"},
		{22, text, "[conformance-session-id][current][conformance]\n"},
		{23, text, "[][]\n"},
		{24, text, "[]\n"},
		{25, text, "pfx-1\n"},
		{26, text, "hook-env|conformance-session-id"},
		{27, text, "custom:echo hi"},
		{43, text, "OPS:<factory>/anything.txt"}, // the functions of the options, for a tool without a context
		{44, text, "custom:echo hi"},
		{46, text, "hook-env|"},
		{28, definitionField("description"), `"Read the contents of a file. Supports text files and images (jpg, png, gif, webp, bmp). Images are sent as attachments. For text files, output is truncated to 2000 lines or 50KB (whichever is hit first).`},
		{28, definitionField("promptSnippet"), `"Read file contents"`},
		{30, definitionField("renderShell"), `"self"`},
		{31, definitionField("outputSchema"), `"exit_code"`},
		{32, definitionField("promptGuidelines"), `null`},
		{33, definitionField("promptSnippet"), `"Search file contents for patterns (respects .gitignore)"`},
	}
	if got, _ := json.Marshal(reference[len(reference)-1]); string(got) != `{"coding":["read","bash","edit","write"],"readOnly":["read","grep","find","ls"]}` {
		t.Fatalf("Pi's own createCodingTools and createReadOnlyTools reported %s", got)
	}
	// Pi writes a truncated read's details with the members of its truncation in the order the tool built them, which JSON.stringify keeps and a map does not:
	// the text of the details, in member order, is the same in every SDK (the comparison below) and is not the sorted order.
	bigResult, _ := reference[len(reference)-2]["result"].(map[string]any)
	if details, _ := bigResult["detailsJSON"].(string); !strings.HasPrefix(details, `{"truncation":{"content":`) {
		t.Fatalf("Pi's own details = %q", details)
	}
	// bash's structured content lists its members in insertion order
	if keys, _ := reference[16]["result"].(map[string]any)["structuredKeys"].([]any); fmt.Sprint(keys) != "[output truncated exit_code wall_time_seconds]" {
		t.Fatalf("Pi's own structuredContent keys = %v", keys)
	}
	for _, e := range expectations {
		if got := e.check(reference[e.index]); !strings.Contains(got, e.want) {
			t.Fatalf("Pi's own code reported %q for case %d (%v), want it to contain %q: the reference is not Pi's behavior", got, e.index, referenceCases[e.index], e.want)
		}
	}

	harnessCases := append(sdkHarnessCases(), packedSDKHarnessCases()...)
	for _, tc := range harnessCases {
		if tc.name == "subprocess-node" {
			continue
		}
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
			got := observe(t, h)
			for i, report := range got {
				if !reflect.DeepEqual(report, reference[i]) {
					a, _ := json.Marshal(report)
					b, _ := json.Marshal(reference[i])
					t.Errorf("case %d %v:\n got  %s\n want %s (Pi's own code)", i, referenceCases[i], a, b)
				}
			}
		})
	}
}

// TestConformance_BuiltinToolOverrideDelegatesAcrossSDKs: an extension overrides the read and bash tools with `{ ...definition, execute }`, where execute runs the
// built-in through the SDK and post-processes its result. The model's calls reach the override through the agent's dispatch, the built-in runs (on the host for
// the native SDKs) in the working directory of the session, and the model gets the post-processed result: a sentinel around text that only the real tool could
// produce (a file's content on the host's disk, the result of shell arithmetic).
func TestConformance_BuiltinToolOverrideDelegatesAcrossSDKs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	if runtime.GOOS == "windows" {
		t.Skip("the cases run POSIX shell commands (arithmetic expansion, exec, printf) through Pi's bash tool, which on Windows needs a Git bash or WSL the Windows runners do not guarantee, and compare file paths as text; the test binary is compiled for windows in CI of this lane (GOOS=windows go test -c) and the PowerShell path (createLocalPowerShellOperations, the powershell tool) has no Windows runner to validate it on")
	}
	t.Parallel()
	harnessCases := append(sdkHarnessCases(), packedSDKHarnessCases()...)
	for _, tc := range harnessCases {
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
			probe := func(c map[string]any) map[string]any {
				args, _ := json.Marshal(c)
				before := len(h.ui.Recorded())
				if !h.runner.ExecuteCommand(context.Background(), "builtin_probe", string(args)) {
					t.Fatal("fixture has no builtin_probe command")
				}
				var reported string
				pollUntilConformance(t, "builtin_probe: no report", func() bool {
					for _, n := range h.ui.Recorded()[before:] {
						if rest, ok := strings.CutPrefix(n, "builtin:"); ok {
							reported = strings.TrimSuffix(strings.TrimSuffix(rest, ":info"), ":end")
							return true
						}
					}
					return false
				})
				var got map[string]any
				if err := json.Unmarshal([]byte(reported), &got); err != nil {
					t.Fatal(err)
				}
				return got
			}
			hostCwd := probe(map[string]any{"kind": "cwd"})["cwd"].(string)
			unrelated := t.TempDir()
			if err := os.WriteFile(filepath.Join(hostCwd, "doc.txt"), []byte("distinct-file-body-9f3a\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			registered := probe(map[string]any{"kind": "register", "factoryCwd": unrelated})["registered"]
			if !reflect.DeepEqual(registered, []any{"read", "bash"}) {
				t.Fatalf("registered = %v, want the read and bash overrides", registered)
			}
			results := dispatchToolCalls(t, h, []ai.FauxContentBlock{
				ai.FauxToolCall("read", map[string]any{"path": "doc.txt"}, &ai.FauxToolCallOptions{ID: "override-read"}),
				ai.FauxToolCall("bash", map[string]any{"command": "echo answer-$((6*7))"}, &ai.FauxToolCallOptions{ID: "override-bash"}),
				ai.FauxToolCall("read", map[string]any{"path": "missing.txt"}, &ai.FauxToolCallOptions{ID: "override-missing"}),
			})
			if len(results) != 3 {
				t.Fatalf("%d results, want 3: %s", len(results), describeToolResults(results))
			}
			if results[0].IsError || results[0].Text() != "«delegated:distinct-file-body-9f3a\n»" {
				t.Errorf("read override = %q (error %t), want the file's content wrapped in the sentinel", results[0].Text(), results[0].IsError)
			}
			if results[1].IsError || results[1].Text() != "«answer-42\n»" {
				t.Errorf("bash override = %q (error %t), want the command's output wrapped in the sentinel", results[1].Text(), results[1].IsError)
			}
			// The built-in throws for a missing file; the override does not catch it, so the model sees Pi's error.
			if !results[2].IsError || !strings.Contains(results[2].Text(), "ENOENT: no such file or directory, access") {
				t.Errorf("read of a missing file = %q (error %t), want the built-in's thrown error", results[2].Text(), results[2].IsError)
			}
		})
	}
}
