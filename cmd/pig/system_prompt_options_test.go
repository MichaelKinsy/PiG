package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi agent-session.ts:1371-1394,2965,3125 rebuilds from the registry and loaded resources after resources_discover; all modes bind the same base options to command contexts.
func TestSystemPromptOptionsThroughHeadlessStartup(t *testing.T) {
	t.Parallel()
	binary := buildPigBinaryForSignalTest(t)
	for _, tc := range []struct{ mode, custom string }{{"print", "CUSTOM"}, {"json", "CUSTOM"}, {"rpc", "CUSTOM"}, {"print", ""}, {"json", ""}, {"rpc", ""}} {
		t.Run(tc.mode+"/custom="+tc.custom, func(t *testing.T) {
			mode := tc.mode
			home := t.TempDir()
			cwd, agentDir := filepath.Join(home, "project"), filepath.Join(home, "agent")
			contextPath := filepath.Join(cwd, "AGENTS.md")
			writeStartupFixtureFile(t, contextPath, "PROJECT RULES\n")
			skillPath := filepath.Join(home, "review", "SKILL.md")
			writeStartupFixtureFile(t, skillPath, "---\nname: review\ndescription: Review code\n---\nReview instructions\n")
			explicitPath := filepath.Join(home, "explicit", "SKILL.md")
			writeStartupFixtureFile(t, explicitPath, "---\nname: explicit\ndescription: Explicit skill\n---\nExplicit instructions\n")
			artifact, entry := filepath.Join(home, "options.json"), filepath.Join(home, "options.mjs")
			writeStartupFixtureFile(t, entry, `import {writeFileSync} from "node:fs";
export default function(pi) {
 pi.on("resources_discover", () => ({skillPaths:[`+strconv.Quote(skillPath)+`]}));
 pi.registerCommand("inspect-options", {handler: async (_args,ctx) => {
  writeFileSync(`+strconv.Quote(artifact)+`, JSON.stringify(ctx.getSystemPromptOptions()));
 }});
}`)
			customSource := tc.custom
			if customSource == "" {
				customSource = filepath.Join(agentDir, "SYSTEM.md")
				writeStartupFixtureFile(t, customSource, "")
			}
			args := []string{"--no-session", "--no-skills", "--skill", explicitPath, "--system-prompt", customSource, "--append-system-prompt", "APPENDED", "-e", entry}
			input := ""
			switch mode {
			case "print":
				args = append(args, "-p", "/inspect-options")
			case "json":
				args = append(args, "--mode", "json", "/inspect-options")
			case "rpc":
				args = append(args, "--mode", "rpc")
				input = "{\"id\":\"inspect\",\"type\":\"prompt\",\"message\":\"/inspect-options\"}\n"
			}
			run := runPigStartup(t, binary, home, agentDir, cwd, input, args...)
			if run.err != nil {
				t.Fatalf("%v\nstdout: %s\nstderr: %s", run.err, run.stdout, run.stderr)
			}
			if mode == "rpc" && !strings.Contains(run.stdout, `"success":true`) {
				t.Fatalf("RPC command did not complete: %s", run.stdout)
			}
			data, err := os.ReadFile(artifact)
			if err != nil {
				t.Fatal(err)
			}
			var got extension.BuildSystemPromptOptions
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatal(err)
			}
			if got.CustomPrompt != tc.custom || !got.CustomPromptSet || got.AppendSystemPrompt != "APPENDED" || got.Cwd != canonicalTestPath(t, cwd) {
				t.Fatalf("prompt inputs = %s", data)
			}
			if !reflect.DeepEqual(got.SelectedTools, []string{"read", "bash", "edit", "write"}) || got.ToolSnippets["grep"] != "Search file contents for patterns (respects .gitignore)" || !reflect.DeepEqual(got.ToolGuidelines["read"], []string{"Use read to examine files instead of cat or sed."}) {
				t.Fatalf("registry inputs = %s", data)
			}
			if !reflect.DeepEqual(got.ContextFiles, []extension.SystemPromptContextFile{{Path: canonicalTestPath(t, contextPath), Content: "PROJECT RULES\n"}}) {
				t.Fatalf("context files = %+v", got.ContextFiles)
			}
			if len(got.Skills) != 2 || got.Skills[0].Name != "explicit" || got.Skills[1].Name != "review" {
				t.Fatalf("skill order = %+v", got.Skills)
			}
			for i, want := range []extension.SystemPromptSkill{
				// A standalone --skill path retains Pi's local/temporary provenance; only CLI extension-source metadata is stamped cli (resource-loader.ts:436-445, skills.ts:150-154).
				{Name: "explicit", Description: "Explicit skill", BaseDir: filepath.Dir(explicitPath), FilePath: explicitPath, SourceInfo: map[string]any{"path": explicitPath, "source": "local", "scope": "temporary", "origin": "top-level", "baseDir": filepath.Dir(explicitPath)}},
				{Name: "review", Description: "Review code", BaseDir: filepath.Dir(skillPath), FilePath: skillPath, SourceInfo: map[string]any{"path": skillPath, "source": "extension:options.mjs", "scope": "temporary", "origin": "top-level", "baseDir": home}},
			} {
				if !reflect.DeepEqual(got.Skills[i], want) {
					t.Fatalf("skill = %#v, want %#v", got.Skills[i], want)
				}
			}
		})
	}
}

// Pi agent-session.ts:1372-1392 passes the valid active names, so --no-tools reports selectedTools [] rather than system-prompt.ts:57's default list.
func TestSystemPromptRebuilderKeepsEmptyToolSelection(t *testing.T) {
	_, options := systemPromptRebuilder(t.TempDir(), t.TempDir(), true, CLIFlags{}, []string{})(nil, nil)
	if got := extension.NormalizeBuildSystemPromptOptions(options).SelectedTools; got == nil || len(got) != 0 {
		t.Fatalf("selectedTools = %#v, want []", got)
	}
}
