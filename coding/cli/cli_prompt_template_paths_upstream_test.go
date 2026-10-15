package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// upstream: main.ts:728 resolves the --prompt-template paths once against the launch directory, without trimming them, and
// resource-loader.ts reload trims and checks each local one (isLocalPath) for existence (:615-626, :1035-1036); npm: and git: sources
// are not checked.
func TestAdditionalPromptTemplatePathsAreTheLaunchResolvedLocalOnes(t *testing.T) {
	launch := t.TempDir()
	abs := filepath.Join(t.TempDir(), "abs.md")
	flags, err := resolveCLIResourceFlags(Args{PromptTemplates: []string{"nosuch.md", " lead.md", "npm:pkg", "git:github.com/user/repo", abs}}, launch)
	if err != nil {
		t.Fatal(err)
	}
	got := additionalPromptTemplatePaths(flags)
	if want := []string{filepath.Join(launch, "nosuch.md"), filepath.Join(launch, " lead.md"), abs}; !slices.Equal(got, want) {
		t.Fatalf("additionalPromptTemplatePaths = %q, want %q", got, want)
	}
}

// Pi's resource loader reports a missing --prompt-template path each time a Session's resources load (resource-loader.ts:615-626): the
// interactive startup listing shows it, and so does the listing after /new builds the replacement Session.
func TestInteractiveListingsReportAMissingPromptTemplatePath(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and starts the pig binary")
	}
	t.Parallel()
	home, dir := t.TempDir(), t.TempDir()
	seedFirstRunDone(t, filepath.Join(home, "agent"))
	writeStartupFixtureFile(t, filepath.Join(home, "agent", "settings.json"), "{}")
	cmd := exec.Command(buildPigBinaryForSignalTest(t), "--model", "test-faux/faux-1", "--no-session", "--offline", "--no-context-files", "--prompt-template", "nosuch.md")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "HOME="+home, "USERPROFILE="+home, "PIG_HOME="+filepath.Join(home, ".pig"), "PIG_CODING_AGENT_DIR="+filepath.Join(home, "agent"),
		"PIG_TEST_FAUX=1", "PI_OFFLINE=1", "PI_SKIP_VERSION_CHECK=1", "TERM=xterm-256color")
	terminal, stop := startPigOnTerminal(t, cmd)
	defer stop()
	screen := make(chan string, 64)
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := terminal.Read(buf)
			if n > 0 {
				screen <- string(buf[:n])
			}
			if err != nil {
				close(screen)
				return
			}
		}
	}()
	deadline := time.Now().Add(testbudget.Wait(t))
	var seen strings.Builder
	waitFor := func(what, text string) {
		t.Helper()
		for !strings.Contains(seen.String(), text) {
			select {
			case chunk, ok := <-screen:
				if !ok {
					t.Fatalf("pig exited while waiting for %s", what)
				}
				seen.WriteString(chunk)
			case <-time.After(100 * time.Millisecond):
			}
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for %s\nscreen:\n%s", what, seen.String())
			}
		}
	}
	const missing = "Prompt template path does not exist"
	waitFor("the startup listing", missing)
	seen.Reset()
	_, _ = terminal.Write([]byte("/new"))
	time.Sleep(200 * time.Millisecond)
	_, _ = terminal.Write([]byte("\r"))
	waitFor("the listing after /new", missing)
}

// The check uses the path main.ts resolved against the launch directory (:728), not the raw flag: with a --session whose cwd is
// another directory, "found.md" in the launch directory exists and "nosuch.md" is the only missing path in [Prompt conflicts].
func TestMissingPromptTemplateCheckUsesTheLaunchDirectory(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and starts the pig binary")
	}
	t.Parallel()
	home, launch, other := t.TempDir(), t.TempDir(), t.TempDir()
	seedFirstRunDone(t, filepath.Join(home, "agent"))
	writeStartupFixtureFile(t, filepath.Join(home, "agent", "settings.json"), "{}")
	writeStartupFixtureFile(t, filepath.Join(launch, "found.md"), "---\ndescription: found here\n---\nbody\n")
	cwd, err := json.Marshal(other)
	if err != nil {
		t.Fatal(err)
	}
	session := filepath.Join(t.TempDir(), "s1.jsonl")
	writeStartupFixtureFile(t, session, `{"type":"session","version":3,"id":"s1","timestamp":"2026-01-01T00:00:00.000Z","cwd":`+string(cwd)+"}\n")
	cmd := exec.Command(buildPigBinaryForSignalTest(t), "--model", "test-faux/faux-1", "--offline", "--no-context-files", "--session", session,
		"--prompt-template", "found.md", "--prompt-template", "nosuch.md")
	cmd.Dir = launch
	cmd.Env = append(os.Environ(), "HOME="+home, "USERPROFILE="+home, "PIG_HOME="+filepath.Join(home, ".pig"), "PIG_CODING_AGENT_DIR="+filepath.Join(home, "agent"),
		"PIG_TEST_FAUX=1", "PI_OFFLINE=1", "PI_SKIP_VERSION_CHECK=1", "TERM=xterm-256color")
	terminal, stop := startPigOnTerminal(t, cmd)
	defer stop()
	screen := make(chan string, 64)
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := terminal.Read(buf)
			if n > 0 {
				screen <- string(buf[:n])
			}
			if err != nil {
				close(screen)
				return
			}
		}
	}()
	const missing = "Prompt template path does not exist"
	deadline := time.Now().Add(testbudget.Wait(t))
	// checkListing reads a listing up to its missing-path error, and the frame's rest, and checks that each [Prompt conflicts] section
	// has one error and none names found.md.
	checkListing := func(what string) {
		t.Helper()
		var seen strings.Builder
		for !strings.Contains(seen.String(), missing) {
			select {
			case chunk, ok := <-screen:
				if !ok {
					t.Fatalf("pig exited before %s", what)
				}
				seen.WriteString(chunk)
			case <-time.After(100 * time.Millisecond):
			}
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for %s\nscreen:\n%s", what, seen.String())
			}
		}
		for quiet := time.After(testbudget.Wait(t) / 30); ; {
			select {
			case chunk, ok := <-screen:
				if ok {
					seen.WriteString(chunk)
					continue
				}
			case <-quiet:
			}
			break
		}
		text := seen.String()
		if sections, errors := strings.Count(text, "[Prompt conflicts]"), strings.Count(text, missing); sections == 0 || errors != sections || strings.Contains(text, "found.md") {
			t.Fatalf("%s: [Prompt conflicts] sections = %d with %d missing-path errors, or one names found.md; want one error, for nosuch.md\nscreen:\n%s", what, sections, errors, text)
		}
	}
	checkListing("the startup listing")
	_, _ = terminal.Write([]byte("/new"))
	time.Sleep(200 * time.Millisecond)
	_, _ = terminal.Write([]byte("\r"))
	checkListing("the listing after /new")
}
