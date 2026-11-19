//go:build !pig_strip_node_extensions

package coding

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func sdkSiblingSource(command string) string {
	return fmt.Sprintf("export default function (pi) {\n  pi.registerCommand(%q, {description: \"sibling\", handler: async () => {}});\n}\n", command)
}

func sdkExtensionCommands(session *Session) []string {
	var names []string
	for _, command := range session.SlashCommands() {
		if command.Source == "extension" {
			names = append(names, command.Name)
		}
	}
	slices.Sort(names)
	return names
}

// agent-session.ts reload() runs resourceLoader.reload(), which discovers the extension files again, for a session an SDK embedder created over a DefaultResourceLoader: an extension file removed, added or changed before Session.Reload is gone, loaded, or loaded as it now is.
func TestSessionReloadRediscoversTheExtensionsOnDisk(t *testing.T) {
	root := t.TempDir()
	cwd, agentDir := filepath.Join(root, "project"), filepath.Join(root, "agent")
	extensions := filepath.Join(agentDir, "extensions")
	for path, content := range map[string]string{
		filepath.Join(extensions, "keep.js"):   sdkSiblingSource("keep"),
		filepath.Join(extensions, "gone.js"):   sdkSiblingSource("gone"),
		filepath.Join(extensions, "edited.js"): sdkSiblingSource("edited-before"),
	} {
		writeExtensionFiles(t, map[string]string{path: content})
	}
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	trusted := true
	services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: cwd, AgentDir: agentDir, ProjectTrusted: &trusted})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(services.Close)
	result, err := CreateAgentSessionFromServices(CreateAgentSessionFromServicesOptions{Services: services, Model: fakeModel()})
	if err != nil {
		t.Fatal(err)
	}
	session := result.Session
	t.Cleanup(func() { _ = session.Close() })
	if got, want := sdkExtensionCommands(session), []string{"edited-before", "gone", "keep"}; !slices.Equal(got, want) {
		t.Fatalf("commands before the reload = %q, want %q", got, want)
	}
	if err := os.Remove(filepath.Join(extensions, "gone.js")); err != nil {
		t.Fatal(err)
	}
	writeExtensionFiles(t, map[string]string{
		filepath.Join(extensions, "added.js"):  sdkSiblingSource("added"),
		filepath.Join(extensions, "edited.js"): sdkSiblingSource("edited-after"),
	})
	if err := session.Reload(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got, want := sdkExtensionCommands(session), []string{"added", "edited-after", "keep"}; !slices.Equal(got, want) {
		t.Fatalf("commands after the reload = %q, want %q", got, want)
	}
}
