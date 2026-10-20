//go:build linux

package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

// The four extension shapes of one real failure all load from the agent directory. Startup names every one of them with the one command (nothing is
// left out because the go command prints ten errors or a recorded failure names none), and one yes upgrades all four, so the session starts.
func TestStartupOffersEveryDriftedExtensionAndOneYesUpgradesThemAll(t *testing.T) {
	binary := buildPigBinaryForSignalTest(t)
	t.Run("print mode names all four", func(t *testing.T) {
		home := t.TempDir()
		agentDir := filepath.Join(home, "agent")
		dirs := writeOldShapeExtensions(t, filepath.Join(agentDir, "extensions"))
		stdout, stderr, code := runDriftStartup(t, binary, home, agentDir, nil, "-p", "hello")
		if code != 1 || stdout != "" {
			t.Fatalf("exit=%d stdout=%q\n%s", code, stdout, stderr)
		}
		for _, name := range []string{"ask", "context-info", "subagent", "web-search"} {
			if !strings.Contains(stderr, `Extension "`+name+`" was written for an older SDK:`) {
				t.Errorf("stderr lacks %s:\n%s", name, stderr)
			}
		}
		if !strings.Contains(stderr, "Run: pig extension upgrade "+strings.Join(dirs, " ")+"\n") {
			t.Errorf("stderr lacks the one command for all four:\n%s", stderr)
		}
	})
	t.Run("extensionsAutoUpgrade upgrades all four and the run finishes", func(t *testing.T) {
		home := t.TempDir()
		agentDir := filepath.Join(home, "agent")
		dirs := writeOldShapeExtensions(t, filepath.Join(agentDir, "extensions"))
		writeStartupFixtureFile(t, filepath.Join(agentDir, "settings.json"), `{"extensionsAutoUpgrade":true}`)
		stdout, stderr, code := runDriftStartup(t, binary, home, agentDir, nil, "-p", "reply with exactly: ready")
		if code != 0 || strings.TrimSpace(stdout) != "ready" || strings.Contains(stderr, "Failed to load extension") {
			t.Fatalf("exit=%d stdout=%q\n%s", code, stdout, stderr)
		}
		if lines := strings.Count(stderr, "upgraded extension "); lines != 4 {
			t.Fatalf("want one line for each of four extensions:\n%s", stderr)
		}
		if !strings.Contains(readFile(t, filepath.Join(dirs[2], "extension.go")), "pigUpgradeIsIdle(ctx)") {
			t.Fatal("the subagent shape was not upgraded")
		}
	})
	t.Run("one yes at the prompt", func(t *testing.T) {
		home := t.TempDir()
		agentDir := filepath.Join(home, "agent")
		dirs := writeOldShapeExtensions(t, filepath.Join(agentDir, "extensions"))
		session := startSpriteSession(t, binary, filepath.Join(home, "pig"), agentDir)
		session.await("asking", 0, "Run pig extension upgrade ")
		session.await("the question", 0, "now? [y/N] ")
		mark := session.send("y\r")
		session.await("the session starts", mark, "[Extensions]")
		for _, dir := range dirs {
			if strings.Contains(readFile(t, filepath.Join(dir, "extension.go")), "ctx.ConfigHome()") && !strings.Contains(readFile(t, filepath.Join(dir, "extension.go")), "configHome, err := ctx.ConfigHome()") {
				t.Errorf("%s was not upgraded", dir)
			}
		}
	})
}
