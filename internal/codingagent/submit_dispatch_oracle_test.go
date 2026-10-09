package codingagent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// interactive-mode.ts setupEditorSubmitHandler against pinned Pi: which built-in command (and which argument) a submitted line runs, or whether it
// is sent as a prompt. Pi's onSubmit runs over a recording `this`; Pig's pipeline is isUserBashCommand, resolvableSlashCommand and the slash registry
// with its handlers replaced by recorders.
func TestSubmitDispatchMatchesPi(t *testing.T) {
	commands := map[string]string{
		"showSettingsSelector": "settings", "showModelsSelector": "scoped-models", "handleModelCommand": "model", "handleThinkingCommand": "thinking",
		"handleExportCommand": "export", "handleImportCommand": "import", "handleShareCommand": "share", "handleBugCommand": "bug", "handleCopyCommand": "copy",
		"handleNameCommand": "name", "handleSessionCommand": "session", "handleChangelogCommand": "changelog", "handleHotkeysCommand": "hotkeys",
		"showUserMessageSelector": "fork", "handleCloneCommand": "clone", "showTreeSelector": "tree", "showTrustSelector": "trust", "handleLoginCommand": "login",
		"showOAuthSelector": "logout", "handleClearCommand": "new", "handleCompactCommand": "compact", "handleReloadCommand": "reload", "handleDebugCommand": "debug",
		"handleArminSaysHi": "arminsayshi", "handleDementedDelves": "dementedelves", "showSessionSelector": "resume", "shutdown": "quit",
	}
	// Commands whose argument Pi parses before calling the handler; the others receive the whole line and parse it themselves.
	withArgument := map[string]bool{"model": true, "thinking": true, "login": true, "compact": true, "bug": true}

	registry := NewSlashRegistry()
	names := map[string]bool{}
	for _, command := range BuiltinSlashCommands() {
		names[command.Name] = true
		for _, alias := range command.Aliases {
			names[alias] = true
		}
	}
	for _, name := range commands {
		names[name] = true
	}
	type recorded struct{ command, args string }
	var last *recorded
	for _, command := range BuiltinSlashCommands() {
		command.Handler = func(sc *SlashContext) error { last = &recorded{command.Name, sc.Args}; return nil }
		registry.Register(command)
	}
	m := &InteractiveMode{slashRegistry: registry}

	suffixes := []string{"", " ", " x", "  x y ", "\tx", "\u00a0x", "\nx", "\u2003x", "x", "/", " /x", "\ufeffx", " \tx"}
	prefixes := []string{"/", "//", " /", "\t/", "/ ", "\u00a0/", "\ufeff/", "!", "!!", "! /"}
	var texts []string
	for name := range names {
		for _, suffix := range suffixes {
			texts = append(texts, "/"+name+suffix, "/"+strings.ToUpper(name)+suffix)
		}
		for _, prefix := range prefixes {
			texts = append(texts, prefix+name)
		}
	}
	texts = append(texts, "", " ", "hello", "!", "!!", "! ", "!!ls", "! ls", "!!  ls ", "/unknown", "/unknown x", "/", "/ ", "//", "!/model", "/model-x", "/models", "/m")
	slices.Sort(texts)
	texts = slices.Compact(texts)

	extensionNames := []string{"ext", "session", "model", "a-b", "Ext", "x\ty"}
	var extensionTexts []string
	for _, name := range append(extensionNames, "nope") {
		for _, suffix := range []string{"", " ", " x", "  x y", "\tx", "\u00a0x", " x  ", "\nx", "x"} {
			extensionTexts = append(extensionTexts, "/"+name+suffix)
		}
	}
	extensionTexts = append(extensionTexts, "ext", "/", "// ext", "/ ext")
	// Pi's submit handler trims the line before anything else sees it.
	for i, text := range extensionTexts {
		extensionTexts[i] = widthx.JSTrim(text)
	}
	texts = append(texts, extensionTexts...)
	slices.Sort(texts)
	texts = slices.Compact(texts)
	input, err := json.Marshal(map[string]any{"texts": texts, "extensionNames": extensionNames, "extensionTexts": extensionTexts})
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/submit_dispatch.mjs", pigversion.UpstreamVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var oracle struct {
		Results          [][][]any
		ExtensionResults []struct {
			IsExtensionCommand bool
			Ran                bool
			Args               *string
		}
	}
	if err := json.Unmarshal(output, &oracle); err != nil {
		t.Fatal(err)
	}

	want := oracle.Results
	failures := 0
	for i, raw := range texts {
		// What Pi did: the first recorded call.
		wantAction, wantArg := "none", ""
		if len(want[i]) > 0 {
			call := want[i][0]
			method := call[0].(string)
			switch method {
			case "handleBashCommand":
				wantAction = "bash"
			case "onInputCallback", "prompt":
				wantAction = "prompt"
			default:
				wantAction = commands[method]
				if wantAction == "" {
					t.Fatalf("unmapped Pi call %q for %q", method, raw)
				}
				if withArgument[wantAction] && len(call) > 1 && call[1] != nil {
					wantArg = fmt.Sprint(call[1])
				}
			}
		}

		// What Pig's pipeline does.
		text := widthx.JSTrim(raw)
		gotAction, gotArg := "none", ""
		last = nil
		switch {
		case text == "":
		case hiddenSubmitCommand(text) != "":
			gotAction = hiddenSubmitCommand(text)
		case isUserBashCommand(text):
			gotAction = "bash"
		case m.resolvableSlashCommand(text):
			if err := registry.Dispatch(&SlashContext{}, text, nil); err != nil {
				t.Fatal(err)
			}
			gotAction = last.command
			if withArgument[gotAction] {
				gotArg = last.args
			}
		default:
			gotAction = "prompt"
		}
		if gotAction != wantAction || gotArg != wantArg {
			if failures++; failures <= 25 {
				t.Errorf("%q: Pig %s %q, Pi %s %q", raw, gotAction, gotArg, wantAction, wantArg)
			}
		}
	}
	if failures > 25 {
		t.Errorf("%d of %d lines differ", failures, len(texts))
	}

	// Extension commands (agent-session.ts _tryExecuteExtensionCommand, interactive-mode.ts isExtensionCommand): the name is the text before the first
	// space and the handler receives everything after it, untrimmed. The registry carries the same names and a builtin named like one of them.
	var dynamic []SlashCommand
	var gotArgs *string
	for _, name := range extensionNames {
		dynamic = append(dynamic, SlashCommand{Name: name, Handler: func(_ *ExtensionContext, args string) error { gotArgs = &args; return nil }})
	}
	registry.ReplaceDynamic(dynamic)
	for i, text := range extensionTexts {
		// Pi runs a built-in first: a line the built-in chain matches never reaches the extension command.
		want := oracle.ExtensionResults[i]
		if idx := slices.Index(texts, text); len(oracle.Results[idx]) > 0 && oracle.Results[idx][0][0].(string) != "onInputCallback" {
			want.Ran = false
		}
		gotArgs = nil
		match, matched := registry.Match(text)
		viaExtension := matched && match.Dynamic != nil
		if viaExtension {
			if err := registry.Dispatch(&SlashContext{}, text, &ExtensionContext{}); err != nil {
				t.Fatal(err)
			}
		}
		if viaExtension != want.Ran || (want.Ran && (gotArgs == nil || want.Args == nil || *gotArgs != *want.Args)) {
			t.Errorf("extension line %q: Pig ran=%v args=%v, Pi ran=%v args=%v", text, viaExtension, gotArgs, want.Ran, want.Args)
		}
	}
}

// interactive-mode.ts getPathCommandArgument (/export and /import) against pinned Pi: the path argument of a trimmed line, with quoting and
// ECMAScript whitespace.
func TestPathCommandArgumentMatchesPi(t *testing.T) {
	spaces := []string{" ", "  ", "\t", "\u00a0", "\u2003", "\ufeff", "\u0085", "\u180e", "\u2028", "\n", " \t "}
	bodies := []string{"", "out.html", "a.jsonl b", "\"q p.html\"", "'q p.html'", "\"unclosed", "'unclosed x", "\"\"", "''", "\"a\"b", "x\"y z", "a\u00a0b", "a\tb c", "\"a'b\"", "'a\"b'", "~/x y", "\"\u00a0\"", "a\u0085b"}
	type probe struct {
		Text    string `json:"text"`
		Command string `json:"command"`
	}
	var probes []probe
	for _, command := range []string{"/export", "/import"} {
		probes = append(probes, probe{command, command}, probe{command + " ", command}, probe{command + "x", command})
		for _, space := range spaces {
			for _, body := range bodies {
				probes = append(probes, probe{command + space + body, command}, probe{command + " " + space + body, command}, probe{command + " " + body + space + "tail", command})
			}
		}
	}
	for i := range probes {
		probes[i].Text = widthx.JSTrim(probes[i].Text)
	}
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/path_command_argument.mjs", pigversion.UpstreamVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var want []*string
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatal(err)
	}
	registry := NewSlashRegistry()
	failures := 0
	for i, p := range probes {
		// Pig reaches the argument through the registry's match; a line that is not the command has none.
		got := ""
		if match, ok := registry.Match(p.Text); ok && match.Builtin != nil && "/"+match.Builtin.Name == p.Command {
			got = pathCommandArgument(match.Args)
		}
		// Go's string cannot tell an empty quoted path from an absent one; both mean "no path" to /export and /import.
		wantValue := ""
		if want[i] != nil {
			wantValue = *want[i]
		}
		if got != wantValue {
			if failures++; failures <= 15 {
				t.Errorf("%q: Pig %q, Pi %q", p.Text, got, wantValue)
			}
		}
	}
	if failures > 15 {
		t.Errorf("%d of %d probes differ", failures, len(probes))
	}
}

// Pi runs /reload and /fork only for their exact text; PiG's approved additive arguments are /reload --explain (D21) and, for Session.DispatchSlash,
// the entry id of /fork. A mutant that drops either additive rule, or that lets /fork take an argument interactively, fails here.
func TestSlashAdditiveArgumentsStayOutsidePiLines(t *testing.T) {
	registry := NewSlashRegistry()
	for _, tc := range []struct {
		line     string
		headless bool
		builtin  string
		args     string
	}{
		{"/reload", false, "reload", ""},
		{"/reload --explain", false, "reload", "--explain"},
		{"/reload explain", false, "reload", "explain"},
		{"/reload now", false, "", ""},
		{"/fork", false, "fork", ""},
		{"/fork abc123", false, "", ""},
		{"/fork abc123", true, "fork", "abc123"},
		{"/fork   ", true, "fork", ""},
		{"/session x", true, "", ""},
	} {
		match, ok := registry.match(tc.line, tc.headless)
		got := ""
		if ok && match.Builtin != nil {
			got = match.Builtin.Name
		}
		if got != tc.builtin || (got != "" && match.Args != tc.args) {
			t.Errorf("match(%q, headless %v) = builtin %q args %q, want %q %q", tc.line, tc.headless, got, match.Args, tc.builtin, tc.args)
		}
	}
}
