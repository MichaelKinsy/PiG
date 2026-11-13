//go:build linux

package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"
)

// docsHeroANSI matches the terminal control sequences of a screen capture.
var docsHeroANSI = regexp.MustCompile(`\x1b\[[0-9;?<=>]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b[@-_]`)

const (
	// docsHeroStartupNotice answers a slash command entered before the extensions have loaded. The command stays in the editor.
	docsHeroStartupNotice = "Startup is still in progress"
	// docsHeroReloading is the box /reload shows while it works, and docsHeroReloaded the line it ends with.
	docsHeroReloading = "Reloading keybindings, extensions, skills, prompts, themes, and context files..."
	docsHeroReloaded  = "Reloaded keybindings, extensions, skills, prompts, themes, and context files"
	// docsHeroBusy answers /reload while a response is running.
	docsHeroBusy = "Wait for the current response to finish before reloading."
)

// docsHeroTerminal is one interactive pig on a pseudo-terminal.
type docsHeroTerminal struct {
	t      *testing.T
	master *os.File
	output *ptyOutput
	exited chan struct{}
	budget time.Duration
}

// startDocsHeroTerminal starts pig interactively with args and waits until it shows its model.
func startDocsHeroTerminal(t *testing.T, binary, cwd string, env []string, args ...string) *docsHeroTerminal {
	t.Helper()
	master, slave := openPTY(t, 40, 160)
	ctx, cancel := context.WithCancel(t.Context())
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir, cmd.Env = cwd, append(os.Environ(), env...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	terminal := &docsHeroTerminal{t: t, master: master, output: &ptyOutput{}, exited: make(chan struct{}), budget: probeWait(t, 5*time.Minute)}
	go func() {
		buffer := make([]byte, 4096)
		for {
			n, err := master.Read(buffer)
			if n > 0 {
				_, _ = terminal.output.Write(buffer[:n])
			}
			if err != nil {
				return
			}
		}
	}()
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	_ = slave.Close()
	go func() { defer close(terminal.exited); _ = cmd.Wait() }()
	t.Cleanup(func() { cancel(); <-terminal.exited; _ = master.Close() })
	terminal.await(0, "", "faux-1")
	return terminal
}

// screen returns what the terminal was sent since mark, without control sequences.
func (s *docsHeroTerminal) screen(mark int) string {
	return docsHeroANSI.ReplaceAllString(string(s.output.since(mark)), "")
}

// send writes keys and returns the mark before them.
func (s *docsHeroTerminal) send(keys string) int {
	s.t.Helper()
	mark := s.output.mark()
	if _, err := s.master.WriteString(keys); err != nil {
		s.t.Fatal(err)
	}
	return mark
}

// await waits until the screen since mark shows one of texts, after the last occurrence of after when after is not
// empty, and returns that text. A repaint sends earlier lines of the chat again, so a text that an earlier step also
// printed is looked for only below the line that this step printed.
func (s *docsHeroTerminal) await(mark int, after string, texts ...string) string {
	s.t.Helper()
	started := time.Now()
	for {
		screen := s.screen(mark)
		rest, found := screen, after == ""
		if at := strings.LastIndex(screen, after); after != "" && at >= 0 {
			rest, found = screen[at+len(after):], true
		}
		for _, text := range texts {
			if found && strings.Contains(rest, text) {
				return text
			}
		}
		if time.Since(started) > s.budget {
			s.t.Fatalf("the screen did not show %q within %s:\n%s", texts, s.budget, screen)
		}
		select {
		case <-s.exited:
			s.t.Fatalf("pig exited before the screen showed %q:\n%s", texts, s.screen(0))
		case <-time.After(20 * time.Millisecond):
		}
	}
}

// command enters a slash command and waits for one of texts. While startup refuses the command it stays in the editor,
// so Enter is pressed again.
func (s *docsHeroTerminal) command(line string, texts ...string) string {
	s.t.Helper()
	mark := s.send(line + "\r")
	for {
		got := s.await(mark, "", append([]string{docsHeroStartupNotice}, texts...)...)
		if got != docsHeroStartupNotice {
			return got
		}
		time.Sleep(200 * time.Millisecond)
		mark = s.send("\r")
	}
}

// reload enters /reload, again while a response that is ending refuses it, waits until the reload box is gone and one
// of ends is on the screen, and returns the screen of the reload.
func (s *docsHeroTerminal) reload(ends ...string) string {
	s.t.Helper()
	started := time.Now()
	mark := s.send("/reload\r")
	for s.await(mark, "", docsHeroReloading, docsHeroBusy) == docsHeroBusy {
		time.Sleep(300 * time.Millisecond)
		if strings.Contains(s.screen(mark), docsHeroReloading) {
			break
		}
		mark = s.send("/reload\r")
	}
	s.await(mark, docsHeroReloading, ends...)
	screen := s.screen(mark)
	s.t.Logf("/reload took %s; screen:\n%s", time.Since(started).Round(time.Millisecond), screen)
	return screen
}

// TestDocsGoHotReloadPage follows the page's steps in an interactive session: load the page's Go extension, edit its
// source, enter /reload and read the changed output. It then pins the page's rules: what a reload keeps when the source
// did not change, what a source that does not compile shows, and the repair.
func TestDocsGoHotReloadPage(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the pig binary and a Go extension")
	}
	page, examples := docsHeroExamples(t, docsGoHotReloadPagePath, "greeter/extension.go")
	// What the page says the screen shows is what the steps below wait for.
	for _, shown := range []string{"Building extensions [0/1] (first run, will be cached): greeter", "Building extensions [1/1] (first run, will be cached)", docsHeroReloading, docsHeroReloaded, docsHeroBusy, "hello from v1 (call 2)", "hello from v2 (call 1)", "tool: hello from v2", "Failed to load extension: parse "} {
		if !strings.Contains(page, shown) {
			t.Fatalf("%s does not show %q", docsGoHotReloadPagePath, shown)
		}
	}
	binary := buildPigBinaryForSignalTest(t)
	home, cwd, dir := t.TempDir(), t.TempDir(), t.TempDir()
	agentDir := filepath.Join(home, "pig")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(`{"quietStartup":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	env := []string{"HOME=" + home, "PIG_HOME=" + filepath.Join(home, ".pig"), "PIG_CODING_AGENT_DIR=" + agentDir, "PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic", "PIG_OFFLINE=1", "TERM=xterm-256color"}
	extension := docsHeroGoExtension(t, binary, env, dir, "greeter", examples["greeter/extension.go"])
	source := filepath.Join(extension, "extension.go")
	edit := func(t *testing.T, old, replacement string) {
		t.Helper()
		text := string(mustReadFile(source))
		if !strings.Contains(text, old) {
			t.Fatalf("%s does not contain %q", source, old)
		}
		if err := os.WriteFile(source, []byte(strings.Replace(text, old, replacement, 1)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	start := func(t *testing.T, extensions ...string) *docsHeroTerminal {
		t.Helper()
		args := []string{"--no-extensions", "--model", "test-faux/faux-1", "--no-session", "--approve"}
		for _, path := range extensions {
			args = append(args, "-e", path)
		}
		return startDocsHeroTerminal(t, binary, cwd, env, args...)
	}

	t.Run("the steps of the page", func(t *testing.T) {
		terminal := start(t, extension)
		terminal.command("/greet", "hello from v1 (call 1)")
		terminal.command("/greet", "hello from v1 (call 2)")
		// The test model calls the tool an "ext tool" prompt names.
		terminal.command("Run: ext tool greet", "tool: hello from v1")

		// An edit and /reload: the new source runs in a new process, so the package variable starts again.
		edit(t, `"hello from v1"`, `"hello from v2"`)
		screen := terminal.reload(docsHeroReloaded)
		for _, line := range []string{"Building extensions [0/1] (first run, will be cached): greeter", "Building extensions [1/1] (first run, will be cached)"} {
			if !strings.Contains(screen, line) {
				t.Fatalf("the reload of the edited source did not show %q:\n%s", line, screen)
			}
		}
		terminal.command("/greet", "hello from v2 (call 1)")
		terminal.command("Run: ext tool greet", "tool: hello from v2")

		// /reload with no edit: the process stays and Extension() runs again, so the package variable keeps its value.
		terminal.reload(docsHeroReloaded)
		if got := terminal.command("/greet", "hello from v2 (call 1)", "hello from v2 (call 2)"); got != "hello from v2 (call 2)" {
			t.Fatalf("/greet after a reload with no edit shows %q, want the kept count (call 2)", got)
		}

		// A source that does not compile: the reload reports it and the extension is not loaded, so /greet is no command
		// and reaches the model.
		edit(t, "return ext\n", "return ext +\n")
		terminal.reload("Failed to load extension: parse ")
		terminal.command("/greet", `unhandled request "/greet"`)

		// The repair.
		edit(t, "return ext +\n", "return ext\n")
		terminal.reload(docsHeroReloaded)
		terminal.command("/greet", "hello from v2 (call 1)")
	})

	t.Run("a failed build leaves the other extension loaded", func(t *testing.T) {
		other := filepath.Join(dir, "other.mjs")
		if err := os.WriteFile(other, []byte("export default function (pi) {\n  pi.registerCommand(\"other\", { description: \"Report\", handler: async (_args, ctx) => { ctx.ui.notify(\"the other extension is loaded\", \"info\"); } });\n}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		terminal := start(t, extension, other)
		terminal.command("/greet", "hello from v2 (call 1)")
		edit(t, "return ext\n", "return ext +\n")
		terminal.reload("Failed to load extension: parse ")
		terminal.command("/other", "the other extension is loaded")
		terminal.command("/greet", `unhandled request "/greet"`)
	})
}
