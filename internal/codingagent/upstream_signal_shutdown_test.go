package codingagent

import (
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/tui"
)

type upstreamShutdownRenderer struct {
	*tui.TUI
	order *[]string
}

func (r *upstreamShutdownRenderer) Stop() { *r.order = append(*r.order, "stop"); r.TUI.Stop() }

func upstreamShutdownMode(t *testing.T, order *[]string, persisted bool) *InteractiveMode {
	t.Helper()
	runner := inproc.NewRunner([]extension.Extension{{Path: "cleanup", Handlers: map[string][]extension.HandlerFn{
		"session_shutdown": {func(...any) (any, error) { *order = append(*order, "dispose"); return nil, nil }},
	}}}, t.TempDir())
	m := &InteractiveMode{newRunner: runner, tuiInst: &upstreamShutdownRenderer{TUI: tui.NewWithOutput(io.Discard, 80, 24), order: order}}
	m.rawDrain = func() { *order = append(*order, "drainInput") }
	m.rawRestore = func() {}
	if persisted {
		session, err := tempSessionMgr(t).Create("test-session", "")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := session.AppendMessage(mkAssistantMsg("finished")); err != nil {
			t.Fatal(err)
		}
		m.opts.SessionHandle = &recordingCompactHandle{inner: session}
	}
	return m
}

func TestSignalShutdownJoinsCleanupBeforeOwnerTeardown(t *testing.T) {
	var order []string
	m := upstreamShutdownMode(t, &order, false)
	started, release := make(chan struct{}), make(chan struct{})
	m.newRunner = inproc.NewRunner([]extension.Extension{{Path: "cleanup", Handlers: map[string][]extension.HandlerFn{
		"session_shutdown": {func(...any) (any, error) {
			close(started)
			<-release
			order = append(order, "dispose")
			return nil, nil
		}},
	}}}, t.TempDir())
	signalDone := make(chan struct{})
	go func() { m.ShutdownFromSignal(); close(signalDone) }()
	<-started
	ownerDone := make(chan error, 1)
	go func() { ownerDone <- m.finishInteractiveShutdown() }()
	var ownerErr error
	early := false
	select {
	case ownerErr = <-ownerDone:
		early = true
		t.Errorf("teardown returned before cleanup: %v", ownerErr)
	default:
	}
	close(release)
	<-signalDone
	if !early {
		ownerErr = <-ownerDone
	}
	if ownerErr != nil {
		t.Fatal(ownerErr)
	}
	if want := []string{"dispose", "drainInput", "stop"}; !slices.Equal(order, want) {
		t.Fatalf("order=%v; want %v", order, want)
	}
}

func TestUpstreamSignalShutdownExtensionCleanup(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/5080-signal-shutdown-extension-cleanup.test.ts:110
	t.Run("signal-triggered shutdown emits session_shutdown before terminal writes", func(t *testing.T) {
		var order []string
		m := upstreamShutdownMode(t, &order, false)
		m.ShutdownFromSignal()
		// Run's deferred teardown follows signal cleanup; it must not precede it.
		m.stopInteractiveTui()
		if want := []string{"dispose", "drainInput", "stop"}; !slices.Equal(order, want) {
			t.Fatalf("order=%v; want %v", order, want)
		}
		if !m.signalShutdownDone.Load() {
			t.Fatal("shutdown did not retain its entered state")
		}
		if os.Getenv("PIG_PT_SHUTDOWN_PROBE") == "1" {
			fmt.Println("SHUTDOWN_ORDER signal " + strings.Join(order, ","))
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/5080-signal-shutdown-extension-cleanup.test.ts:123
	t.Run("interactive quit stops the TUI before emitting session_shutdown", func(t *testing.T) {
		var order []string
		m := upstreamShutdownMode(t, &order, false)
		m.requestShutdown()
		if err := runShutdownInputLoop(t, m, strings.NewReader("")); err != nil {
			t.Fatal(err)
		}
		if want := []string{"drainInput", "stop", "dispose"}; !slices.Equal(order, want) {
			t.Fatalf("order=%v; want %v", order, want)
		}
		if os.Getenv("PIG_PT_SHUTDOWN_PROBE") == "1" {
			fmt.Println("SHUTDOWN_ORDER interactive " + strings.Join(order, ","))
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/5080-signal-shutdown-extension-cleanup.test.ts:135
	t.Run("interactive quit prints a resume hint for persisted sessions", func(t *testing.T) {
		setStdoutTTY(t, true)
		var order []string
		m := upstreamShutdownMode(t, &order, true)
		output := captureStdout(t, func() {
			m.requestShutdown()
			if err := runShutdownInputLoop(t, m, strings.NewReader("")); err != nil {
				t.Fatal(err)
			}
		})
		if want := []string{"drainInput", "stop", "dispose"}; !slices.Equal(order, want) {
			t.Fatalf("order=%v; want %v", order, want)
		}
		if want := "\x1b[2mTo resume this session:\x1b[22m pig --session test-session\n"; output != want {
			t.Fatalf("output=%q; want %q", output, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/5080-signal-shutdown-extension-cleanup.test.ts:154
	t.Run("signal-triggered shutdown does not print a resume hint", func(t *testing.T) {
		setStdoutTTY(t, true)
		var order []string
		m := upstreamShutdownMode(t, &order, true)
		output := captureStdout(t, func() { m.ShutdownFromSignal(); m.stopInteractiveTui() })
		if strings.Contains(output, "To resume this session:") {
			t.Fatalf("signal wrote resume hint: %q", output)
		}
	})
	// upstream: packages/coding-agent/src/modes/interactive/interactive-mode.ts:310
	t.Run("interactive quit does not print a resume hint to non-TTY stdout", func(t *testing.T) {
		var order []string
		m := upstreamShutdownMode(t, &order, true)
		output := captureStdout(t, func() {
			if stdoutIsTTY() {
				t.Fatal("stdout pipe must not be a terminal")
			}
			m.requestShutdown()
			if err := runShutdownInputLoop(t, m, strings.NewReader("")); err != nil {
				t.Fatal(err)
			}
		})
		if output != "" {
			t.Fatalf("non-TTY output=%q; want empty", output)
		}
		if want := []string{"drainInput", "stop", "dispose"}; !slices.Equal(order, want) {
			t.Fatalf("order=%v; want %v", order, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/5080-signal-shutdown-extension-cleanup.test.ts:172
	t.Run("re-entrant shutdown is a no-op", func(t *testing.T) {
		var order []string
		m := upstreamShutdownMode(t, &order, false)
		m.signalShutdownDone.Store(true)
		m.ShutdownFromSignal()
		if len(order) != 0 {
			t.Fatalf("reentrant shutdown performed work: %v", order)
		}
	})
}
