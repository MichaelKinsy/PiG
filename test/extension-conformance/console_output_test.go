package extensionconformance

import (
	"bytes"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
)

// Pi runs an extension in its own process, so what an extension writes to the console reaches Pi's terminal: console.error always writes to stderr, and console.log
// writes to stdout in interactive mode and to stderr in print, json and rpc mode, where Pi has taken stdout over for its own output (main.ts takeOverStdout,
// core/output-guard.ts). Pi does not intercept either stream in interactive mode. A Go, Rust, Python or Lisp extension runs in a process PiG starts, so PiG's host
// must forward that process's stdout and stderr the way Pi's own process shows them: the stdout and stderr lines of one extension in the order it wrote them when
// they share a stream. Before this row the host dropped every runtime's console but Node's (a Go extension's stderr went only to a log file that is removed
// unless a load failure names it, and its stdout to /dev/null), so an extension's console.error-style notice, such as Posthorse's automatic-rollover failure
// report, never reached the user.
//
// The console-probe command of every SDK cell writes probe-out-1, probe-err-1, probe-out-2, probe-err-2 (each flushed). The row runs each cell, isolated and packed,
// in every mode and reads PiG's own stdout and stderr. The lines are bound to the probe's values, so an SDK or a host path that drops or merges them fails. A fused
// extension shares PiG's own stdout, which it must not write (AGENTS.md: fuse hazards), so its probe writes the error lines only.
func TestConformance_ExtensionConsoleReachesTheTerminalInEverySDK(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Cleanup(func() { rpcRowMode = "" })
	for _, mode := range []string{"print", "json", "rpc", "tui"} {
		rpcRowMode = mode
		for _, tc := range componentKitSDKCells() {
			t.Run(mode+"/"+tc.name, func(t *testing.T) {
				capture := swapProcessStd(t)
				h := startKitCell(t, tc)
				args := ""
				if strings.HasPrefix(tc.name, "fused") {
					args = "stderr"
				}
				runKitCommand(t, h, "console-probe", args)
				wantLines := 4
				if args == "stderr" {
					wantLines = 2
				}
				waitFor(t, func() bool {
					out, errOut := capture.snapshot()
					return len(probeLines(out))+len(probeLines(errOut)) >= wantLines
				})
				out, errOut := capture.stop()
				gotOut, gotErr := probeLines(out), probeLines(errOut)
				var wantOut, wantErr []string
				switch {
				case args == "stderr":
					wantErr = []string{"probe-err-1", "probe-err-2"}
				case mode == "tui":
					wantOut = []string{"probe-out-1", "probe-out-2"}
					wantErr = []string{"probe-err-1", "probe-err-2"}
				default:
					wantErr = []string{"probe-out-1", "probe-err-1", "probe-out-2", "probe-err-2"}
				}
				if !equalLines(gotOut, wantOut) || !equalLines(gotErr, wantErr) {
					t.Errorf("%s: terminal stdout %q stderr %q, want stdout %q stderr %q", tc.name, gotOut, gotErr, wantOut, wantErr)
				}
			})
		}
	}
}

func equalLines(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// probeLines are the console-probe lines of text, in order: the rest of PiG's output (diagnostics from other parts of the run) is not the row's concern.
func probeLines(text string) []string {
	var lines []string
	for line := range strings.SplitSeq(text, "\n") {
		if strings.HasPrefix(line, "probe-out-") || strings.HasPrefix(line, "probe-err-") {
			lines = append(lines, line)
		}
	}
	return lines
}

// processStd replaces PiG's own stdout and stderr with pipes: the host reads os.Stdout and os.Stderr when it starts an extension process, so the swap precedes the cell.
type processStd struct {
	mu     sync.Mutex
	out    bytes.Buffer
	errOut bytes.Buffer
	stop   func() (string, string)
}

func (p *processStd) snapshot() (string, string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return lineEndings(p.out.String()), lineEndings(p.errOut.String())
}

// lineEndings reads the terminal's bytes as lines: a Python extension's print writes "\r\n" on Windows, where its stdio translates "\n" (as C stdio does), and the
// host forwards the bytes as written. The row binds the lines the extension wrote, not its runtime's newline.
func lineEndings(text string) string { return strings.ReplaceAll(text, "\r\n", "\n") }

func swapProcessStd(t *testing.T) *processStd {
	t.Helper()
	origOut, origErr := os.Stdout, os.Stderr
	p := &processStd{}
	type pipe struct {
		r, w *os.File
		sink *bytes.Buffer
		done chan struct{}
	}
	open := func(sink *bytes.Buffer) *pipe {
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		c := &pipe{r: r, w: w, sink: sink, done: make(chan struct{})}
		go func() {
			defer close(c.done)
			buf := make([]byte, 4096)
			for {
				n, err := r.Read(buf)
				if n > 0 {
					p.mu.Lock()
					c.sink.Write(buf[:n])
					p.mu.Unlock()
				}
				if err != nil {
					if err != io.EOF {
						return
					}
					return
				}
			}
		}()
		return c
	}
	out, errc := open(&p.out), open(&p.errOut)
	os.Stdout, os.Stderr = out.w, errc.w
	var once sync.Once
	var gotOut, gotErr string
	restore := func() {
		once.Do(func() {
			os.Stdout, os.Stderr = origOut, origErr
			_ = out.w.Close()
			_ = errc.w.Close()
			<-out.done
			<-errc.done
			_ = out.r.Close()
			_ = errc.r.Close()
			gotOut, gotErr = lineEndings(p.out.String()), lineEndings(p.errOut.String())
		})
	}
	t.Cleanup(restore)
	p.stop = func() (string, string) { restore(); return gotOut, gotErr }
	return p
}
