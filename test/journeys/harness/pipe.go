package harness

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// PipeOpts configures one process driven through standard input and output.
type PipeOpts struct {
	Dir  string
	Env  []string // complete child environment; nothing is inherited
	Argv []string
}

// PipeDriver runs a program with its standard input and output on pipes. Screen is the program's standard output so far, one JSON record per line, so a wait predicate is a substring test on records the program wrote.
type PipeDriver struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	readEnd []io.Closer // our ends of the stdout and stderr pipes

	mu      sync.Mutex
	changed chan struct{}
	out     strings.Builder
	outputs int
	stderr  strings.Builder
	closed  bool

	exited chan struct{}
	code   int
	waitEr error
}

// StartPipe launches Argv with piped standard streams.
func StartPipe(o PipeOpts) (*PipeDriver, error) {
	cmd := exec.Command(o.Argv[0], o.Argv[1:]...)
	cmd.Dir = o.Dir
	cmd.Env = o.Env
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	errPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	p := &PipeDriver{cmd: cmd, stdin: in, readEnd: []io.Closer{out, errPipe}, changed: make(chan struct{}), exited: make(chan struct{})}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", o.Argv[0], err)
	}
	var readers sync.WaitGroup
	readers.Go(func() { p.readStdout(out) })
	readers.Go(func() { p.readStderr(errPipe) })
	go func() {
		readers.Wait()
		err := cmd.Wait()
		p.mu.Lock()
		defer p.mu.Unlock()
		var ee *exec.ExitError
		switch {
		case err == nil:
		case errors.As(err, &ee):
			p.code = ee.ExitCode()
		default:
			p.waitEr = err
		}
		close(p.exited)
		p.bumpLocked()
	}()
	return p, nil
}

func (p *PipeDriver) bumpLocked() {
	p.outputs++
	close(p.changed)
	p.changed = make(chan struct{})
}

func (p *PipeDriver) readStdout(r io.Reader) {
	br := bufio.NewReader(r)
	for {
		line, err := br.ReadString('\n')
		if line != "" {
			p.mu.Lock()
			p.out.WriteString(line)
			if !strings.HasSuffix(line, "\n") {
				p.out.WriteByte('\n')
			}
			p.bumpLocked()
			p.mu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

func (p *PipeDriver) readStderr(r io.Reader) {
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			p.mu.Lock()
			p.stderr.Write(buf[:n])
			p.mu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

func (p *PipeDriver) Changed() <-chan struct{} {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.changed
}

func (p *PipeDriver) Outputs() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.outputs
}

func (p *PipeDriver) Screen() (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.out.String(), nil
}

func (p *PipeDriver) History() (string, error) { return p.Screen() }

func (p *PipeDriver) Exited() <-chan struct{} { return p.exited }

func (p *PipeDriver) ExitCode() (int, error) {
	select {
	case <-p.exited:
	default:
		return 0, errors.New("still running")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.code, p.waitEr
}

func (p *PipeDriver) PID() int { return p.cmd.Process.Pid }

func (p *PipeDriver) Stderr() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stderr.String()
}

// Close kills the process if it still runs.
func (p *PipeDriver) Close() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	p.mu.Unlock()
	select {
	case <-p.exited:
	default:
		_ = p.cmd.Process.Kill()
	}
	_ = p.stdin.Close()
	select {
	case <-p.exited:
	case <-time.After(10 * time.Second):
		// A descendant that inherited standard output keeps the pipe open after the program is killed, so the readers never see EOF.
		// Closing our ends ends them; the run's leak sweep reports the descendant.
		for _, c := range p.readEnd {
			_ = c.Close()
		}
		<-p.exited
	}
}

func (p *PipeDriver) Resize(int, int) error { return errors.New("a pipe has no size") }

// SendText writes s to standard input.
func (p *PipeDriver) SendText(s string) error {
	_, err := io.WriteString(p.stdin, s)
	return err
}

func (p *PipeDriver) SendKeys(...string) error { return errors.New("a pipe has no keys") }

func (p *PipeDriver) SendBytes(b []byte) error {
	_, err := p.stdin.Write(b)
	return err
}

// Quit closes standard input, which ends an RPC session.
func (p *PipeDriver) Quit() error { return p.stdin.Close() }
