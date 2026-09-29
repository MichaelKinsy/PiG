//go:build parity

package runner

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// Pi 0.87.1 core/system-prompt.ts:147-160 supplies these sentences and destinations. Only D2/D22 identity spellings differ.
const identityDocs = `<docs>
Pi documentation (read only when the user asks about pi itself, its SDK, extensions, themes, skills, or TUI):
- Main documentation: /oracle/package/README.md
- Additional docs: /oracle/package/docs
- Examples: /oracle/package/examples (extensions, custom tools, SDK)
- When reading pi docs or examples, resolve docs/... under Additional docs and examples/... under Examples, not the current working directory
- When asked about: extensions (docs/extensions.md, examples/extensions/), themes (docs/themes.md), skills (docs/skills.md), prompt templates (docs/prompt-templates.md), TUI components (docs/tui.md), keybindings (docs/keybindings.md), SDK integrations (docs/sdk.md), custom providers (docs/custom-provider.md), adding models (docs/models.md), pi packages (docs/packages.md), environment variables (docs/environment-variables.md)
- When working on pi topics, read the docs and examples, and follow .md cross-references before implementing
- Always read pi .md files completely and follow links to related docs (e.g., tui.md for TUI API details)
</docs>`

func statsIdentityRecords(t *testing.T) (Result, Result) {
	t.Helper()
	record := func(docs, preamble, cwd, file, id string) string {
		rows := []any{
			map[string]any{"type": "message_start", "message": map[string]any{"role": "system", "sections": map[string]any{"docs": docs, "preamble": preamble, "cwd": "<cwd>\n" + cwd + "\n</cwd>", "project_context": "<project_instructions path=\"" + cwd + "/AGENTS.md\">Keep this instruction.</project_instructions>", "rules": "Use read to examine files."}}},
			map[string]any{"type": "response", "command": "get_session_stats", "data": map[string]any{"sessionFile": file, "sessionId": id, "toolCalls": 1, "contextUsage": map[string]any{"tokens": 1484, "percent": 1.159375}}},
		}
		var output strings.Builder
		for _, row := range rows {
			data, err := json.Marshal(row)
			if err != nil {
				t.Fatal(err)
			}
			output.Write(data)
			output.WriteByte('\n')
		}
		return output.String()
	}
	pigDocs := strings.NewReplacer("Pi documentation", "PiG documentation", "about pi itself", "about pig itself", "reading pi docs", "reading pig docs", "pi packages (", "pig packages (", "on pi topics", "on pig topics", "read pi .md", "read pig .md", "/oracle/package/README.md", "/host/docs/README.md", "/oracle/package/docs", "/host/docs", "/oracle/package/examples", "https://github.com/MichaelKinsy/PiG/tree/main/examples").Replace(identityDocs)
	const pigID = "00000000-0000-7000-8000-000000000001"
	const piID = "00000000-0000-7000-8000-000000000002"
	pig := Result{Output: record(pigDocs, "You are operating inside pig, a coding agent harness.", "/host/cwd", "/host/temp/sessions/2026-09-27T07-56-30-167Z_"+pigID+".jsonl", pigID), IdentityRoots: resultIdentityRoots("/host/cwd", "/host/temp", []string{"PIG_HOME=/host"})}
	pi := Result{Output: record(identityDocs, "You are operating inside pi, a coding agent harness.", "/oracle/cwd", "/oracle/temp/sessions/2026-09-27T07-56-30-375Z_"+piID+".jsonl", piID), IdentityRoots: resultIdentityRoots("/oracle/cwd", "/oracle/temp", []string{"PI_PACKAGE_DIR=/oracle/package"})}
	return pig, pi
}

func TestDocumentationDestinationsRequireExplicitRootSelection(t *testing.T) {
	pig := Result{Output: `{"path":"https://github.com/MichaelKinsy/PiG/tree/main/examples"}`, IdentityRoots: resultIdentityRoots("", "", []string{"PIG_HOME=/host"})}
	pi := Result{Output: `{"path":"/oracle/package/examples"}`, IdentityRoots: resultIdentityRoots("", "", []string{"PI_PACKAGE_DIR=/oracle/package"})}
	rules := []JSONAliasRule{{Paths: []string{"/*/path"}, Kind: "path", Reason: "Standard path roots only."}}
	if compareJSONResults(pig, pi, rules) == nil {
		t.Fatal("new documentation destination implicitly broadened an existing path alias")
	}
	rules[0].Roots = []string{"examples"}
	if err := compareJSONResults(pig, pi, rules); err != nil {
		t.Fatal(err)
	}
}

func TestSessionStatsAliasesRetainInitialShellUpdateShape(t *testing.T) {
	sc, err := LoadScenario(filepath.Join("..", "scenarios", "rpc", "26-rpc-session-stats-context-estimate.toml"))
	if err != nil {
		t.Fatal(err)
	}
	// Pi 0.87.1 core/tools/bash.ts:301-302 emits an empty array before output exists. This is not a product-identity difference.
	pig := Result{Output: `{"type":"tool_execution_update","partialResult":{"content":[{"type":"text","text":""}]}}`}
	pi := Result{Output: `{"type":"tool_execution_update","partialResult":{"content":[]}}`}
	if err := compareJSONResults(pig, pi, sc.Assert.JSONAliases); err == nil {
		t.Fatal("identity aliases hid the initial shell update shape")
	}
}

func TestLiteralAliasesAreScopedAndDoNotCascade(t *testing.T) {
	rules := []JSONAliasRule{
		{Paths: []string{"/*/docs"}, Kind: "literal", Pig: "Pig docs", Pi: "Pi docs", Reason: "D2 heading."},
		{Paths: []string{"/*/docs"}, Kind: "literal", Pig: "Pi docs", Pi: "Other docs", Reason: "Distinct identity pair must not cascade."},
	}
	for _, tc := range []struct {
		pig, pi string
		equal   bool
	}{
		{`{"docs":"Pig docs"}`, `{"docs":"Pi docs"}`, true},
		{`{"docs":"Pig docs"}`, `{"docs":"Other docs"}`, false},
		{`{"content":"Pig docs"}`, `{"content":"Pi docs"}`, false},
		{`{"docs":"Pig docs Pig docs"}`, `{"docs":"Pi docs"}`, false},
		{`{"docs":"Pig docs"}`, `{"docs":"literal:0"}`, false},
	} {
		err := compareJSONResults(Result{Output: tc.pig}, Result{Output: tc.pi}, rules)
		if (err == nil) != tc.equal {
			t.Fatalf("%s / %s: %v", tc.pig, tc.pi, err)
		}
	}
	rules[0].Pig = ""
	if compareJSONResults(Result{Output: `{}`}, Result{Output: `{}`}, rules) == nil {
		t.Fatal("empty literal alias accepted")
	}
}

func TestSessionStatsIdentityAliasesPreserveSystemSections(t *testing.T) {
	sc, err := LoadScenario(filepath.Join("..", "scenarios", "rpc", "26-rpc-session-stats-context-estimate.toml"))
	if err != nil {
		t.Fatal(err)
	}
	pig, pi := statsIdentityRecords(t)
	if err := compareJSONResults(pig, pi, sc.Assert.JSONAliases); err != nil {
		t.Fatalf("approved identity differences remain: %v", err)
	}
	for _, tc := range []struct{ name, old, new string }{
		{"instruction", "read the docs and examples", "ignore the docs and examples"},
		{"whitespace", "skills, or TUI", "skills,  or TUI"},
		{"documentation filename", "docs/tui.md", "docs/other.md"},
		{"README filename", "/host/docs/README.md", "/host/docs/OTHER.md"},
		{"examples destination suffix", "/tree/main/examples (", "/tree/main/examples/wrong ("},
		{"added sentence", `\u003c/docs\u003e`, `Do not follow links.\u003c/docs\u003e`},
		{"missing instruction", "- Always read pig .md files completely", ""},
		{"unknown identity", "inside pig,", "inside another, "},
		{"unrelated product mention", "Keep this instruction.", "Keep pig identity literal."},
		{"context instruction", "Keep this instruction.", "Lose this instruction."},
		{"context path suffix", "/host/cwd/AGENTS.md", "/host/cwd/OTHER.md"},
		{"other section", "Use read to examine files.", "Use pig packages (docs/packages.md) instead."},
		{"statistic", `"tokens":1484`, `"tokens":0`},
		{"directory suffix", "/host/temp/sessions/", "/host/temp/wrong/"},
		{"filename extension", ".jsonl", ".txt"},
		{"filename timestamp", "2026-09-27T07-56-30-167Z", "2026-09-27T07-56-30-167999Z"},
		{"invalid filename date", "2026-09-27T07-56-30-167Z", "2026-99-27T07-56-30-167Z"},
		{"filename ID reference", "167Z_00000000-0000-7000-8000-000000000001", "167Z_00000000-0000-7000-8000-000000000003"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := pig
			changed.Output = strings.ReplaceAll(pig.Output, tc.old, tc.new)
			if changed.Output == pig.Output {
				t.Fatal("mutation did not reach fixture")
			}
			if err := compareJSONResults(changed, pi, sc.Assert.JSONAliases); err == nil {
				t.Fatal("identity aliases hid substantive drift")
			}
		})
	}
}
