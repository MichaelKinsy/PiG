//go:build linux

package codingagent_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/factoryload"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/mcpext"
	"github.com/MichaelKinsy/PiG/coding/mcpext/mcptui"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

const startupWriteKindsChildEnv = "PIG_TEST_STARTUP_WRITE_KINDS_CHILD"

// A plain interactive startup writes what Pi's init() writes after its first frame and no more (interactive-mode.ts:1141-1162, tui.ts:1025-1031). Measured in Pi
// 1.5.2 on a pseudo-terminal that answers no query, five runs, identical: the first frame and its cursor placement, the window title, the frame init() ends in
// (renderNow) and its cursor placement, the frame of the warnings run() shows after init() (the model fallback message and a crash notice) and its cursor placement. Init() has no render between the bind and
// renderInitialMessages (requests only), and ends in renderNow, which clears the requests made before it. pig once rendered right after the bind and again at the
// end, a frame was painted between the colour reply and the header, and every render with nothing to change wrote a bare cursor placement, ESC[<n>G ESC[?25l,
// outside a frame.
func TestStartupWritesTheKindsPiWritesAfterItsFirstFrame(t *testing.T) {
	captured := runStartupWriteKindsChild(t)
	start := bytes.Index(captured, []byte("\x1b[?2026h"))
	end := bytes.Index(captured, []byte("\x1b]777;mark;init-idle\x07"))
	if start < 0 || end < start {
		t.Fatalf("no first frame (%d) before the idle mark (%d) in %q", start, end, captured[:min(len(captured), 400)])
	}
	got := startupWriteKinds(string(captured[start:end]))
	want := []string{"frame", "cursor-placement", "title", "frame", "cursor-placement", "frame", "cursor-placement"}
	if !slices.Equal(got, want) {
		t.Fatalf("writes from the first frame to the first idle = %q, want Pi's %q\nthe stream: %q", got, want, captured[start:end])
	}
}

var startupWriteMove = regexp.MustCompile(`^\x1b\[[0-9;]*[ABCDGHd]$`)

var startupWriteToken = regexp.MustCompile("\x1b\\[[0-9;?]*[ -/]*[@-~]|\x1b\\][^\x07\x1b]*(?:\x07|\x1b\\\\)|[^\x1b]+")

// startupWriteKinds reduces a terminal stream to the kinds of its writes: a synchronized frame, a cursor placement (cursor moves ending in CSI ? 25 l or h), a title.
func startupWriteKinds(stream string) []string {
	var kinds []string
	moved := false
	inFrame := false
	for _, token := range startupWriteToken.FindAllString(stream, -1) {
		switch {
		case token == "\x1b[?2026h":
			inFrame = true
		case token == "\x1b[?2026l":
			inFrame = false
			kinds = append(kinds, "frame")
		case inFrame:
		case startupWriteMove.MatchString(token):
			moved = true
		case token == "\x1b[?25l" || token == "\x1b[?25h":
			kinds = append(kinds, "cursor-placement")
			moved = false
		case strings.HasPrefix(token, "\x1b]0;"):
			kinds = append(kinds, "title")
		default:
			if moved {
				kinds = append(kinds, "cursor-move")
				moved = false
			}
			kinds = append(kinds, fmt.Sprintf("%q", token))
		}
	}
	return kinds
}

func TestStartupWriteKindsChild(t *testing.T) {
	report := os.Getenv(startupWriteKindsChildEnv)
	if report == "" {
		t.Skip("runs only as the pty child of TestStartupWritesTheKindsPiWritesAfterItsFirstFrame")
	}
	fail := func(format string, args ...any) {
		_ = os.WriteFile(report, fmt.Appendf(nil, format, args...), 0o600)
		t.Fatalf(format, args...)
	}
	agentDir := filepath.Join(os.Getenv("PIG_HOME"), "agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		fail("%v", err)
	}
	// Pi's and pig's default mode is fullscreen, which has no standalone cursor placement; the regular mode is the one that has it.
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(`{"tuiMode":"regular"}`), 0o600); err != nil {
		fail("%v", err)
	}
	// A crash record Pi's run() has not shown yet: its notice comes after the model fallback message, so the start shows two warnings in a row, and Pi's showWarning only
	// requests a render (interactive-mode.ts:4812-4816), so both are painted by one frame; a render of each would write a frame of its own.
	crash := `[{"timestamp":"2099-06-15T12:00:00.000Z","version":"0.0.0","kind":"uncaught_exception","message":"Boom","stack":null,"sessionFile":null,"cwd":"/"}]`
	if err := os.WriteFile(filepath.Join(agentDir, "crashes.json"), []byte(crash), 0o600); err != nil {
		fail("%v", err)
	}
	cwd := t.TempDir()
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: cwd, AgentDir: agentDir})
	if err != nil {
		fail("%v", err)
	}
	manager, err := coding.NewInMemorySessionManager(cwd)
	if err != nil {
		fail("%v", err)
	}
	model := &ai.Model{ID: "faux-1", DisplayName: "faux-1", Capabilities: ai.ModelCapabilities{ContextWindow: 100000}}
	// A plain start loads the built-in MCP extension, the factory a plain pig loads, with no server configured. Its session_start handler returns without awaiting in Pi
	// (extensions/mcp/index.ts:1115-1152), so the bind of the start is one that never pends.
	runtime := extension.CreateExtensionRuntime()
	mcp, err := factoryload.LoadExtensionFromFactory(mcpext.CreateMcpExtension(mcpext.Options{Terminal: mcptui.Terminal(), LoadConfig: func(context.Context) mcpext.LoadedMcpConfig { return mcpext.LoadedMcpConfig{} }}), cwd, extension.CreateEventBus(), runtime, "builtin:mcp")
	if err != nil {
		fail("%v", err)
	}
	runner := inproc.NewRunner([]extension.Extension{mcp}, cwd, runtime)
	session, err := coding.NewSession(services, coding.SessionOptions{Model: model, SessionManager: manager, Runner: runner})
	if err != nil {
		fail("%v", err)
	}
	defer func() { _ = session.Close() }()
	mode := icodingagent.NewInteractiveMode(nil, icodingagent.InteractiveModeOptions{
		CWD: cwd, AgentDir: agentDir, Model: model, SessionHandle: session, NoThemes: true, ExtensionRunner: runner,
		// The built-in header: main.go sets it for every start that is not print or rpc and not quiet (LoginVisible: showBanner). Without it nothing changes between the first
		// frame and the end of init(), whose renderNow then writes only a cursor placement; Pi mounts its header after the colour wait, in the frame the end of init() paints.
		LoginVisible: true,
		// A plain first start has no model: Pi's run() shows this warning after init() returns (interactive-mode.ts:1257), and the frame that paints it is the third.
		ModelFallbackMessage: "No models available. Use /login to log into a provider via OAuth or API key.",
		SettingsManager:      services.SettingsManager(), Settings: services.SettingsManager().Get(),
	})
	if err := mode.Init(t.Context()); err != nil {
		fail("Init: %v", err)
	}
	mode.FlushStartupFramesForTest()
	_, _ = os.Stdout.WriteString("\x1b]777;mark;init-idle\x07")
	if err := mode.Stop(); err != nil {
		fail("Stop: %v", err)
	}
}

// runStartupWriteKindsChild runs the child on a pseudo-terminal that answers no query and returns everything it wrote.
func runStartupWriteKindsChild(t *testing.T) []byte {
	t.Helper()
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY|syscall.O_CLOEXEC, 0)
	if err != nil {
		t.Fatalf("open /dev/ptmx: %v", err)
	}
	defer func() { _ = master.Close() }()
	if err := unix.IoctlSetPointerInt(int(master.Fd()), unix.TIOCSPTLCK, 0); err != nil {
		t.Fatalf("unlock pseudo-terminal: %v", err)
	}
	number, err := unix.IoctlGetInt(int(master.Fd()), unix.TIOCGPTN)
	if err != nil {
		t.Fatalf("pseudo-terminal number: %v", err)
	}
	slave, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", number), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatalf("open pseudo-terminal slave: %v", err)
	}
	defer func() { _ = slave.Close() }()
	if err := unix.IoctlSetWinsize(int(master.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Row: 30, Col: 100}); err != nil {
		t.Fatalf("set pseudo-terminal size: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), testbudget.Wait(t))
	defer cancel()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	report := filepath.Join(t.TempDir(), "report")
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestStartupWriteKindsChild$", "-test.count=1", "-test.v")
	cmd.Env = append(os.Environ(), startupWriteKindsChildEnv+"="+report, "TERM=xterm-256color", "PIG_HOME="+t.TempDir(), "TMPDIR="+t.TempDir())
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	_ = slave.Close()
	var captured bytes.Buffer
	copied := make(chan struct{})
	go func() {
		defer close(copied)
		_, _ = io.Copy(&captured, master)
	}()
	waitErr := cmd.Wait()
	_ = master.Close()
	<-copied
	if failure, _ := os.ReadFile(report); waitErr != nil || len(failure) > 0 || !bytes.Contains(captured.Bytes(), []byte("--- PASS: TestStartupWriteKindsChild")) {
		t.Fatalf("child failed: %v: %s\n%q", waitErr, failure, captured.Bytes()[max(0, captured.Len()-600):])
	}
	return captured.Bytes()
}
