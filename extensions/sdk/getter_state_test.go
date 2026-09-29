package sdk

import (
	"encoding/json"
	"strings"
	"testing"
)

// Pi's getters return undefined for absent state and throw for a failed host
// call. The Go getters return a nil pointer for the first and an error for the
// second, never an empty value or a default.
func TestContextGettersDistinguishAbsentEmptyAndFailure(t *testing.T) {
	type probe struct {
		name   string
		reply  string
		call   func(Context) (any, error)
		want   string
		absent bool
	}
	failed := `ERR`
	probes := []probe{
		{name: "session name value", reply: `{"name":"named"}`, call: func(c Context) (any, error) { return c.GetSessionName() }, want: `"named"`},
		{name: "session name absent", reply: `{"name":""}`, call: func(c Context) (any, error) { return c.GetSessionName() }, absent: true},
		{name: "session file absent", reply: `{"sessionFile":""}`, call: func(c Context) (any, error) { return c.GetSessionFile() }, absent: true},
		{name: "leaf id null", reply: `{"leafId":null}`, call: func(c Context) (any, error) { return c.GetLeafID() }, absent: true},
		{name: "context usage absent", reply: `null`, call: func(c Context) (any, error) { return c.GetContextUsage() }, absent: true},
		{name: "model absent", reply: `{}`, call: func(c Context) (any, error) { return c.GetModelInfo() }, absent: true},
		{name: "editor text empty is a value", reply: `{"text":""}`, call: func(c Context) (any, error) { return c.GetEditorText() }, want: `""`},
		{name: "flag false is a value", reply: `{"value":false}`, call: func(c Context) (any, error) { return c.GetFlag("f") }, want: `false`},
		{name: "flag undefined", reply: `{}`, call: func(c Context) (any, error) { return c.GetFlag("f") }, absent: true},
		{name: "idle false is a value", reply: `{"idle":false}`, call: func(c Context) (any, error) { return c.IsIdle() }, want: `false`},
		{name: "session name failure", reply: failed, call: func(c Context) (any, error) { return c.GetSessionName() }},
		{name: "editor text failure", reply: failed, call: func(c Context) (any, error) { return c.GetEditorText() }},
		{name: "flag failure keeps its default out", reply: failed, call: func(c Context) (any, error) { return c.GetFlag("f") }},
		{name: "tools failure", reply: failed, call: func(c Context) (any, error) { return c.GetActiveTools() }},
		{name: "context usage failure", reply: failed, call: func(c Context) (any, error) { return c.GetContextUsage() }},
		{name: "idle failure is not idle", reply: failed, call: func(c Context) (any, error) { return c.IsIdle() }},
		{name: "trust failure is not trusted", reply: failed, call: func(c Context) (any, error) { return c.IsProjectTrusted() }},
		{name: "reply without its field", reply: `{"unrelated":1}`, call: func(c Context) (any, error) { return c.GetThinkingLevel() }},
	}
	for _, tc := range probes {
		t.Run(tc.name, func(t *testing.T) {
			ext := New("getter-state")
			ext.Flag("f", FlagOptions{Type: FlagString, Default: "fallback"})
			var got any
			var gotErr error
			ext.Command("probe", "probe", func(ctx Context, _ string) error {
				got, gotErr = tc.call(ctx)
				return nil
			})
			host, _, done := surfaceHost(t, ext, nil)
			defer surfaceShutdown(t, host, done)
			runSurfaceCommand(t, host, "probe", func(*callMsg) *callResultMsg {
				if tc.reply == failed {
					return &callResultMsg{Error: &errorInfo{Code: "host_failed", Message: "boom"}}
				}
				return &callResultMsg{Result: json.RawMessage(tc.reply)}
			})
			switch {
			case tc.reply == failed:
				if gotErr == nil || !strings.Contains(gotErr.Error(), "boom") {
					t.Fatalf("error = %v, want the host failure", gotErr)
				}
			case tc.name == "reply without its field":
				if gotErr == nil {
					t.Fatalf("value %v without an error, want a protocol error", got)
				}
			case gotErr != nil:
				t.Fatal(gotErr)
			case tc.absent:
				if tc.name == "flag undefined" {
					// A registered default fills an undefined override.
					if got != "fallback" {
						t.Fatalf("flag = %#v, want the registered default", got)
					}
					return
				}
				if !isNilValue(got) {
					t.Fatalf("value = %#v, want absent", got)
				}
			default:
				encoded, err := json.Marshal(got)
				if err != nil {
					t.Fatal(err)
				}
				if string(encoded) != tc.want {
					t.Fatalf("value = %s, want %s", encoded, tc.want)
				}
			}
		})
	}
}

func isNilValue(v any) bool {
	encoded, _ := json.Marshal(v)
	return string(encoded) == "null"
}

// Upstream hands extensions the collection-complete options (system-prompt.ts:37-64), so an empty
// selectedTools is [] and toolGuidelines is decoded, never dropped.
func TestGetSystemPromptOptionsKeepsEmptyCollectionsAndToolGuidelines(t *testing.T) {
	ext := New("prompt-options")
	var got SystemPromptOptions
	var gotErr error
	ext.Command("probe", "probe", func(ctx Context, _ string) error {
		got, gotErr = ctx.GetSystemPromptOptions()
		return nil
	})
	host, _, done := surfaceHost(t, ext, nil)
	defer surfaceShutdown(t, host, done)
	runSurfaceCommand(t, host, "probe", func(*callMsg) *callResultMsg {
		return &callResultMsg{Result: json.RawMessage(`{"selectedTools":[],"toolSnippets":{},"toolGuidelines":{"read":["Use read."]},"promptGuidelines":[],"appendSystemPrompt":"","sections":{},"cwd":"/x","contextFiles":[],"skills":[]}`)}
	})
	if gotErr != nil {
		t.Fatal(gotErr)
	}
	if got.SelectedTools == nil || got.ToolSnippets == nil || got.PromptGuidelines == nil || got.ContextFiles == nil || got.Skills == nil || got.Sections == nil {
		t.Fatalf("empty collections were dropped: %#v", got)
	}
	if len(got.ToolGuidelines["read"]) != 1 || got.ToolGuidelines["read"][0] != "Use read." {
		t.Fatalf("toolGuidelines = %#v", got.ToolGuidelines)
	}
}

// A failed session-log subscription is returned by every mirror read, not hidden as an empty or partial mirror.
func TestSessionLogGettersReportSubscriptionFailure(t *testing.T) {
	ext := New("mirror-failure")
	var branchErr, entriesErr error
	ext.Command("probe", "probe", func(ctx Context, _ string) error {
		_, branchErr = ctx.GetBranch()
		_, entriesErr = ctx.GetEntries()
		return nil
	})
	host, _, done := surfaceHost(t, ext, nil)
	defer surfaceShutdown(t, host, done)
	calls, _ := runSurfaceCommand(t, host, "probe", func(*callMsg) *callResultMsg {
		return &callResultMsg{Error: &errorInfo{Code: "host_failed", Message: "log unavailable"}}
	})
	if len(calls) != 1 {
		t.Fatalf("watchSessionLog calls = %d, want one attempt", len(calls))
	}
	for label, err := range map[string]error{"GetBranch": branchErr, "GetEntries": entriesErr} {
		if err == nil || !strings.Contains(err.Error(), "log unavailable") {
			t.Errorf("%s error = %v, want the subscription failure", label, err)
		}
	}
}
