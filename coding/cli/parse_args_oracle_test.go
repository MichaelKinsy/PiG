package cli

import (
	"bytes"
	"encoding/json"
	"math/rand/v2"
	"os/exec"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// canonicalArgs is Pi's Args as parseArgs returns it: a property is present only when parseArgs set it.
func canonicalArgs(a Args) map[string]any {
	out := map[string]any{"messages": nonNil(a.Messages), "fileArgs": nonNil(a.FileArgs)}
	str := func(key, value string) {
		if value != "" {
			out[key] = value
		}
	}
	flag := func(key string, value bool) {
		if value {
			out[key] = true
		}
	}
	list := func(key string, value []string) {
		if value != nil {
			out[key] = value
		}
	}
	str("provider", a.Provider)
	str("model", a.Model)
	str("apiKey", a.APIKey)
	if a.systemPromptSet {
		out["systemPrompt"] = a.SystemPrompt
	}
	list("appendSystemPrompt", a.AppendSystemPrompt)
	str("thinking", a.Thinking)
	flag("continue", a.Continue)
	flag("resume", a.Resume)
	flag("help", a.Help)
	flag("version", a.Version)
	if a.modeSet || a.Mode != "text" {
		out["mode"] = a.Mode
	}
	if a.NameSet {
		out["name"] = a.Name
	}
	flag("noSession", a.NoSession)
	str("session", a.Session)
	str("sessionId", a.SessionID)
	str("fork", a.Fork)
	str("sessionDir", a.SessionDir)
	list("models", a.Models)
	list("tools", a.Tools)
	list("excludeTools", a.ExcludeTools)
	flag("noTools", a.NoTools)
	flag("noBuiltinTools", a.NoBuiltinTools)
	list("extensions", a.Extensions)
	flag("noExtensions", a.NoExtensions)
	flag("noMcp", a.NoMcp)
	flag("print", a.Print)
	str("export", a.Export)
	flag("noSkills", a.NoSkills)
	list("skills", a.Skills)
	list("promptTemplates", a.PromptTemplates)
	flag("noPromptTemplates", a.NoPromptTemplates)
	list("themes", a.Themes)
	if a.UseTheme != nil {
		out["useTheme"] = *a.UseTheme
	}
	flag("noThemes", a.NoThemes)
	flag("noContextFiles", a.NoContextFiles)
	switch {
	case a.ListModelsAll:
		out["listModels"] = true
	case a.ListModels != "":
		out["listModels"] = a.ListModels
	}
	flag("offline", a.Offline)
	str("tuiMode", a.TuiMode)
	flag("verbose", a.Verbose)
	if a.ProjectTrustOverride != nil {
		out["projectTrustOverride"] = *a.ProjectTrustOverride
	}
	flags := make([]any, 0, len(a.UnknownFlagOrder))
	for _, name := range a.UnknownFlagOrder {
		flags = append(flags, []any{name, a.UnknownFlags[name]})
	}
	out["unknownFlags"] = flags
	diagnostics := make([]any, 0, len(a.Diagnostics))
	for _, d := range a.Diagnostics {
		diagnostics = append(diagnostics, map[string]any{"type": d.Type, "message": d.Message})
	}
	out["diagnostics"] = diagnostics
	return out
}

func nonNil(list []string) []string {
	if list == nil {
		return []string{}
	}
	return list
}

// cli/args.ts parseArgs against pinned Pi over generated argument vectors: every flag with every kind of value (missing, empty, a flag-like
// value, an @file, a ---prefixed value), so each option's value-consumption rule and diagnostic is compared.
func TestParseArgsMatchesPi(t *testing.T) {
	flags := []string{"--help", "-h", "--version", "-v", "--mode", "--continue", "-c", "--resume", "-r", "--provider", "--model", "--api-key", "--system-prompt",
		"--append-system-prompt", "--name", "-n", "--no-session", "--session", "--session-id", "--fork", "--session-dir", "--models", "--no-tools", "-nt",
		"--no-builtin-tools", "-nbt", "--tools", "-t", "--exclude-tools", "-xt", "--thinking", "--print", "-p", "--export", "--extension", "-e",
		"--no-extensions", "-ne", "--no-mcp", "--skill", "--prompt-template", "--theme", "--use-theme", "--no-skills", "-ns", "--no-prompt-templates", "-np",
		"--no-themes", "--no-context-files", "-nc", "--list-models", "--tui-mode", "--verbose", "--approve", "-a", "--no-approve", "-na", "--offline",
		"--", "--unknown", "--unknown=v", "--x=", "-z", "-"}
	values := []string{"x", "openai", "off", "high", "max", "bogus", "a,b", " a , ,b ", "read", "+grep", "-grep", "read,+grep", "-x", "--y", "@f", "---z", "text", "json", "rpc",
		"regular", "fullscreen", "", "  ", "msg", "two words", "@", "a=b"}
	r := rand.New(rand.NewPCG(1, 2))
	var vectors [][]string
	for _, flag := range flags {
		vectors = append(vectors, []string{flag})
		for _, value := range values {
			vectors = append(vectors, []string{flag, value}, []string{value, flag}, []string{flag, value, "tail"})
		}
	}
	vocabulary := append(append([]string{}, flags...), values...)
	for range 6000 {
		n := 2 + r.IntN(5)
		vector := make([]string, n)
		for i := range vector {
			vector[i] = vocabulary[r.IntN(len(vocabulary))]
		}
		vectors = append(vectors, vector)
	}
	input, err := json.Marshal(vectors)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/parse_args.mjs", pigversion.UpstreamVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []map[string]any
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	// A Go string field cannot tell an empty value from an omitted one, and Pi's callers read these options by truthiness, so an empty
	// value is compared as absent. name, systemPrompt and useTheme have explicit presence in Args and are compared exactly.
	for _, want := range expected {
		for _, key := range []string{"provider", "model", "apiKey", "session", "sessionId", "fork", "sessionDir", "export", "thinking", "tuiMode"} {
			if want[key] == "" {
				delete(want, key)
			}
		}
	}
	// --list-models with an empty value searches for "", which filters nothing, as no pattern does (list-models.ts `if (searchPattern)`).
	for _, want := range expected {
		if want["listModels"] == "" {
			want["listModels"] = true
		}
	}
	failures := 0
	for i, vector := range vectors {
		raw, err := json.Marshal(canonicalArgs(parseArgs(vector)))
		if err != nil {
			t.Fatal(err)
		}
		var got map[string]any
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, expected[i]) {
			if failures++; failures <= 8 {
				t.Errorf("parseArgs(%q):\n  Pig %v\n  Pi  %v", vector, got, expected[i])
			}
		}
	}
	if failures > 8 {
		t.Errorf("%d of %d vectors differ from Pi", failures, len(vectors))
	}
}
