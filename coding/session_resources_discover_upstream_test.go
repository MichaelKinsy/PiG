package coding

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// Ports packages/coding-agent/src/core/agent-session.ts:3267-3290 extendResourcesFromExtensions and :3292-3314 buildExtensionResourcePaths:
// after session_start, bindExtensions asks the resources_discover handlers for paths, extends the Session's resource loader with them
// (source "extension:<name>", scope "temporary", origin "top-level", baseDir the extension's directory) and rebuilds the system prompt.
func TestBindExtensionsExtendsTheResourceLoaderFromResourcesDiscover(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, "discovered", "lint")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: lint\ndescription: Lints the tree.\n---\nRun the linter.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	loader := NewDefaultResourceLoader(DefaultResourceLoaderOptions{CWD: root, AgentDir: filepath.Join(root, "agent"), NoSkills: true, NoPromptTemplates: true, NoThemes: true, NoContextFiles: true})
	if err := loader.Reload(); err != nil {
		t.Fatal(err)
	}
	extensionPath := filepath.Join(root, "ext", "finder.ts")
	var seen extension.ResourcesDiscoverEvent
	runner := inproc.NewRunner([]extension.Extension{{Path: extensionPath, ResolvedPath: extensionPath, Handlers: map[string][]extension.HandlerFn{
		"resources_discover": {func(args ...any) (any, error) {
			seen = args[0].(extension.ResourcesDiscoverEvent)
			return &extension.ResourcesDiscoverResult{SkillPaths: []string{skillDir}}, nil
		}},
	}}}, root)
	session, err := NewSession(newTestServices(t), SessionOptions{NoSession: true, Runner: runner, ResourceLoader: loader})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	if len(session.ResourceLoader().GetSkills().Skills) != 0 {
		t.Fatalf("skills before binding: %+v", session.ResourceLoader().GetSkills().Skills)
	}
	if err := session.BindExtensions(t.Context(), ExtensionBindings{}); err != nil {
		t.Fatal(err)
	}
	if seen.Type != "resources_discover" || seen.Reason != "startup" || seen.Cwd != session.CWD() {
		t.Fatalf("resources_discover event = %+v", seen)
	}
	skills := session.ResourceLoader().GetSkills().Skills
	if len(skills) != 1 || skills[0].Name != "lint" {
		t.Fatalf("skills = %+v, want the discovered lint skill", skills)
	}
	info := skills[0].SourceInfo
	if info.Source != "extension:finder" || info.Scope != "temporary" || info.Origin != "top-level" || info.BaseDir != filepath.Dir(extensionPath) {
		t.Fatalf("source info = %+v", info)
	}
	if prompt := session.SystemPrompt(); !strings.Contains(prompt, "lint") || !strings.Contains(prompt, "Lints the tree.") {
		t.Fatalf("the rebuilt system prompt does not list the discovered skill:\n%s", prompt)
	}
}
