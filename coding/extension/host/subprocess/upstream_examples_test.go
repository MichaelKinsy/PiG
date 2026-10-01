package subprocess

import (
	"maps"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

type exampleUserMessage struct {
	content any
	options SendUserMessageOptions
}

type exampleCalls struct {
	mu       sync.Mutex
	execs    []string
	messages []exampleUserMessage
}

// The unchanged Pi example runs in the production Node subprocess. Go owns the inputs, exec boundary, event dispatch, and assertions, not a replacement TypeScript test runner.
func loadUpstreamExample(t *testing.T, name, cwd string, results map[string]extension.ExecResult) (*inproc.Runner, *exampleCalls) {
	t.Helper()
	calls := &exampleCalls{}
	host := NewHostWithConfigRoot(cwd, t.TempDir())
	t.Cleanup(func() { host.Shutdown("test done") })
	bridge := NewUIBridge(func() {})
	bridge.SetActions(&HostCallbacks{
		Exec: func(command string, args []string, _ *extension.ExecOptions) (extension.ExecResult, error) {
			calls.mu.Lock()
			defer calls.mu.Unlock()
			key := strings.Join(append([]string{command}, args...), " ")
			calls.execs = append(calls.execs, key)
			if result, ok := results[key]; ok {
				return result, nil
			}
			return extension.ExecResult{Stderr: "error", Code: 1}, nil
		},
		SendUserMessage: func(content any, options SendUserMessageOptions) error {
			calls.mu.Lock()
			defer calls.mu.Unlock()
			calls.messages = append(calls.messages, exampleUserMessage{content, options})
			return nil
		},
	})
	host.SetUIBridge(bridge)
	entry := filepath.Join(findModuleRoot(t), ".upstream", "current", "packages", "coding-agent", "examples", "extensions", name+".ts")
	loaded, errs := host.LoadAll(t.Context(), []ExtConfig{{Name: name, Source: entry, Enabled: true}})
	if len(errs) != 0 || len(loaded) != 1 {
		t.Fatalf("load %s: %v (%d extensions)", name, errs, len(loaded))
	}
	return inproc.NewRunner(loaded, cwd), calls
}

func TestInputTransformStreamingExample(t *testing.T) {
	t.Parallel()
	const diff = " src/index.ts | 5 ++---\n 1 file changed, 2 insertions(+), 3 deletions(-)"
	success := extension.ExecResult{Stdout: diff}
	for _, tt := range []struct {
		name, text, streaming string
		result                extension.ExecResult
		exec, transform       bool
	}{
		// .upstream/v0.87.1/packages/coding-agent/test/input-transform-streaming-example.test.ts:42
		{"skips exec during steering", "what changes did I make?", "steer", success, false, false},
		// .upstream/v0.87.1/packages/coding-agent/test/input-transform-streaming-example.test.ts:49
		{"transforms when idle and text matches trigger", "review my changes", "", success, true, true},
		// .upstream/v0.87.1/packages/coding-agent/test/input-transform-streaming-example.test.ts:59
		{"transforms when queued as follow-up", "show me the diff", "followUp", success, true, true},
		// .upstream/v0.87.1/packages/coding-agent/test/input-transform-streaming-example.test.ts:66
		{"continues when text does not match trigger", "explain this function", "", success, false, false},
		// .upstream/v0.87.1/packages/coding-agent/test/input-transform-streaming-example.test.ts:73
		{"continues when git diff is empty", "any changes?", "", extension.ExecResult{}, true, false},
		// .upstream/v0.87.1/packages/coding-agent/test/input-transform-streaming-example.test.ts:79
		{"continues when git fails", "show modified files", "", extension.ExecResult{Stderr: "not a git repo", Code: 128}, true, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			runner, calls := loadUpstreamExample(t, "input-transform-streaming", t.TempDir(), map[string]extension.ExecResult{"git diff --stat": tt.result})
			got, err := runner.EmitInput(t.Context(), tt.text, nil, "interactive", tt.streaming)
			if err != nil {
				t.Fatal(err)
			}
			var want extension.InputEventResult = extension.InputEventResultContinue{}
			if tt.transform {
				want = extension.InputEventResultTransform{Text: tt.text + "\n\nCurrent uncommitted changes:\n```\n" + strings.TrimSpace(diff) + "\n```"}
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("input result = %#v, want %#v", got, want)
			}
			calls.mu.Lock()
			defer calls.mu.Unlock()
			var wantExecs []string
			if tt.exec {
				wantExecs = []string{"git diff --stat"}
			}
			if !slices.Equal(calls.execs, wantExecs) {
				t.Fatalf("exec calls = %v, want %v", calls.execs, wantExecs)
			}
		})
	}
}

func TestGitMergeAndResolveExample(t *testing.T) {
	t.Parallel()
	ok := extension.ExecResult{}
	fail := extension.ExecResult{Stderr: "error", Code: 1}
	withUpstream := func() map[string]extension.ExecResult {
		return map[string]extension.ExecResult{
			"git rev-parse --git-dir":                              ok,
			"git rev-parse MERGE_HEAD":                             fail,
			"git status --porcelain":                               ok,
			"git rev-parse --abbrev-ref --symbolic-full-name @{u}": {Stdout: "origin/main\n"},
			"git fetch origin":                                     ok,
		}
	}
	for _, tt := range []struct {
		name                  string
		upstream              bool
		results               map[string]extension.ExecResult
		file, content, report string
		oneExec, noFetch      bool
	}{
		// .upstream/v0.87.1/packages/coding-agent/test/git-merge-and-resolve-extension.test.ts:63
		{name: "skips when not a git repository", results: map[string]extension.ExecResult{"git rev-parse --git-dir": fail}, oneExec: true},
		// .upstream/v0.87.1/packages/coding-agent/test/git-merge-and-resolve-extension.test.ts:75
		{name: "skips when no upstream is configured", results: map[string]extension.ExecResult{"git rev-parse --git-dir": ok, "git rev-parse --abbrev-ref --symbolic-full-name @{u}": fail}},
		// .upstream/v0.87.1/packages/coding-agent/test/git-merge-and-resolve-extension.test.ts:87
		{name: "re-sends conflicts when in an unfinished merge", results: map[string]extension.ExecResult{"git rev-parse --git-dir": ok, "git rev-parse MERGE_HEAD": ok, "git diff --name-only --diff-filter=U": {Stdout: "file.ts\n"}}, file: "file.ts", content: "<<<<<<< HEAD\nours\n=======\ntheirs\n>>>>>>> origin/main", report: "file.ts:1-5", noFetch: true},
		// .upstream/v0.87.1/packages/coding-agent/test/git-merge-and-resolve-extension.test.ts:107
		{name: "skips when working tree is dirty and not in a merge", results: map[string]extension.ExecResult{"git rev-parse --git-dir": ok, "git rev-parse MERGE_HEAD": fail, "git status --porcelain": {Stdout: " M src/index.ts\n"}}, noFetch: true},
		// .upstream/v0.87.1/packages/coding-agent/test/git-merge-and-resolve-extension.test.ts:121
		{name: "skips when fetch fails", upstream: true, results: map[string]extension.ExecResult{"git fetch origin": fail}},
		// .upstream/v0.87.1/packages/coding-agent/test/git-merge-and-resolve-extension.test.ts:132
		{name: "skips when merge is clean", upstream: true, results: map[string]extension.ExecResult{"git merge --no-ff origin/main": ok}},
		// .upstream/v0.87.1/packages/coding-agent/test/git-merge-and-resolve-extension.test.ts:143
		{name: "sends conflict report as a follow-up", upstream: true, results: map[string]extension.ExecResult{"git merge --no-ff origin/main": fail, "git diff --name-only --diff-filter=U": {Stdout: "src/index.ts\n"}}, file: "src/index.ts", content: "line 1\n<<<<<<< HEAD\nour change\n=======\ntheir change\n>>>>>>> origin/main\nline 7\n<<<<<<< HEAD\nsecond conflict\n=======\ntheir second\n>>>>>>> origin/main", report: "src/index.ts:2-6 (ours 3, theirs 5)\nsrc/index.ts:8-12 (ours 9, theirs 11)"},
		// .upstream/v0.87.1/packages/coding-agent/test/git-merge-and-resolve-extension.test.ts:177
		{name: "handles empty ours or theirs sections", upstream: true, results: map[string]extension.ExecResult{"git merge --no-ff origin/main": fail, "git diff --name-only --diff-filter=U": {Stdout: "empty-ours.ts\n"}}, file: "empty-ours.ts", content: "<<<<<<< HEAD\n=======\nonly theirs\n>>>>>>> origin/main", report: "empty-ours.ts:1-4 (ours empty, theirs 3)"},
		// .upstream/v0.87.1/packages/coding-agent/test/git-merge-and-resolve-extension.test.ts:195
		{name: "skips message when merge fails but no conflict markers found", upstream: true, results: map[string]extension.ExecResult{"git merge --no-ff origin/main": fail, "git diff --name-only --diff-filter=U": ok}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cwd := t.TempDir()
			if tt.file != "" {
				write(t, filepath.Join(cwd, tt.file), tt.content)
			}
			results := map[string]extension.ExecResult{}
			if tt.upstream {
				results = withUpstream()
			}
			maps.Copy(results, tt.results)
			runner, calls := loadUpstreamExample(t, "git-merge-and-resolve", cwd, results)
			if _, err := runner.Emit(t.Context(), extension.AgentEndEvent{Type: "agent_end"}); err != nil {
				t.Fatal(err)
			}
			calls.mu.Lock()
			defer calls.mu.Unlock()
			if tt.oneExec && len(calls.execs) != 1 {
				t.Fatalf("exec calls = %v, want one", calls.execs)
			}
			if tt.noFetch && slices.Contains(calls.execs, "git fetch origin") {
				t.Fatal("unexpected fetch")
			}
			if tt.report == "" {
				if len(calls.messages) != 0 {
					t.Fatalf("unexpected messages: %+v", calls.messages)
				}
				return
			}
			if len(calls.messages) != 1 {
				t.Fatalf("messages = %+v, want one", calls.messages)
			}
			message, ok := calls.messages[0].content.(string)
			if !ok {
				t.Fatalf("message content = %#v", calls.messages[0].content)
			}
			for line := range strings.SplitSeq(tt.report, "\n") {
				if !strings.Contains(message, line) {
					t.Errorf("message %q does not contain %q", message, line)
				}
			}
			if calls.messages[0].options != (SendUserMessageOptions{DeliverAs: "followUp"}) {
				t.Fatalf("options = %+v", calls.messages[0].options)
			}
		})
	}
}
