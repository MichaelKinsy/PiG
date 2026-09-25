package buildprogress

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/charmbracelet/x/ansi"
	"golang.org/x/term"
)

// Reporter owns a build's status animation. Close joins its timer before returning.
type Reporter struct {
	mu      sync.Mutex
	out     io.Writer
	tty     bool
	verbose bool
	width   func() int
	now     func() time.Time
	start   time.Time
	phase   string
	step    string
	tail    outputTail
	frame   int
	drawn   bool
	closed  bool
	stop    chan struct{}
	done    chan struct{}
}

// New starts a live display only when out is a terminal; pipes receive plain phase lines.
func New(out io.Writer, verbose bool) *Reporter {
	file, isFile := out.(*os.File)
	tty := isFile && term.IsTerminal(int(file.Fd())) && os.Getenv("TERM") != "dumb"
	width := func() int {
		if tty {
			if n, _, err := term.GetSize(int(file.Fd())); err == nil {
				return n
			}
		}
		return 80
	}
	r := newReporter(out, verbose, tty, width, time.Now)
	if tty {
		r.stop, r.done = make(chan struct{}), make(chan struct{})
		go r.animate()
	}
	return r
}

func newReporter(out io.Writer, verbose, tty bool, width func() int, now func() time.Time) *Reporter {
	return &Reporter{out: out, verbose: verbose, tty: tty, width: width, now: now, start: now()}
}

// upstream: packages/tui/src/components/loader.ts:DEFAULT_FRAMES
const frames = "⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏"

func (r *Reporter) animate() {
	// upstream: packages/tui/src/components/loader.ts:DEFAULT_INTERVAL_MS
	ticker := time.NewTicker(80 * time.Millisecond)
	defer ticker.Stop()
	defer close(r.done)
	for {
		select {
		case <-r.stop:
			return
		case <-ticker.C:
			r.mu.Lock()
			r.frame++
			r.render()
			r.mu.Unlock()
		}
	}
}

// Write displays a diagnostic block without terminal controls or overwriting the live status rows.
func (r *Reporter) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return 0, io.ErrClosedPipe
	}
	r.clear()
	text := diagnosticText(string(p))
	_, err := io.WriteString(r.out, text)
	if r.tty && text != "" && !strings.HasSuffix(text, "\n") {
		_, _ = io.WriteString(r.out, "\n")
	}
	r.render()
	if err != nil {
		return 0, err
	}
	return len(p), nil
}

// Handle consumes a real phase or toolchain line. It is safe for concurrent tool output.
func (r *Reporter) Handle(event Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	if event.Output != "" {
		line := fmt.Sprintf("[%s] %s\n", plain(event.Member), toolText(event.Output))
		_, _ = r.tail.Write([]byte(line))
		if r.verbose {
			r.clear()
			_, _ = io.WriteString(r.out, line)
			r.render()
		}
		return
	}
	r.phase, r.step = plain(event.Phase), plain(event.Step)
	r.tail = r.tail[:0]
	if r.tty {
		r.render()
	} else {
		_, _ = fmt.Fprintf(r.out, "%s (%s) — %s\n", r.phase, r.elapsed(), r.step)
	}
}

type outputTail []byte

func (tail *outputTail) Write(p []byte) (int, error) {
	n := len(p)
	if len(p) > diagnosticTailBytes {
		p = p[len(p)-diagnosticTailBytes:]
	}
	if *tail == nil {
		*tail = make([]byte, 0, diagnosticTailBytes)
	}
	if overflow := len(*tail) + len(p) - diagnosticTailBytes; overflow > 0 {
		copy(*tail, (*tail)[overflow:])
		*tail = (*tail)[:len(*tail)-overflow]
	}
	*tail = append(*tail, p...)
	return n, nil
}

func plain(s string) string {
	return strings.TrimSpace(strings.Map(func(c rune) rune {
		if unicode.IsControl(c) {
			return -1
		}
		return c
	}, ansi.Strip(s)))
}

func toolText(s string) string {
	return strings.ReplaceAll(diagnosticText(s), "\n", "")
}

func diagnosticText(s string) string {
	return strings.Map(func(c rune) rune {
		if unicode.IsControl(c) && c != '\t' && c != '\n' {
			return -1
		}
		return c
	}, ansi.Strip(s))
}

func (r *Reporter) elapsed() string { return r.now().Sub(r.start).Truncate(time.Second).String() }

func (r *Reporter) clear() {
	if r.drawn {
		_, _ = io.WriteString(r.out, "\r\x1b[2K\x1b[1A\r\x1b[2K")
		r.drawn = false
	}
}

func (r *Reporter) render() {
	if !r.tty || r.closed || r.phase == "" {
		return
	}
	r.clear()
	indicators := []rune(frames)
	width := max(1, r.width()-1)
	title := fmt.Sprintf("%c %s (%s)", indicators[r.frame%len(indicators)], r.phase, r.elapsed())
	_, _ = fmt.Fprintf(r.out, "%s\n\x1b[2m%s\x1b[0m", ansi.Truncate(title, width, "…"), ansi.Truncate("  "+r.step, width, "…"))
	r.drawn = true
}

// Close clears the live display and stops all further writes. It is idempotent.
func (r *Reporter) Close() {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		if r.done != nil {
			<-r.done
		}
		return
	}
	r.closed = true
	r.clear()
	if r.stop != nil {
		close(r.stop)
	}
	r.mu.Unlock()
	if r.done != nil {
		<-r.done
	}
}

const diagnosticTailBytes = 16 * 1024

// Failure includes the last real phase and an actionable hint. Long compiler output is reduced to its diagnostic tail and terminal controls are removed.
func (r *Reporter) Failure(err error) error {
	r.Close()
	r.mu.Lock()
	defer r.mu.Unlock()
	message := err.Error()
	if len(r.tail) > 0 {
		message, _, _ = strings.Cut(message, "\n")
		message += "\ntoolchain output (tail):\n" + strings.ToValidUTF8(string(r.tail), "�")
	} else if len(message) > diagnosticTailBytes {
		message = "[diagnostic truncated; last 16 KiB]\n" + strings.ToValidUTF8(message[len(message)-diagnosticTailBytes:], "�")
	}
	message = diagnosticText(message)
	return &phaseFailure{message: fmt.Sprintf("%s failed (%s): %s\n%s\nhint: fix the diagnostic above and retry; use --verbose for live toolchain output (check toolchains with `pig setup`)", r.phase, r.elapsed(), r.step, message), cause: err}
}

type phaseFailure struct {
	message string
	cause   error
}

func (e *phaseFailure) Error() string { return e.message }
func (e *phaseFailure) Unwrap() error { return e.cause }

// Success prints the completed binary path, size, and total elapsed time after joining the display.
func (r *Reporter) Success(path string, size int64) {
	r.Close()
	r.mu.Lock()
	defer r.mu.Unlock()
	_, _ = fmt.Fprintf(r.out, "Built %s (%d bytes) in %s\n", plain(path), size, r.elapsed())
}
