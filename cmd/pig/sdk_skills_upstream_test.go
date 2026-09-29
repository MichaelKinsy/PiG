package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// Ports packages/coding-agent/test/sdk-skills.test.ts through the production subprocess loader. The child session owns its resources; the enclosing host must neither substitute its catalog nor rediscover a supplied empty loader.
func TestUpstreamSDKSkillsSubprocess(t *testing.T) {
	for _, isolation := range []string{"isolated", "shared-ok"} {
		for _, scenario := range []string{"default discovery", "empty supplied loader", "custom supplied skill"} {
			t.Run(isolation+"/"+scenario, func(t *testing.T) {
				root := t.TempDir()
				t.Setenv("HOME", filepath.Join(root, "home"))
				t.Setenv("PIG_HOME", filepath.Join(root, "home", ".pig"))
				childDir := filepath.Join(root, "child")
				writeResourceLoaderFixture(t, filepath.Join(childDir, "skills", "test-skill", "SKILL.md"), "---\nname: test-skill\ndescription: A test skill for SDK tests.\n---\n\n# Test Skill\n\nThis is a test skill.\n")
				cwd, agentDir := filepath.Join(root, "project"), filepath.Join(root, "agent")
				for _, dir := range []string{cwd, agentDir} {
					if err := os.MkdirAll(dir, 0o755); err != nil {
						t.Fatal(err)
					}
				}
				entry := filepath.Join(root, "sdk-skills.ts")
				writeResourceLoaderFixture(t, entry, fmt.Sprintf(`import { createAgentSession, createExtensionRuntime, createSyntheticSourceInfo, SessionManager } from "@earendil-works/pi-coding-agent";
export default async function(pi) {
 const root = %q, scenario = %q;
 const customSkill = {name: "custom-skill", description: "A custom skill", filePath: "/fake/path/SKILL.md", baseDir: "/fake/path", sourceInfo: createSyntheticSourceInfo("/fake/path/SKILL.md", {source: "sdk"}), disableModelInvocation: false};
 const suppliedSkills = scenario === "custom supplied skill" ? [customSkill] : [];
 const resourceLoader = scenario === "default discovery" ? undefined : {
  getExtensions: () => ({extensions: [], errors: [], runtime: createExtensionRuntime()}),
  getSkills: () => ({skills: suppliedSkills, diagnostics: []}),
  getPrompts: () => ({prompts: [], diagnostics: []}), getThemes: () => ({themes: [], diagnostics: []}),
  getAgentsFiles: () => ({agentsFiles: []}), getSystemPrompt: () => undefined,
  getSystemPromptSource: () => undefined, getAppendSystemPrompt: () => [], getAppendSystemPromptSources: () => [],
  extendResources() {}, async reload() {},
 };
 const {session} = await createAgentSession({cwd: root, agentDir: root, sessionManager: SessionManager.inMemory(), resourceLoader});
 try {
  const result = session.resourceLoader.getSkills();
  pi.registerCommand("inspect-skills", {description: JSON.stringify(result), handler: async () => {}});
 } finally { session.dispose(); }
}`, childDir, scenario))
				configs := collectExtensionConfigs(cwd, agentDir, codingagent.NewSettingsManager(cwd, agentDir), CLIFlags{Extensions: []string{entry}}, nil)
				for i := range configs {
					configs[i].Isolation = isolation
				}
				exts, host, _, errs := loadSubprocessExtensions(t.Context(), cwd, extension.ModePrint, nil, configs, nil, nil)
				if host != nil {
					t.Cleanup(func() { host.Shutdown("test complete") })
				}
				if len(errs) != 0 || len(exts) != 1 {
					t.Fatalf("load SDK skills fixture: extensions=%#v errors=%v", exts, errs)
				}
				command, ok := exts[0].Commands["inspect-skills"]
				if !ok {
					t.Fatal("SDK skills result was not registered")
				}
				var result struct {
					Skills      []map[string]any `json:"skills"`
					Diagnostics []any            `json:"diagnostics"`
				}
				if err := json.Unmarshal([]byte(command.Description), &result); err != nil {
					t.Fatal(err)
				}
				if scenario == "default discovery" {
					found := false
					for _, skill := range result.Skills {
						found = found || skill["name"] == "test-skill"
					}
					if len(result.Skills) == 0 || !found {
						t.Fatalf("default session skills = %#v", result.Skills)
					}
					return
				}
				want := []map[string]any{}
				if scenario == "custom supplied skill" {
					want = append(want, map[string]any{
						"name": "custom-skill", "description": "A custom skill", "filePath": "/fake/path/SKILL.md", "baseDir": "/fake/path", "disableModelInvocation": false,
						"sourceInfo": map[string]any{"path": "/fake/path/SKILL.md", "source": "sdk", "scope": "temporary", "origin": "top-level"},
					})
				}
				if !reflect.DeepEqual(result.Skills, want) || !reflect.DeepEqual(result.Diagnostics, []any{}) {
					t.Fatalf("supplied skills result = %s; want skills=%#v, diagnostics=[]", command.Description, want)
				}
			})
		}
	}
}
